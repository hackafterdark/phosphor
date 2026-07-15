//go:build !cgo

package lang_go

// GetLanguage returns nil when CGO is disabled.
func GetLanguage() any {
	return nil
}
