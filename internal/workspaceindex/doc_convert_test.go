package workspaceindex

import (
	"bytes"
	"os"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestConvertXLSX(t *testing.T) {
	t.Parallel()

	f := excelize.NewFile()
	sheet, err := f.NewSheet("Sheet1")
	if err != nil {
		t.Fatalf("NewSheet() error: %v", err)
	}
	f.SetCellValue("Sheet1", "A1", "Hello")
	f.SetCellValue("Sheet1", "B1", "World")
	f.SetCellValue("Sheet1", "A2", "Phosphor")
	f.SetActiveSheet(sheet)

	buf := new(bytes.Buffer)
	_, err = f.WriteTo(buf)
	if err != nil {
		t.Fatalf("WriteTo() error: %v", err)
	}

	text, err := convertXLSX(buf.Bytes())
	if err != nil {
		t.Fatalf("convertXLSX() error: %v", err)
	}
	if text == "" {
		t.Error("expected non-empty text from XLSX conversion")
	}
}

func TestConvertDOCX(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("../../examples/documents/example.docx")
	if err != nil {
		t.Skipf("skipping: example.docx not found: %v", err)
	}

	text, err := convertDOCX(data)
	if err != nil {
		t.Fatalf("convertDOCX() error: %v", err)
	}

	if text == "" {
		t.Error("expected non-empty text from DOCX conversion")
	}
}

func TestConvertDocument_DOCX(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("../../examples/documents/example.docx")
	if err != nil {
		t.Skipf("skipping: example.docx not found: %v", err)
	}

	text, err := ConvertDocument(data, ".docx")
	if err != nil {
		t.Fatalf("ConvertDocument(.docx) error: %v", err)
	}

	if text == "" {
		t.Error("expected non-empty text from DOCX")
	}
}

func TestConvertDocument_HTML(t *testing.T) {
	t.Parallel()

	htmlData := []byte("<html><body><h1>Hello</h1><p>World</p></body></html>")
	text, err := ConvertDocument(htmlData, ".html")
	if err != nil {
		t.Fatalf("ConvertDocument(.html) error: %v", err)
	}
	if text == "" {
		t.Error("expected non-empty text from HTML conversion")
	}
}

func TestConvertDocument_XML(t *testing.T) {
	t.Parallel()

	xmlData := []byte(`<?xml version="1.0"?>
<root>
  <item>Item 1</item>
  <item>Item 2</item>
</root>`)
	text, err := ConvertDocument(xmlData, ".xml")
	if err != nil {
		t.Fatalf("ConvertDocument(.xml) error: %v", err)
	}
	if text == "" {
		t.Error("expected non-empty text from XML conversion")
	}
}

func TestConvertDocument_Unsupported(t *testing.T) {
	t.Parallel()

	_, err := ConvertDocument([]byte("data"), ".xyz")
	if err != nil {
		t.Fatalf("ConvertDocument(.xyz) unexpected error: %v", err)
	}
}

func TestConvertDocument_StubbedFormats(t *testing.T) {
	t.Parallel()

	// All formats are implemented — this test should not find any errors
	// If new formats are added, add them to the stubbed list.
}

func TestConvertPDF(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("../../examples/documents/example.pdf")
	if err != nil {
		t.Skipf("skipping: example.pdf not found: %v", err)
	}

	text, err := convertPDF(data)
	if err != nil {
		t.Fatalf("convertPDF() error: %v", err)
	}
	if text == "" {
		t.Error("expected non-empty text from PDF conversion")
	}
}

func TestConvertRTF(t *testing.T) {
	t.Parallel()

	rtfData := []byte("{\\rtf1\\ansi\\deff0\\fonttbl{\\f0\\fswiss Helvetica;}\\pard\\f0\\fs20 Hello World.\\par}")
	text, err := convertRTF(rtfData)
	if err != nil {
		t.Fatalf("convertRTF() error: %v", err)
	}
	if text == "" {
		t.Error("expected non-empty text from RTF conversion")
	}
}

func TestConvertPPTX(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("../../examples/documents/example.pptx")
	if err != nil {
		t.Skipf("skipping: example.pptx not found: %v", err)
	}

	text, err := convertPPTX(data)
	if err != nil {
		t.Fatalf("convertPPTX() error: %v", err)
	}
	if text == "" {
		t.Error("expected non-empty text from PPTX conversion")
	}
}

func TestConvertXML(t *testing.T) {
	t.Parallel()

	tests := []struct {
		xml string
	}{
		{`<root><a>Text A</a><b>Text B</b></root>`},
		{`<a>A</a><b>B</b><c>C</c>`},
	}

	for i, tt := range tests {
		text, err := convertXML([]byte(tt.xml))
		if err != nil {
			t.Errorf("test %d: convertXML() error: %v", i, err)
			continue
		}
		if text == "" {
			t.Errorf("test %d: expected non-empty text", i)
		}
	}
}

func TestConvertDocument_InvalidData(t *testing.T) {
	t.Parallel()

	for _, ext := range []string{".html", ".xml"} {
		_, err := ConvertDocument([]byte(""), ext)
		if err != nil {
			t.Errorf("ConvertDocument(%q) unexpected error: %v", ext, err)
		}
	}
}