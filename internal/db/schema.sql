-- Idempotent schema. The events table is the source of truth; patients
-- and notes are projections and may be rebuilt from events at any time.

CREATE TABLE IF NOT EXISTS events (
    seq              BIGSERIAL PRIMARY KEY,
    event_type       TEXT        NOT NULL,
    aggregate_type   TEXT        NOT NULL,
    aggregate_id     UUID        NOT NULL,
    payload          BYTEA       NOT NULL, -- canonical bytes, exact
    payload_json     JSONB       NOT NULL, -- queryable mirror of payload
    payload_encoding TEXT        NOT NULL DEFAULT 'json/v1',
    actor_hpi        TEXT        NOT NULL,
    at               TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS events_aggregate_idx
    ON events (aggregate_type, aggregate_id, seq);

CREATE TABLE IF NOT EXISTS audit_events (
    seq           BIGINT      PRIMARY KEY, -- explicit: chained, no gaps
    prev_hash     BYTEA       NOT NULL,
    hash          BYTEA       NOT NULL,
    actor_hpi     TEXT        NOT NULL,
    action        TEXT        NOT NULL, -- C create | R read
    resource_type TEXT        NOT NULL,
    resource_id   TEXT        NOT NULL,
    at            TIMESTAMPTZ NOT NULL,
    detail        TEXT        NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS patients (
    id             UUID   PRIMARY KEY,
    nhi            TEXT   NOT NULL UNIQUE,
    nhi_format     TEXT   NOT NULL,
    family_name    TEXT   NOT NULL,
    given_name     TEXT   NOT NULL,
    birth_date     DATE,
    last_event_seq BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS notes (
    id             UUID        PRIMARY KEY,
    patient_id     UUID        NOT NULL REFERENCES patients(id),
    author_hpi     TEXT        NOT NULL,
    text           TEXT        NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL,
    last_event_seq BIGINT      NOT NULL
);

CREATE TABLE IF NOT EXISTS projection_state (
    id       TEXT   PRIMARY KEY,
    last_seq BIGINT NOT NULL
);
INSERT INTO projection_state (id, last_seq) VALUES ('main', 0)
    ON CONFLICT DO NOTHING;
