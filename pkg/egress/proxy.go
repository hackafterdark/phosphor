package egress

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// The brokered egress path: a loopback HTTP/CONNECT proxy that enforces the
// net-policy and the resolve-at-the-trusted-hop rule. Alongside
// [RoundTripperPolicy] in transport.go it is one of the two places that turn a
// sealed [SecretStore] token back into plaintext. Both refuse a request rather
// than forward an unresolvable token or a destination outside the allowlist.

// proxyAuthHeader is the hop-by-hop header the broker accepts. Standard clients
// populate it from proxy-URL userinfo using Basic auth; an optional Bearer form
// is also accepted for clients that can set the header directly. It is never
// forwarded upstream.
const proxyAuthHeader = "Proxy-Authorization"

// Proxy is the loopback egress broker.
type Proxy struct {
	policy    Policy
	store     TokenResolver
	authToken string
	srv       *http.Server
	ln        net.Listener
	inner     http.RoundTripper
	mu        sync.Mutex
	closed    bool
}

// NewProxy starts a broker bound to the loopback interface on an ephemeral port
// and returns once it is accepting. It never binds a routable interface: the
// broker is reachable only from the same machine, and every request must also
// carry the per-process random proxy-auth token, so a neighbouring process cannot
// use it as an open proxy.
func NewProxy(policy Policy, store TokenResolver) (*Proxy, error) {
	if store == nil {
		store = Store()
	}
	tok, err := randomToken()
	if err != nil {
		return nil, err
	}
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("egress: default transport is not a *http.Transport")
	}
	inner := base.Clone()
	inner.Proxy = nil
	inner.DialContext = policy.Dialer().DialContext
	inner.MaxIdleConns = 100
	inner.MaxIdleConnsPerHost = 10
	inner.IdleConnTimeout = 90 * time.Second

	p := &Proxy{policy: policy, store: store, authToken: tok, inner: inner}
	p.srv = &http.Server{Handler: http.HandlerFunc(p.serve), ReadHeaderTimeout: 15 * time.Second}
	p.ln, err = net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	go func() { _ = p.srv.Serve(p.ln) }()
	return p, nil
}

// Addr is the "host:port" the broker listens on, for building a proxy URL.
func (p *Proxy) Addr() string { return p.ln.Addr().String() }

// ProxyURL is the http:// URL a client sets as HTTP_PROXY/HTTPS_PROXY. It is
// returned without authentication so callers can inspect the loopback address
// without handling a credential.
func (p *Proxy) ProxyURL() string { return "http://" + p.Addr() }

// ProxyAuthURL is the proxy URL that carries the per-process authentication token
// as standard proxy-URL userinfo. Well-behaved CLIs use this form so they can
// authenticate to the broker without Phosphor modifying their request headers.
func (p *Proxy) ProxyAuthURL() string { return "http://" + p.authToken + "@" + p.Addr() }

// AuthToken is the per-process secret the broker requires in Proxy-Authorization.
func (p *Proxy) AuthToken() string { return p.authToken }

// Close stops the broker.
func (p *Proxy) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	return p.srv.Close()
}

// ProxyEnv returns the environment entries that route a well-behaved subprocess
// through the broker. The proxy URL carries the per-process token in standard
// userinfo form so ordinary HTTP clients send the required Proxy-Authorization
// header. NO_PROXY only exempts loopback/local traffic; the unrelated public host
// is intentionally not exempted.
func (p *Proxy) ProxyEnv() []string {
	url := p.ProxyAuthURL()
	noProxy := "localhost,127.0.0.1,::1"
	return []string{
		"HTTP_PROXY=" + url,
		"HTTPS_PROXY=" + url,
		"http_proxy=" + url,
		"https_proxy=" + url,
		"NO_PROXY=" + noProxy,
		"no_proxy=" + noProxy,
		"PHOSPHOR_PROXY_AUTH=" + p.authToken,
	}
}

func (p *Proxy) serve(w http.ResponseWriter, r *http.Request) {
	if !p.authorized(r) {
		http.Error(w, "proxy authentication required", http.StatusUnauthorized)
		return
	}
	if strings.EqualFold(r.Method, "CONNECT") {
		p.handleConnect(w, r)
		return
	}
	p.handleForward(w, r)
}

func (p *Proxy) authorized(r *http.Request) bool {
	value := strings.TrimSpace(r.Header.Get(proxyAuthHeader))
	if value == "" {
		return false
	}
	token, ok := proxyAuthorizationToken(value)
	if !ok {
		return false
	}
	return constantTimeEq(token, p.authToken)
}

func proxyAuthorizationToken(value string) (string, bool) {
	switch {
	case strings.HasPrefix(strings.ToUpper(value), "BASIC "):
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value[len("BASIC "):]))
		if err != nil {
			return "", false
		}
		user, _, found := strings.Cut(string(raw), ":")
		if !found {
			user = string(raw)
		}
		return user, true
	case strings.HasPrefix(strings.ToUpper(value), "BEARER "):
		return strings.TrimSpace(value[len("BEARER "):]), true
	default:
		return "", false
	}
}

// handleConnect is the connect-or-refuse gate. A CONNECT tunnel carries only the
// destination (host:port) in cleartext, so the broker can enforce the allowlist on
// where the subprocess is going. It does not attempt to read inside the tunnel
// (that would be a man-in-the-middle and we hold no such intent): the guarantee it
// provides to a subprocess is purely the destination policy. A token that a
// subprocess holds is therefore useless toward a non-allowlisted host regardless of
// what the injected prompt tells it to do.
func (p *Proxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	hostport := r.Host
	if hostport == "" {
		hostport = r.URL.Host
	}
	if act, reason := p.policy.CheckHostContext(r.Context(), hostport); act == Deny {
		slog.Debug("Egress proxy refused CONNECT", "target", hostport, "reason", reason)
		http.Error(w, "egress denied: "+reason, http.StatusForbidden)
		return
	}

	upstream, err := p.policy.Dialer().DialContext(r.Context(), "tcp", hostport)
	if err != nil {
		http.Error(w, "upstream unreachable", http.StatusBadGateway)
		return
	}

	hj, ok := w.(http.Hijacker)
	if !ok {
		upstream.Close()
		http.Error(w, "connection hijack unsupported", http.StatusBadGateway)
		return
	}
	client, brw, err := hj.Hijack()
	if err != nil {
		upstream.Close()
		return
	}
	defer upstream.Close()
	defer client.Close()

	// brw carries any bytes the server already buffered off the client connection;
	// for a CONNECT it is empty, but writing the 200 through it keeps ordering right.
	if _, err := brw.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}
	brw.Flush()

	copyDone := make(chan struct{}, 2)
	go func() {
		defer func() { copyDone <- struct{}{} }()
		_, _ = io.Copy(upstream, brw) // client -> upstream
	}()
	go func() {
		defer func() { copyDone <- struct{}{} }()
		_, _ = io.Copy(client, upstream) // upstream -> client
	}()
	<-copyDone
	<-copyDone
}

// handleForward is the cleartext, absolute-form path. Here the broker holds the
// request in the clear, so after the destination passes policy it resolves any
// sealed sentinels in the body and headers — the one place a token becomes a
// credential again — and then refuses rather than forward if a token would not
// open, so an opaque handle never reaches the destination.
func (p *Proxy) handleForward(w http.ResponseWriter, r *http.Request) {
	if act, reason := p.policy.CheckContext(r.Context(), r.URL); act == Deny {
		slog.Debug("Egress proxy refused forward", "url", r.URL.String(), "reason", reason)
		http.Error(w, "egress denied: "+reason, http.StatusForbidden)
		return
	}

	var body []byte
	if r.Body != nil {
		b, err := readCapped(r.Body, p.policy.MaxBody())
		if err != nil {
			http.Error(w, "request body exceeds the configured size limit", http.StatusRequestEntityTooLarge)
			return
		}
		body = b
	}

	outBody, _, bodyUnresolved := RestoreString(string(body), p.store)
	outHeader, hdrUnresolved := p.resolveHeader(r.Header)
	if bodyUnresolved+hdrUnresolved > 0 {
		slog.Debug("Egress proxy refused unresolved sentinel", "url", r.URL.String())
		http.Error(w, ErrUnresolvedSentinel.Error(), http.StatusFailedDependency)
		return
	}

	out, err := http.NewRequestWithContext(r.Context(), r.Method, r.URL.String(), nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	maps.Copy(out.Header, r.Header)
	for k := range out.Header {
		out.Header[k] = append([]string{}, out.Header[k]...)
	}
	delete(out.Header, proxyAuthHeader)
	maps.Copy(out.Header, outHeader)
	if len(outBody) > 0 {
		out.Body = io.NopCloser(bytes.NewReader([]byte(outBody)))
		out.ContentLength = int64(len(outBody))
	}

	resp, err := p.inner.RoundTrip(out)
	if err != nil {
		http.Error(w, "egress forward failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	maps.Copy(w.Header(), resp.Header)
	for k := range w.Header() {
		w.Header()[k] = append([]string{}, w.Header()[k]...)
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// resolveHeader rewrites every header value through the sentinel transform,
// returning the rewritten values and the count of tokens that would not open.
func (p *Proxy) resolveHeader(h http.Header) (http.Header, int) {
	out := make(http.Header, len(h))
	unresolved := 0
	for k, vs := range h {
		nv := make([]string, len(vs))
		for i, v := range vs {
			restored, _, u := RestoreString(v, p.store)
			nv[i] = restored
			unresolved += u
		}
		out[k] = nv
	}
	return out, unresolved
}

func constantTimeEq(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v ^= a[i] ^ b[i]
	}
	return v == 0
}

func randomToken() (string, error) {
	var b [18]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// readCapped reads at most max bytes from r, erroring (rather than buffering more)
// once the stream is longer than max. It is the DoS bound on the buffered forward
// path.
func readCapped(r io.Reader, max int64) ([]byte, error) {
	if max <= 0 {
		return io.ReadAll(r)
	}
	var buf bytes.Buffer
	// Read one byte past the cap so an over-long body is detected rather than
	// silently truncated; CopyN stops at n, so a body of exactly max+n bytes shows
	// up as written == max+1 and we refuse it.
	n, err := io.CopyN(&buf, r, max+1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if n > max {
		return nil, errors.New("egress: request body exceeds size limit")
	}
	return buf.Bytes(), nil
}
