package store

import (
	"testing"
	"time"

	"github.com/s1ns3nz0/compliance-ops/internal/oscal"
)

func d(y int, m time.Month, day int) *time.Time {
	t := time.Date(y, m, day, 0, 0, 0, 0, time.UTC)
	return &t
}

func TestDeriveRequirement(t *testing.T) {
	// ac-2 with items a, b, c.
	parts := []oscal.Part{{ID: "ac-2_smt", Name: "statement", Parts: []oscal.Part{
		{ID: "ac-2_smt.a", Name: "item"}, {ID: "ac-2_smt.b", Name: "item"}, {ID: "ac-2_smt.c", Name: "item"},
	}}, {ID: "ac-2_gdn", Name: "guidance"}}
	row := func(part, status, owner string, due *time.Time) PartTracking {
		return PartTracking{RequirementID: "r", PartID: "ac-2_smt." + part, Status: status, Owner: owner, DueDate: due}
	}
	cases := []struct {
		name       string
		rows       []PartTracking
		wantStatus string
		wantOwner  string
		wantDue    *time.Time
	}{
		{"no rows", nil, StatusPlanned, "", nil},
		{"all implemented", []PartTracking{row("a", StatusImplemented, "", nil), row("b", StatusImplemented, "", nil), row("c", StatusImplemented, "", nil)}, StatusImplemented, "", nil},
		{"some implemented, missing rows are planned", []PartTracking{row("a", StatusImplemented, "", nil)}, StatusPartial, "", nil},
		{"all not applicable", []PartTracking{row("a", StatusNotApplicable, "", nil), row("b", StatusNotApplicable, "", nil), row("c", StatusNotApplicable, "", nil)}, StatusNotApplicable, "", nil},
		{"n/a ignored, rest implemented", []PartTracking{row("a", StatusNotApplicable, "", nil), row("b", StatusImplemented, "", nil), row("c", StatusImplemented, "", nil)}, StatusImplemented, "", nil},
		{"n/a plus planned", []PartTracking{row("a", StatusNotApplicable, "", nil)}, StatusPlanned, "", nil},
		{"alternative and implemented", []PartTracking{row("a", StatusAlternative, "", nil), row("b", StatusImplemented, "", nil), row("c", StatusImplemented, "", nil)}, StatusAlternative, "", nil},
		{"all alternative", []PartTracking{row("a", StatusAlternative, "", nil), row("b", StatusAlternative, "", nil), row("c", StatusAlternative, "", nil)}, StatusAlternative, "", nil},
		{"alternative with a planned part", []PartTracking{row("a", StatusAlternative, "", nil), row("b", StatusImplemented, "", nil)}, StatusPartial, "", nil},
		{"alternative with a partial part", []PartTracking{row("a", StatusAlternative, "", nil), row("b", StatusPartial, "", nil), row("c", StatusImplemented, "", nil)}, StatusPartial, "", nil},
		{"one partial", []PartTracking{row("a", StatusPartial, "", nil)}, StatusPartial, "", nil},
		{"planned with n/a and alternative", []PartTracking{row("a", StatusNotApplicable, "", nil), row("b", StatusAlternative, "", nil)}, StatusPartial, "", nil},
		{"owner majority", []PartTracking{row("a", StatusPlanned, "bob", nil), row("b", StatusPlanned, "alice", nil), row("c", StatusPlanned, "alice", nil)}, StatusPlanned, "alice", nil},
		{"owner tie → first in part order", []PartTracking{row("c", StatusPlanned, "alice", nil), row("a", StatusPlanned, "bob", nil)}, StatusPlanned, "bob", nil},
		{"owner ignores empty", []PartTracking{row("a", StatusPlanned, "", nil), row("b", StatusPlanned, "", nil), row("c", StatusPlanned, "carol", nil)}, StatusPlanned, "carol", nil},
		{"due earliest among open parts", []PartTracking{row("a", StatusPlanned, "", d(2026, 12, 1)), row("b", StatusPartial, "", d(2026, 10, 1)), row("c", StatusPlanned, "", d(2026, 11, 1))}, StatusPartial, "", d(2026, 10, 1)},
		{"due ignores implemented and n/a", []PartTracking{row("a", StatusImplemented, "", d(2026, 1, 1)), row("b", StatusNotApplicable, "", d(2026, 2, 1)), row("c", StatusPlanned, "", d(2026, 12, 1))}, StatusPartial, "", d(2026, 12, 1)},
		{"due nil when only closed parts have dates", []PartTracking{row("a", StatusImplemented, "", d(2026, 1, 1)), row("b", StatusImplemented, "", nil), row("c", StatusImplemented, "", nil)}, StatusImplemented, "", nil},
		{"alternative keeps its due date", []PartTracking{row("a", StatusAlternative, "", d(2026, 5, 5)), row("b", StatusImplemented, "", nil), row("c", StatusImplemented, "", nil)}, StatusAlternative, "", d(2026, 5, 5)},
		{"rows for unknown parts are ignored", []PartTracking{{PartID: "ac-2_gdn", Status: StatusImplemented, Owner: "x", DueDate: d(2026, 1, 1)}, {PartID: "ac-2_smt", Status: StatusImplemented}}, StatusPlanned, "", nil},
		{"unknown status counts as planned", []PartTracking{row("a", "bogus", "", nil), row("b", StatusImplemented, "", nil), row("c", StatusImplemented, "", nil)}, StatusPartial, "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, owner, due := DeriveRequirement(parts, "ac-2", tc.rows)
			if status != tc.wantStatus || owner != tc.wantOwner {
				t.Fatalf("status/owner = %s/%q, want %s/%q", status, owner, tc.wantStatus, tc.wantOwner)
			}
			switch {
			case tc.wantDue == nil && due != nil:
				t.Fatalf("due = %v, want nil", due)
			case tc.wantDue != nil && (due == nil || !due.Equal(*tc.wantDue)):
				t.Fatalf("due = %v, want %v", due, tc.wantDue)
			}
		})
	}
}

func TestDeriveRequirement_SingleAndSyntheticParts(t *testing.T) {
	// Statement without items: the statement part itself is the only trackable part.
	stmt := []oscal.Part{{ID: "ac-3_smt", Name: "statement", Prose: "Enforce."}}
	status, owner, due := DeriveRequirement(stmt, "ac-3", []PartTracking{{PartID: "ac-3_smt", Status: StatusImplemented, Owner: "bob", DueDate: d(2026, 9, 25)}})
	if status != StatusImplemented || owner != "bob" || due != nil {
		t.Fatalf("statement-only = %s/%q/%v", status, owner, due)
	}
	// No statement at all: synthetic <control>_smt.
	status, owner, due = DeriveRequirement(nil, "ac-9", []PartTracking{{PartID: "ac-9_smt", Status: StatusPartial, Owner: "eve", DueDate: d(2026, 9, 25)}})
	if status != StatusPartial || owner != "eve" || due == nil || !due.Equal(*d(2026, 9, 25)) {
		t.Fatalf("synthetic = %s/%q/%v", status, owner, due)
	}
	if status, _, _ := DeriveRequirement(nil, "ac-9", nil); status != StatusPlanned {
		t.Fatalf("empty synthetic = %s", status)
	}
}
