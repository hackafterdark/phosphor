package server

import _ "embed"

//go:embed mermaid/mermaid.min.js
var mermaidJS string

//go:embed mermaid/mermaid.html
var mermaidHTMLTemplate string