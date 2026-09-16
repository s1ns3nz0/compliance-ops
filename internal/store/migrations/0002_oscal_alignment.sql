-- 0002_oscal_alignment: framework short names, structured control parts,
-- OSCAL implementation-status values, implementation activities and
-- link-type evidence.

-- Frameworks: user-editable display short name (derived on import when empty).
ALTER TABLE frameworks ADD COLUMN IF NOT EXISTS short_name text NOT NULL DEFAULT '';

-- Requirements: structured OSCAL parts and related control ids.
ALTER TABLE requirements ADD COLUMN IF NOT EXISTS parts   jsonb NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE requirements ADD COLUMN IF NOT EXISTS related jsonb NOT NULL DEFAULT '[]'::jsonb;

-- Requirements: migrate the status vocabulary to OSCAL implementation-status.
--   not_started -> planned, in_progress -> partial.
ALTER TABLE requirements DROP CONSTRAINT IF EXISTS requirements_status_check;
UPDATE requirements SET status = 'planned' WHERE status = 'not_started';
UPDATE requirements SET status = 'partial' WHERE status = 'in_progress';
ALTER TABLE requirements ALTER COLUMN status SET DEFAULT 'planned';
ALTER TABLE requirements ADD CONSTRAINT requirements_status_check
    CHECK (status IN ('implemented', 'partial', 'planned', 'alternative', 'not_applicable'));

-- Implementation activities: free-text entries per requirement, optionally
-- scoped to one control part id (e.g. "ac-2_smt.a").
CREATE TABLE IF NOT EXISTS implementation_activities (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    requirement_id uuid NOT NULL REFERENCES requirements(id) ON DELETE CASCADE,
    part_id        text NOT NULL DEFAULT '',
    body           text NOT NULL,
    actor          text NOT NULL DEFAULT '',
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS implementation_activities_requirement_idx
    ON implementation_activities (requirement_id, created_at);

-- Evidence: file (default, existing rows) or link.
ALTER TABLE evidence ADD COLUMN IF NOT EXISTS kind text NOT NULL DEFAULT 'file';
ALTER TABLE evidence ADD COLUMN IF NOT EXISTS url  text NOT NULL DEFAULT '';
ALTER TABLE evidence DROP CONSTRAINT IF EXISTS evidence_kind_check;
ALTER TABLE evidence ADD CONSTRAINT evidence_kind_check CHECK (kind IN ('file', 'link'));
