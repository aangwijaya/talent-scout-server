-- +goose Up
CREATE TABLE audio_uploads (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id uuid NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    object_path text NOT NULL,
    content_type text NOT NULL,
    size_bytes integer NOT NULL CHECK (size_bytes >= 0),
    duration_ms integer NOT NULL CHECK (duration_ms >= 0),
    message_id uuid REFERENCES messages (id) ON DELETE SET NULL,
    uploaded_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);
ALTER TABLE audio_uploads ENABLE ROW LEVEL SECURITY;
REVOKE ALL ON TABLE audio_uploads FROM anon, authenticated;

CREATE INDEX audio_uploads_retention_idx ON audio_uploads (deleted_at, message_id, uploaded_at);

-- +goose Down
DROP TABLE IF EXISTS audio_uploads;
