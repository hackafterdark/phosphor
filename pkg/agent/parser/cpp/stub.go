//go:build !cgo

package lang_cpp

// GetLanguage returns nil when CGO is disabled.
func GetLanguage() any {
	return nil
}
