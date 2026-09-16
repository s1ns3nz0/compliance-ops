CREATE TABLE scope_categories (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL CHECK (name = btrim(name) AND char_length(name) BETWEEN 1 AND 100),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX scope_categories_name_lower_uidx ON scope_categories (lower(name));
CREATE TABLE part_scope_categories (
    requirement_id uuid NOT NULL,
    part_id text NOT NULL,
    scope_category_id uuid NOT NULL REFERENCES scope_categories(id) ON DELETE RESTRICT,
    PRIMARY KEY (requirement_id, part_id, scope_category_id),
    FOREIGN KEY (requirement_id, part_id) REFERENCES part_tracking(requirement_id, part_id) ON DELETE CASCADE
);
CREATE INDEX part_scope_categories_category_idx ON part_scope_categories(scope_category_id);
INSERT INTO scope_categories(name)
SELECT DISTINCT CASE btrim(scope)
    WHEN 'DEMO: fictional production service scope' THEN 'Demo production service'
    WHEN 'node-operator repository at commit 8f4121f' THEN 'node-operator'
    ELSE btrim(scope)
END FROM part_tracking WHERE btrim(scope) <> ''
ON CONFLICT (lower(name)) DO NOTHING;
INSERT INTO part_scope_categories(requirement_id, part_id, scope_category_id)
SELECT pt.requirement_id, pt.part_id, sc.id FROM part_tracking pt
JOIN scope_categories sc ON lower(sc.name) = lower(CASE btrim(pt.scope)
    WHEN 'DEMO: fictional production service scope' THEN 'Demo production service'
    WHEN 'node-operator repository at commit 8f4121f' THEN 'node-operator'
    ELSE btrim(pt.scope) END)
WHERE btrim(pt.scope) <> '';
ALTER TABLE part_tracking DROP COLUMN scope;