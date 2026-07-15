//go:build !cgo

package lang_css

// GetLanguage returns nil when CGO is disabled.
func GetLanguage() any {
	return nil
}
