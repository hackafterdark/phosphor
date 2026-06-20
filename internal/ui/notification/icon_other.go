//go:build !darwin

package notification

import (
	_ "embed"
)

//go:embed phosphor-icon-solo.png
var Icon []byte
