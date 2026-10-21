package egress

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"syscall"
	"time"
)

// DefaultKeepAlive is the TCP keep-alive interval configured on broker-aware
// transports, matching the default transport's production keep-alive value.
const DefaultKeepAlive = 30 * time.Second

// ErrBrokerFailedToStart is the fail-closed error returned when the operator
// explicitly requested egress isolation but the broker did not start.
var ErrBrokerFailedToStart = errors.New("egress: broker failed to start")

// NewHTTPTransport returns a production HTTP transport whose dialing is governed
// by the frozen process-wide egress policy. When isolation is off the transport is
// behaviorally equivalent to a clone of http.DefaultTransport, including normal
// environment-proxy handling; when it is on, requests use direct policy-aware
// transports so an operator-set proxy cannot become a destination-policy bypass.
func NewHTTPTransport() *http.Transport {
	tr := defaultTransportClone()
	tr.Proxy = envProxyWhenBrokerInactive
	tr.DialContext = dialPolicyAware
	return tr
}

func newDirectTransport() *http.Transport {
	tr := defaultTransportClone()
	tr.Proxy = nil
	tr.DialContext = dialPolicyAware
	return tr
}

func defaultTransportClone() *http.Transport {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return &http.Transport{}
	}
	tr := base.Clone()
	tr.MaxIdleConns = 100
	tr.MaxIdleConnsPerHost = 10
	tr.IdleConnTimeout = 90 * time.Second
	return tr
}

func envProxyWhenBrokerInactive(r *http.Request) (*url.URL, error) {
	_, active, failed := brokerPolicy()
	if failed || active {
		return nil, nil
	}
	return http.ProxyFromEnvironment(r)
}

// TransportOption customizes the RoundTripper returned by [Transport].
type TransportOption func(*RoundTripperPolicy)

// WithDialGuard composes guard into the resolved-address checks of the direct
// transport used while the broker is active. Without it, wrapping a guarded
// base transport would silently drop the caller's dial gate on the active
// path, because the active path replaces the base transport.
func WithDialGuard(guard func(ctx context.Context, network, address string) error) TransportOption {
	return func(rt *RoundTripperPolicy) {
		rt.dialGuard = guard
	}
}

// Transport wraps base with the broker-aware policy and sentinel resolver. A nil
// base uses a broker-aware clone of http.DefaultTransport. The wrapper consults
// the frozen broker state on each request, so a client constructed before the
// coordinator arms the tier still honors it after [Start].
func Transport(base http.RoundTripper, opts ...TransportOption) http.RoundTripper {
	if base == nil {
		base = NewHTTPTransport()
	}
	rt := &RoundTripperPolicy{
		store:      Store(),
		inner:      base,
		direct:     newDirectTransport(),
		policyFunc: brokerPolicy,
	}
	for _, opt := range opts {
		opt(rt)
	}
	if rt.dialGuard != nil {
		if tr, ok := rt.direct.(*http.Transport); ok {
			tr.DialContext = dialPolicyAwareGuarded(rt.dialGuard)
		}
	}
	return rt
}

func dialPolicyAware(ctx context.Context, network, address string) (net.Conn, error) {
	return dialPolicyAwareGuarded(nil)(ctx, network, address)
}

func dialPolicyAwareGuarded(guard func(ctx context.Context, network, address string) error) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		policy, active, failed := brokerPolicy()
		if failed {
			return nil, ErrBrokerFailedToStart
		}
		dialer := &net.Dialer{KeepAlive: DefaultKeepAlive}
		if active {
			dialer.ControlContext = func(ctx context.Context, network, address string, c syscall.RawConn) error {
				if err := policy.ControlContext(ctx, network, address, c); err != nil {
					return err
				}
				if guard != nil {
					return guard(ctx, network, address)
				}
				return nil
			}
		} else if guard != nil {
			dialer.ControlContext = func(ctx context.Context, network, address string, _ syscall.RawConn) error {
				return guard(ctx, network, address)
			}
		}
		return dialer.DialContext(ctx, network, address)
	}
}

// RoundTripperPolicy is an [http.RoundTripper] that enforces the net-policy and
// resolves sentinels for requests Phosphor itself issues. It is the production
// transport path the fetch tools and brokered HTTP clients use.
type RoundTripperPolicy struct {
	policy     Policy
	policyFunc func() (Policy, bool, bool)
	store      TokenResolver
	inner      http.RoundTripper
	direct     http.RoundTripper
	dialGuard  func(ctx context.Context, network, address string) error
}

// NewRoundTripper builds a broker transport over inner (nil means a broker-aware
// clone of the default transport) under a frozen policy.
func NewRoundTripper(policy Policy, store TokenResolver, inner http.RoundTripper) *RoundTripperPolicy {
	if store == nil {
		store = Store()
	}
	if inner == nil {
		inner = NewHTTPTransport()
	}
	return &RoundTripperPolicy{policy: policy, store: store, inner: inner, direct: newDirectTransport()}
}

// RoundTrip enforces the policy, then resolves sentinels in the request body and
// headers at the (now allowlisted) destination, refusing on any unresolvable token.
// It is fail-closed: a request it cannot complete safely is not sent.
func (rt *RoundTripperPolicy) RoundTrip(r *http.Request) (*http.Response, error) {
	if r == nil {
		return nil, ErrUnresolvedSentinel
	}
	policy, active, failed := rt.currentPolicy()
	if failed {
		return nil, ErrBrokerFailedToStart
	}
	if !active {
		return rt.inner.RoundTrip(r)
	}
	if r.URL == nil {
		return nil, ErrUnresolvedSentinel
	}

	req := r.Clone(r.Context())
	if act, reason := policy.CheckContext(req.Context(), req.URL); act == Deny {
		return nil, errors.New("egress: " + reason)
	}

	body, hadBody, err := snapshotRequestBody(req, policy.MaxBody())
	if err != nil {
		return nil, err
	}
	outBody, _, unresolved := RestoreString(string(body), rt.store)
	if unresolved > 0 {
		return nil, ErrUnresolvedSentinel
	}
	if outBody != string(body) {
		body = []byte(outBody)
	}
	if hadBody {
		setRequestBody(req, body)
	}

	for key, values := range req.Header {
		for i, value := range values {
			restored, _, unresolved := RestoreString(value, rt.store)
			if unresolved > 0 {
				return nil, ErrUnresolvedSentinel
			}
			values[i] = restored
		}
		req.Header[key] = values
	}

	inner := rt.inner
	if rt.direct != nil {
		inner = rt.direct
	}
	return inner.RoundTrip(req)
}

func (rt *RoundTripperPolicy) currentPolicy() (Policy, bool, bool) {
	if rt.policyFunc != nil {
		return rt.policyFunc()
	}
	return rt.policy, true, false
}

func snapshotRequestBody(r *http.Request, max int64) ([]byte, bool, error) {
	var body io.ReadCloser
	if r.GetBody != nil {
		var err error
		body, err = r.GetBody()
		if err != nil {
			return nil, true, err
		}
	} else {
		body = r.Body
	}
	if body == nil {
		return nil, false, nil
	}
	defer body.Close()

	data, err := readCapped(body, max)
	if err != nil {
		return nil, true, err
	}
	return data, true, nil
}

func setRequestBody(r *http.Request, body []byte) {
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	r.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
}
