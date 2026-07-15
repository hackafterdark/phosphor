//go:build !cgo

package lang_rust

// GetLanguage returns nil when CGO is disabled.
func GetLanguage() any {
	return nil
}
