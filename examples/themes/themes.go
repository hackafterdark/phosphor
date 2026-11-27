// Package themes embeds the bundled example themes so they can be
// compiled into the binary and selected by name.
package themes

import "embed"

//go:embed *.yaml
var FS embed.FS
