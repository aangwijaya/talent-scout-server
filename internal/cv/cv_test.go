package cv_test

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aangwijaya/talent-scout-server/internal/cv"
)

func TestExtractText(t *testing.T) {
	data := minimalPDF(t, "Senior Engineer with Svelte experience")
	got, err := cv.NewPDF().Extract(data)
	if err != nil {
		t.Fatalf("Extract() error: %v", err)
	}
	if !strings.Contains(got, "Senior Engineer") {
		t.Errorf("extracted text = %q, want it to contain the CV text", got)
	}
}

func TestExtractNonPDF(t *testing.T) {
	_, err := cv.NewPDF().Extract([]byte("this is not a pdf file at all"))
	if !errors.Is(err, cv.ErrNotPDF) {
		t.Fatalf("err = %v, want ErrNotPDF", err)
	}
}

func TestExtractEmptyPDF(t *testing.T) {
	data := minimalPDF(t, " ")
	_, err := cv.NewPDF().Extract(data)
	if !errors.Is(err, cv.ErrNotPDF) {
		t.Fatalf("err = %v, want ErrNotPDF (empty text cannot be extracted)", err)
	}
}

// minimalPDF builds a small but structurally valid PDF with a single page
// containing the given text, so the extractor can be tested without fixtures.
func minimalPDF(t *testing.T, text string) []byte {
	t.Helper()

	content := "BT /F1 12 Tf 72 712 Td (" + text + ") Tj ET"
	objects := []string{
		"1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n",
		"2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n",
		"3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>\nendobj\n",
		fmt.Sprintf("4 0 obj\n<< /Length %d >>\nstream\n%s\nendstream\nendobj\n", len(content), content),
		"5 0 obj\n<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>\nendobj\n",
	}

	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	offsets := make([]int, 6)
	for i := 1; i <= 5; i++ {
		offsets[i] = buf.Len()
		buf.WriteString(objects[i-1])
	}

	xrefOffset := buf.Len()
	buf.WriteString("xref\n0 6\n")
	buf.WriteString("0000000000 65535 f \n")
	for i := 1; i <= 5; i++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[i])
	}
	buf.WriteString("trailer\n<< /Size 6 /Root 1 0 R >>\n")
	buf.WriteString("startxref\n")
	fmt.Fprintf(&buf, "%d\n", xrefOffset)
	buf.WriteString("%%EOF\n")
	return buf.Bytes()
}
