//go:build !cgo

package lang_scala

// GetLanguage returns nil when CGO is disabled.
func GetLanguage() any {
	return nil
}
