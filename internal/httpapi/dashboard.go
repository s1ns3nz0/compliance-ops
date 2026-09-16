package httpapi

import (
	"math"
	"net/http"
	"sort"
	"time"

	"github.com/s1ns3nz0/compliance-ops/internal/store"
)

// StatusCounts breaks requirements down by OSCAL implementation status.
type StatusCounts struct {
	Implemented   int `json:"implemented"`
	Partial       int `json:"partial"`
	Planned       int `json:"planned"`
	Alternative   int `json:"alternative"`
	NotApplicable int `json:"not_applicable"`
}

// Stats is the coverage summary shared by frameworks and totals.
type Stats struct {
	Total           int          `json:"total"`
	Applicable      int          `json:"applicable"`
	Implemented     int          `json:"implemented"`
	CoveragePercent float64      `json:"coveragePercent"`
	ByStatus        StatusCounts `json:"byStatus"`
	// Overdue counts open requirements whose due date is before today.
	Overdue int `json:"overdue"`
	// DueSoon counts open requirements due today or within the next 30 days.
	DueSoon int `json:"dueSoon"`
}

// FrameworkStats is Stats for one framework.
type FrameworkStats struct {
	FrameworkID string `json:"frameworkId"`
	Title       string `json:"title"`
	ShortName   string `json:"shortName"`
	Stats
}

// UpcomingRequirement is an open requirement with a due date, annotated for
// the deadline list.
type UpcomingRequirement struct {
	store.Requirement
	FrameworkShortName string `json:"frameworkShortName"`
	// DaysUntilDue is negative when the requirement is overdue.
	DaysUntilDue int  `json:"daysUntilDue"`
	Overdue      bool `json:"overdue"`
}

// Dashboard is the computed overview.
type Dashboard struct {
	GeneratedAt          time.Time             `json:"generatedAt"`
	Frameworks           []FrameworkStats      `json:"frameworks"`
	Totals               Stats                 `json:"totals"`
	UpcomingRequirements []UpcomingRequirement `json:"upcomingRequirements"`
	RecentActivity       []store.AuditEntry    `json:"recentActivity"`
}

const (
	dueSoonDays     = 30
	upcomingN       = 20
	recentActivityN = 20
	dashboardPage   = 200
)

func (s *Stats) add(r store.Requirement, days int, hasDue bool) {
	s.Total++
	switch r.Status {
	case store.StatusImplemented:
		s.ByStatus.Implemented++
		s.Implemented++
	case store.StatusPartial:
		s.ByStatus.Partial++
	case store.StatusPlanned:
		s.ByStatus.Planned++
	case store.StatusAlternative:
		s.ByStatus.Alternative++
	case store.StatusNotApplicable:
		s.ByStatus.NotApplicable++
	}
	if !hasDue {
		return
	}
	switch {
	case days < 0:
		s.Overdue++
	case days <= dueSoonDays:
		s.DueSoon++
	}
}

func (s *Stats) finalize() {
	s.Applicable = s.Total - s.ByStatus.NotApplicable
	if s.Applicable > 0 {
		s.CoveragePercent = math.Round(float64(s.Implemented)/float64(s.Applicable)*1000) / 10
	}
}

// isOpen reports whether a requirement still needs work (a due date is only
// meaningful for these).
func isOpen(r store.Requirement) bool {
	return r.Status != store.StatusImplemented && r.Status != store.StatusNotApplicable
}

// daysUntil returns whole days from today (UTC midnight) to the due date's
// UTC calendar day; negative when the date has passed.
func daysUntil(today, due time.Time) int {
	u := due.UTC()
	d := time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
	return int(math.Round(d.Sub(today).Hours() / 24))
}

// BuildDashboard is a pure computation over already-fetched entities.
// today is now truncated to a UTC date. A requirement is "open" when its
// status is neither implemented nor not_applicable; an open requirement with a
// due date is overdue when that date is strictly before today and due soon
// when it falls within the next 30 days (today inclusive).
// upcomingRequirements lists every open requirement with a due date, sorted by
// due date then controlId, capped at 20 rows. Coverage = implemented /
// (total - not_applicable) * 100, rounded to one decimal, 0 when nothing is
// applicable.
func BuildDashboard(now time.Time, frameworks []store.Framework, reqs []store.Requirement, audit []store.AuditEntry) Dashboard {
	now = now.UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	perFW := make(map[string]*FrameworkStats, len(frameworks))
	order := make([]string, 0, len(frameworks))
	for _, f := range frameworks {
		perFW[f.ID] = &FrameworkStats{FrameworkID: f.ID, Title: f.Title, ShortName: f.ShortName}
		order = append(order, f.ID)
	}
	var totals Stats
	upcoming := []UpcomingRequirement{}
	for _, r := range reqs {
		hasDue := r.DueDate != nil && isOpen(r)
		days := 0
		if hasDue {
			days = daysUntil(today, *r.DueDate)
		}
		totals.add(r, days, hasDue)
		fs, ok := perFW[r.FrameworkID]
		if !ok {
			fs = &FrameworkStats{FrameworkID: r.FrameworkID}
			perFW[r.FrameworkID] = fs
			order = append(order, r.FrameworkID)
		}
		fs.add(r, days, hasDue)
		if hasDue {
			r.Parts, r.Params, r.References = nil, nil, nil // summary payload: served by the detail route
			upcoming = append(upcoming, UpcomingRequirement{Requirement: r, FrameworkShortName: fs.ShortName, DaysUntilDue: days, Overdue: days < 0})
		}
	}
	totals.finalize()
	fws := make([]FrameworkStats, 0, len(order))
	for _, id := range order {
		fs := perFW[id]
		fs.finalize()
		fws = append(fws, *fs)
	}
	sort.SliceStable(upcoming, func(i, j int) bool {
		if !upcoming[i].DueDate.Equal(*upcoming[j].DueDate) {
			return upcoming[i].DueDate.Before(*upcoming[j].DueDate)
		}
		return upcoming[i].ControlID < upcoming[j].ControlID
	})
	if len(upcoming) > upcomingN {
		upcoming = upcoming[:upcomingN]
	}

	if len(audit) > recentActivityN {
		audit = audit[:recentActivityN]
	}
	if audit == nil {
		audit = []store.AuditEntry{}
	}
	return Dashboard{
		GeneratedAt:          now,
		Frameworks:           fws,
		Totals:               totals,
		UpcomingRequirements: upcoming,
		RecentActivity:       audit,
	}
}

func (a *api) getDashboard(w http.ResponseWriter, r *http.Request) {
	if !allowedQuery(r) {
		writeError(w, http.StatusBadRequest, codeInvalidQuery, "")
		return
	}
	ctx := r.Context()
	st := a.deps.Store
	frameworks, err := st.ListFrameworks(ctx)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	var reqs []store.Requirement
	for offset := 0; ; {
		page, total, err := st.ListRequirements(ctx, store.RequirementFilter{Limit: dashboardPage, Offset: offset})
		if err != nil {
			writeStoreError(w, err)
			return
		}
		reqs = append(reqs, page...)
		offset += len(page)
		if len(page) == 0 || offset >= total {
			break
		}
	}
	audit, err := st.ListAudit(ctx, recentActivityN)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, BuildDashboard(a.deps.Now(), frameworks, reqs, audit))
}
