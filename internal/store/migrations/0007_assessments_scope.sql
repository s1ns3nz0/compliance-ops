ALTER TABLE part_tracking ADD COLUMN scope text NOT NULL DEFAULT '';

UPDATE part_tracking pt
SET scope = CASE f.short_name
    WHEN 'NIST SP 800-53' THEN 'DEMO: fictional production service scope'
    WHEN 'NIST SP 800-218 SSDF' THEN 'node-operator repository at commit 8f4121f'
    ELSE ''
END
FROM requirements r
JOIN frameworks f ON f.id = r.framework_id
WHERE pt.requirement_id = r.id;

ALTER TABLE oscal_uploads
    ADD COLUMN linked_framework_id uuid NULL REFERENCES frameworks(id) ON DELETE SET NULL;

CREATE TABLE assessment_item_mappings (
    upload_id uuid NOT NULL REFERENCES oscal_uploads(id) ON DELETE CASCADE,
    item_key text NOT NULL,
    requirement_id uuid NOT NULL REFERENCES requirements(id) ON DELETE CASCADE,
    part_id text NOT NULL DEFAULT '',
    mapped_by text NOT NULL,
    mapped_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (upload_id, item_key)
);
CREATE INDEX assessment_item_mappings_requirement_idx ON assessment_item_mappings (requirement_id);
