package tools

import (
	"strings"
	"testing"

	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/hackafterdark/phosphor/pkg/secrets"
	"github.com/stretchr/testify/require"
)

// These tests prove the known-value registry erases the exact credential strings
// Phosphor loaded, independently of the gitleaks value scanner. They register a
// token that matches no vendor/regex rule and turn the gitleaks pass off, so the
// only layer that can remove it is the registry itself. They mutate process-global
// state (the registry and the redaction policy), so they do not run in parallel.

func TestRegistryScrub_WireIndependentOfGitleaks(t *testing.T) {
	useRedaction(t, RedactionPolicyOptions{SecretsEnabled: ptrBool(false)})
	require.False(t, SecretsEnabled(), "gitleaks must be off to isolate the registry")

	const token = "zq-RegW1re-9f3c1a7b2e"
	config.RegisterSecret(token)

	out := RedactSecretsForWire("Authorization: Bearer " + token + "\nnext line")
	require.NotContains(t, out, token)
	require.Contains(t, out, "next line")
}

func TestRegistryScrub_ToolPathFiresWithoutGitleaks(t *testing.T) {
	useRedaction(t, RedactionPolicyOptions{SecretsEnabled: ptrBool(false)})

	const token = "zq-RegTool-4b7d2c8e1f"
	secrets.Register(token)

	// A benign source-file path would normally take the code-file FP path; the
	// registry runs before that gating so the value is erased regardless.
	out := redactSecretsForTool("loaded key = "+token, "internal/app/app.go", "view")
	require.NotContains(t, out, token)
}

func TestRegistryScrub_IgnoresUnexpandedTemplate(t *testing.T) {
	useRedaction(t, RedactionPolicyOptions{SecretsEnabled: ptrBool(false)})

	// A still-templated config value is not a real secret; RegisterSecret skips it,
	// so the literal placeholder text survives redaction untouched.
	const tmpl = "$PROVIDER_API_KEY_PLACEHOLDER"
	config.RegisterSecret(tmpl)

	require.True(t, strings.Contains(RedactSecretsForWire("key="+tmpl), tmpl),
		"unexpanded template placeholders should not be scrubbed")
}

func TestRegistryScrub_DisabledLeavesTextIntact(t *testing.T) {
	useRedaction(t, RedactionPolicyOptions{SecretsEnabled: ptrBool(false)})

	const token = "zq-RegOff-7e2a9c4d6b"
	secrets.Register(token)
	secrets.SetEnabled(false)
	t.Cleanup(func() { secrets.SetEnabled(true) })

	require.Contains(t, RedactSecretsForWire("v="+token), token)
}
