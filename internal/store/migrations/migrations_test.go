package migrations_test

import (
	"strings"
	"testing"

	"github.com/aangwijaya/talent-scout-server/internal/store/migrations"
)

func TestVoiceSnapshotMigrationDefaultsExistingRowsToText(t *testing.T) {
	data, err := migrations.FS.ReadFile("00006_voice_snapshot.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	up := string(data)
	for _, want := range []string{
		"ALTER TABLE jobs ADD COLUMN spoken_english_required boolean NOT NULL DEFAULT false",
		"ALTER TABLE sessions ADD COLUMN voice boolean NOT NULL DEFAULT false",
		"ALTER TABLE sessions ADD COLUMN recording_consent boolean",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("migration is missing %q", want)
		}
	}
}

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
