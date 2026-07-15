//go:build !cgo

package lang_typescript

// GetLanguage returns nil when CGO is disabled.
func GetLanguage() any {
	return nil
}
