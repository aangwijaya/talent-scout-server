package conformance_test

import (
	"testing"

	"github.com/aangwijaya/talent-scout-server/internal/conformance"
)

func TestSpecIsValid(t *testing.T) {
	conformance.New(t)
}
