//go:build !cgo

package lang_hcl

// GetLanguage returns nil when CGO is disabled.
func GetLanguage() any {
	return nil
}
