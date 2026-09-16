CREATE TABLE oscal_uploads (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id text NOT NULL,
    document_type text NOT NULL,
    title text NOT NULL,
    version text NOT NULL DEFAULT '',
    last_modified text NOT NULL DEFAULT '',
    sha256 text UNIQUE NOT NULL,
    size_bytes bigint NOT NULL CHECK (size_bytes BETWEEN 1 AND 10485760),
    content jsonb NOT NULL,
    uploaded_by text NOT NULL,
    uploaded_at timestamptz NOT NULL DEFAULT now(),
    imported_framework_id uuid NULL REFERENCES frameworks(id) ON DELETE SET NULL
);
CREATE INDEX oscal_uploads_uploaded_at_idx ON oscal_uploads (uploaded_at DESC);
CREATE INDEX oscal_uploads_document_id_idx ON oscal_uploads (document_id);