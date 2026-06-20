//go:build !cgo

package lang_ruby

// GetLanguage returns nil when CGO is disabled.
func GetLanguage() any {
	return nil
}
