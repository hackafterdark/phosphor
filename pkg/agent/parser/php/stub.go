//go:build !cgo

package lang_php

// GetLanguage returns nil when CGO is disabled.
func GetLanguage() any {
	return nil
}
