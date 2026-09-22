//go:build (darwin || linux || windows || freebsd || openbsd || netbsd) && !ios && !android

package clipboard

import (
	"context"
	"fmt"

	"golang.design/x/clipboard"
)

func initClipboard() error {
	return clipboard.Init()
}

func writeText(text string) {
	_, _ = clipboard.Write(context.Background(), clipboard.FmtText, []byte(text))
}

func read(f Format) ([]byte, error) {
	var format clipboard.Format
	switch f {
	case FormatText:
		format = clipboard.FmtText
	case FormatImage:
		format = clipboard.FmtImage
	default:
		return nil, ErrEmpty
	}
	data, err := clipboard.Read(context.Background(), format)
	if err != nil {
		return nil, fmt.Errorf("failed to read clipboard: %w", err)
	}
	if data == nil {
		return nil, ErrEmpty
	}
	return data, nil
}
