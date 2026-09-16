package store

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/s1ns3nz0/compliance-ops/internal/oscal"
)

// migrationFiles holds the embedded SQL migrations, applied in lexical order.
//
//go:embed migrations/*.sql
var migrationFiles embed.FS

// postMigrationHooks run inside the transaction of the named migration,
// right after its SQL and before the schema_migrations row is written. They
// therefore run exactly once per database (the row guards re-runs) and roll
// back together with the SQL on failure.
var postMigrationHooks = map[string]func(context.Context, pgx.Tx) error{
	// The hook runs after 0007 so the shared part scanner can read scope while
	// still migrating the legacy rows created by 0004.
	"0007_assessments_scope": seedPartTrackingFromLegacy,
}

// Migrate applies every embedded migration that has not been recorded in
// schema_migrations yet. Each file runs inside its own transaction together
// with the bookkeeping row (and its Go post-hook, if any), so a failed
// migration leaves no partial state. Calling Migrate repeatedly is safe.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    text PRIMARY KEY,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	names, err := migrationNames()
	if err != nil {
		return err
	}
	for _, name := range names {
		if err := applyMigration(ctx, pool, name); err != nil {
			return err
		}
	}
	return nil
}

func migrationNames() ([]string, error) {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names, nil
}

func applyMigration(ctx context.Context, pool *pgxpool.Pool, name string) error {
	version := strings.TrimSuffix(name, ".sql")
	sqlBytes, err := migrationFiles.ReadFile("migrations/" + name)
	if err != nil {
		return fmt.Errorf("read migration %s: %w", name, err)
	}

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", name, err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	// Serialize concurrent migrators (e.g. several replicas starting at once).
	if _, err := tx.Exec(ctx, `LOCK TABLE schema_migrations IN ACCESS EXCLUSIVE MODE`); err != nil {
		return fmt.Errorf("lock schema_migrations: %w", err)
	}
	var applied bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`, version).Scan(&applied); err != nil {
		return fmt.Errorf("check migration %s: %w", name, err)
	}
	if applied {
		return tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, string(sqlBytes)); err != nil {
		return fmt.Errorf("apply migration %s: %w", name, err)
	}
	if hook := postMigrationHooks[version]; hook != nil {
		if err := hook(ctx, tx); err != nil {
			return fmt.Errorf("post-migration hook %s: %w", name, err)
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
		return fmt.Errorf("record migration %s: %w", name, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit migration %s: %w", name, err)
	}
	return nil
}

// legacyRequirement is a requirement as read during the 0004 data migration.
type legacyRequirement struct {
	id, controlID, status, owner string
	due                          *time.Time
	parts                        []oscal.Part
}

// seedPartTrackingFromLegacy completes migration 0004 in Go, where the parts
// tree is available. For every requirement that carried tracking values
// (status other than planned, an owner or a due date) it seeds one
// part_tracking row per trackable part with those values so the derivation
// reproduces them; rows migrated from activities on non-trackable part ids
// are folded into the first trackable part's description; finally the derived
// columns of every requirement with rows are recomputed.
func seedPartTrackingFromLegacy(ctx context.Context, tx pgx.Tx) error {
	rows, err := tx.Query(ctx, `SELECT r.id::text, r.control_id, r.status, r.owner, r.due_date, r.parts
		FROM requirements r
		WHERE r.status <> 'planned' OR r.owner <> '' OR r.due_date IS NOT NULL
		   OR EXISTS (SELECT 1 FROM part_tracking pt WHERE pt.requirement_id = r.id)
		ORDER BY r.id`)
	if err != nil {
		return err
	}
	var reqs []legacyRequirement
	for rows.Next() {
		var lr legacyRequirement
		var raw []byte
		if err := rows.Scan(&lr.id, &lr.controlID, &lr.status, &lr.owner, &lr.due, &raw); err != nil {
			rows.Close()
			return err
		}
		if lr.parts, err = decodeParts(raw); err != nil {
			rows.Close()
			return fmt.Errorf("requirement %s: %w", lr.id, err)
		}
		reqs = append(reqs, lr)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, lr := range reqs {
		if err := seedRequirementParts(ctx, tx, lr); err != nil {
			return fmt.Errorf("requirement %s (%s): %w", lr.id, lr.controlID, err)
		}
		if err := recomputeRequirement(ctx, tx, lr.id); err != nil {
			return fmt.Errorf("recompute %s (%s): %w", lr.id, lr.controlID, err)
		}
	}
	return nil
}

func seedRequirementParts(ctx context.Context, tx pgx.Tx, lr legacyRequirement) error {
	trackable := oscal.TrackableParts(lr.parts, lr.controlID)
	isTrackable := make(map[string]bool, len(trackable))
	for _, p := range trackable {
		isTrackable[p.ID] = true
	}
	existing, err := loadParts(ctx, tx, lr.id)
	if err != nil {
		return err
	}
	// Fold rows on non-trackable part ids (e.g. the statement part itself
	// when it has items, guidance parts, or an empty part id) into the first
	// trackable part so no migrated text is lost.
	var orphaned []string
	for _, pt := range existing {
		if !isTrackable[pt.PartID] {
			label := pt.PartID
			if label == "" {
				label = "requirement"
			}
			orphaned = append(orphaned, "_Migrated from "+label+":_\n"+pt.Description)
			if _, err := tx.Exec(ctx, `DELETE FROM part_tracking WHERE requirement_id = $1 AND part_id = $2`, lr.id, pt.PartID); err != nil {
				return err
			}
		}
	}
	if len(orphaned) > 0 {
		first := trackable[0].ID
		_, err := tx.Exec(ctx, `INSERT INTO part_tracking (requirement_id, part_id, description)
			VALUES ($1, $2, $3)
			ON CONFLICT (requirement_id, part_id) DO UPDATE
			SET description = CASE WHEN part_tracking.description = '' THEN EXCLUDED.description ELSE part_tracking.description || E'\n' || EXCLUDED.description END`,
			lr.id, first, strings.Join(orphaned, "\n"))
		if err != nil {
			return err
		}
	}
	if lr.status == StatusPlanned && lr.owner == "" && lr.due == nil {
		return nil
	}
	for _, p := range trackable {
		_, err := tx.Exec(ctx, `INSERT INTO part_tracking (requirement_id, part_id, status, owner, due_date)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (requirement_id, part_id) DO UPDATE
			SET status = EXCLUDED.status, owner = EXCLUDED.owner, due_date = EXCLUDED.due_date`,
			lr.id, p.ID, lr.status, lr.owner, dateArg(lr.due))
		if err != nil {
			return err
		}
	}
	return nil
}
