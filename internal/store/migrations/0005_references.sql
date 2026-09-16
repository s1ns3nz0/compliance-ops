-- 0005_references: resolved OSCAL external references on requirements.
--
-- `related` keeps only control ids (links with rel=related). External
-- references (rel=external_reference / rel=reference into back-matter),
-- including those carried by folded SSDF tasks, are stored here as
-- [{uuid, title, citation, url, text, taskId}] and refreshed on import.
ALTER TABLE requirements ADD COLUMN IF NOT EXISTS references_json jsonb NOT NULL DEFAULT '[]'::jsonb;
