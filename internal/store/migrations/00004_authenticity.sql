-- +goose Up
ALTER TABLE messages ADD COLUMN entry jsonb;
ALTER TABLE messages ADD CONSTRAINT messages_entry_object_check
    CHECK (entry IS NULL OR jsonb_typeof(entry) = 'object');

ALTER TABLE applications ADD COLUMN authenticity jsonb;
ALTER TABLE applications ADD CONSTRAINT applications_authenticity_object_check
    CHECK (authenticity IS NULL OR jsonb_typeof(authenticity) = 'object');

-- +goose Down
ALTER TABLE applications DROP CONSTRAINT applications_authenticity_object_check;
ALTER TABLE applications DROP COLUMN authenticity;

ALTER TABLE messages DROP CONSTRAINT messages_entry_object_check;
ALTER TABLE messages DROP COLUMN entry;
