-- Idempotent schema. The events table is the source of truth; all other
-- tables are projections and may be rebuilt from events at any time.

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

-- Core identity projection — HISO 10046:2025 §2 person identity fields.
-- Everything else (allergies, medications, history) is a module projection.
CREATE TABLE IF NOT EXISTS patients (
    id                UUID   PRIMARY KEY,
    nhi               TEXT   NOT NULL UNIQUE,
    nhi_format        TEXT   NOT NULL,
    family_name       TEXT   NOT NULL,
    given_name        TEXT   NOT NULL,
    birth_date        DATE,
    gender            TEXT,           -- male | female | other | unknown
    ethnicity_codes   TEXT[],         -- level-4 codes, up to 6
    nz_citizenship    TEXT,           -- citizenship status code
    citizenship_src   TEXT,           -- information source
    birth_country     TEXT,           -- ISO-3166 2-letter
    birth_place       TEXT,           -- free-text city/town
    dhb_code          TEXT,           -- HPI-ORG DHB code
    iwi_codes         TEXT[],         -- Stats NZ iwi codes
    last_event_seq    BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS notes (
    id             UUID        PRIMARY KEY,
    patient_id     UUID        NOT NULL REFERENCES patients(id),
    author_hpi     TEXT        NOT NULL,
    text           TEXT        NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL,
    last_event_seq BIGINT      NOT NULL
);

-- Encounters: GP visits, ED admissions, ward stays, telehealth consults.
CREATE TABLE IF NOT EXISTS encounters (
    id              UUID        PRIMARY KEY,
    patient_id      UUID        NOT NULL REFERENCES patients(id),
    encounter_class TEXT        NOT NULL, -- ambulatory | emergency | inpatient | virtual
    status          TEXT        NOT NULL DEFAULT 'in-progress', -- in-progress | finished
    facility        TEXT,
    ward            TEXT,
    disposition     TEXT,               -- set on close: home | transfer | deceased
    opened_at       TIMESTAMPTZ NOT NULL,
    closed_at       TIMESTAMPTZ,
    last_event_seq  BIGINT      NOT NULL
);
CREATE INDEX IF NOT EXISTS encounters_patient_idx ON encounters (patient_id);

-- Diagnoses recorded during an encounter. Working diagnoses may be
-- superseded; discharge diagnoses flow into the problem list on close.
CREATE TABLE IF NOT EXISTS encounter_diagnoses (
    id           UUID        PRIMARY KEY,
    encounter_id UUID        NOT NULL REFERENCES encounters(id),
    patient_id   UUID        NOT NULL REFERENCES patients(id),
    code         TEXT,                -- SNOMED CT or ICD-10-AM
    display      TEXT        NOT NULL,
    type         TEXT        NOT NULL, -- working | discharge | billing
    rank         INT         NOT NULL DEFAULT 1,
    recorded_at  TIMESTAMPTZ NOT NULL,
    last_event_seq BIGINT    NOT NULL
);
CREATE INDEX IF NOT EXISTS encounter_diagnoses_patient_idx ON encounter_diagnoses (patient_id);
CREATE INDEX IF NOT EXISTS encounter_diagnoses_encounter_idx ON encounter_diagnoses (encounter_id);

-- Problem list — active conditions. When resolved, they move to past_history.
CREATE TABLE IF NOT EXISTS problems (
    id           UUID        PRIMARY KEY,
    patient_id   UUID        NOT NULL REFERENCES patients(id),
    code         TEXT,
    display      TEXT        NOT NULL,
    category     TEXT,                -- medical | surgical | social | psychiatric
    status       TEXT        NOT NULL DEFAULT 'active', -- active | resolved
    onset_date   DATE,
    resolved_at  TIMESTAMPTZ,
    last_event_seq BIGINT    NOT NULL
);

-- Past medical/surgical/social history — resolved problems land here.
CREATE TABLE IF NOT EXISTS past_history (
    problem_id   UUID PRIMARY KEY REFERENCES problems(id),
    patient_id   UUID NOT NULL REFERENCES patients(id),
    category     TEXT NOT NULL,
    display      TEXT NOT NULL,
    onset_date   DATE,
    resolved_at  TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS past_history_patient_idx ON past_history (patient_id);

-- Family connections between patient records.
CREATE TABLE IF NOT EXISTS family_connections (
    id           UUID PRIMARY KEY,
    patient_id   UUID NOT NULL REFERENCES patients(id),
    relative_id  UUID NOT NULL REFERENCES patients(id),
    relationship TEXT NOT NULL,       -- parent | child | sibling | spouse | ...
    recorded_at  TIMESTAMPTZ NOT NULL,
    removed_at   TIMESTAMPTZ,
    last_event_seq BIGINT NOT NULL
);
CREATE INDEX IF NOT EXISTS family_connections_patient_idx ON family_connections (patient_id);

-- Care team — clinicians involved in a patient's care, current and past.
CREATE TABLE IF NOT EXISTS care_team (
    id            UUID PRIMARY KEY,
    patient_id    UUID NOT NULL REFERENCES patients(id),
    clinician_hpi TEXT NOT NULL,
    role          TEXT NOT NULL,       -- GP | SMO | HouseOfficer | Gastroenterologist | ...
    start_date    DATE NOT NULL,
    end_date      DATE,
    last_event_seq BIGINT NOT NULL
);
CREATE INDEX IF NOT EXISTS care_team_patient_idx ON care_team (patient_id);

-- Allergies — owned by module/allergies.
CREATE TABLE IF NOT EXISTS allergies (
    id              UUID PRIMARY KEY,
    patient_id      UUID NOT NULL REFERENCES patients(id),
    substance       TEXT NOT NULL,
    reaction        TEXT,
    severity        TEXT,            -- mild | moderate | severe | life-threatening
    status          TEXT NOT NULL DEFAULT 'active', -- active | inactive
    recorded_by_hpi TEXT NOT NULL,
    recorded_at     TIMESTAMPTZ NOT NULL,
    removed_at      TIMESTAMPTZ,
    removed_by_hpi  TEXT,
    reason          TEXT,
    last_event_seq  BIGINT NOT NULL
);
CREATE INDEX IF NOT EXISTS allergies_patient_idx ON allergies (patient_id);

-- Projection checkpoint — tracks which events have been applied.
CREATE TABLE IF NOT EXISTS projection_state (
    id       TEXT   PRIMARY KEY,
    last_seq BIGINT NOT NULL
);
INSERT INTO projection_state (id, last_seq) VALUES ('main', 0)
    ON CONFLICT DO NOTHING;
