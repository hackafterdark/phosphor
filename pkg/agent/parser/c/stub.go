//go:build !cgo

package lang_c

// GetLanguage returns nil when CGO is disabled.
func GetLanguage() any {
	return nil
}
