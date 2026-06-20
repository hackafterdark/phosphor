//go:build !cgo

package lang_bash

// GetLanguage returns nil when CGO is disabled.
func GetLanguage() any {
	return nil
}
