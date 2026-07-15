//go:build !cgo

package lang_python

// GetLanguage returns nil when CGO is disabled.
func GetLanguage() any {
	return nil
}
