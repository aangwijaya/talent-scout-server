-- +goose Up
ALTER TABLE audio_uploads ADD COLUMN stored boolean NOT NULL DEFAULT true;

-- +goose Down
ALTER TABLE audio_uploads DROP COLUMN stored;
