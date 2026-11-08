-- +goose Up
ALTER TABLE node_events ALTER COLUMN node_id DROP NOT NULL;
ALTER TABLE node_events ALTER COLUMN attempt DROP NOT NULL;

-- +goose Down
ALTER TABLE node_events ALTER COLUMN node_id SET NOT NULL;
ALTER TABLE node_events ALTER COLUMN attempt SET NOT NULL;
