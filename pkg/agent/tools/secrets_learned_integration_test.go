package tools

import (
	"testing"

	"github.com/hackafterdark/phosphor/pkg/secrets"
	"github.com/stretchr/testify/require"
	"github.com/zricethezav/gitleaks/v8/report"
)

// The two integration points of the hashed learned-secret memory, driven directly
// so the contract is asserted independently of which exact gitleaks rule fires:
//
//   (1) redactSecretsAt records the keyed hash of everything it judges sensitive;
//   (2) filterFindings, which drops the generic KEY=value family on source-code
//       reads, restores a dropped finding whose value was already learned — so a
//       secret first seen as a confident hit stays scrubbed on a later source-file
//       read where its shape alone would have been treated as false-positive noise.
//
// These mutate process-global state (the learned set and the policy snapshot) so
// they intentionally do not run in parallel.

const (
	learnedGenericValue = "L3arn3dG3n3ricV4lu3ABC123"
	unknownGenericValue = "UnKn0wnG3n3ricV4lu3XYZ789"
)

func TestRedactSecretsAt_RecordsJudgedSecrets(t *testing.T) {
	useRedaction(t, RedactionPolicyOptions{}) // secure defaults: learned memory on.
	secrets.ResetLearned()
	t.Cleanup(secrets.ResetLearned)

	before := secrets.LearnedLen()
	got := redactSecretsAt("aws_access_key_id = "+testAWSKeyID, "app/config.go", "view")
	require.NotContains(t, got, testAWSKeyID, "the AWS key must be redacted")
	require.Contains(t, got, sentinelPrefix)

	require.Greater(t, secrets.LearnedLen(), before,
		"a scrubbed secret must have its keyed hash remembered")
}

func TestRedactSecretsAt_ToggleOffRecordsNothing(t *testing.T) {
	useRedaction(t, RedactionPolicyOptions{LearnedSecretMemoryEnabled: ptrBool(false)})
	secrets.ResetLearned()
	t.Cleanup(secrets.ResetLearned)

	before := secrets.LearnedLen()
	got := redactSecretsAt("aws_access_key_id = "+testAWSKeyID, "app/config.go", "view")
	require.NotContains(t, got, testAWSKeyID, "redaction itself is unaffected by the toggle")
	require.Equal(t, before, secrets.LearnedLen(),
		"a disabled learned memory records no new digests")
}

func TestFilterFindings_CodeFileKeepsLearnedGenericDropsUnknown(t *testing.T) {
	useRedaction(t, RedactionPolicyOptions{}) // learned memory + code-file FP mode on.
	secrets.ResetLearned()
	t.Cleanup(secrets.ResetLearned)

	learned := report.Finding{RuleID: "generic-api-key", Match: learnedGenericValue}
	unknown := report.Finding{RuleID: "generic-secret", Match: unknownGenericValue}
	precise := report.Finding{RuleID: "aws-access-token", Match: testAWSKeyID}

	secrets.Learn(learnedGenericValue)

	in := []report.Finding{learned, unknown, precise}
	out := filterFindings(append([]report.Finding{}, in...), ScanCodeFile)

	require.Len(t, out, 2, "the learned generic hit and the high-precision hit survive")
	require.Contains(t, out, learned, "a generic finding whose value was already learned is restored")
	require.NotContains(t, out, unknown, "an un-learned generic finding is still suppressed on source code")
	require.Contains(t, out, precise)
}

func TestFilterFindings_ToggleOffRestoresNoGenerics(t *testing.T) {
	useRedaction(t, RedactionPolicyOptions{LearnedSecretMemoryEnabled: ptrBool(false)})
	secrets.ResetLearned()
	t.Cleanup(secrets.ResetLearned)

	learned := report.Finding{RuleID: "generic-api-key", Match: learnedGenericValue}
	precise := report.Finding{RuleID: "aws-access-token", Match: testAWSKeyID}

	secrets.Learn(learnedGenericValue) // present, but the consult is gated off.

	out := filterFindings([]report.Finding{learned, precise}, ScanCodeFile)
	require.Len(t, out, 1, "with the memory off, the generic family is dropped as before")
	require.NotContains(t, out, learned)
	require.Contains(t, out, precise)
}

func TestFilterFindings_FullModeUnaffected(t *testing.T) {
	useRedaction(t, RedactionPolicyOptions{})
	secrets.ResetLearned()
	t.Cleanup(secrets.ResetLearned)

	in := []report.Finding{
		{RuleID: "generic-api-key", Match: learnedGenericValue},
		{RuleID: "generic-secret", Match: unknownGenericValue},
		{RuleID: "aws-access-token", Match: testAWSKeyID},
	}
	out := filterFindings(append([]report.Finding{}, in...), ScanFull)
	require.Len(t, out, len(in), "full mode never drops a finding on learned state")
}
