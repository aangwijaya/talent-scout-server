package conformance

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pb33f/libopenapi"
	openapivalidator "github.com/pb33f/libopenapi-validator"
	validatorerrors "github.com/pb33f/libopenapi-validator/errors"
)

type Validator struct {
	validator openapivalidator.Validator
}

func New(t *testing.T) *Validator {
	t.Helper()

	spec, err := os.ReadFile(SpecPath())
	if err != nil {
		t.Fatalf("read openapi spec: %v", err)
	}
	document, err := libopenapi.NewDocument(spec)
	if err != nil {
		t.Fatalf("load openapi spec: %v", err)
	}
	v, errs := openapivalidator.NewValidator(document)
	if len(errs) > 0 {
		t.Fatalf("build openapi validator: %v", errs)
	}
	if ok, validationErrs := v.ValidateDocument(); !ok {
		t.Fatalf("openapi spec is invalid: %s", formatErrors(validationErrs))
	}
	return &Validator{validator: v}
}

func (v *Validator) ValidateResponse(t *testing.T, req *http.Request, resp *http.Response) {
	t.Helper()

	ok, errs := v.validator.ValidateHttpResponse(req, resp)
	if !ok {
		t.Fatalf("response for %s %s does not match openapi: %s", req.Method, req.URL.Path, formatErrors(errs))
	}
}

func SpecPath() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "api", "openapi.yaml")
}

func formatErrors(errs []*validatorerrors.ValidationError) string {
	if len(errs) == 0 {
		return "unknown validation error"
	}
	messages := make([]string, 0, len(errs))
	for _, err := range errs {
		messages = append(messages, err.Error())
	}
	return strings.Join(messages, "; ")
}
