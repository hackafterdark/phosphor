package proto_test

import (
	"encoding/json"
	"testing"

	"github.com/hackafterdark/phosphor/internal/proto"
	"github.com/stretchr/testify/require"
)

func TestAttachmentJSON(t *testing.T) {
	t.Parallel()

	original := proto.Attachment{
		FilePath: "test_path.png",
		FileName: "test_path.png",
		MimeType: "image/png",
		Content:  []byte("fake-binary-image-data-here-12345"),
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	t.Logf("Serialized JSON: %s", string(data))

	var decoded proto.Attachment
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	require.Equal(t, original.FilePath, decoded.FilePath)
	require.Equal(t, original.FileName, decoded.FileName)
	require.Equal(t, original.MimeType, decoded.MimeType)
	require.Equal(t, original.Content, decoded.Content)
}
