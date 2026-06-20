//go:build !cgo

package lang_sql

// GetLanguage returns nil when CGO is disabled.
func GetLanguage() any {
	return nil
}
