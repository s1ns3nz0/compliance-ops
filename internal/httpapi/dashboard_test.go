package httpapi_test

import (
	"testing"
	"time"

	"github.com/s1ns3nz0/compliance-ops/internal/httpapi"
	"github.com/s1ns3nz0/compliance-ops/internal/oscal"
	"github.com/s1ns3nz0/compliance-ops/internal/store"
)

func date(y int, m time.Month, d int) *time.Time {
	t := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	return &t
}

func TestBuildDashboard_CoverageAndUpcoming(t *testing.T) {
	now := time.Date(2026, 9, 15, 10, 30, 0, 0, time.UTC)
	fws := []store.Framework{{ID: "fw1", Title: "Catalog A", ShortName: "CAT-A"}, {ID: "fw2", Title: "Empty"}}
	reqs := []store.Requirement{
		{ID: "r1", FrameworkID: "fw1", ControlID: "ac-1", Status: store.StatusImplemented, DueDate: date(2026, 1, 1)},   // implemented → never listed
		{ID: "r2", FrameworkID: "fw1", ControlID: "ac-2", Status: store.StatusImplemented},                              // no due date
		{ID: "r3", FrameworkID: "fw1", ControlID: "ac-3", Status: store.StatusNotApplicable, DueDate: date(2026, 1, 1)}, // n/a → never listed
		{ID: "r4", FrameworkID: "fw1", ControlID: "ac-4", Status: store.StatusPlanned, DueDate: date(2026, 9, 14)},      // yesterday → overdue (-1)
		{ID: "r5", FrameworkID: "fw1", ControlID: "ac-5", Status: store.StatusPartial, DueDate: date(2026, 9, 15)},      // today → due soon (0)
		{ID: "r6", FrameworkID: "fw1", ControlID: "ac-6", Status: store.StatusAlternative, DueDate: date(2026, 9, 1)},   // alternative → overdue (-14)
		{ID: "r7", FrameworkID: "fw1", ControlID: "ac-7", Status: store.StatusPlanned, DueDate: date(2026, 10, 15)},     // +30 → due soon (inclusive)
		{ID: "r8", FrameworkID: "fw1", ControlID: "ac-8", Status: store.StatusPlanned, DueDate: date(2026, 10, 16)},     // +31 → upcoming but not due soon
		{ID: "r9", FrameworkID: "fw1", ControlID: "ac-0", Status: store.StatusPlanned, DueDate: date(2026, 9, 14)},      // same day as r4, sorts first by controlId
		{ID: "r10", FrameworkID: "fw1", ControlID: "ac-10", Status: store.StatusPlanned, DueDate: date(2026, 9, 14),
			Parts: []oscal.Part{{Name: "statement"}}, Params: []oscal.Param{{ID: "p"}}}, // parts/params stripped
	}
	audit := make([]store.AuditEntry, 25)
	for i := range audit {
		audit[i] = store.AuditEntry{ID: string(rune('a' + i))}
	}

	d := httpapi.BuildDashboard(now, fws, reqs, audit)

	if !d.GeneratedAt.Equal(now) {
		t.Fatalf("generatedAt = %v", d.GeneratedAt)
	}
	if len(d.Frameworks) != 2 {
		t.Fatalf("frameworks = %d", len(d.Frameworks))
	}
	fw := d.Frameworks[0]
	if fw.FrameworkID != "fw1" || fw.Title != "Catalog A" || fw.ShortName != "CAT-A" || fw.Total != 10 || fw.Applicable != 9 || fw.Implemented != 2 || fw.CoveragePercent != 22.2 {
		t.Fatalf("fw1 stats = %+v", fw)
	}
	if fw.Overdue != 4 || fw.DueSoon != 2 {
		t.Fatalf("fw1 overdue/dueSoon = %d/%d", fw.Overdue, fw.DueSoon)
	}
	if fw.ByStatus != (httpapi.StatusCounts{Implemented: 2, Partial: 1, Planned: 5, Alternative: 1, NotApplicable: 1}) {
		t.Fatalf("byStatus = %+v", fw.ByStatus)
	}
	empty := d.Frameworks[1]
	if empty.Total != 0 || empty.Applicable != 0 || empty.CoveragePercent != 0 || empty.Overdue != 0 || empty.DueSoon != 0 {
		t.Fatalf("empty framework stats = %+v", empty)
	}
	if d.Totals.Total != 10 || d.Totals.Applicable != 9 || d.Totals.CoveragePercent != 22.2 || d.Totals.Overdue != 4 || d.Totals.DueSoon != 2 {
		t.Fatalf("totals = %+v", d.Totals)
	}
	// Sorted by due date asc, then controlId: r6 (Sep 1), r9/r10/r4 (Sep 14: ac-0 < ac-10 < ac-4), r5, r7, r8.
	var ids []string
	for _, u := range d.UpcomingRequirements {
		ids = append(ids, u.ID)
	}
	want := []string{"r6", "r9", "r10", "r4", "r5", "r7", "r8"}
	if len(ids) != len(want) {
		t.Fatalf("upcoming ids = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("upcoming ids = %v, want %v", ids, want)
		}
	}
	u := d.UpcomingRequirements
	if u[0].DaysUntilDue != -14 || !u[0].Overdue || u[0].FrameworkShortName != "CAT-A" {
		t.Fatalf("r6 = %+v", u[0])
	}
	if u[3].DaysUntilDue != -1 || !u[3].Overdue {
		t.Fatalf("r4 = %+v", u[3])
	}
	if u[4].DaysUntilDue != 0 || u[4].Overdue {
		t.Fatalf("r5 = %+v", u[4])
	}
	if u[5].DaysUntilDue != 30 || u[5].Overdue || u[6].DaysUntilDue != 31 {
		t.Fatalf("r7/r8 = %+v / %+v", u[5], u[6])
	}
	if u[2].Parts != nil || u[2].Params != nil {
		t.Fatalf("upcoming rows must omit parts/params: %+v", u[2])
	}
	if len(d.RecentActivity) != 20 || d.RecentActivity[0].ID != "a" {
		t.Fatalf("recentActivity = %d", len(d.RecentActivity))
	}
}

func TestBuildDashboard_UpcomingCappedAt20(t *testing.T) {
	now := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	var reqs []store.Requirement
	for i := 0; i < 25; i++ {
		reqs = append(reqs, store.Requirement{ID: string(rune('A' + i)), FrameworkID: "f", ControlID: "c", Status: store.StatusPlanned, DueDate: date(2026, 9, 1+i)})
	}
	d := httpapi.BuildDashboard(now, []store.Framework{{ID: "f", Title: "F", ShortName: "F"}}, reqs, nil)
	if len(d.UpcomingRequirements) != 20 || d.UpcomingRequirements[0].ID != "A" || d.UpcomingRequirements[19].ID != "T" {
		t.Fatalf("upcoming = %d rows", len(d.UpcomingRequirements))
	}
	// Counts are over all rows, not just the capped list: Sep 1..14 overdue (14), Sep 15..25 due soon (11).
	if d.Totals.Overdue != 14 || d.Totals.DueSoon != 11 {
		t.Fatalf("totals = %+v", d.Totals)
	}
}

func TestBuildDashboard_CoverageRoundsToOneDecimal(t *testing.T) {
	// 4 requirements: 2 implemented, 1 not_applicable, 1 planned → applicable 3, 66.7%.
	reqs := []store.Requirement{
		{ID: "1", FrameworkID: "f", Status: store.StatusImplemented},
		{ID: "2", FrameworkID: "f", Status: store.StatusImplemented},
		{ID: "3", FrameworkID: "f", Status: store.StatusNotApplicable},
		{ID: "4", FrameworkID: "f", Status: store.StatusPlanned},
	}
	d := httpapi.BuildDashboard(time.Now(), []store.Framework{{ID: "f", Title: "F"}}, reqs, nil)
	if d.Totals.Applicable != 3 || d.Totals.CoveragePercent != 66.7 {
		t.Fatalf("totals = %+v", d.Totals)
	}
	if d.Frameworks[0].CoveragePercent != 66.7 {
		t.Fatalf("framework coverage = %v", d.Frameworks[0].CoveragePercent)
	}
	if d.UpcomingRequirements == nil || d.RecentActivity == nil {
		t.Fatalf("slices must be non-nil for JSON arrays")
	}
	// All not_applicable → coverage 0, not NaN.
	d = httpapi.BuildDashboard(time.Now(), nil, []store.Requirement{{ID: "1", FrameworkID: "f", Status: store.StatusNotApplicable}}, nil)
	if d.Totals.CoveragePercent != 0 || d.Totals.Applicable != 0 {
		t.Fatalf("all-n/a totals = %+v", d.Totals)
	}
}
