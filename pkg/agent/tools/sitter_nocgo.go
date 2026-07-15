//go:build !cgo

package tools

import "fmt"

func isSitterAvailable() bool {
	return false
}

func verifySyntax(newString string, filePath string) error {
	return nil
}

func generateOutline(code []byte, filePath string) (string, error) {
	return "", fmt.Errorf("tree-sitter is not available in non-cgo builds")
}

func findNodeSitter(code []byte, nodeName string, filePath string) (string, int, error) {
	return "", 0, fmt.Errorf("tree-sitter is not available in non-cgo builds")
}
