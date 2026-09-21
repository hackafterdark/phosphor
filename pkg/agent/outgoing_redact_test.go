package agent

import (
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
)

func TestMaskPII(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		in        string
		wantToken string
		wantGone  string
	}{
		{"email", "contact me at jane.doe@Example-Co.com please", "<pii:email>", "jane.doe@Example-Co.com"},
		{"ssn", "my ssn is 123-45-6789 ok", "<pii:ssn>", "123-45-6789"},
		{"credit_card_luhn", "card 4111 1111 1111 1111 here", "<pii:credit-card>", "4111 1111 1111 1111"},
		{"phone", "call +1 (555) 867-5309 now", "<pii:phone>", "5309"},
		{"ipv4", "server at 192.168.1.10 up", "<pii:ip>", "192.168.1.10"},
		{"mac_colon", "nic 00:11:22:33:44:55 up", "<pii:mac>", "00:11:22:33:44:55"},
		{"mac_dash", "nic aa-bb-cc-dd-ee-ff up", "<pii:mac>", "aa-bb-cc-dd-ee-ff"},
		{"ipv6_loopback", "listen on fe80::1 now", "<pii:ip6>", "fe80::1"},
		{"ipv6_compressed", "route 2001:db8:85a3::8a2f up", "<pii:ip6>", "2001:db8:85a3::8a2f"},
		{"ipv6_full", "addr 2001:0db8:85a3:0000:0000:0000:0000:8a2f ok", "<pii:ip6>", "8a2f"},
		{"uuid", "trace 123e4567-88ea-41b5-b2f1-81e2c9d1f1a2 done", "<pii:uuid>", "123e4567-88ea-41b5-b2f1-81e2c9d1f1a2"},
		{"iban_ungrouped", "pay to DE89370400440532013000 now", "<pii:iban>", "DE89370400440532013000"},
		{"iban_grouped", "pay to DE89 3704 0044 0532 0130 00 now", "<pii:iban>", "3704 0044"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := maskPII(c.in)
			require.Contains(t, got, c.wantToken)
			require.NotContains(t, got, c.wantGone)
		})
	}
}

func TestMaskPII_IBAN_InvalidNotMasked(t *testing.T) {
	t.Parallel()

	// Shape-matches the IBAN regex but fails the ISO 13616 mod-97 check (the
	// final digit is perturbed), so the validator must leave it verbatim.
	in := "wire DE89370400440532013001 please"
	got := maskPII(in)
	require.Equal(t, in, got)
	require.NotContains(t, got, "<pii:iban>")
}

func TestMaskPII_BenignHexLikeStringsSurvive(t *testing.T) {
	t.Parallel()

	// Colons and hex that are NOT a MAC/IPv6 must not be masked: a time, a
	// short hex run, and a two-group colon list are the shapes most likely to
	// collide with the IPv6/MAC patterns.
	benign := []string{
		"build finished at 12:34:56",
		"git commit abcd1234",
		"ratio 3:4 and 16:9",
	}
	for _, b := range benign {
		require.Equal(t, b, maskPII(b))
	}
}

func TestSessionAgent_RedactOutgoing_PIIFormsPassTheGate(t *testing.T) {
	t.Parallel()

	// These forms have few or no digits and rely on ':' / long alnum runs, so
	// they are exactly what a too-tight piiGate would silently drop on the wire.
	// With the PII pass on and the secret pass off, each must still reach the
	// masker and produce its sentinel.
	cases := map[string]struct {
		in   string
		want string
		gone string
	}{
		"mac":  {"nic 00:11:22:33:44:55", "<pii:mac>", "00:11:22:33:44:55"},
		"ipv6": {"peer fe80::1 here", "<pii:ip6>", "fe80::1"},
		"ipv4": {"host 10.0.0.5 up", "<pii:ip>", "10.0.0.5"},
		"iban": {"iban DE89 3704 0044 0532 0130 00", "<pii:iban>", "3704 0044"},
	}
	a := &sessionAgent{redactOutgoingPII: true}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.True(t, piiGate(c.in), "piiGate must not drop this form")
			msg := fantasy.Message{
				Role:    fantasy.MessageRoleUser,
				Content: []fantasy.MessagePart{fantasy.TextPart{Text: c.in}},
			}
			out := a.redactOutgoingMessages([]fantasy.Message{msg})
			require.Contains(t, textOf(out[0]), c.want)
			require.NotContains(t, textOf(out[0]), c.gone)
		})
	}
}

func TestMaskPII_LeavesBenignText(t *testing.T) {
	t.Parallel()

	benign := []string{
		"package main\n\nfunc main() { fmt.Println(\"hi\") }",
		"x := 42\ny := x * 2",
		"// no pii here at all",
	}
	for _, b := range benign {
		require.Equal(t, b, maskPII(b))
	}
}

func TestLuhnValid(t *testing.T) {
	t.Parallel()
	require.True(t, luhnValid("4111111111111111"))
	require.True(t, luhnValid("4111 1111 1111 1111"))
	require.False(t, luhnValid("4111111111111112")) // fails checksum
	require.False(t, luhnValid("12345678901"))      // too short (<13)
}

func TestSessionAgent_RedactOutgoingMessages(t *testing.T) {
	t.Parallel()

	const secret = "AKIAIOSVPK253PIP5TGP"
	userWithSecret := fantasy.Message{
		Role:    fantasy.MessageRoleUser,
		Content: []fantasy.MessagePart{fantasy.TextPart{Text: "AWS_ACCESS_KEY_ID := \"" + secret + "\""}},
	}
	userWithEmail := fantasy.Message{
		Role:    fantasy.MessageRoleUser,
		Content: []fantasy.MessagePart{fantasy.TextPart{Text: "email is bob@corp.io for the report"}},
	}

	// Both passes enabled: secret masked and email masked.
	a := &sessionAgent{redactOutgoingSecrets: true, redactOutgoingPII: true}
	out := a.redactOutgoingMessages([]fantasy.Message{userWithSecret, userWithEmail})
	require.NotContains(t, textOf(out[0]), secret)
	require.Contains(t, textOf(out[0]), "<redacted:gitleaks:")
	require.NotContains(t, textOf(out[1]), "bob@corp.io")
	require.Contains(t, textOf(out[1]), "<pii:email>")

	// The input messages must not have been mutated (fantasy owns them).
	require.Contains(t, textOf(userWithSecret), secret, "input mutated")
	require.Contains(t, textOf(userWithEmail), "bob@corp.io", "input mutated")
}

func TestSessionAgent_RedactOutgoing_DisabledIsPassthrough(t *testing.T) {
	t.Parallel()

	const secret = "AKIAIOSVPK253PIP5TGP"
	msg := fantasy.Message{
		Role:    fantasy.MessageRoleUser,
		Content: []fantasy.MessagePart{fantasy.TextPart{Text: "AWS_ACCESS_KEY_ID := \"" + secret + "\""}},
	}

	// Disabled: content is returned verbatim (identity, not even copied).
	a := &sessionAgent{}
	in := []fantasy.Message{msg}
	out := a.redactOutgoingMessages(in)
	require.Equal(t, in, out)
	require.Contains(t, textOf(out[0]), secret)
}

func TestSessionAgent_RedactOutgoing_SecretsOnlyPIIStays(t *testing.T) {
	t.Parallel()

	a := &sessionAgent{redactOutgoingSecrets: true, redactOutgoingPII: false}
	msg := fantasy.Message{
		Role:    fantasy.MessageRoleUser,
		Content: []fantasy.MessagePart{fantasy.TextPart{Text: "email is alice@corp.io"}},
	}
	out := a.redactOutgoingMessages([]fantasy.Message{msg})
	require.Contains(t, textOf(out[0]), "alice@corp.io", "PII must survive when the PII pass is off")
	require.NotContains(t, textOf(out[0]), "<pii:email>")
}

// textOf concatenates the text parts of a message for assertions.
func textOf(m fantasy.Message) string {
	var sb string
	for _, p := range m.Content {
		if tp, ok := p.(fantasy.TextPart); ok {
			sb += tp.Text
		}
	}
	return sb
}

func TestSessionAgent_RedactOutgoing_WireSecretsForcedOverridesDisable(t *testing.T) {
	t.Parallel()

	// redact_outgoing_secrets is off but the hard-boundary force is on: the
	// provider wire must still mask the secret, which is the whole point of the
	// force path (a user turning the opt-in off cannot leak a key to the provider).
	const secret = "AKIAIOSVPK253PIP5TGP"
	msg := fantasy.Message{
		Role:    fantasy.MessageRoleUser,
		Content: []fantasy.MessagePart{fantasy.TextPart{Text: "AWS_ACCESS_KEY_ID := \"" + secret + "\""}},
	}

	a := &sessionAgent{redactOutgoingSecrets: false, wireSecretsForced: true}
	out := a.redactOutgoingMessages([]fantasy.Message{msg})
	require.NotContains(t, textOf(out[0]), secret, "forced wire boundary must mask despite the disabled opt-in")
	require.Contains(t, textOf(out[0]), "<redacted:gitleaks:")
}

func TestSessionAgent_RedactOutgoing_ForceOffAndSecretsOffIsPassthrough(t *testing.T) {
	t.Parallel()

	// The one way to fully disable wire secret masking is to also turn the force
	// boundary off; then the wire is a pure passthrough.
	const secret = "AKIAIOSVPK253PIP5TGP"
	msg := fantasy.Message{
		Role:    fantasy.MessageRoleUser,
		Content: []fantasy.MessagePart{fantasy.TextPart{Text: "AWS_ACCESS_KEY_ID := \"" + secret + "\""}},
	}

	a := &sessionAgent{redactOutgoingSecrets: false, wireSecretsForced: false}
	out := a.redactOutgoingMessages([]fantasy.Message{msg})
	require.Contains(t, textOf(out[0]), secret, "with force off and secrets off nothing is masked")
}

func TestSessionAgent_OutgoingRedactionEnabled(t *testing.T) {
	t.Parallel()

	// Truth table for the single gate the PrepareStep call site and
	// redactOutgoingMessages both consult. The force-only row is the regression
	// guard: previously the call site looked only at redactOutgoingSecrets, so a
	// user who set redact_outgoing_secrets=false left the wire unmasked even with
	// the hard force boundary on.
	cases := []struct {
		name    string
		secrets bool
		pii     bool
		force   bool
		want    bool
	}{
		{"all_off", false, false, false, false},
		{"force_only", false, false, true, true},
		{"optin_only", true, false, false, true},
		{"pii_only", false, true, false, true},
		{"optin_and_force", true, false, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			a := &sessionAgent{
				redactOutgoingSecrets: c.secrets,
				redactOutgoingPII:     c.pii,
				wireSecretsForced:     c.force,
			}
			require.Equal(t, c.want, a.outgoingRedactionEnabled())
		})
	}
}

func TestSessionAgent_OutgoingRedaction_ForceOnlyMasksThroughCallSite(t *testing.T) {
	t.Parallel()

	// End-to-end at the decision boundary: when only the force boundary is on,
	// outgoingRedactionEnabled must be true so the wire mask actually runs, and
	// the secret must not survive to the provider.
	const secret = "AKIAIOSVPK253PIP5TGP"
	a := &sessionAgent{redactOutgoingSecrets: false, wireSecretsForced: true}
	require.True(t, a.outgoingRedactionEnabled(), "call site must not skip the mask when force is on")

	msg := fantasy.Message{
		Role:    fantasy.MessageRoleUser,
		Content: []fantasy.MessagePart{fantasy.TextPart{Text: "AWS_ACCESS_KEY_ID := \"" + secret + "\""}},
	}
	out := a.redactOutgoingMessages([]fantasy.Message{msg})
	require.NotContains(t, textOf(out[0]), secret)
	require.Contains(t, textOf(out[0]), "<redacted:gitleaks:")
}
