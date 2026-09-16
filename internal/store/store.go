// Package store defines the persistence model and the Store contract used by
// the HTTP API. Two implementations exist: PostgreSQL (production) and an
// in-memory store (tests).
package store

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/s1ns3nz0/compliance-ops/internal/oscal"
)

// ErrNotFound is returned when an entity does not exist.
var ErrNotFound = errors.New("not found")

// ErrInvalid is returned for invalid input values.
var ErrInvalid = errors.New("invalid")

// ErrConflict is returned when an operation conflicts with existing state.
var ErrConflict = errors.New("conflict")

// ErrPreconditionFailed identifies an optimistic-concurrency conflict without
// exposing the expected or current value. It also matches ErrConflict.
var ErrPreconditionFailed = errors.New("precondition failed")

var preconditionConflict = errors.Join(ErrConflict, ErrPreconditionFailed)

// ErrPartNotTrackable identifies a valid request shape whose part ID is not a
// trackable statement part. It also matches ErrInvalid for store callers.
var ErrPartNotTrackable = errors.New("part is not trackable")

var partNotTrackable = errors.Join(ErrInvalid, ErrPartNotTrackable)

func evidenceDigestLockKey(digest string) (int64, error) {
	raw, err := hex.DecodeString(digest)
	if err != nil || len(raw) != sha256.Size {
		return 0, ErrInvalid
	}
	return int64(binary.BigEndian.Uint64(raw[:8])), nil
}

// ConflictError includes an existing duplicate upload.
type ConflictError struct{ Existing OscalUpload }

func (e *ConflictError) Error() string { return ErrConflict.Error() }
func (e *ConflictError) Unwrap() error { return ErrConflict }

// Requirement statuses (OSCAL implementation-status values).
const (
	StatusImplemented   = "implemented"
	StatusPartial       = "partial"
	StatusPlanned       = "planned"
	StatusAlternative   = "alternative"
	StatusNotApplicable = "not_applicable"
)

// Statuses lists every requirement status in display order.
var Statuses = []string{StatusImplemented, StatusPartial, StatusPlanned, StatusAlternative, StatusNotApplicable}

// Evidence kinds.
const (
	EvidenceKindFile = "file"
	EvidenceKindLink = "link"
)

// Limits on user-supplied values.
const (
	MaxShortNameLength    = 60
	MaxEvidenceURL        = 2048
	MaxOwnerLength        = 200
	MaxNotesLength        = 10000
	MaxPartDescription    = 20000
	MaxPartIDLength       = 200
	MaxScopeCategoryName  = 100
	MaxEvidenceTitle      = 200
	MaxEvidenceDescLength = 10000
)

// ValidStatus reports whether s is a requirement status.
func ValidStatus(s string) bool {
	switch s {
	case StatusImplemented, StatusPartial, StatusPlanned, StatusAlternative, StatusNotApplicable:
		return true
	}
	return false
}

// ValidEvidenceKind reports whether s is an evidence kind.
func ValidEvidenceKind(s string) bool {
	return s == EvidenceKindFile || s == EvidenceKindLink
}

// ValidShortName reports whether s is an acceptable framework short name.
func ValidShortName(s string) bool {
	return s != "" && strings.TrimSpace(s) == s && utf8.RuneCountInString(s) <= MaxShortNameLength
}

// ValidEvidenceURL reports whether raw is an acceptable link evidence URL:
// http(s), non-empty host, no userinfo, at most MaxEvidenceURL characters.
func ValidEvidenceURL(raw string) bool {
	if raw == "" || len(raw) > MaxEvidenceURL || strings.TrimSpace(raw) != raw {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	return u.Host != "" && u.Hostname() != "" && u.User == nil
}

// Framework is an imported OSCAL catalog or profile.
type Framework struct {
	ID               string    `json:"id"`
	OscalDocumentID  string    `json:"oscalDocumentId"`
	Type             string    `json:"type"`
	Title            string    `json:"title"`
	ShortName        string    `json:"shortName"`
	LastModified     string    `json:"lastModified,omitempty"`
	ImportedAt       time.Time `json:"importedAt"`
	RequirementCount int       `json:"requirementCount"`
}

// Requirement is a trackable control. Status, Owner and DueDate are derived
// from the tracking of its statement parts (see DeriveRequirement); Status is
// the effective value (StatusOverride when set, else DerivedStatus).
type Requirement struct {
	ID          string `json:"id"`
	FrameworkID string `json:"frameworkId"`
	ControlID   string `json:"controlId"`
	Title       string `json:"title"`
	Text        string `json:"text"`
	// Status is the effective status: StatusOverride if set, else DerivedStatus.
	Status string `json:"status"`
	// DerivedStatus is computed from the part statuses.
	DerivedStatus string `json:"derivedStatus"`
	// StatusOverride is the manual status; empty when not set.
	StatusOverride string `json:"statusOverride,omitempty"`
	// Owner is the most common non-empty part owner ("" when none).
	Owner string `json:"owner"`
	// DueDate is the earliest due date among open parts (nil when none).
	DueDate   *time.Time `json:"dueDate,omitempty"`
	Notes     string     `json:"notes"`
	UpdatedAt time.Time  `json:"updatedAt"`
	// Parts is the structured OSCAL part tree. List responses strip it
	// (omitempty); the detail route always renders it as an array.
	Parts []oscal.Part `json:"parts,omitempty"`
	// Params are the control's OSCAL parameters (organization-defined
	// parameters). Omitted from list rows like Parts; the detail route
	// always renders an array.
	Params  []oscal.Param `json:"params,omitempty"`
	Related []string      `json:"related"`
	// References are the control's resolved OSCAL external references
	// (rel=external_reference, plus folded task links). Omitted from list
	// and dashboard rows like Parts; the detail route always renders an
	// array.
	References []oscal.Reference `json:"references,omitempty"`
}

// PartTracking is the implementation tracking of one statement part of a
// requirement. Rows are created lazily: a trackable part without a row reads
// as status planned with empty owner, due date and description.
type PartTracking struct {
	RequirementID string `json:"requirementId"`
	PartID        string `json:"partId"`
	// Status is an OSCAL implementation-status value (default planned).
	Status string `json:"status"`
	Owner  string `json:"owner"`
	// DueDate is a UTC midnight instant; nil when unset.
	DueDate *time.Time `json:"dueDate,omitempty"`
	// Description is free-form markdown (what was done for this part).
	Description string          `json:"description"`
	Scopes      []ScopeCategory `json:"scopes"`
	UpdatedAt   time.Time       `json:"updatedAt"`
}

type ScopeCategory struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func ValidScopeCategoryName(name string) bool {
	return name != "" && strings.TrimSpace(name) == name && utf8.RuneCountInString(name) <= MaxScopeCategoryName
}

// ValidUUID reports whether id has canonical UUID syntax.
func ValidUUID(id string) bool { return validUUID(id) }

// UpdatedAtState identifies whether an optimistic-concurrency precondition was
// omitted, explicitly null, or supplied as a timestamp.
type UpdatedAtState uint8

const (
	UpdatedAtOmitted UpdatedAtState = iota
	UpdatedAtNull
	UpdatedAtValue
)

// UpdatedAtPrecondition preserves all three JSON states needed by lazy rows.
// The zero value is the legacy unconditional behavior.
type UpdatedAtPrecondition struct {
	State UpdatedAtState
	Value time.Time
}

func normalizeUpdatedAt(t time.Time) time.Time { return t.UTC().Truncate(time.Microsecond) }

func checkUpdatedAtPrecondition(p UpdatedAtPrecondition, exists bool, current time.Time) error {
	switch p.State {
	case UpdatedAtOmitted:
		return nil
	case UpdatedAtNull:
		if exists {
			return preconditionConflict
		}
		return nil
	case UpdatedAtValue:
		if !exists || !normalizeUpdatedAt(current).Equal(normalizeUpdatedAt(p.Value)) {
			return preconditionConflict
		}
		return nil
	default:
		return ErrInvalid
	}
}

func nextUpdatedAt(now, previous time.Time) time.Time {
	next := normalizeUpdatedAt(now)
	previous = normalizeUpdatedAt(previous)
	if !next.After(previous) {
		return previous.Add(time.Microsecond)
	}
	return next
}

// PartPatch carries optional part tracking updates. Nil means "unchanged".
type PartPatch struct {
	Status  *string
	Owner   *string
	DueDate *time.Time
	// ClearDueDate removes the due date when true.
	ClearDueDate      bool
	Description       *string
	ScopeCategoryIDs  *[]string
	ExpectedUpdatedAt UpdatedAtPrecondition
}

// RequirementPatch carries optional requirement updates. Nil means
// "unchanged". Status, owner and due date are derived from parts and cannot
// be patched directly; StatusOverride sets the manual status.
type RequirementPatch struct {
	StatusOverride *string
	// ClearStatusOverride removes the manual status when true.
	ClearStatusOverride bool
	Notes               *string
	ExpectedUpdatedAt   UpdatedAtPrecondition
}

// RequirementFilter narrows a requirement listing.
type RequirementFilter struct {
	FrameworkID      string
	Status           string
	Query            string
	ScopeCategoryIDs []string
	Limit            int
	Offset           int
}

// Evidence is stored file metadata (kind=file) or an external link
// (kind=link), linked to requirements. validFrom/validUntil are plain
// metadata.
type Evidence struct {
	ID          string     `json:"id"`
	Kind        string     `json:"kind"`
	URL         string     `json:"url,omitempty"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	FileName    string     `json:"fileName"`
	ContentType string     `json:"contentType"`
	SizeBytes   int64      `json:"sizeBytes"`
	Sha256      string     `json:"sha256"`
	ValidFrom   *time.Time `json:"validFrom,omitempty"`
	ValidUntil  *time.Time `json:"validUntil,omitempty"`
	UploadedBy  string     `json:"uploadedBy"`
	CreatedAt   time.Time  `json:"createdAt"`
	// RequirementIDs are the linked requirements. Never nil.
	RequirementIDs []string `json:"requirementIds"`
}

// EvidenceFilter narrows an evidence listing.
type EvidenceFilter struct {
	RequirementID string
	Kind          string
	Query         string
	Limit         int
	Offset        int
}

// AuditEntry is one append-only audit row.
type AuditEntry struct {
	ID         string         `json:"id"`
	At         time.Time      `json:"at"`
	Actor      string         `json:"actor"`
	Action     string         `json:"action"`
	EntityType string         `json:"entityType"`
	EntityID   string         `json:"entityId"`
	Detail     map[string]any `json:"detail"`
}

// ImportResult summarizes one framework import.
type ImportResult struct {
	Framework Framework `json:"framework"`
	Created   int       `json:"created"`
	Updated   int       `json:"updated"`
}

// OscalUpload is persisted OSCAL JSON. Document is present only on detail reads.
type OscalUpload struct {
	ID                  string          `json:"id"`
	DocumentID          string          `json:"documentId"`
	Type                string          `json:"type"`
	Title               string          `json:"title"`
	Version             string          `json:"version,omitempty"`
	LastModified        string          `json:"lastModified,omitempty"`
	Sha256              string          `json:"sha256"`
	SizeBytes           int64           `json:"sizeBytes"`
	UploadedBy          string          `json:"uploadedBy"`
	UploadedAt          time.Time       `json:"uploadedAt"`
	ImportedFrameworkID string          `json:"importedFrameworkId,omitempty"`
	LinkedFrameworkID   string          `json:"linkedFrameworkId,omitempty"`
	Document            json.RawMessage `json:"document,omitempty"`
}

// AssessmentItemMapping is a manual override from a normalized assessment item.
type AssessmentItemMapping struct {
	UploadID      string    `json:"uploadId"`
	ItemKey       string    `json:"itemKey"`
	RequirementID string    `json:"requirementId"`
	PartID        string    `json:"partId,omitempty"`
	MappedBy      string    `json:"mappedBy"`
	MappedAt      time.Time `json:"mappedAt"`
}

// Store is the persistence contract.
type Store interface {
	// ImportFramework upserts a configured-source framework and its requirements by (document id, control id).
	ImportFramework(ctx context.Context, doc oscal.Document, actor string) (ImportResult, error)
	// ImportUploadedFramework imports a persisted upload and marks it imported
	// atomically, so deletion is blocked exactly when the framework exists.
	ImportUploadedFramework(ctx context.Context, uploadID string, doc oscal.Document, actor string) (ImportResult, error)
	ListFrameworks(ctx context.Context) ([]Framework, error)
	GetFramework(ctx context.Context, id string) (Framework, error)
	// UpdateFramework sets the user-editable short name (audited as framework.update).
	UpdateFramework(ctx context.Context, id, shortName, actor string) (Framework, error)
	CreateScopeCategory(ctx context.Context, name, actor string) (ScopeCategory, error)
	ListScopeCategories(ctx context.Context, query string, limit int) ([]ScopeCategory, error)
	UpdateScopeCategory(ctx context.Context, id, name string, expected UpdatedAtPrecondition, actor string) (ScopeCategory, error)
	DeleteScopeCategory(ctx context.Context, id string, expected UpdatedAtPrecondition, actor string) error

	CreateOscalUpload(ctx context.Context, parsed oscal.Document, raw []byte, sha string, size int64, actor string) (OscalUpload, error)
	ListOscalUploads(ctx context.Context) ([]OscalUpload, error)
	GetOscalUpload(ctx context.Context, id string) (OscalUpload, error)
	DeleteOscalUpload(ctx context.Context, id, actor string) error
	MarkOscalUploadImported(ctx context.Context, id, frameworkID string) error
	SetAssessmentFramework(ctx context.Context, uploadID, frameworkID, actor string) (OscalUpload, error)
	UpsertAssessmentMapping(ctx context.Context, mapping AssessmentItemMapping, actor string) (AssessmentItemMapping, error)
	DeleteAssessmentMapping(ctx context.Context, uploadID, itemKey, actor string) error
	ListAssessmentMappings(ctx context.Context, uploadID string) ([]AssessmentItemMapping, error)
	ListRequirementAssessments(ctx context.Context, requirementID string) ([]AssessmentItemMapping, error)

	ListRequirements(ctx context.Context, f RequirementFilter) ([]Requirement, int, error)
	GetRequirement(ctx context.Context, id string) (Requirement, error)
	// UpdateRequirement sets notes and/or the manual status override
	// (audited as requirement.update).
	UpdateRequirement(ctx context.Context, id string, patch RequirementPatch, actor string) (Requirement, error)

	// GetPartTracking returns the existing tracking rows of a requirement in
	// no particular order (parts without a row are absent). ErrNotFound when
	// the requirement is missing.
	GetPartTracking(ctx context.Context, requirementID string) ([]PartTracking, error)
	// UpsertPartTracking creates or updates the tracking of one trackable
	// part and recomputes the requirement's derived fields in the same
	// transaction. ErrNotFound when the requirement is missing; ErrInvalid
	// when partID is not a trackable part or the status is unknown. Audited
	// as part.update on the requirement.
	UpsertPartTracking(ctx context.Context, requirementID, partID string, patch PartPatch, actor string) (PartTracking, error)

	// CreateEvidence stores evidence. Every RequirementIDs entry must exist
	// (ErrNotFound).
	CreateEvidence(ctx context.Context, e Evidence, actor string) (Evidence, error)
	// AcquireEvidenceDigestLock serializes blob compensation for one SHA-256
	// digest. The returned idempotent release function must be called.
	AcquireEvidenceDigestLock(ctx context.Context, digest string) (func(context.Context) error, error)
	ListEvidence(ctx context.Context, f EvidenceFilter) ([]Evidence, int, error)
	GetEvidence(ctx context.Context, id string) (Evidence, error)

	ListAudit(ctx context.Context, limit int) ([]AuditEntry, error)
}

// ClampLimit bounds a page size to 1..200 with default 50.
func ClampLimit(n int) int {
	if n <= 0 {
		return 50
	}
	if n > 200 {
		return 200
	}
	return n
}
