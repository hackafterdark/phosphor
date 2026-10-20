package server

import _ "embed"

//go:embed memorygraph/view.html
var memoryGraphHTML string

//go:embed memorygraph/cosmos.min.js
var cosmosGraphJS string
