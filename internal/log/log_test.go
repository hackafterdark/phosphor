package log

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCompileFilters(t *testing.T) {
	t.Parallel()

	filters, err := compileFilters([]LogFilter{{Field: "message", Pattern: `Failed to save`}})
	require.NoError(t, err)
	require.Len(t, filters, 1)

	_, err = compileFilters([]LogFilter{{Field: "message", Pattern: `(unclosed`}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "rejected")

	_, err = compileFilters([]LogFilter{{Field: "message", Pattern: `(?:[A-Za-z]+)+=[A-Za-z]+`}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "ReDoS")
}
