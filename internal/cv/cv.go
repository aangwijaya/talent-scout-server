package cv

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	pdf "github.com/dslipak/pdf"
)

// ErrNotPDF indicates the input is not a parseable PDF or its text cannot be extracted.
var ErrNotPDF = errors.New("not a PDF or text cannot be extracted")

// Extractor turns CV bytes into plain text.
type Extractor interface {
	Extract(data []byte) (string, error)
}

// PDF extracts text from a PDF using dslipak/pdf. Scanned (image) PDFs are
// out of scope; an empty result is treated as ErrNotPDF.
type PDF struct{}

func NewPDF() *PDF { return &PDF{} }

func (p *PDF) Extract(data []byte) (string, error) {
	reader, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrNotPDF, err)
	}

	var b strings.Builder
	for i := 1; i <= reader.NumPage(); i++ {
		text, err := reader.Page(i).GetPlainText(nil)
		if err != nil {
			return "", fmt.Errorf("%w: %v", ErrNotPDF, err)
		}
		b.WriteString(text)
		b.WriteByte('\n')
	}

	text := strings.TrimSpace(b.String())
	if text == "" {
		return "", ErrNotPDF
	}
	return text, nil
}
