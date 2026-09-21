package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// decodeToMap is a small helper so the assertions can reason about the
// re-marshalled document structure rather than stringifying it.
func decodeToMap(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(s), &m))
	return m
}

func TestRedactSecretJSONFields_DropsSecretNamedKeys(t *testing.T) {
	in := `{"user":"alice","SecretString":"s_very_secret_value","SessionToken":"tok_abc123","Credentials":{"password":"p@ssw0rd"},"PrivateKey":"-----BEGIN RSA PRIVATE KEY-----"}`

	out, changed := redactSecretJSONFields(in)
	require.True(t, changed, "a document with secret keys must report a change")
	require.NotContains(t, out, "s_very_secret_value")
	require.NotContains(t, out, "tok_abc123")
	require.NotContains(t, out, "p@ssw0rd")
	require.NotContains(t, out, "BEGIN RSA PRIVATE KEY")

	m := decodeToMap(t, out)
	require.Equal(t, "alice", m["user"], "a benign sibling key is preserved")
	require.Equal(t, jsonKeySentinel, m["SecretString"], "the value is replaced by the sentinel")
	// The whole sub-object under a secret key is replaced, not just its members.
	require.Equal(t, jsonKeySentinel, m["Credentials"])
}

func TestRedactSecretJSONFields_NestedAndArrays(t *testing.T) {
	in := `{"meta":{"ok":true},"items":[{"name":"a","api_key":"AKsecret123"},{"name":"b"}],"count":2}`

	out, changed := redactSecretJSONFields(in)
	require.True(t, changed)
	require.NotContains(t, out, "AKsecret123")

	m := decodeToMap(t, out)
	items, ok := m["items"].([]any)
	require.True(t, ok)
	first := items[0].(map[string]any)
	require.Equal(t, "a", first["name"], "the sibling field survives")
	require.Equal(t, jsonKeySentinel, first["api_key"])
	second := items[1].(map[string]any)
	require.Equal(t, "b", second["name"])
}

func TestRedactSecretJSONFields_NonJSONIsUntouched(t *testing.T) {
	// Ordinary command output: not JSON, must pass through byte-for-byte.
	logs := "build ok\n22 tests passed\nwarning: something"
	out, changed := redactSecretJSONFields(logs)
	require.False(t, changed)
	require.Equal(t, logs, out)

	// A line that merely starts with '{' but is not valid JSON is also left alone.
	bad := "{not json at all"
	out2, changed2 := redactSecretJSONFields(bad)
	require.False(t, changed2)
	require.Equal(t, bad, out2)
}

func TestRedactSecretJSONFields_TrailingGarbageIsNotReshaped(t *testing.T) {
	// JSON glued to trailing content is not a bare document; refuse to reshape it.
	mixed := `{"access_token":"tok_999"}` + "\ntrailing log line"
	out, changed := redactSecretJSONFields(mixed)
	require.False(t, changed, "trailing non-JSON content must veto the rewrite")
	require.Equal(t, mixed, out, "the original bytes are preserved when we bail")
}

func TestRedactSecretJSONFields_NoSecretKeysLeavesBytesIntact(t *testing.T) {
	// No secret-shaped key: the document is returned verbatim, not re-marshalled,
	// so key ordering and spacing are unchanged.
	in := `{"sort_key":"id","cache_key":"v1","primary_key":42,"title":"hello"}`
	out, changed := redactSecretJSONFields(in)
	require.False(t, changed)
	require.Equal(t, in, out)
}

func TestRedactSecretJSONFields_PreservesNumbersAndBools(t *testing.T) {
	in := `{"port":5432,"enabled":true,"ratio":1.5,"aws_secret_access_key":"hidden","tag":"x"}`
	out, changed := redactSecretJSONFields(in)
	require.True(t, changed)
	require.NotContains(t, out, "hidden")
	m := decodeToMap(t, out)
	require.Equal(t, float64(5432), m["port"])
	require.Equal(t, true, m["enabled"])
	require.Equal(t, "x", m["tag"])
}

func TestRedactSecretJSONFields_DisabledIsPassthrough(t *testing.T) {
	useRedaction(t, RedactionPolicyOptions{JSONKeyRedactionEnabled: ptrBool(false)})
	in := `{"SecretString":"s_secret"}`
	out, changed := redactSecretJSONFields(in)
	require.False(t, changed)
	require.Equal(t, in, out)
}

func TestRedactJSONForTool_ReportsSentinel(t *testing.T) {
	in := `{"SecretString":"s_secret_value","user":"bob"}`
	out := RedactJSONForTool(in, "mcp")
	require.NotContains(t, out, "s_secret_value")
	require.Contains(t, out, jsonKeySentinel)
	require.Contains(t, out, "bob", "benign fields remain visible to the model")
}

func TestIsSecretJSONKey(t *testing.T) {
	pol := defaultRedactionPolicy()
	require.True(t, pol.isSecretJSONKey("SecretString"))
	require.True(t, pol.isSecretJSONKey("session_token"))
	require.True(t, pol.isSecretJSONKey("DB_PASSWORD"), "case-insensitive + suffix match")
	require.True(t, pol.isSecretJSONKey("GITHUB_TOKEN"), "suffix match on _token")
	require.False(t, pol.isSecretJSONKey("primary_key"), "bare key is not a secret")
	require.False(t, pol.isSecretJSONKey("sort_key"))
	require.False(t, pol.isSecretJSONKey(""))
	require.False(t, pol.isSecretJSONKey("username"))
}

func TestIsSecretJSONKey_ExtendedByConfig(t *testing.T) {
	useRedaction(t, RedactionPolicyOptions{ExtraJSONSecretKeys: []string{"internal_blob"}})
	pol := currentRedactionPolicy()
	require.True(t, pol.isSecretJSONKey("internal_blob"))
	require.True(t, pol.isSecretJSONKey("INTERNAL_BLOB"), "operator keys are matched case-insensitively")
}

func TestRedactSecretJSONFields_AlreadySentinelIsNotReCounted(t *testing.T) {
	in := `{"SecretString":"` + jsonKeySentinel + `"}`
	out, changed := redactSecretJSONFields(in)
	require.False(t, changed, "an already-dropped field is not a new change")
	require.Equal(t, in, out)
}

func TestRedactSecretJSONFields_TopLevelArray(t *testing.T) {
	in := `[{"k":"keep","token":"tok_1"},{"k":"keep2"}]`
	out, changed := redactSecretJSONFields(in)
	require.True(t, changed)
	require.NotContains(t, out, "tok_1")
	require.Contains(t, out, "keep")
	require.Contains(t, out, "keep2")
}

func TestRedactSecretJSONFields_EmptyAndOversize(t *testing.T) {
	_, changed := redactSecretJSONFields("")
	require.False(t, changed)

	huge := `{"token":"` + strings.Repeat("a", jsonKeyScanLimit+1) + `"}`
	_, changed2 := redactSecretJSONFields(huge)
	require.False(t, changed2, "an over-cap payload is skipped rather than parsed")
}
