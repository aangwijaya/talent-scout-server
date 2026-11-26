package migrations_test

import (
	"strings"
	"testing"

	"github.com/aangwijaya/talent-scout-server/internal/store/migrations"
)

func TestAudioUploadsMigrationEnablesRLS(t *testing.T) {
	data, err := migrations.FS.ReadFile("00005_audio_uploads.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	up := string(data)
	for _, want := range []string{
		"CREATE TABLE audio_uploads",
		"ALTER TABLE audio_uploads ENABLE ROW LEVEL SECURITY",
		"REVOKE ALL ON TABLE audio_uploads FROM anon, authenticated",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("migration is missing %q", want)
		}
	}
}
