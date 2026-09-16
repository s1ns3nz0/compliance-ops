-- 0004_part_tracking: per-statement-part implementation tracking replaces
-- implementation activities. Requirement status/owner/due_date become derived
-- from the parts; a manual status override is added.

-- One row per (requirement, statement part). Rows are created lazily by the
-- application; a trackable part without a row reads as planned.
CREATE TABLE IF NOT EXISTS part_tracking (
    requirement_id uuid NOT NULL REFERENCES requirements(id) ON DELETE CASCADE,
    part_id        text NOT NULL,
    status         text NOT NULL DEFAULT 'planned'
                   CHECK (status IN ('implemented', 'partial', 'planned', 'alternative', 'not_applicable')),
    owner          text NOT NULL DEFAULT '',
    due_date       date NULL,
    description    text NOT NULL DEFAULT '',
    updated_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (requirement_id, part_id)
);

-- Existing activities become the markdown description of their part, one
-- bullet per activity in creation order: "- YYYY-MM-DD (actor): body".
-- Rows for part ids that are not trackable are folded into the first
-- trackable part by the Go post-migration hook (seedPartTrackingFromLegacy),
-- which also seeds the parts with the requirement's previous status, owner
-- and due date so the derived values reproduce them.
INSERT INTO part_tracking (requirement_id, part_id, status, description, updated_at)
SELECT a.requirement_id,
       a.part_id,
       'planned',
       string_agg('- ' || to_char(a.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD') || ' (' || a.actor || '): ' || a.body, E'\n' ORDER BY a.created_at, a.id),
       max(a.updated_at)
FROM implementation_activities a
GROUP BY a.requirement_id, a.part_id
ON CONFLICT (requirement_id, part_id) DO NOTHING;

-- Manual status override (NULL = use the derived status).
ALTER TABLE requirements ADD COLUMN IF NOT EXISTS status_override text NULL;
ALTER TABLE requirements DROP CONSTRAINT IF EXISTS requirements_status_override_check;
ALTER TABLE requirements ADD CONSTRAINT requirements_status_override_check
    CHECK (status_override IS NULL OR status_override IN ('implemented', 'partial', 'planned', 'alternative', 'not_applicable'));

-- Activities are gone (evidence stays linked to requirements).
DROP TABLE IF EXISTS evidence_activities;
DROP INDEX IF EXISTS implementation_activities_requirement_idx;
DROP TABLE IF EXISTS implementation_activities;
