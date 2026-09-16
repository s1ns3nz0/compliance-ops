-- 0001_init: core compliance tracking schema.
-- gen_random_uuid() is built into PostgreSQL 13+; no extension required.

CREATE TABLE IF NOT EXISTS frameworks (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    oscal_document_id text NOT NULL UNIQUE,
    type              text NOT NULL DEFAULT '',
    title             text NOT NULL DEFAULT '',
    last_modified     text NOT NULL DEFAULT '',
    imported_at       timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS requirements (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    framework_id uuid NOT NULL REFERENCES frameworks(id) ON DELETE CASCADE,
    control_id   text NOT NULL,
    title        text NOT NULL DEFAULT '',
    text         text NOT NULL DEFAULT '',
    status       text NOT NULL DEFAULT 'not_started'
                 CHECK (status IN ('not_started', 'in_progress', 'implemented', 'not_applicable')),
    owner        text NOT NULL DEFAULT '',
    due_date     date NULL,
    notes        text NOT NULL DEFAULT '',
    updated_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (framework_id, control_id)
);

CREATE INDEX IF NOT EXISTS requirements_framework_status_idx ON requirements (framework_id, status);

CREATE TABLE IF NOT EXISTS evidence (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    title          text NOT NULL DEFAULT '',
    description    text NOT NULL DEFAULT '',
    file_name      text NOT NULL DEFAULT '',
    content_type   text NOT NULL DEFAULT '',
    size_bytes     bigint NOT NULL DEFAULT 0,
    sha256         text NOT NULL DEFAULT '',
    valid_from     date NULL,
    valid_until    date NULL,
    review_state   text NOT NULL DEFAULT 'pending'
                   CHECK (review_state IN ('pending', 'approved', 'rejected')),
    reviewer       text NOT NULL DEFAULT '',
    review_comment text NOT NULL DEFAULT '',
    reviewed_at    timestamptz NULL,
    uploaded_by    text NOT NULL DEFAULT '',
    created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS evidence_review_state_idx ON evidence (review_state);
CREATE INDEX IF NOT EXISTS evidence_valid_until_idx ON evidence (valid_until);
CREATE INDEX IF NOT EXISTS evidence_created_at_idx ON evidence (created_at DESC);

CREATE TABLE IF NOT EXISTS evidence_requirements (
    evidence_id    uuid NOT NULL REFERENCES evidence(id) ON DELETE CASCADE,
    requirement_id uuid NOT NULL REFERENCES requirements(id) ON DELETE CASCADE,
    PRIMARY KEY (evidence_id, requirement_id)
);

CREATE INDEX IF NOT EXISTS evidence_requirements_requirement_idx ON evidence_requirements (requirement_id);

CREATE TABLE IF NOT EXISTS audit_log (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    at          timestamptz NOT NULL DEFAULT now(),
    actor       text NOT NULL DEFAULT '',
    action      text NOT NULL,
    entity_type text NOT NULL,
    entity_id   text NOT NULL DEFAULT '',
    detail      jsonb NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX IF NOT EXISTS audit_log_at_idx ON audit_log (at DESC);
