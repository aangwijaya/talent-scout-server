-- +goose Up
ALTER TABLE jobs ADD COLUMN spoken_english_required boolean NOT NULL DEFAULT false;

ALTER TABLE sessions ADD COLUMN voice boolean NOT NULL DEFAULT false;
ALTER TABLE sessions ADD COLUMN recording_consent boolean;

-- +goose Down
ALTER TABLE sessions DROP COLUMN recording_consent;
ALTER TABLE sessions DROP COLUMN voice;

ALTER TABLE jobs DROP COLUMN spoken_english_required;
