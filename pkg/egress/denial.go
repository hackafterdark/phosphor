package egress

import (
	"fmt"
	"strings"
)

// Denial advice turns a broker denial into the exact config delta that
// would allow it, mirroring the advisor-loop posture of
// _PLANS/NATIVE_SANDBOX.md §5 lesson 5: the denial response carries
// next_steps, the advisor recommends, and the human always approves the
// phosphor.json write. The advisor never writes config itself.

// denialHostHint returns the allowlist entry shape for a denied host.
// The fragment uses the wildcard form only for the apex domain; an exact
// host stays exact so the human reviews the narrowest change.
func denialHostHint(host string) string {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	return host
}

// DenialAdvice renders the config fragment that would allow hostport to
// transit the broker. It is advice for a human reviewer, not a rule the
// agent may apply itself.
func DenialAdvice(hostport string) string {
	host, port := splitHostPort(hostport)
	host = stripHostPort(host)
	hint := denialHostHint(host)
	if port != "" && port != "443" {
		hint = host + ":" + port
	}
	return fmt.Sprintf(
		"To allow this destination, a human should add %q to security.egress_isolation.allowed_hosts in phosphor.json:\n"+
			`  {"security": {"egress_isolation": {"allowed_hosts": [%q]}}}`+"\n"+
			"The advisor recommends; it never writes config. Request this change from the operator.",
		hint, hint,
	)
}
