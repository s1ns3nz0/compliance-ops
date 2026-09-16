package store

import (
	"time"

	"github.com/s1ns3nz0/compliance-ops/internal/oscal"
)

// DeriveRequirement computes a requirement's status, owner and due date from
// the tracking rows of its trackable parts (oscal.TrackableParts). A part
// without a row counts as planned with no owner and no due date; rows for
// non-trackable part ids are ignored.
//
//   - status: all parts not_applicable → not_applicable. Otherwise over the
//     applicable parts (not_applicable excluded): all implemented →
//     implemented; all planned → planned; alternative present, no partial,
//     everything else implemented or alternative → alternative; else partial.
//   - owner: the most common non-empty owner, ties broken by first
//     occurrence in part order; "" when no part has an owner.
//   - due: the earliest due date among parts whose status is neither
//     implemented nor not_applicable; nil when none.
func DeriveRequirement(parts []oscal.Part, controlID string, rows []PartTracking) (status, owner string, due *time.Time) {
	trackable := oscal.TrackableParts(parts, controlID)
	byID := make(map[string]PartTracking, len(rows))
	for _, r := range rows {
		byID[r.PartID] = r
	}

	counts := map[string]int{}
	ownerCount := map[string]int{}
	var ownerOrder []string
	applicable := 0
	for _, p := range trackable {
		row, ok := byID[p.ID]
		if !ok {
			row = PartTracking{Status: StatusPlanned}
		}
		st := row.Status
		if !ValidStatus(st) {
			st = StatusPlanned
		}
		counts[st]++
		if st != StatusNotApplicable {
			applicable++
		}
		if row.Owner != "" {
			if ownerCount[row.Owner] == 0 {
				ownerOrder = append(ownerOrder, row.Owner)
			}
			ownerCount[row.Owner]++
		}
		if row.DueDate != nil && st != StatusImplemented && st != StatusNotApplicable {
			d := row.DueDate.UTC()
			if due == nil || d.Before(*due) {
				due = &d
			}
		}
	}

	switch {
	case applicable == 0:
		status = StatusNotApplicable
	case counts[StatusImplemented] == applicable:
		status = StatusImplemented
	case counts[StatusPlanned] == applicable:
		status = StatusPlanned
	case counts[StatusAlternative] > 0 && counts[StatusPartial] == 0 && counts[StatusImplemented]+counts[StatusAlternative] == applicable:
		status = StatusAlternative
	default:
		status = StatusPartial
	}

	best := 0
	for _, o := range ownerOrder {
		if ownerCount[o] > best {
			best = ownerCount[o]
			owner = o
		}
	}
	return status, owner, due
}

// effectiveStatus returns the override when set, else the derived status.
func effectiveStatus(derived, override string) string {
	if override != "" {
		return override
	}
	return derived
}
