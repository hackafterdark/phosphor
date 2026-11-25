package xai

// authBase is the OAuth issuer. It is a var (not a const) so tests can
// redirect requests to a stub server; production uses the real host.
var authBase = "https://auth.x.ai"

const (
	// clientID is the public Grok-CLI client ID. It is not a secret;
	// third-party clients reuse it because xAI does not offer
	// self-service registration for desktop/CLI apps.
	clientID = "b1a00492-073a-47ea-816f-4c329264a828"

	// signupURL points users at subscribing when auth indicates the
	// account lacks access.
	signupURL = "https://x.ai/grok"
)

// SignupURL is the URL shown to users whose account cannot access the API.
const SignupURL = signupURL

func deviceCodeURL() string  { return authBase + "/oauth2/device/code" }
func accessTokenURL() string { return authBase + "/oauth2/token" }
