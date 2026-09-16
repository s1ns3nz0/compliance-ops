-- 0003_params_activity_evidence: OSCAL control parameters on requirements,
-- evidence <-> activity links, and removal of the evidence review feature.

-- Requirements: the control's OSCAL parameters (id, label, values, choices,
-- howMany) so the UI can show what each [Assignment]/[Selection] refers to.
ALTER TABLE requirements ADD COLUMN IF NOT EXISTS params jsonb NOT NULL DEFAULT '[]'::jsonb;

-- Evidence may be attached to implementation activities. Deleting an
-- activity removes only the link rows; the evidence stays on the requirement.
CREATE TABLE IF NOT EXISTS evidence_activities (
    evidence_id uuid NOT NULL REFERENCES evidence(id) ON DELETE CASCADE,
    activity_id uuid NOT NULL REFERENCES implementation_activities(id) ON DELETE CASCADE,
    PRIMARY KEY (evidence_id, activity_id)
);

CREATE INDEX IF NOT EXISTS evidence_activities_activity_idx ON evidence_activities (activity_id);

-- Evidence review (pending/approved/rejected) is removed. Other evidence
-- data is untouched.
DROP INDEX IF EXISTS evidence_review_state_idx;
ALTER TABLE evidence DROP CONSTRAINT IF EXISTS evidence_review_state_check;
ALTER TABLE evidence
    DROP COLUMN IF EXISTS review_state,
    DROP COLUMN IF EXISTS reviewer,
    DROP COLUMN IF EXISTS review_comment,
    DROP COLUMN IF EXISTS reviewed_at;
