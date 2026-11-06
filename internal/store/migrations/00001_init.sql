-- +goose Up
CREATE TABLE jobs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    title text NOT NULL CHECK (char_length(title) BETWEEN 3 AND 120),
    description text NOT NULL CHECK (char_length(description) BETWEEN 50 AND 8000),
    rubric jsonb NOT NULL CHECK (jsonb_typeof(rubric) = 'array'),
    status text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'closed')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE jobs ENABLE ROW LEVEL SECURITY;
REVOKE ALL ON TABLE jobs FROM anon, authenticated;

CREATE TABLE applications (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id uuid NOT NULL REFERENCES jobs (id),
    name text NOT NULL,
    email text NOT NULL,
    cv_object_path text NOT NULL,
    cv_text text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'applied' CHECK (status IN (
        'applied', 'invited', 'in_session', 'scoring', 'review', 'shortlisted', 'rejected', 'failed'
    )),
    decision_note text NOT NULL DEFAULT '',
    decided_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (job_id, email)
);
ALTER TABLE applications ENABLE ROW LEVEL SECURITY;
REVOKE ALL ON TABLE applications FROM anon, authenticated;

CREATE TABLE sessions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id uuid NOT NULL REFERENCES applications (id) ON DELETE CASCADE,
    token_hash text NOT NULL UNIQUE,
    status text NOT NULL DEFAULT 'invited' CHECK (status IN ('invited', 'active', 'completed', 'expired', 'revoked')),
    expires_at timestamptz NOT NULL,
    started_at timestamptz,
    ended_at timestamptz,
    finish_reason text CHECK (finish_reason IN ('completed', 'time_limit', 'question_limit', 'abandoned', 'failed')),
    question_count integer NOT NULL DEFAULT 0 CHECK (question_count >= 0),
    created_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE sessions ENABLE ROW LEVEL SECURITY;
REVOKE ALL ON TABLE sessions FROM anon, authenticated;

CREATE TABLE topics (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id uuid NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    position integer NOT NULL CHECK (position >= 1),
    title text NOT NULL,
    goal text NOT NULL,
    rubric_keys jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(rubric_keys) = 'array'),
    seed_question text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'active', 'done')),
    follow_up_count integer NOT NULL DEFAULT 0 CHECK (follow_up_count BETWEEN 0 AND 2)
);
ALTER TABLE topics ENABLE ROW LEVEL SECURITY;
REVOKE ALL ON TABLE topics FROM anon, authenticated;

CREATE TABLE messages (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id uuid NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    topic_id uuid REFERENCES topics (id) ON DELETE SET NULL,
    role text NOT NULL CHECK (role IN ('assistant', 'applicant', 'system')),
    kind text NOT NULL CHECK (kind IN ('opening', 'question', 'follow_up', 'answer', 'closing')),
    content text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE messages ENABLE ROW LEVEL SECURITY;
REVOKE ALL ON TABLE messages FROM anon, authenticated;

CREATE TABLE scores (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id uuid NOT NULL REFERENCES applications (id) ON DELETE CASCADE,
    rubric_key text NOT NULL,
    score smallint CHECK (score BETWEEN 1 AND 5),
    rationale text NOT NULL DEFAULT '',
    evidence jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(evidence) = 'array'),
    status text NOT NULL DEFAULT 'valid' CHECK (status IN ('valid', 'needs_manual')),
    attempts integer NOT NULL DEFAULT 1 CHECK (attempts >= 1),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (application_id, rubric_key)
);
ALTER TABLE scores ENABLE ROW LEVEL SECURITY;
REVOKE ALL ON TABLE scores FROM anon, authenticated;

CREATE TABLE graph_runs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id uuid NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    status text NOT NULL DEFAULT 'running' CHECK (status IN ('running', 'succeeded', 'failed')),
    started_at timestamptz NOT NULL DEFAULT now(),
    ended_at timestamptz,
    total_tokens_in integer NOT NULL DEFAULT 0 CHECK (total_tokens_in >= 0),
    total_tokens_out integer NOT NULL DEFAULT 0 CHECK (total_tokens_out >= 0),
    cost_usd numeric(10, 6) NOT NULL DEFAULT 0 CHECK (cost_usd >= 0),
    error text
);
ALTER TABLE graph_runs ENABLE ROW LEVEL SECURITY;
REVOKE ALL ON TABLE graph_runs FROM anon, authenticated;

CREATE TABLE node_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id uuid NOT NULL REFERENCES graph_runs (id) ON DELETE CASCADE,
    seq bigint NOT NULL CHECK (seq >= 1),
    node_id text NOT NULL,
    type text NOT NULL,
    attempt integer NOT NULL DEFAULT 1 CHECK (attempt >= 1),
    model text,
    tokens_in integer NOT NULL DEFAULT 0 CHECK (tokens_in >= 0),
    tokens_out integer NOT NULL DEFAULT 0 CHECK (tokens_out >= 0),
    duration_ms integer NOT NULL DEFAULT 0 CHECK (duration_ms >= 0),
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (run_id, seq)
);
ALTER TABLE node_events ENABLE ROW LEVEL SECURITY;
REVOKE ALL ON TABLE node_events FROM anon, authenticated;

CREATE INDEX applications_job_id_idx ON applications (job_id);
CREATE INDEX sessions_application_id_idx ON sessions (application_id);
CREATE INDEX sessions_status_expires_at_idx ON sessions (status, expires_at);
CREATE INDEX topics_session_id_idx ON topics (session_id);
CREATE INDEX messages_session_id_created_at_idx ON messages (session_id, created_at);
CREATE INDEX graph_runs_session_id_idx ON graph_runs (session_id);

ALTER TABLE goose_db_version ENABLE ROW LEVEL SECURITY;
REVOKE ALL ON TABLE goose_db_version FROM anon, authenticated;

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'anon')
        AND EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'authenticated') THEN
        EXECUTE format('ALTER DEFAULT PRIVILEGES IN SCHEMA %I REVOKE ALL ON TABLES FROM anon, authenticated', current_schema());
        EXECUTE format('ALTER DEFAULT PRIVILEGES IN SCHEMA %I REVOKE ALL ON SEQUENCES FROM anon, authenticated', current_schema());
        EXECUTE format('REVOKE ALL ON ALL TABLES IN SCHEMA %I FROM anon, authenticated', current_schema());
        EXECUTE format('REVOKE ALL ON ALL SEQUENCES IN SCHEMA %I FROM anon, authenticated', current_schema());
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS node_events;
DROP TABLE IF EXISTS graph_runs;
DROP TABLE IF EXISTS scores;
DROP TABLE IF EXISTS messages;
DROP TABLE IF EXISTS topics;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS applications;
DROP TABLE IF EXISTS jobs;
