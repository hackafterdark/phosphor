//go:build !cgo

package lang_json

// GetLanguage returns nil when CGO is disabled.
func GetLanguage() any {
	return nil
}
