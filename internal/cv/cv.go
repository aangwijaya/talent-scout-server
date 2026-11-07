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

// maxPages caps how many pages are read from an uploaded PDF. The page count
// comes from the file's /Count, which a malicious PDF can inflate to burn CPU.
const maxPages = 50

// Extractor turns CV bytes into plain text.
type Extractor interface {
	Extract(data []byte) (string, error)
}

// PDF extracts text from a PDF using dslipak/pdf. Scanned (image) PDFs are
// out of scope; an empty result is treated as ErrNotPDF.
type PDF struct{}

func NewPDF() *PDF { return &PDF{} }

func (p *PDF) Extract(data []byte) (text string, err error) {
	// The PDF library can panic on malformed input (NumPage/Page resolve object
	// references without recovering). Convert any panic to ErrNotPDF so the
	// public endpoint returns a contract error instead of dropping the request.
	defer func() {
		if r := recover(); r != nil {
			text = ""
			err = fmt.Errorf("%w: %v", ErrNotPDF, r)
		}
	}()

	reader, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrNotPDF, err)
	}

	pages := reader.NumPage()
	if pages > maxPages {
		pages = maxPages
	}

	var b strings.Builder
	for i := 1; i <= pages; i++ {
		pageText, err := reader.Page(i).GetPlainText(nil)
		if err != nil {
			return "", fmt.Errorf("%w: %v", ErrNotPDF, err)
		}
		b.WriteString(pageText)
		b.WriteByte('\n')
	}

	text = strings.TrimSpace(b.String())
	if text == "" {
		return "", ErrNotPDF
	}
	return text, nil
}
