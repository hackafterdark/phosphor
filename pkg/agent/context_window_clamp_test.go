package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClampMaxOutputTokens(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		requested     int64
		currentTokens int64
		contextWindow int64
		want          int64
	}{
		{
			"unknown context window returns requested unchanged",
			131072, 133435, 0,
			131072,
		},
		{
			"negative current tokens (unmeasured) returns requested unchanged",
			131072, -1, 262144,
			131072,
		},
		{
			"plenty of headroom leaves requested untouched",
			4096, 1000, 262144,
			4096,
		},
		{
			"the confirmed live scenario: clamps down to fit",
			131072, 133435, 262144,
			262144 - 133435 - contextWindowSafetyMargin,
		},
		{
			"no headroom left at all returns zero so the caller omits the field",
			131072, 262144, 262144,
			0,
		},
		{
			"headroom exactly at the requested amount is not clamped",
			1000, 261144 - contextWindowSafetyMargin, 262144,
			1000,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := clampMaxOutputTokens(c.requested, c.currentTokens, c.contextWindow)
			require.Equal(t, c.want, got)
		})
	}
}
