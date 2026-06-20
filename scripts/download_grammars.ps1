# download_grammars.ps1 — Vendor tree-sitter C sources for deterministic builds
# Usage: powershell -ExecutionPolicy Bypass -File download_grammars.ps1
#
# This script downloads the raw parser.c and scanner.c source files from upstream
# tree-sitter repositories for all 19 supported languages, saving them into the 
# local grammars/ directory. This ensures deterministic CGO builds without needing
# external runtime/system grammar installations.

$ErrorActionPreference = "Stop"
$base = Split-Path -Parent $MyInvocation.MyCommand.Path
$parent = Split-Path -Parent $base
$GrammarsDir = "$parent\grammars"

Write-Host "=== Tree-Sitter Grammar Vendor Script ===" -ForegroundColor Cyan
Write-Host "Target directory: $GrammarsDir"

# Create include directory
$incDir = "$GrammarsDir\include"
if (!(Test-Path $incDir)) { New-Item -ItemType Directory -Force -Path $incDir | Out-Null }

# Download tree-sitter.h
Write-Host "`nDownloading tree-sitter.h..." -ForegroundColor Yellow
$headerPath = "$incDir\tree-sitter.h"
$headerUrl = "https://raw.githubusercontent.com/tree-sitter/tree-sitter/refs/heads/main/include/tree_sitter/api.h"

try {
    Invoke-WebRequest -Uri $headerUrl -OutFile $headerPath -UseBasicParsing
    Write-Host "  -> tree-sitter.h downloaded" -ForegroundColor Green
} catch {
    Write-Host "  -> Trying legacy path..." -ForegroundColor Yellow
    $legacyUrl = "https://raw.githubusercontent.com/tree-sitter/tree-sitter/master/lib/include/tree_sitter/api.h"
    try {
        Invoke-WebRequest -Uri $legacyUrl -OutFile $headerPath -UseBasicParsing
        Write-Host "  -> tree-sitter.h downloaded (legacy path)" -ForegroundColor Green
    } catch {
        Write-Host "  -> ERROR: Failed to download tree-sitter.h" -ForegroundColor Red
        exit 1
    }
}

# Define grammars: [name, repo, branch, hasScanner]
$grammars = @(
    @("sql",     "DerekStride/tree-sitter-sql",     "master", $true),
    @("go",      "tree-sitter/tree-sitter-go",      "master", $true),
    @("typescript", "tree-sitter/tree-sitter-typescript", "master", $true),
    @("javascript", "tree-sitter/tree-sitter-javascript", "master", $true),
    @("python",  "tree-sitter/tree-sitter-python",  "master", $true),
    @("rust",    "tree-sitter/tree-sitter-rust",    "master", $true),
    @("java",    "tree-sitter/tree-sitter-java",    "master", $true),
    @("csharp",  "tree-sitter/tree-sitter-c-sharp", "master", $true),
    @("php",     "tree-sitter/tree-sitter-php",     "master", $true),
    @("cpp",     "tree-sitter/tree-sitter-cpp",     "master", $true),
    @("c",       "tree-sitter/tree-sitter-c",       "master", $true),
    @("bash",    "tree-sitter/tree-sitter-bash",    "master", $true),
    @("hcl",     "tree-sitter-grammars/tree-sitter-hcl", "master", $true),
    @("ruby",    "tree-sitter/tree-sitter-ruby",    "master", $true),
    @("json",    "tree-sitter/tree-sitter-json",    "master", $true),
    @("html",    "tree-sitter/tree-sitter-html",    "master", $true),
    @("css",     "tree-sitter/tree-sitter-css",     "master", $true),
    @("toml",    "tree-sitter-grammars/tree-sitter-toml", "master", $true),
    @("scala",   "tree-sitter/tree-sitter-scala",   "master", $true)
)

$count = 0
foreach ($g in $grammars) {
    $lang = $g[0]
    $repo = $g[1]
    $branch = $g[2]
    $hasScanner = $g[3]
    
    $srcDir = "$GrammarsDir\$lang\src"
    if (!(Test-Path $srcDir)) { New-Item -ItemType Directory -Force -Path $srcDir | Out-Null }
    
    Write-Host "Fetching $lang from $repo (branch: $branch)..." -ForegroundColor Yellow
    
    # Download parser.c
    $parserUrl = "https://raw.githubusercontent.com/$repo/$branch/src/parser.c"
    $parserPath = "$srcDir\parser.c"
    
    try {
        Invoke-WebRequest -Uri $parserUrl -OutFile $parserPath -UseBasicParsing
        $size = (Get-Item $parserPath).Length
        Write-Host "  -> parser.c ($size bytes)" -ForegroundColor Green
    } catch {
        Write-Host "  -> ERROR: Failed to download parser.c" -ForegroundColor Red
        if (Test-Path $parserPath) { Remove-Item $parserPath }
        continue
    }
    
    # Download scanner.c (optional)
    if ($hasScanner) {
        $scannerUrl = "https://raw.githubusercontent.com/$repo/$branch/src/scanner.c"
        $scannerPath = "$srcDir\scanner.c"
        
        try {
            Invoke-WebRequest -Uri $scannerUrl -OutFile $scannerPath -UseBasicParsing
            $size = (Get-Item $scannerPath).Length
            Write-Host "  -> scanner.c ($size bytes)" -ForegroundColor Green
        } catch {
            Write-Host "  -> scanner.c: not found or failed (optional)" -ForegroundColor DarkYellow
            if (Test-Path $scannerPath) { Remove-Item $scannerPath }
        }
    }
    
    $count++
}

Write-Host "`n=== Download Complete ===" -ForegroundColor Cyan
Write-Host "$count language directories created with C sources" -ForegroundColor Green
Write-Host "Grammars vendored to: $GrammarsDir"
