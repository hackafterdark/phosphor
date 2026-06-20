# copy_header.ps1 — Helper to vendor tree-sitter parser header
#
# This script copies the parser.h header file from the go-tree-sitter module
# in your local Go module cache into the grammars/include/tree_sitter directory.
# This ensures CGO compilation has local access to the correct version of the header.

$ErrorActionPreference = "Continue"
$modcache = go env GOMODCACHE
$header = "$modcache\github.com\tree-sitter\go-tree-sitter@v0.25.0\include\tree_sitter\parser.h"
Copy-Item $header grammars/include/tree_sitter/parser.h -Force
Write-Host "Copied parser.h from $header"
