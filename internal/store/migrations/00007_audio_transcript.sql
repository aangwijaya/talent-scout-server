-- +goose Up
ALTER TABLE audio_uploads ADD COLUMN transcript text NOT NULL DEFAULT '';
ALTER TABLE audio_uploads ADD COLUMN language text NOT NULL DEFAULT '';
ALTER TABLE audio_uploads ADD COLUMN cost_usd numeric(10, 6) NOT NULL DEFAULT 0 CHECK (cost_usd >= 0);

CREATE INDEX audio_uploads_session_id_idx ON audio_uploads (session_id);

-- +goose Down
DROP INDEX IF EXISTS audio_uploads_session_id_idx;
ALTER TABLE audio_uploads DROP COLUMN cost_usd;
ALTER TABLE audio_uploads DROP COLUMN language;
ALTER TABLE audio_uploads DROP COLUMN transcript;
