-- +goose Up
ALTER TABLE jobs DROP CONSTRAINT jobs_description_check;
ALTER TABLE jobs ADD CONSTRAINT jobs_description_check
    CHECK (char_length(description) BETWEEN 50 AND 12000);

-- +goose Down
ALTER TABLE jobs DROP CONSTRAINT jobs_description_check;
ALTER TABLE jobs ADD CONSTRAINT jobs_description_check
    CHECK (char_length(description) BETWEEN 50 AND 8000);
