package openai

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMessageToDelta(t *testing.T) {
	t.Run("empty delta text", func(t *testing.T) {
		delta := messageToDelta("")
		assert.Equal(t, "assistant", delta.Role)
		assert.Nil(t, delta.Content)
	})

	t.Run("non-empty delta text", func(t *testing.T) {
		delta := messageToDelta("hello")
		assert.Equal(t, "assistant", delta.Role)
		assert.JSONEq(t, `"hello"`, string(delta.Content))
	})
}
