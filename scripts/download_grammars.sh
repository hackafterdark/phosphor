#!/bin/bash
# download_grammars.sh — Vendor tree-sitter C sources for deterministic builds
# Usage: bash download_grammars.sh
#
# This script downloads the raw parser.c and scanner.c source files from upstream
# tree-sitter repositories for all 19 supported languages, saving them into the 
# local grammars/ directory. This ensures deterministic CGO builds without needing
# external runtime/system grammar installations.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
GRAMMARS_DIR="$SCRIPT_DIR/grammars"

echo "=== Tree-Sitter Grammar Vendor Script ==="
echo "Target directory: $GRAMMARS_DIR"

# Create include directory for tree-sitter.h
mkdir -p "$GRAMMARS_DIR/include"

# Download the core tree-sitter header (current stable location)
echo "Downloading tree-sitter.h..."
curl -sL "https://raw.githubusercontent.com/tree-sitter/tree-sitter/refs/heads/main/include/tree_sitter/api.h" \
  -o "$GRAMMARS_DIR/include/tree-sitter.h" || {
    # Fallback to older path
    echo "Trying legacy header path..."
    curl -sL "https://raw.githubusercontent.com/tree-sitter/tree-sitter/master/lib/include/tree_sitter/api.h" \
      -o "$GRAMMARS_DIR/include/tree-sitter.h"
}

if [ ! -f "$GRAMMARS_DIR/include/tree-sitter.h" ]; then
    echo "ERROR: Failed to download tree-sitter.h"
    exit 1
fi
echo "  -> $GRAMMARS_DIR/include/tree-sitter.h"

# Define grammars: "directory_name:repo_path:branch:has_scanner"
# branch: master or main
# has_scanner: true/false
declare -A GRAMMAR_REPOS
GRAMMAR_REPOS=(
    ["sql"]="DerekStride/tree-sitter-sql:main:true"
    ["go"]="tree-sitter/tree-sitter-go:main:true"
    ["typescript"]="tree-sitter/tree-sitter-typescript:main:true"
    ["javascript"]="tree-sitter/tree-sitter-javascript:main:true"
    ["python"]="tree-sitter/tree-sitter-python:main:true"
    ["rust"]="tree-sitter/tree-sitter-rust:main:true"
    ["java"]="tree-sitter/tree-sitter-java:main:true"
    ["csharp"]="tree-sitter/tree-sitter-c-sharp:main:true"
    ["php"]="tree-sitter/tree-sitter-php:main:true"
    ["cpp"]="tree-sitter/tree-sitter-cpp:main:true"
    ["c"]="tree-sitter/tree-sitter-c:main:true"
    ["bash"]="tree-sitter/tree-sitter-bash:main:true"
    ["hcl"]="tree-sitter-grammars/tree-sitter-hcl:main:true"
    ["ruby"]="tree-sitter/tree-sitter-ruby:main:true"
    ["json"]="tree-sitter/tree-sitter-json:main:true"
    ["html"]="tree-sitter/tree-sitter-html:main:true"
    ["css"]="tree-sitter/tree-sitter-css:main:true"
    ["toml"]="tree-sitter-grammars/tree-sitter-toml:main:true"
    ["scala"]="tree-sitter/tree-sitter-scala:main:true"
)

for lang in "${!GRAMMAR_REPOS[@]}"; do
    IFS=':' read -r repo branch has_scanner <<< "${GRAMMAR_REPOS[$lang]}"
    src_dir="$GRAMMARS_DIR/$lang/src"
    mkdir -p "$src_dir"

    echo "Fetching $lang from $repo (branch: $branch)..."

    # Download parser.c
    parser_url="https://raw.githubusercontent.com/$repo/$branch/src/parser.c"
    if curl -sL "$parser_url" -o "$src_dir/parser.c"; then
        echo "  -> parser.c ($(wc -c < "$src_dir/parser.c") bytes)"
    else
        echo "  -> ERROR: Failed to download parser.c"
        rm -f "$src_dir/parser.c"
        continue
    fi

    # Download scanner.c (optional — many grammars don't have one)
    if [ "$has_scanner" = "true" ]; then
        scanner_url="https://raw.githubusercontent.com/$repo/$branch/src/scanner.c"
        if curl -sL "$scanner_url" -o "$src_dir/scanner.c"; then
            echo "  -> scanner.c ($(wc -c < "$src_dir/scanner.c") bytes)"
        else
            echo "  -> scanner.c: not found or failed (optional)"
            rm -f "$src_dir/scanner.c"
        fi
    fi
done

echo ""
echo "=== Download Complete ==="
echo "Grammars vendored to: $GRAMMARS_DIR"
ls -1 "$GRAMMARS_DIR" | wc -l
echo "language directories created"
