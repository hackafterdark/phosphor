// Package embeddings provides text chunking for codebase indexing.
package embeddings

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// BinaryExtensions maps file extensions to a skip list.
var BinaryExtensions = map[string]bool{
	".exe": true, ".dll": true, ".so": true, ".dylib": true,
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
	".zip": true, ".tar": true, ".gz": true, ".rar": true,
	".pdf": true, ".doc": true, ".docx": true, ".ppt": true,
	".pptx": true, ".xls": true, ".xlsx": true, ".bmp": true,
	".ico": true, ".tiff": true, ".webp": true, ".svg": true,
	".mp3": true, ".mp4": true, ".avi": true, ".mkv": true,
	".woff": true, ".woff2": true, ".eot": true, ".ttf": true,
}

// Chunk splits text into overlapping chunks of maxChars size.
func Chunk(text string, maxChars, overlap int) []string {
	if len(text) <= maxChars {
		return []string{text}
	}
	var chunks []string
	for i := 0; i < len(text); i += maxChars - overlap {
		end := i + maxChars
		if end > len(text) {
			end = len(text)
		}
		chunks = append(chunks, text[i:end])
	}
	return chunks
}

// ChunkFile reads a file and splits its content into overlapping chunks.
func ChunkFile(path string, maxChars, overlap int) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Chunk(string(data), maxChars, overlap), nil
}

// ChunkID generates a deterministic ID for a given file path and chunk index.
func ChunkID(filePath string, chunkIndex int) string {
	id := fmt.Sprintf("%s:%d", filePath, chunkIndex)
	hash := sha256.Sum256([]byte(id))
	return hex.EncodeToString(hash[:])
}

// ShouldSkip returns true if the file extension is in the binary skip list.
func ShouldSkip(path string) bool {
	return BinaryExtensions[strings.ToLower(filepath.Ext(path))]
}

// IsTextFile returns true if the file contains no null bytes.
func IsTextFile(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return !bytes.Contains(data, []byte{0})
}

// ContentHash computes a SHA-256 hash of the given data and returns it as a hex string.
func ContentHash(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
