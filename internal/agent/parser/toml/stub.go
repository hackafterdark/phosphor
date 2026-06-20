//go:build !cgo

package lang_toml

// GetLanguage returns nil when CGO is disabled.
func GetLanguage() any {
	return nil
}
