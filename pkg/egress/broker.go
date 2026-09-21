package egress

import (
	"net/http"
	"sync"
)

// The process-wide egress broker. It is the single place the operator's
// configuration turns the opt-in credential-isolation tier on: once the
// coordinator has frozen the net-policy it calls [Start] here, the loopback
// [Proxy] begins accepting, production transports consult the same policy, and a
// bash child process is pointed at it through [SubprocessProxyEnv]. Like the
// [SecretStore] singleton, this is a frozen-at-startup control: a prompt-injected
// tool that mutates the environment or re-reads config mid-session cannot arm or
// disarm it, because nothing consults live configuration per call.
//
// The whole struct is inert until [Start] is called with an enabled policy; the
// shipped default leaves every field at its zero value so an ordinary Phosphor
// session never binds the loopback listener, wraps a transport, or injects any
// proxy environment.
type brokerState struct {
	mu      sync.RWMutex
	proxy   *Proxy
	policy  Policy
	route   bool
	active  bool
	failed  bool
	lastErr error
}

var broker = &brokerState{}

// Start begins the process-wide egress broker under the given net-policy. It is
// idempotent and frozen once started: the first call with an enabled policy binds
// the loopback listener and records whether subprocesses should be routed through
// it; every later call is a no-op, so repeated agent builds and model updates that
// rebuild the tools neither spawn a second listener nor rebind a policy that was
// frozen at startup. A call with a disabled policy records nothing and starts
// nothing. Disabling an already-started broker mid-session is deliberately
// unsupported — the same "cannot silently disarm" discipline the read-path
// snapshot follows.
//
// routeSubprocesses is latched alongside the policy and consulted by
// [SubprocessProxyEnv]; it has no effect unless the broker is active.
func Start(policy Policy, routeSubprocesses bool) error {
	broker.mu.Lock()
	defer broker.mu.Unlock()
	if broker.active {
		return nil
	}
	if broker.failed {
		return broker.lastErr
	}
	if !policy.Enabled {
		return nil
	}
	p, err := NewProxy(policy, nil)
	if err != nil {
		broker.failed = true
		broker.lastErr = err
		return err
	}
	broker.proxy = p
	broker.policy = policy
	broker.route = routeSubprocesses
	broker.active = true
	return nil
}

// SubprocessProxyEnv returns the environment entries that route a well-behaved
// bash child through the broker, or nil when isolation is off, the broker was
// never started, or subprocess routing is disabled. Callers append the result
// to the child environment verbatim; the nil return keeps the default bash path
// byte-for-byte unchanged.
func SubprocessProxyEnv() []string {
	broker.mu.RLock()
	defer broker.mu.RUnlock()
	if !broker.active || !broker.route || broker.proxy == nil {
		return nil
	}
	return broker.proxy.ProxyEnv()
}

// BrokerActive reports whether the process-wide broker has been started. It is
// informational (tests, diagnostics); nothing on the hot path branches on it.
func BrokerActive() bool {
	broker.mu.RLock()
	defer broker.mu.RUnlock()
	return broker.active
}

func brokerPolicy() (Policy, bool, bool) {
	broker.mu.RLock()
	defer broker.mu.RUnlock()
	return broker.policy, broker.active, broker.failed
}

// WrapClient returns an isolated clone of c whose transport is brokered when the
// process-wide egress tier is active. A nil client is returned unchanged, and a
// client whose transport has already been brokered is returned unchanged so repeated
// tool construction does not nest policies or duplicate sentinel resolution.
func WrapClient(c *http.Client) *http.Client {
	if c == nil {
		return nil
	}
	if _, wrapped := c.Transport.(*RoundTripperPolicy); wrapped {
		return c
	}
	cloned := &http.Client{
		Jar:           c.Jar,
		Timeout:       c.Timeout,
		Transport:     Transport(c.Transport),
		CheckRedirect: c.CheckRedirect,
	}
	return cloned
}

// ResetBrokerForTest stops any started broker and clears the frozen state so a
// later [Start] can bind again. Intended for tests only.
func ResetBrokerForTest() {
	broker.mu.Lock()
	defer broker.mu.Unlock()
	if broker.proxy != nil {
		_ = broker.proxy.Close()
	}
	broker.proxy = nil
	broker.policy = Policy{}
	broker.route = false
	broker.active = false
	broker.failed = false
	broker.lastErr = nil
}
