-- +goose Up
ALTER TABLE applications ADD COLUMN fluency jsonb;
ALTER TABLE applications ADD CONSTRAINT applications_fluency_object_check
    CHECK (fluency IS NULL OR jsonb_typeof(fluency) = 'object');

ALTER TABLE audio_uploads ADD COLUMN transcript_words jsonb;
ALTER TABLE audio_uploads ADD CONSTRAINT audio_uploads_transcript_words_object_check
    CHECK (transcript_words IS NULL OR jsonb_typeof(transcript_words) = 'object');

-- +goose Down
ALTER TABLE audio_uploads DROP CONSTRAINT audio_uploads_transcript_words_object_check;
ALTER TABLE audio_uploads DROP COLUMN transcript_words;

ALTER TABLE applications DROP CONSTRAINT applications_fluency_object_check;
ALTER TABLE applications DROP COLUMN fluency;
