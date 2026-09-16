package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/s1ns3nz0/compliance-ops/internal/oscal"
)

// Postgres is the production Store backed by PostgreSQL.
type Postgres struct {
	pool *pgxpool.Pool
}

// NewPostgres wraps an existing pool. The caller is responsible for running
// Migrate before use and for closing the pool.
func NewPostgres(pool *pgxpool.Pool) *Postgres { return &Postgres{pool: pool} }

// OpenPostgres connects to databaseURL, verifies connectivity and applies
// pending migrations.
func OpenPostgres(ctx context.Context, databaseURL string) (*Postgres, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	if err := Migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return NewPostgres(pool), nil
}

// Pool exposes the underlying pool (health checks, shutdown).
func (p *Postgres) Pool() *pgxpool.Pool { return p.pool }

// Close releases the connection pool.
func (p *Postgres) Close() { p.pool.Close() }

func (p *Postgres) AcquireEvidenceDigestLock(ctx context.Context, digest string) (func(context.Context) error, error) {
	key, err := evidenceDigestLockKey(digest)
	if err != nil {
		return nil, err
	}
	conn, err := p.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, key); err != nil {
		conn.Release()
		return nil, err
	}
	var once sync.Once
	var releaseErr error
	return func(releaseCtx context.Context) error {
		once.Do(func() {
			var unlocked bool
			releaseErr = conn.QueryRow(releaseCtx, `SELECT pg_advisory_unlock($1)`, key).Scan(&unlocked)
			if releaseErr == nil && !unlocked {
				releaseErr = errors.New("postgres advisory lock was not held")
			}
			if releaseErr != nil {
				physical := conn.Hijack()
				_ = physical.Close(context.Background())
				return
			}
			conn.Release()
		})
		return releaseErr
	}, nil
}

const oscalUploadCols = `u.id::text, u.document_id, u.document_type, u.title, u.version, u.last_modified, u.sha256, u.size_bytes, u.uploaded_by, u.uploaded_at, COALESCE(u.imported_framework_id::text, ''), COALESCE(u.linked_framework_id::text, '')`

func scanOscalUpload(row pgx.Row, withDocument bool) (OscalUpload, error) {
	var u OscalUpload
	var err error
	if withDocument {
		err = row.Scan(&u.ID, &u.DocumentID, &u.Type, &u.Title, &u.Version, &u.LastModified, &u.Sha256, &u.SizeBytes, &u.UploadedBy, &u.UploadedAt, &u.ImportedFrameworkID, &u.LinkedFrameworkID, &u.Document)
	} else {
		err = row.Scan(&u.ID, &u.DocumentID, &u.Type, &u.Title, &u.Version, &u.LastModified, &u.Sha256, &u.SizeBytes, &u.UploadedBy, &u.UploadedAt, &u.ImportedFrameworkID, &u.LinkedFrameworkID)
	}
	if err != nil {
		return OscalUpload{}, mapErr(err)
	}
	u.UploadedAt = u.UploadedAt.UTC()
	return u, nil
}

func (p *Postgres) CreateOscalUpload(ctx context.Context, d oscal.Document, raw []byte, sha string, size int64, actor string) (OscalUpload, error) {
	var out OscalUpload
	err := p.withTx(ctx, func(tx pgx.Tx) error {
		var id string
		err := tx.QueryRow(ctx, `INSERT INTO oscal_uploads (document_id, document_type, title, version, last_modified, sha256, size_bytes, content, uploaded_by) VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9) ON CONFLICT (sha256) DO NOTHING RETURNING id::text`,
			d.ID, d.Type, d.Title, d.Version, d.LastModified, sha, size, raw, actor).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			existing, getErr := scanOscalUpload(tx.QueryRow(ctx, `SELECT `+oscalUploadCols+` FROM oscal_uploads u WHERE u.sha256=$1`, sha), false)
			if getErr != nil {
				return getErr
			}
			return &ConflictError{Existing: existing}
		}
		if err != nil {
			return err
		}
		u, err := scanOscalUpload(tx.QueryRow(ctx, `SELECT `+oscalUploadCols+` FROM oscal_uploads u WHERE u.id=$1`, id), false)
		if err != nil {
			return err
		}
		out = u
		return insertAudit(ctx, tx, actor, "oscal.upload", "oscal_upload", u.ID, map[string]any{"documentId": u.DocumentID, "type": u.Type, "title": u.Title, "sha256": u.Sha256, "sizeBytes": u.SizeBytes})
	})
	return out, err
}

func (p *Postgres) ListOscalUploads(ctx context.Context) ([]OscalUpload, error) {
	rows, err := p.pool.Query(ctx, `SELECT `+oscalUploadCols+` FROM oscal_uploads u ORDER BY u.uploaded_at DESC,u.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OscalUpload{}
	for rows.Next() {
		u, err := scanOscalUpload(rows, false)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
func (p *Postgres) GetOscalUpload(ctx context.Context, id string) (OscalUpload, error) {
	if !validUUID(id) {
		return OscalUpload{}, ErrNotFound
	}
	return scanOscalUpload(p.pool.QueryRow(ctx, `SELECT `+oscalUploadCols+`, u.content FROM oscal_uploads u WHERE u.id=$1`, id), true)
}
func (p *Postgres) DeleteOscalUpload(ctx context.Context, id, actor string) error {
	if !validUUID(id) {
		return ErrNotFound
	}
	return p.withTx(ctx, func(tx pgx.Tx) error {
		u, err := scanOscalUpload(tx.QueryRow(ctx, `SELECT `+oscalUploadCols+` FROM oscal_uploads u WHERE u.id=$1 FOR UPDATE`, id), false)
		if err != nil {
			return err
		}
		if u.ImportedFrameworkID != "" {
			return ErrConflict
		}
		if err := insertAudit(ctx, tx, actor, "oscal.delete", "oscal_upload", id, map[string]any{"documentId": u.DocumentID, "sha256": u.Sha256}); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `DELETE FROM oscal_uploads WHERE id=$1`, id)
		return err
	})
}
func (p *Postgres) MarkOscalUploadImported(ctx context.Context, id, frameworkID string) error {
	if !validUUID(id) || !validUUID(frameworkID) {
		return ErrNotFound
	}
	tag, err := p.pool.Exec(ctx, `UPDATE oscal_uploads SET imported_framework_id=COALESCE(imported_framework_id,$2) WHERE id=$1`, id, frameworkID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *Postgres) SetAssessmentFramework(ctx context.Context, uploadID, frameworkID, actor string) (OscalUpload, error) {
	if !validUUID(uploadID) || (frameworkID != "" && !validUUID(frameworkID)) {
		return OscalUpload{}, ErrNotFound
	}
	var out OscalUpload
	err := p.withTx(ctx, func(tx pgx.Tx) error {
		u, err := scanOscalUpload(tx.QueryRow(ctx, `SELECT `+oscalUploadCols+` FROM oscal_uploads u WHERE id=$1 FOR UPDATE`, uploadID), false)
		if err != nil {
			return err
		}
		if !assessmentUploadType(u.Type) {
			return ErrInvalid
		}
		if frameworkID != "" {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM frameworks WHERE id=$1)`, frameworkID).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return ErrNotFound
			}
		}
		var fw any
		if frameworkID != "" {
			fw = frameworkID
		}
		if _, err = tx.Exec(ctx, `UPDATE oscal_uploads SET linked_framework_id=$2 WHERE id=$1`, uploadID, fw); err != nil {
			return err
		}
		out, err = scanOscalUpload(tx.QueryRow(ctx, `SELECT `+oscalUploadCols+` FROM oscal_uploads u WHERE id=$1`, uploadID), false)
		if err != nil {
			return err
		}
		return insertAudit(ctx, tx, actor, "assessment.framework_link", "oscal_upload", uploadID, map[string]any{"frameworkId": fw})
	})
	return out, err
}

func scanAssessmentMapping(row pgx.Row) (AssessmentItemMapping, error) {
	var m AssessmentItemMapping
	if err := row.Scan(&m.UploadID, &m.ItemKey, &m.RequirementID, &m.PartID, &m.MappedBy, &m.MappedAt); err != nil {
		return m, mapErr(err)
	}
	m.MappedAt = m.MappedAt.UTC()
	return m, nil
}

const assessmentMappingCols = `upload_id::text,item_key,requirement_id::text,part_id,mapped_by,mapped_at`

func (p *Postgres) UpsertAssessmentMapping(ctx context.Context, m AssessmentItemMapping, actor string) (AssessmentItemMapping, error) {
	if !validUUID(m.UploadID) || !validUUID(m.RequirementID) || strings.TrimSpace(m.ItemKey) == "" {
		return AssessmentItemMapping{}, ErrInvalid
	}
	var out AssessmentItemMapping
	err := p.withTx(ctx, func(tx pgx.Tx) error {
		var typ, control string
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT u.document_type,r.control_id,r.parts FROM oscal_uploads u CROSS JOIN requirements r WHERE u.id=$1 AND r.id=$2`, m.UploadID, m.RequirementID).Scan(&typ, &control, &raw); err != nil {
			return mapErr(err)
		}
		if !assessmentUploadType(typ) {
			return ErrInvalid
		}
		parts, err := decodeParts(raw)
		if err != nil {
			return err
		}
		if m.PartID != "" && !isTrackable(&Requirement{ControlID: control, Parts: parts}, m.PartID) {
			return ErrInvalid
		}
		out, err = scanAssessmentMapping(tx.QueryRow(ctx, `INSERT INTO assessment_item_mappings(upload_id,item_key,requirement_id,part_id,mapped_by) VALUES($1,$2,$3,$4,$5) ON CONFLICT(upload_id,item_key) DO UPDATE SET requirement_id=EXCLUDED.requirement_id,part_id=EXCLUDED.part_id,mapped_by=EXCLUDED.mapped_by,mapped_at=now() RETURNING `+assessmentMappingCols, m.UploadID, m.ItemKey, m.RequirementID, m.PartID, actor))
		if err != nil {
			return err
		}
		return insertAudit(ctx, tx, actor, "assessment.item_map", "oscal_upload", m.UploadID, map[string]any{"itemKey": m.ItemKey, "requirementId": m.RequirementID, "partId": m.PartID})
	})
	return out, err
}
func (p *Postgres) DeleteAssessmentMapping(ctx context.Context, uploadID, itemKey, actor string) error {
	if !validUUID(uploadID) {
		return ErrNotFound
	}
	return p.withTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM assessment_item_mappings WHERE upload_id=$1 AND item_key=$2`, uploadID, itemKey)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return insertAudit(ctx, tx, actor, "assessment.item_unmap", "oscal_upload", uploadID, map[string]any{"itemKey": itemKey})
	})
}
func listMappings(ctx context.Context, q querier, where string, arg string) ([]AssessmentItemMapping, error) {
	rows, err := q.Query(ctx, `SELECT `+assessmentMappingCols+` FROM assessment_item_mappings WHERE `+where+` ORDER BY item_key`, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AssessmentItemMapping{}
	for rows.Next() {
		m, err := scanAssessmentMapping(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
func (p *Postgres) ListAssessmentMappings(ctx context.Context, uploadID string) ([]AssessmentItemMapping, error) {
	if !validUUID(uploadID) {
		return nil, ErrNotFound
	}
	var exists bool
	if err := p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM oscal_uploads WHERE id=$1)`, uploadID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	return listMappings(ctx, p.pool, "upload_id=$1", uploadID)
}
func (p *Postgres) ListRequirementAssessments(ctx context.Context, requirementID string) ([]AssessmentItemMapping, error) {
	if !validUUID(requirementID) {
		return nil, ErrNotFound
	}
	var exists bool
	if err := p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM requirements WHERE id=$1)`, requirementID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	return listMappings(ctx, p.pool, "requirement_id=$1", requirementID)
}

// querier is satisfied by both *pgxpool.Pool and pgx.Tx.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// ---- helpers ---------------------------------------------------------------

// validUUID reports whether s is a canonical 36-char hex UUID. Lookups with
// malformed ids return ErrNotFound instead of a database error.
func validUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
			if !isHex {
				return false
			}
		}
	}
	return true
}

func mapErr(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// dateArg normalizes a *time.Time to a UTC midnight value suitable for a DATE
// column; nil stays NULL.
func dateArg(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	d := time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
	return &d
}

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

func insertAudit(ctx context.Context, q querier, actor, action, entityType, entityID string, detail map[string]any) error {
	if detail == nil {
		detail = map[string]any{}
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		return fmt.Errorf("encode audit detail: %w", err)
	}
	_, err = q.Exec(ctx, `INSERT INTO audit_log (actor, action, entity_type, entity_id, detail) VALUES ($1, $2, $3, $4, $5::jsonb)`,
		actor, action, entityType, entityID, raw)
	if err != nil {
		return fmt.Errorf("insert audit: %w", err)
	}
	return nil
}

// withTx runs fn inside a transaction, committing on nil error.
func (p *Postgres) withTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ---- frameworks ------------------------------------------------------------

const frameworkCols = `f.id::text, f.oscal_document_id, f.type, f.title, f.short_name, f.last_modified, f.imported_at,
	(SELECT count(*) FROM requirements r WHERE r.framework_id = f.id)::int`

func scanFramework(row pgx.Row) (Framework, error) {
	var f Framework
	err := row.Scan(&f.ID, &f.OscalDocumentID, &f.Type, &f.Title, &f.ShortName, &f.LastModified, &f.ImportedAt, &f.RequirementCount)
	if err != nil {
		return Framework{}, mapErr(err)
	}
	f.ImportedAt = f.ImportedAt.UTC()
	return f, nil
}

// ImportFramework implements Store. The whole import is one transaction.
func (p *Postgres) ImportFramework(ctx context.Context, doc oscal.Document, actor string) (ImportResult, error) {
	var res ImportResult
	err := p.withTx(ctx, func(tx pgx.Tx) error {
		var err error
		res, err = p.importFrameworkTx(ctx, tx, doc, actor)
		return err
	})
	if err != nil {
		return ImportResult{}, err
	}
	return res, nil
}

// ImportUploadedFramework imports and marks an upload in one transaction.
func (p *Postgres) ImportUploadedFramework(ctx context.Context, uploadID string, doc oscal.Document, actor string) (ImportResult, error) {
	if !validUUID(uploadID) {
		return ImportResult{}, ErrNotFound
	}
	var res ImportResult
	err := p.withTx(ctx, func(tx pgx.Tx) error {
		var documentID, documentType string
		if err := tx.QueryRow(ctx, `SELECT document_id, document_type FROM oscal_uploads WHERE id=$1 FOR UPDATE`, uploadID).Scan(&documentID, &documentType); err != nil {
			return mapErr(err)
		}
		if documentID != doc.ID || documentType != doc.Type {
			return ErrInvalid
		}
		var err error
		res, err = p.importFrameworkTx(ctx, tx, doc, actor)
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE oscal_uploads SET imported_framework_id=COALESCE(imported_framework_id,$2) WHERE id=$1`, uploadID, res.Framework.ID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrNotFound
		}
		return nil
	})
	if err != nil {
		return ImportResult{}, err
	}
	return res, nil
}

func (p *Postgres) importFrameworkTx(ctx context.Context, tx pgx.Tx, doc oscal.Document, actor string) (ImportResult, error) {
	var res ImportResult
	err := func() error {
		var fwID string
		// short_name is derived only when empty so user edits survive re-imports.
		err := tx.QueryRow(ctx, `INSERT INTO frameworks (oscal_document_id, type, title, short_name, last_modified, imported_at)
			VALUES ($1, $2, $3, $5, $4, now())
			ON CONFLICT (oscal_document_id) DO UPDATE
			SET type = EXCLUDED.type, title = EXCLUDED.title, last_modified = EXCLUDED.last_modified, imported_at = now(),
			    short_name = CASE WHEN frameworks.short_name = '' THEN EXCLUDED.short_name ELSE frameworks.short_name END
			RETURNING id::text`, doc.ID, doc.Type, doc.Title, doc.LastModified, oscal.ShortName(doc.Title, doc.Props())).Scan(&fwID)
		if err != nil {
			return fmt.Errorf("upsert framework: %w", err)
		}
		for _, c := range doc.Controls {
			parts, related, params, refs, err := encodeParts(c.Parts, c.Related, c.Params, c.References)
			if err != nil {
				return fmt.Errorf("encode parts %s: %w", c.ID, err)
			}
			var inserted bool
			err = tx.QueryRow(ctx, `INSERT INTO requirements (framework_id, control_id, title, text, parts, related, params, references_json)
				VALUES ($1, $2, $3, $4, $5::jsonb, $6::jsonb, $7::jsonb, $8::jsonb)
				ON CONFLICT (framework_id, control_id) DO UPDATE
				SET title = EXCLUDED.title, text = EXCLUDED.text, parts = EXCLUDED.parts, related = EXCLUDED.related, params = EXCLUDED.params, references_json = EXCLUDED.references_json
				RETURNING (xmax = 0) AS inserted`, fwID, c.ID, c.Title, c.Text, parts, related, params, refs).Scan(&inserted)
			if err != nil {
				return fmt.Errorf("upsert requirement %s: %w", c.ID, err)
			}
			if inserted {
				res.Created++
			} else {
				res.Updated++
			}
		}
		if err := recomputeFramework(ctx, tx, fwID); err != nil {
			return err
		}
		fw, err := scanFramework(tx.QueryRow(ctx, `SELECT `+frameworkCols+` FROM frameworks f WHERE f.id = $1`, fwID))
		if err != nil {
			return fmt.Errorf("reload framework: %w", err)
		}
		res.Framework = fw
		return insertAudit(ctx, tx, actor, "framework.import", "framework", fw.ID, map[string]any{"created": res.Created, "updated": res.Updated})
	}()
	if err != nil {
		return ImportResult{}, err
	}
	return res, nil
}

// ListFrameworks implements Store.
func (p *Postgres) ListFrameworks(ctx context.Context) ([]Framework, error) {
	rows, err := p.pool.Query(ctx, `SELECT `+frameworkCols+` FROM frameworks f ORDER BY f.title, f.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Framework{}
	for rows.Next() {
		f, err := scanFramework(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// GetFramework implements Store.
func (p *Postgres) GetFramework(ctx context.Context, id string) (Framework, error) {
	if !validUUID(id) {
		return Framework{}, ErrNotFound
	}
	return scanFramework(p.pool.QueryRow(ctx, `SELECT `+frameworkCols+` FROM frameworks f WHERE f.id = $1`, id))
}

// UpdateFramework implements Store.
func (p *Postgres) UpdateFramework(ctx context.Context, id, shortName, actor string) (Framework, error) {
	if !ValidShortName(shortName) {
		return Framework{}, ErrInvalid
	}
	if !validUUID(id) {
		return Framework{}, ErrNotFound
	}
	var out Framework
	err := p.withTx(ctx, func(tx pgx.Tx) error {
		var previous string
		if err := tx.QueryRow(ctx, `SELECT short_name FROM frameworks WHERE id = $1 FOR UPDATE`, id).Scan(&previous); err != nil {
			return mapErr(err)
		}
		if _, err := tx.Exec(ctx, `UPDATE frameworks SET short_name = $2 WHERE id = $1`, id, shortName); err != nil {
			return err
		}
		fw, err := scanFramework(tx.QueryRow(ctx, `SELECT `+frameworkCols+` FROM frameworks f WHERE f.id = $1`, id))
		if err != nil {
			return err
		}
		out = fw
		return insertAudit(ctx, tx, actor, "framework.update", "framework", out.ID, map[string]any{"shortName": map[string]any{"from": previous, "to": shortName}})
	})
	if err != nil {
		return Framework{}, err
	}
	return out, nil
}

const scopeCategoryCols = `id::text,name,created_at,updated_at`

func scanScopeCategory(row pgx.Row) (ScopeCategory, error) {
	var c ScopeCategory
	if err := row.Scan(&c.ID, &c.Name, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return c, mapErr(err)
	}
	c.CreatedAt, c.UpdatedAt = c.CreatedAt.UTC(), c.UpdatedAt.UTC()
	return c, nil
}
func scopeCategoryWriteErr(err error) error {
	var e *pgconn.PgError
	if errors.As(err, &e) && e.Code == "23505" {
		return ErrConflict
	}
	return err
}
func (p *Postgres) CreateScopeCategory(ctx context.Context, name, actor string) (ScopeCategory, error) {
	name = strings.TrimSpace(name)
	if !ValidScopeCategoryName(name) {
		return ScopeCategory{}, ErrInvalid
	}
	var out ScopeCategory
	err := p.withTx(ctx, func(tx pgx.Tx) error {
		var err error
		out, err = scanScopeCategory(tx.QueryRow(ctx, `INSERT INTO scope_categories(name) VALUES($1) RETURNING `+scopeCategoryCols, name))
		if err != nil {
			return scopeCategoryWriteErr(err)
		}
		return insertAudit(ctx, tx, actor, "scope-category.create", "scope_category", out.ID, map[string]any{"changed": true})
	})
	return out, err
}
func (p *Postgres) ListScopeCategories(ctx context.Context, query string, limit int) ([]ScopeCategory, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	q := "%" + escapeLike(strings.TrimSpace(query)) + "%"
	rows, err := p.pool.Query(ctx, `SELECT `+scopeCategoryCols+` FROM scope_categories WHERE name ILIKE $1 ESCAPE '\' ORDER BY lower(name),name,id LIMIT $2`, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ScopeCategory{}
	for rows.Next() {
		c, err := scanScopeCategory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (p *Postgres) UpdateScopeCategory(ctx context.Context, id, name string, expected UpdatedAtPrecondition, actor string) (ScopeCategory, error) {
	name = strings.TrimSpace(name)
	if !validUUID(id) {
		return ScopeCategory{}, ErrNotFound
	}
	if !ValidScopeCategoryName(name) {
		return ScopeCategory{}, ErrInvalid
	}
	var out ScopeCategory
	err := p.withTx(ctx, func(tx pgx.Tx) error {
		current, err := scanScopeCategory(tx.QueryRow(ctx, `SELECT `+scopeCategoryCols+` FROM scope_categories WHERE id=$1 FOR UPDATE`, id))
		if err != nil {
			return err
		}
		if err := checkUpdatedAtPrecondition(expected, true, current.UpdatedAt); err != nil {
			return err
		}
		out, err = scanScopeCategory(tx.QueryRow(ctx, `UPDATE scope_categories SET name=$2,updated_at=GREATEST(clock_timestamp(),updated_at + interval '1 microsecond') WHERE id=$1 RETURNING `+scopeCategoryCols, id, name))
		if err != nil {
			return scopeCategoryWriteErr(err)
		}
		return insertAudit(ctx, tx, actor, "scope-category.update", "scope_category", id, map[string]any{"changed": true})
	})
	return out, err
}
func (p *Postgres) DeleteScopeCategory(ctx context.Context, id string, expected UpdatedAtPrecondition, actor string) error {
	if !validUUID(id) {
		return ErrNotFound
	}
	return p.withTx(ctx, func(tx pgx.Tx) error {
		current, err := scanScopeCategory(tx.QueryRow(ctx, `SELECT `+scopeCategoryCols+` FROM scope_categories WHERE id=$1 FOR UPDATE`, id))
		if err != nil {
			return err
		}
		if err := checkUpdatedAtPrecondition(expected, true, current.UpdatedAt); err != nil {
			return err
		}
		var used bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM part_scope_categories WHERE scope_category_id=$1)`, id).Scan(&used); err != nil {
			return err
		}
		if used {
			return ErrConflict
		}
		tag, err := tx.Exec(ctx, `DELETE FROM scope_categories WHERE id=$1`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return insertAudit(ctx, tx, actor, "scope-category.delete", "scope_category", id, map[string]any{"changed": true})
	})
}

// ---- requirements ----------------------------------------------------------

const requirementCols = `r.id::text, r.framework_id::text, r.control_id, r.title, r.text, r.status, COALESCE(r.status_override, ''), r.owner, r.due_date, r.notes, r.updated_at, r.parts, r.related, r.params, r.references_json`

func encodeParts(parts []oscal.Part, related []string, params []oscal.Param, refs []oscal.Reference) (partsJSON, relatedJSON, paramsJSON, refsJSON []byte, err error) {
	if parts == nil {
		parts = []oscal.Part{}
	}
	if related == nil {
		related = []string{}
	}
	if params == nil {
		params = []oscal.Param{}
	}
	if refs == nil {
		refs = []oscal.Reference{}
	}
	if partsJSON, err = json.Marshal(parts); err != nil {
		return nil, nil, nil, nil, err
	}
	if relatedJSON, err = json.Marshal(related); err != nil {
		return nil, nil, nil, nil, err
	}
	if paramsJSON, err = json.Marshal(params); err != nil {
		return nil, nil, nil, nil, err
	}
	if refsJSON, err = json.Marshal(refs); err != nil {
		return nil, nil, nil, nil, err
	}
	return partsJSON, relatedJSON, paramsJSON, refsJSON, nil
}

func scanRequirement(row pgx.Row, extra ...any) (Requirement, error) {
	var r Requirement
	var parts, related, params, refs []byte
	dest := []any{&r.ID, &r.FrameworkID, &r.ControlID, &r.Title, &r.Text, &r.DerivedStatus, &r.StatusOverride, &r.Owner, &r.DueDate, &r.Notes, &r.UpdatedAt, &parts, &related, &params, &refs}
	dest = append(dest, extra...)
	if err := row.Scan(dest...); err != nil {
		return Requirement{}, mapErr(err)
	}
	r.Status = effectiveStatus(r.DerivedStatus, r.StatusOverride)
	r.DueDate = utcPtr(r.DueDate)
	r.UpdatedAt = r.UpdatedAt.UTC()
	if len(parts) > 0 {
		if err := json.Unmarshal(parts, &r.Parts); err != nil {
			return Requirement{}, fmt.Errorf("decode parts: %w", err)
		}
	}
	if r.Parts == nil {
		r.Parts = []oscal.Part{}
	}
	if len(related) > 0 {
		if err := json.Unmarshal(related, &r.Related); err != nil {
			return Requirement{}, fmt.Errorf("decode related: %w", err)
		}
	}
	if r.Related == nil {
		r.Related = []string{}
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &r.Params); err != nil {
			return Requirement{}, fmt.Errorf("decode params: %w", err)
		}
	}
	if r.Params == nil {
		r.Params = []oscal.Param{}
	}
	if len(refs) > 0 {
		if err := json.Unmarshal(refs, &r.References); err != nil {
			return Requirement{}, fmt.Errorf("decode references: %w", err)
		}
	}
	if r.References == nil {
		r.References = []oscal.Reference{}
	}
	return r, nil
}

// ListRequirements implements Store.
func (p *Postgres) ListRequirements(ctx context.Context, f RequirementFilter) ([]Requirement, int, error) {
	where := []string{"TRUE"}
	args := []any{}
	if f.FrameworkID != "" {
		if !validUUID(f.FrameworkID) {
			return []Requirement{}, 0, nil
		}
		args = append(args, f.FrameworkID)
		where = append(where, fmt.Sprintf("r.framework_id = $%d", len(args)))
	}
	if f.Status != "" {
		args = append(args, f.Status)
		where = append(where, fmt.Sprintf("COALESCE(r.status_override, r.status) = $%d", len(args)))
	}
	if len(f.ScopeCategoryIDs) > 0 {
		for _, id := range f.ScopeCategoryIDs {
			if !validUUID(id) {
				return []Requirement{}, 0, nil
			}
		}
		args = append(args, dedupe(f.ScopeCategoryIDs))
		where = append(where, fmt.Sprintf("EXISTS (SELECT 1 FROM part_scope_categories psc WHERE psc.requirement_id=r.id AND psc.scope_category_id=ANY($%d::uuid[]))", len(args)))
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		args = append(args, "%"+escapeLike(q)+"%")
		n := len(args)
		where = append(where, fmt.Sprintf("(r.control_id ILIKE $%d ESCAPE '\\' OR r.title ILIKE $%d ESCAPE '\\' OR r.text ILIKE $%d ESCAPE '\\')", n, n, n))
	}
	limit := ClampLimit(f.Limit)
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}
	args = append(args, limit, offset)
	sql := `SELECT ` + requirementCols + `, count(*) OVER()::int FROM requirements r WHERE ` + strings.Join(where, " AND ") +
		fmt.Sprintf(` ORDER BY r.framework_id, r.control_id LIMIT $%d OFFSET $%d`, len(args)-1, len(args))

	rows, err := p.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Requirement{}
	total := 0
	for rows.Next() {
		var n int
		r, err := scanRequirement(rows, &n)
		if err != nil {
			return nil, 0, err
		}
		total = n
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if len(out) == 0 {
		// Offset past the end: the window function yields no rows, so count separately.
		countSQL := `SELECT count(*)::int FROM requirements r WHERE ` + strings.Join(where, " AND ")
		if err := p.pool.QueryRow(ctx, countSQL, args[:len(args)-2]...).Scan(&total); err != nil {
			return nil, 0, err
		}
	}
	return out, total, nil
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

// GetRequirement implements Store.
func (p *Postgres) GetRequirement(ctx context.Context, id string) (Requirement, error) {
	if !validUUID(id) {
		return Requirement{}, ErrNotFound
	}
	return scanRequirement(p.pool.QueryRow(ctx, `SELECT `+requirementCols+` FROM requirements r WHERE r.id = $1`, id))
}

// UpdateRequirement implements Store. Patch semantics mirror Memory exactly.
func (p *Postgres) UpdateRequirement(ctx context.Context, id string, patch RequirementPatch, actor string) (Requirement, error) {
	if !validUUID(id) {
		return Requirement{}, ErrNotFound
	}
	if patch.StatusOverride != nil && !ValidStatus(*patch.StatusOverride) {
		return Requirement{}, ErrInvalid
	}
	var out Requirement
	err := p.withTx(ctx, func(tx pgx.Tx) error {
		r, err := scanRequirement(tx.QueryRow(ctx, `SELECT `+requirementCols+` FROM requirements r WHERE r.id = $1 FOR UPDATE`, id))
		if err != nil {
			return err
		}
		if err := checkUpdatedAtPrecondition(patch.ExpectedUpdatedAt, true, r.UpdatedAt); err != nil {
			return err
		}
		detail := map[string]any{}
		if patch.ClearStatusOverride {
			if r.StatusOverride != "" {
				detail["statusOverride"] = map[string]any{"from": r.StatusOverride, "to": nil}
			}
			r.StatusOverride = ""
		} else if patch.StatusOverride != nil && *patch.StatusOverride != r.StatusOverride {
			var from any
			if r.StatusOverride != "" {
				from = r.StatusOverride
			}
			detail["statusOverride"] = map[string]any{"from": from, "to": *patch.StatusOverride}
			r.StatusOverride = *patch.StatusOverride
		}
		if patch.Notes != nil && *patch.Notes != r.Notes {
			detail["notes"] = true
			r.Notes = *patch.Notes
		}
		var override *string
		if r.StatusOverride != "" {
			override = &r.StatusOverride
		}
		updated, err := scanRequirement(tx.QueryRow(ctx, `UPDATE requirements r
			SET status_override = $2, notes = $3,
				updated_at = GREATEST(clock_timestamp(), r.updated_at + interval '1 microsecond')
			WHERE r.id = $1
			RETURNING `+requirementCols, id, override, r.Notes))
		if err != nil {
			return err
		}
		out = updated
		return insertAudit(ctx, tx, actor, "requirement.update", "requirement", out.ID, detail)
	})
	if err != nil {
		return Requirement{}, err
	}
	return out, nil
}

// ---- part tracking ---------------------------------------------------------

const partCols = `pt.requirement_id::text, pt.part_id, pt.status, pt.owner, pt.due_date, pt.description, pt.updated_at`

func scanPart(row pgx.Row) (PartTracking, error) {
	var pt PartTracking
	if err := row.Scan(&pt.RequirementID, &pt.PartID, &pt.Status, &pt.Owner, &pt.DueDate, &pt.Description, &pt.UpdatedAt); err != nil {
		return PartTracking{}, mapErr(err)
	}
	pt.DueDate = utcPtr(pt.DueDate)
	pt.UpdatedAt = pt.UpdatedAt.UTC()
	return pt, nil
}

// loadParts returns every tracking row of a requirement.
func loadParts(ctx context.Context, q querier, requirementID string) ([]PartTracking, error) {
	rows, err := q.Query(ctx, `SELECT `+partCols+` FROM part_tracking pt WHERE pt.requirement_id = $1 ORDER BY pt.part_id`, requirementID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PartTracking{}
	for rows.Next() {
		pt, err := scanPart(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, pt)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var relation *string
	if err := q.QueryRow(ctx, `SELECT to_regclass('part_scope_categories')::text`).Scan(&relation); err != nil {
		return nil, err
	}
	if relation == nil {
		return out, nil
	}
	for i := range out {
		scopes, err := loadPartScopes(ctx, q, out[i].RequirementID, out[i].PartID)
		if err != nil {
			return nil, err
		}
		out[i].Scopes = scopes
	}
	return out, nil
}

func loadPartScopes(ctx context.Context, q querier, requirementID, partID string) ([]ScopeCategory, error) {
	rows, err := q.Query(ctx, `SELECT sc.`+scopeCategoryCols+` FROM scope_categories sc JOIN part_scope_categories psc ON psc.scope_category_id=sc.id WHERE psc.requirement_id=$1 AND psc.part_id=$2 ORDER BY lower(sc.name),sc.name,sc.id`, requirementID, partID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ScopeCategory{}
	for rows.Next() {
		c, err := scanScopeCategory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetPartTracking implements Store.
func (p *Postgres) GetPartTracking(ctx context.Context, requirementID string) ([]PartTracking, error) {
	if !validUUID(requirementID) {
		return nil, ErrNotFound
	}
	var exists bool
	if err := p.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM requirements WHERE id = $1)`, requirementID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	return loadParts(ctx, p.pool, requirementID)
}

// recomputeRequirement rewrites the derived columns of one requirement from
// its part tracking rows. The caller holds the transaction.
func recomputeRequirement(ctx context.Context, q querier, requirementID string) error {
	var controlID string
	var raw []byte
	if err := q.QueryRow(ctx, `SELECT control_id, parts FROM requirements WHERE id = $1`, requirementID).Scan(&controlID, &raw); err != nil {
		return mapErr(err)
	}
	parts, err := decodeParts(raw)
	if err != nil {
		return err
	}
	rows, err := loadParts(ctx, q, requirementID)
	if err != nil {
		return err
	}
	status, owner, due := DeriveRequirement(parts, controlID, rows)
	_, err = q.Exec(ctx, `UPDATE requirements SET status = $2, owner = $3, due_date = $4,
		updated_at = GREATEST(clock_timestamp(), updated_at + interval '1 microsecond') WHERE id = $1`, requirementID, status, owner, dateArg(due))
	return err
}

// recomputeFramework refreshes the derived columns of every requirement of a
// framework: requirements without tracking rows get the defaults in one
// statement, the rest are derived in Go.
func recomputeFramework(ctx context.Context, q querier, frameworkID string) error {
	if _, err := q.Exec(ctx, `UPDATE requirements r SET status = 'planned', owner = '', due_date = NULL
		WHERE r.framework_id = $1
		  AND NOT EXISTS (SELECT 1 FROM part_tracking pt WHERE pt.requirement_id = r.id)
		  AND (r.status <> 'planned' OR r.owner <> '' OR r.due_date IS NOT NULL)`, frameworkID); err != nil {
		return fmt.Errorf("reset derived columns: %w", err)
	}
	rows, err := q.Query(ctx, `SELECT DISTINCT r.id::text FROM requirements r JOIN part_tracking pt ON pt.requirement_id = r.id WHERE r.framework_id = $1`, frameworkID)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if err := recomputeRequirement(ctx, q, id); err != nil {
			return fmt.Errorf("recompute %s: %w", id, err)
		}
	}
	return nil
}

func decodeParts(raw []byte) ([]oscal.Part, error) {
	var parts []oscal.Part
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &parts); err != nil {
			return nil, fmt.Errorf("decode parts: %w", err)
		}
	}
	return parts, nil
}

// UpsertPartTracking implements Store.
func (p *Postgres) UpsertPartTracking(ctx context.Context, requirementID, partID string, patch PartPatch, actor string) (PartTracking, error) {
	if !validUUID(requirementID) {
		return PartTracking{}, ErrNotFound
	}
	if patch.Status != nil && !ValidStatus(*patch.Status) {
		return PartTracking{}, ErrInvalid
	}
	var out PartTracking
	err := p.withTx(ctx, func(tx pgx.Tx) error {
		var controlID string
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT control_id, parts FROM requirements WHERE id = $1 FOR UPDATE`, requirementID).Scan(&controlID, &raw); err != nil {
			return mapErr(err)
		}
		parts, err := decodeParts(raw)
		if err != nil {
			return err
		}
		if !isTrackable(&Requirement{ControlID: controlID, Parts: parts}, partID) {
			return partNotTrackable
		}
		pt, err := scanPart(tx.QueryRow(ctx, `SELECT `+partCols+` FROM part_tracking pt WHERE pt.requirement_id = $1 AND pt.part_id = $2 FOR UPDATE`, requirementID, partID))
		exists := err == nil
		if errors.Is(err, ErrNotFound) {
			pt = PartTracking{RequirementID: requirementID, PartID: partID, Status: StatusPlanned, Scopes: []ScopeCategory{}}
		} else if err != nil {
			return err
		} else {
			pt.Scopes, err = loadPartScopes(ctx, tx, requirementID, partID)
			if err != nil {
				return err
			}
		}
		if err := checkUpdatedAtPrecondition(patch.ExpectedUpdatedAt, exists, pt.UpdatedAt); err != nil {
			return err
		}
		if patch.ScopeCategoryIDs != nil {
			ids := dedupe(*patch.ScopeCategoryIDs)
			for _, id := range ids {
				if !validUUID(id) {
					return ErrNotFound
				}
			}
			if len(ids) > 0 {
				var n int
				if err := tx.QueryRow(ctx, `SELECT count(*)::int FROM scope_categories WHERE id=ANY($1::uuid[])`, ids).Scan(&n); err != nil {
					return err
				}
				if n != len(ids) {
					return ErrNotFound
				}
			}
			pt.Scopes = make([]ScopeCategory, len(ids))
		}
		detail, err := applyPartPatch(&pt, patch)
		if err != nil {
			return err
		}
		saved, err := scanPart(tx.QueryRow(ctx, `INSERT INTO part_tracking AS pt (requirement_id, part_id, status, owner, due_date, description, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, clock_timestamp())
			ON CONFLICT (requirement_id, part_id) DO UPDATE
			SET status = EXCLUDED.status, owner = EXCLUDED.owner, due_date = EXCLUDED.due_date, description = EXCLUDED.description,
				updated_at = GREATEST(clock_timestamp(), pt.updated_at + interval '1 microsecond')
			RETURNING `+partCols, requirementID, partID, pt.Status, pt.Owner, pt.DueDate, pt.Description))
		if err != nil {
			return fmt.Errorf("upsert part: %w", err)
		}
		if patch.ScopeCategoryIDs != nil {
			if _, err := tx.Exec(ctx, `DELETE FROM part_scope_categories WHERE requirement_id=$1 AND part_id=$2`, requirementID, partID); err != nil {
				return err
			}
			for _, id := range dedupe(*patch.ScopeCategoryIDs) {
				if _, err := tx.Exec(ctx, `INSERT INTO part_scope_categories(requirement_id,part_id,scope_category_id) VALUES($1,$2,$3)`, requirementID, partID, id); err != nil {
					return err
				}
			}
		}
		saved.Scopes, err = loadPartScopes(ctx, tx, requirementID, partID)
		if err != nil {
			return err
		}
		if err := recomputeRequirement(ctx, tx, requirementID); err != nil {
			return err
		}
		out = saved
		return insertAudit(ctx, tx, actor, "part.update", "requirement", requirementID, detail)
	})
	if err != nil {
		return PartTracking{}, err
	}
	return out, nil
}

// ---- evidence --------------------------------------------------------------

const evidenceCols = `e.id::text, e.kind, e.url, e.title, e.description, e.file_name, e.content_type, e.size_bytes, e.sha256,
	e.valid_from, e.valid_until, e.uploaded_by, e.created_at,
	COALESCE((SELECT array_agg(er.requirement_id::text ORDER BY er.requirement_id) FROM evidence_requirements er WHERE er.evidence_id = e.id), '{}'::text[])`

func scanEvidence(row pgx.Row, extra ...any) (Evidence, error) {
	var e Evidence
	dest := []any{&e.ID, &e.Kind, &e.URL, &e.Title, &e.Description, &e.FileName, &e.ContentType, &e.SizeBytes, &e.Sha256,
		&e.ValidFrom, &e.ValidUntil, &e.UploadedBy, &e.CreatedAt, &e.RequirementIDs}
	dest = append(dest, extra...)
	if err := row.Scan(dest...); err != nil {
		return Evidence{}, mapErr(err)
	}
	e.ValidFrom = utcPtr(e.ValidFrom)
	e.ValidUntil = utcPtr(e.ValidUntil)
	e.CreatedAt = e.CreatedAt.UTC()
	if e.RequirementIDs == nil {
		e.RequirementIDs = []string{}
	}
	return e, nil
}

// CreateEvidence implements Store.
func (p *Postgres) CreateEvidence(ctx context.Context, e Evidence, actor string) (Evidence, error) {
	if err := normalizeEvidence(&e); err != nil {
		return Evidence{}, err
	}
	reqIDs := dedupe(e.RequirementIDs)
	for _, rid := range reqIDs {
		if !validUUID(rid) {
			return Evidence{}, ErrNotFound
		}
	}
	var out Evidence
	err := p.withTx(ctx, func(tx pgx.Tx) error {
		if len(reqIDs) > 0 {
			var n int
			if err := tx.QueryRow(ctx, `SELECT count(DISTINCT id)::int FROM requirements WHERE id = ANY($1::uuid[])`, reqIDs).Scan(&n); err != nil {
				return err
			}
			if n != len(reqIDs) {
				return ErrNotFound
			}
		}
		var id string
		err := tx.QueryRow(ctx, `INSERT INTO evidence (title, description, file_name, content_type, size_bytes, sha256, valid_from, valid_until, uploaded_by, created_at, kind, url)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now(), $10, $11)
			RETURNING id::text`,
			e.Title, e.Description, e.FileName, e.ContentType, e.SizeBytes, e.Sha256, dateArg(e.ValidFrom), dateArg(e.ValidUntil), actor, e.Kind, e.URL).Scan(&id)
		if err != nil {
			return fmt.Errorf("insert evidence: %w", err)
		}
		for _, rid := range reqIDs {
			if _, err := tx.Exec(ctx, `INSERT INTO evidence_requirements (evidence_id, requirement_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, id, rid); err != nil {
				return fmt.Errorf("link evidence: %w", err)
			}
		}
		created, err := scanEvidence(tx.QueryRow(ctx, `SELECT `+evidenceCols+` FROM evidence e WHERE e.id = $1`, id))
		if err != nil {
			return err
		}
		out = created
		return insertAudit(ctx, tx, actor, "evidence.create", "evidence", out.ID, evidenceAuditDetail(out))
	})
	if err != nil {
		return Evidence{}, err
	}
	return out, nil
}

func dedupe(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// ListEvidence implements Store. Newest first.
func (p *Postgres) ListEvidence(ctx context.Context, f EvidenceFilter) ([]Evidence, int, error) {
	where := []string{"TRUE"}
	args := []any{}
	if f.RequirementID != "" {
		if !validUUID(f.RequirementID) {
			return []Evidence{}, 0, nil
		}
		args = append(args, f.RequirementID)
		where = append(where, fmt.Sprintf("EXISTS (SELECT 1 FROM evidence_requirements er WHERE er.evidence_id = e.id AND er.requirement_id = $%d)", len(args)))
	}
	if f.Kind != "" {
		args = append(args, f.Kind)
		where = append(where, fmt.Sprintf("e.kind = $%d", len(args)))
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		args = append(args, "%"+escapeLike(q)+"%")
		n := len(args)
		where = append(where, fmt.Sprintf("(e.title ILIKE $%d ESCAPE '\\' OR e.description ILIKE $%d ESCAPE '\\' OR e.file_name ILIKE $%d ESCAPE '\\' OR e.url ILIKE $%d ESCAPE '\\')", n, n, n, n))
	}
	limit := ClampLimit(f.Limit)
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}
	args = append(args, limit, offset)
	sql := `SELECT ` + evidenceCols + `, count(*) OVER()::int FROM evidence e WHERE ` + strings.Join(where, " AND ") +
		fmt.Sprintf(` ORDER BY e.created_at DESC, e.id DESC LIMIT $%d OFFSET $%d`, len(args)-1, len(args))

	rows, err := p.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Evidence{}
	total := 0
	for rows.Next() {
		var n int
		e, err := scanEvidence(rows, &n)
		if err != nil {
			return nil, 0, err
		}
		total = n
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if len(out) == 0 {
		countSQL := `SELECT count(*)::int FROM evidence e WHERE ` + strings.Join(where, " AND ")
		if err := p.pool.QueryRow(ctx, countSQL, args[:len(args)-2]...).Scan(&total); err != nil {
			return nil, 0, err
		}
	}
	return out, total, nil
}

// GetEvidence implements Store.
func (p *Postgres) GetEvidence(ctx context.Context, id string) (Evidence, error) {
	if !validUUID(id) {
		return Evidence{}, ErrNotFound
	}
	return scanEvidence(p.pool.QueryRow(ctx, `SELECT `+evidenceCols+` FROM evidence e WHERE e.id = $1`, id))
}

// ---- audit -----------------------------------------------------------------

// ListAudit implements Store. Newest first.
func (p *Postgres) ListAudit(ctx context.Context, limit int) ([]AuditEntry, error) {
	limit = ClampLimit(limit)
	rows, err := p.pool.Query(ctx, `SELECT id::text, at, actor, action, entity_type, entity_id, detail FROM audit_log ORDER BY at DESC, id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]AuditEntry, 0, limit)
	for rows.Next() {
		var a AuditEntry
		var raw []byte
		if err := rows.Scan(&a.ID, &a.At, &a.Actor, &a.Action, &a.EntityType, &a.EntityID, &raw); err != nil {
			return nil, err
		}
		a.At = a.At.UTC()
		a.Detail = map[string]any{}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &a.Detail); err != nil {
				return nil, fmt.Errorf("decode audit detail: %w", err)
			}
			if a.Detail == nil {
				a.Detail = map[string]any{}
			}
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

var _ Store = (*Postgres)(nil)
