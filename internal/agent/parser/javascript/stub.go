//go:build !cgo

package lang_javascript

// GetLanguage returns nil when CGO is disabled.
func GetLanguage() any {
	return nil
}
