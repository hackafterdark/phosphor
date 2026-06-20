# patch_all_grammars.ps1 — Patch tree-sitter grammars for compatibility
#
# This script walks all parser.c files in the grammars/ directory and applies patches:
# 1. Updates version definition from .version to .abi_version for ABI compatibility.
# 2. Renames TSFieldMapSlice to TSMapSlice.
# 3. Renames TSLexMode to TSLexerMode.
# This is necessary because some upstream grammars use types or fields that are
# incompatible with the go-tree-sitter package version being compiled.

$ErrorActionPreference = "Continue"
$grammars = Get-ChildItem grammars/*/src/parser.c -ErrorAction SilentlyContinue
foreach ($f in $grammars) {
    $content = Get-Content $f.FullName -Raw
    $changed = $false
    
    if ($content -match '\.version = LANGUAGE_VERSION') {
        $content = $content -replace '\.version = LANGUAGE_VERSION', '.abi_version = LANGUAGE_VERSION'
        $changed = $true
    }
    
    if ($content -match 'TSFieldMapSlice') {
        $content = $content -replace 'TSFieldMapSlice', 'TSMapSlice'
        $changed = $true
    }
    
    if ($content -match 'TSLexMode') {
        $content = $content -replace 'TSLexMode', 'TSLexerMode'
        $changed = $true
    }
    
    if ($changed) {
        $content | Set-Content $f.FullName
        Write-Host "Patched $($f.Directory.Name)/parser.c"
    }
}
