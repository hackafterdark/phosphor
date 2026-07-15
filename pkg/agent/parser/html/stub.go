//go:build !cgo

package lang_html

// GetLanguage returns nil when CGO is disabled.
func GetLanguage() any {
	return nil
}
