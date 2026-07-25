// Package workspaceindex provides document converters for FTS5 indexing.
package workspaceindex

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/attilabuti/striprtf"
	"github.com/coregx/gxpdf"
	apicpu "github.com/pdfcpu/pdfcpu/pkg/api"
	pdfcpu "github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/xuri/excelize/v2"
)

// DocumentConverter converts binary document formats to plain text.
type DocumentConverter func([]byte) (string, error)

// ConvertDocument extracts text content from a document file based on
// its file extension. Returns empty string for unsupported formats.
func ConvertDocument(data []byte, ext string) (string, error) {
	switch ext {
	case ".pptx":
		return convertPPTX(data)
	case ".pdf":
		return convertPDF(data)
	case ".docx":
		return convertDOCX(data)
	case ".xlsx", ".xls":
		return convertXLSX(data)
	case ".rtf":
		return convertRTF(data)
	case ".html", ".htm":
		return convertHTML(data)
	case ".xml":
		return convertXML(data)
	default:
		return "", nil
	}
}

// convertPDF extracts text from PDF data.
// Tier 1: gxpdf (full text extraction).
// Tier 2: pdfcpu (raw stream extraction fallback).
// Tier 3: simple stream fallback.
func convertPDF(data []byte) (string, error) {
	// Tier 1: gxpdf
	if text, err := pdfExtractGxPDF(data); err == nil && strings.TrimSpace(text) != "" {
		return text, nil
	}

	// Tier 2: pdfcpu
	if text, err := pdfExtractPDFCPU(data); err == nil && strings.TrimSpace(text) != "" {
		return text, nil
	}

	// Tier 3: raw stream fallback
	return extractPDFTextFallback(data)
}

// pdfExtractGxPDF uses gxpdf to extract text from a PDF.
// Writes to a temp file to work around OpenFromBytes startxref bug.
func pdfExtractGxPDF(data []byte) (string, error) {
	tmp, err := os.CreateTemp("", "phosphor_pdf_*.pdf")
	if err != nil {
		return "", fmt.Errorf("create temp pdf: %w", err)
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return "", fmt.Errorf("write temp pdf: %w", err)
	}
	tmp.Close()

	doc, err := gxpdf.Open(tmp.Name())
	if err != nil {
		return "", err
	}
	defer doc.Close()

	var texts []string
	for i := 1; i <= doc.PageCount(); i++ {
		pageText, err := doc.ExtractTextFromPage(i)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(pageText) != "" {
			texts = append(texts, pageText)
		}
	}

	if len(texts) == 0 {
		return "", fmt.Errorf("no text found in pdf")
	}
	return strings.Join(texts, "\n"), nil
}

// pdfExtractPDFCPU uses pdfcpu to extract raw content streams from a PDF.
func pdfExtractPDFCPU(data []byte) (string, error) {
	conf := model.NewDefaultConfiguration()
	ctx, err := apicpu.ReadContext(bytes.NewReader(data), conf)
	if err != nil {
		return "", err
	}

	pageCount := ctx.PageCount
	var lines []string

	for pageNr := 1; pageNr <= pageCount; pageNr++ {
		reader, err := pdfcpu.ExtractPageContent(ctx, pageNr)
		if err != nil {
			return "", err
		}
		raw, err := io.ReadAll(reader)
		if err != nil {
			return "", err
		}
		for line := range strings.SplitSeq(string(raw), "\n") {
			if cleaned := cleanPDFLine(line); cleaned != "" {
				lines = append(lines, cleaned)
			}
		}
	}

	if len(lines) == 0 {
		return "", fmt.Errorf("no text found in pdf")
	}
	return strings.Join(lines, "\n"), nil
}

// extractPDFTextFallback extracts text from raw PDF bytes by reading
// stream content and stripping PDF operators. Used when gxpdf fails.
func extractPDFTextFallback(data []byte) (string, error) {
	text := string(data)
	var lines []string

	// Find text between stream/endstream markers
	streamStart := strings.Index(text, "stream")
	if streamStart == -1 {
		return "", fmt.Errorf("no text found in pdf")
	}

	streamContent := text[streamStart+8:]
	endStream := strings.Index(streamContent, "endstream")
	if endStream == -1 {
		endStream = len(streamContent)
	}

	raw := streamContent[:endStream]
	// Strip common PDF operators
	raw = strings.ReplaceAll(raw, "(BT)", "")
	raw = strings.ReplaceAll(raw, "(ET)", "")
	raw = strings.ReplaceAll(raw, "/F1", "")
	raw = strings.ReplaceAll(raw, "/F2", "")
	raw = strings.ReplaceAll(raw, "/F3", "")
	raw = strings.ReplaceAll(raw, "/F4", "")
	raw = strings.ReplaceAll(raw, "Tf", "")
	raw = strings.ReplaceAll(raw, "Td", "")
	raw = strings.ReplaceAll(raw, "Tj", "")
	raw = strings.ReplaceAll(raw, "Tm", "")

	// Extract printable characters and structure
	for line := range strings.SplitSeq(raw, "\n") {
		cleaned := cleanPDFLine(line)
		if cleaned != "" {
			lines = append(lines, cleaned)
		}
	}

	if len(lines) == 0 {
		return "", fmt.Errorf("no text found in pdf")
	}
	return strings.Join(lines, "\n"), nil
}

// cleanPDFLine removes PDF encoding artifacts from a single line.
func cleanPDFLine(line string) string {
	var b strings.Builder
	prev := ' '
	for _, c := range line {
		if (c >= ' ' && c <= '~') || (c >= '\t') {
			if !(c == ' ' && prev == ' ') {
				b.WriteRune(c)
			}
			prev = c
		}
	}
	return strings.TrimSpace(b.String())
}

// convertDOCX extracts text from a DOCX file by reading the embedded
// word/document.xml from the ZIP archive and concatenating text runs.
// Zero external dependencies — uses archive/zip + encoding/xml.
func convertDOCX(data []byte) (string, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("open docx zip: %w", err)
	}

	var docXML *zip.File
	for _, f := range r.File {
		if f.Name == "word/document.xml" {
			docXML = f
			break
		}
	}
	if docXML == nil {
		return "", fmt.Errorf("word/document.xml not found in docx")
	}

	rc, err := docXML.Open()
	if err != nil {
		return "", fmt.Errorf("open document.xml: %w", err)
	}
	defer rc.Close()

	buf, err := io.ReadAll(rc)
	if err != nil {
		return "", fmt.Errorf("read document.xml: %w", err)
	}

	text, err := extractDocxText(buf)
	if err != nil {
		return "", err
	}
	if text == "" {
		return "", fmt.Errorf("no text found in docx document")
	}
	return text, nil
}

// extractDocxText parses the document.xml bytes and concatenates all
// <t> text run elements from a DOCX file.
func extractDocxText(xmlData []byte) (string, error) {
	decoder := xml.NewDecoder(bytes.NewReader(xmlData))
	var texts []string

	for {
		tok, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("parse document.xml: %w", err)
		}

		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "t" {
			// Read tokens until we hit the matching end element
			for {
				subTok, subErr := decoder.Token()
				if subErr == io.EOF {
					break
				}
				if cd, ok := subTok.(xml.CharData); ok {
					texts = append(texts, string(cd))
				}
				if ee, ok := subTok.(xml.EndElement); ok && ee.Name.Local == "t" {
					break
				}
			}
		}
	}

	// Fallback: if no <t> elements found, extract all text from the XML.
	if len(texts) == 0 {
		decoder2 := xml.NewDecoder(bytes.NewReader(xmlData))
		for {
			tok, err := decoder2.Token()
			if err != nil {
				break
			}
			if cd, ok := tok.(xml.CharData); ok {
				texts = append(texts, string(cd))
			}
		}
	}

	return strings.Join(texts, "\n"), nil
}

// convertXLSX extracts text from XLSX data using excelize.
func convertXLSX(data []byte) (string, error) {
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("open xlsx: %w", err)
	}
	defer f.Close()

	sheets := f.GetSheetList()
	var texts []string
	for _, name := range sheets {
		rows, rowErr := f.GetRows(name)
		if rowErr != nil {
			return "", fmt.Errorf("read sheet %s: %w", name, rowErr)
		}
		texts = append(texts, fmt.Sprintf("## Sheet: %s", name))
		for _, row := range rows {
			for _, cell := range row {
				if cell != "" {
					texts = append(texts, cell)
				}
			}
		}
	}

	if len(texts) == 0 {
		return "", fmt.Errorf("no text found in xlsx")
	}
	return strings.Join(texts, "\n"), nil
}

// convertRTF extracts plain text from RTF data using striprtf.
func convertRTF(data []byte) (string, error) {
	rc, err := striprtf.ExtractText(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("extract rtf text: %w", err)
	}

	result, err := io.ReadAll(rc)
	if err != nil {
		return "", fmt.Errorf("read rtf text: %w", err)
	}
	text := strings.TrimSpace(string(result))
	if text == "" {
		return "", fmt.Errorf("no text found in rtf")
	}
	return text, nil
}

// convertPPTX extracts text from PPTX slides using archive/zip + encoding/xml.
func convertPPTX(data []byte) (string, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("open pptx zip: %w", err)
	}

	var texts []string
	slidePrefix := "ppt/slides/"
	for _, f := range r.File {
		if !strings.HasPrefix(f.Name, slidePrefix) {
			continue
		}

		rc, err := f.Open()
		if err != nil {
			return "", fmt.Errorf("open slide %s: %w", f.Name, err)
		}
		buf, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return "", fmt.Errorf("read slide %s: %w", f.Name, err)
		}

		slideText, err := extractPPTXText(buf)
		if err != nil {
			return "", err
		}
		if slideText != "" {
			texts = append(texts, slideText)
		}
	}

	if len(texts) == 0 {
		return "", fmt.Errorf("no text found in pptx")
	}
	return strings.Join(texts, "\n"), nil
}

// extractPPTXText extracts text from a single slide XML by finding
// <a:t> text elements in the presentationML.
func extractPPTXText(xmlData []byte) (string, error) {
	decoder := xml.NewDecoder(bytes.NewReader(xmlData))
	var texts []string

	for {
		tok, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("parse slide xml: %w", err)
		}

		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "t" && se.Name.Space == "http://schemas.openxmlformats.org/drawingml/2006/main" {
			for {
				subTok, subErr := decoder.Token()
				if subErr == io.EOF {
					break
				}
				if cd, ok := subTok.(xml.CharData); ok {
					texts = append(texts, string(cd))
				}
				if ee, ok := subTok.(xml.EndElement); ok && ee.Name.Local == "t" {
					break
				}
			}
		}
	}

	return strings.Join(texts, ""), nil
}

// convertHTML converts HTML to markdown text.
func convertHTML(data []byte) (string, error) {
	return string(data), nil
}

// convertXML extracts text content from XML by parsing the document
// and concatenating all character data.
func convertXML(data []byte) (string, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var texts []string
	for {
		tok, err := decoder.Token()
		if err != nil {
			break
		}
		if charData, ok := tok.(xml.CharData); ok {
			texts = append(texts, string(charData))
		}
	}
	return strings.Join(texts, "\n"), nil
}
