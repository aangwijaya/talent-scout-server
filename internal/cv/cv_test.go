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

func TestExtractCapsPages(t *testing.T) {
	const total = 60
	texts := make([]string, total)
	for i := 0; i < total; i++ {
		texts[i] = fmt.Sprintf("marker%02d", i+1)
	}
	data := pagesPDF(t, texts, 0)

	got, err := cv.NewPDF().Extract(data)
	if err != nil {
		t.Fatalf("Extract() error: %v", err)
	}
	if !strings.Contains(got, "marker01") {
		t.Errorf("extracted text = %q, want the first page's text", got)
	}
	if strings.Contains(got, "marker60") {
		t.Errorf("extracted text contains a page beyond the cap")
	}
}

func TestExtractRecoversFromMalformedPDF(t *testing.T) {
	data := malformedRootPDF(t)
	_, err := cv.NewPDF().Extract(data)
	if !errors.Is(err, cv.ErrNotPDF) {
		t.Fatalf("err = %v, want ErrNotPDF (recovered from a malformed PDF)", err)
	}
}

// buildPDF assembles a PDF from full object bodies (each includes its own
// "N 0 obj ... endobj" wrapper). Objects must be numbered 1..len(bodies)
// contiguously. An optional prefix is written right after the header; when
// prefixObj is non-zero, that object's xref entry points at the prefix bytes
// instead of its own object (to craft a malformed reference).
func buildPDF(t *testing.T, bodies []string, prefix string, prefixObj int) []byte {
	t.Helper()
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	prefixOffset := buf.Len()
	buf.WriteString(prefix)

	offsets := make([]int, len(bodies)+1)
	for i, body := range bodies {
		offsets[i+1] = buf.Len()
		buf.WriteString(body)
	}

	xrefOffset := buf.Len()
	buf.WriteString("xref\n0 ")
	fmt.Fprintf(&buf, "%d\n", len(bodies)+1)
	buf.WriteString("0000000000 65535 f \n")
	for i := 1; i <= len(bodies); i++ {
		off := offsets[i]
		if i == prefixObj {
			off = prefixOffset
		}
		fmt.Fprintf(&buf, "%010d 00000 n \n", off)
	}
	buf.WriteString("trailer\n<< /Size ")
	fmt.Fprintf(&buf, "%d", len(bodies)+1)
	buf.WriteString(" /Root 1 0 R >>\n")
	buf.WriteString("startxref\n")
	fmt.Fprintf(&buf, "%d\n", xrefOffset)
	buf.WriteString("%%EOF\n")
	return buf.Bytes()
}

// pagesPDF builds a structurally valid PDF with one page per text entry. If
// countOverride is positive it is written as /Count instead of len(texts).
func pagesPDF(t *testing.T, texts []string, countOverride int) []byte {
	t.Helper()
	n := len(texts)
	count := n
	if countOverride > 0 {
		count = countOverride
	}

	kids := make([]string, n)
	for k := 0; k < n; k++ {
		kids[k] = fmt.Sprintf("%d 0 R", 4+2*k)
	}

	bodies := []string{
		"1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n",
		fmt.Sprintf("2 0 obj\n<< /Type /Pages /Kids [%s] /Count %d >>\nendobj\n", strings.Join(kids, " "), count),
		"3 0 obj\n<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>\nendobj\n",
	}
	for k := 0; k < n; k++ {
		content := "BT /F1 12 Tf 72 712 Td (" + texts[k] + ") Tj ET"
		bodies = append(bodies,
			fmt.Sprintf("%d 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>\nendobj\n", 4+2*k, 5+2*k),
			fmt.Sprintf("%d 0 obj\n<< /Length %d >>\nstream\n%s\nendstream\nendobj\n", 5+2*k, len(content), content),
		)
	}

	return buildPDF(t, bodies, "", 0)
}

// minimalPDF builds a small but structurally valid PDF with a single page
// containing the given text, so the extractor can be tested without fixtures.
func minimalPDF(t *testing.T, text string) []byte {
	t.Helper()
	return pagesPDF(t, []string{text}, 0)
}

// malformedRootPDF builds a PDF whose trailer /Root points at a bare integer
// (not an object definition), so resolving it panics inside the library.
func malformedRootPDF(t *testing.T) []byte {
	t.Helper()
	content := "BT /F1 12 Tf 72 712 Td (hello) Tj ET"
	bodies := []string{
		"1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n",
		"2 0 obj\n<< /Type /Pages /Kids [4 0 R] /Count 1 >>\nendobj\n",
		"3 0 obj\n<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>\nendobj\n",
		"4 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 3 0 R >> >> /Contents 5 0 R >>\nendobj\n",
		fmt.Sprintf("5 0 obj\n<< /Length %d >>\nstream\n%s\nendstream\nendobj\n", len(content), content),
	}
	return buildPDF(t, bodies, "999\n", 1)
}
