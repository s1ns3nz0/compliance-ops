package assessment

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestParseAssessmentResults(t *testing.T) {
	raw := []byte(`{"assessment-results":{"uuid":"ar-1","metadata":{"title":"Quarterly assessment","version":"1","last-modified":"2026-09-01T00:00:00Z"},"import-ap":{"href":"#ap-1"},"results":[{"uuid":"result-1","title":"Run","reviewed-controls":{"control-selections":[{"include-controls":[{"control-id":"AC-2","statement-ids":["ac-2_smt.a"]}]}]},"observations":[{"uuid":"obs-1","title":"Observed","description":"Application reviewed","methods":["EXAMINE"],"subjects":[{"subject-uuid":"asset-1"}],"relevant-evidence":[{"href":"#ev-1"}]}],"findings":[{"uuid":"finding-1","title":"Finding","description":"Gap","target":{"target-id":"AC-2","type":"objective-id","status":{"state":"not-satisfied"}},"related-observations":[{"observation-uuid":"obs-1"}]}],"risks":[{"uuid":"risk-1","title":"Risk","description":"Exposure","status":"open","characterizations":[{"facets":[{"name":"impact","value":"high"}]}],"related-observations":[{"observation-uuid":"obs-1"}]}],"attestations":[{"responsible-parties":[{"role-id":"assessor"}],"parts":[{"name":"authorization","prose":"Attested"}]}],"log":{"entries":[{"uuid":"log-1","title":"Started"}]}}]}}`)
	d, err := ParseJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if d.Type != TypeResults || d.DocumentID != "ar-1" || d.Title != "Quarterly assessment" || d.ImportRefs[0] != "ap-1" {
		t.Fatalf("bad metadata: %#v", d)
	}
	if len(d.Results) != 1 || len(d.Observations) != 1 || len(d.Findings) != 1 || len(d.Risks) != 1 || len(d.Attestations) != 1 || len(d.LogEntries) != 1 {
		t.Fatalf("bad counts: %#v", d.Counts())
	}
	f := d.Findings[0]
	if f.ItemKey != "assessment-results/result-1/finding/finding-1" || f.ObjectiveIDs[0] != "AC-2" || f.Status != "not-satisfied" || f.RelatedObservationIDs[0] != "obs-1" {
		t.Fatalf("bad finding: %#v", f)
	}
	if d.Observations[0].EvidenceRefs[0] != "ev-1" {
		t.Fatalf("bad evidence: %#v", d.Observations[0])
	}
}

func TestParsePlanAndPOAM(t *testing.T) {
	fixtures := []struct{ raw, typ string }{
		{`{"assessment-plan":{"uuid":"ap-1","metadata":{"title":"Plan"},"import-ssp":{"href":"#ssp-1"},"reviewed-controls":{"control-selections":[{"include-controls":[{"control-id":"AC-2"}]}]},"assessment-subjects":[{"type":"component","include-subjects":[{"subject-uuid":"asset-1"}]}],"tasks":[{"uuid":"task-1","title":"Interview","description":"Talk","associated-activities":[{"activity-uuid":"act-1"}],"responsible-roles":[{"role-id":"assessor"}],"timing":{"within-date-range":{"start":"2026-09-01","end":"2026-09-02"}}}]}}`, TypePlan},
		{`{"plan-of-action-and-milestones":{"uuid":"poam-1","metadata":{"title":"POAM"},"import-ssp":{"href":"#ssp-1"},"poam-items":[{"uuid":"item-1","title":"Fix access","description":"Remediate","related-findings":[{"finding-uuid":"f-1"}],"related-observations":[{"observation-uuid":"o-1"}],"related-risks":[{"risk-uuid":"r-1"}],"associated-risks":["r-1"],"props":[{"name":"status","value":"open"},{"name":"severity","value":"high"},{"name":"scheduled-completion-date","value":"2026-10-01"}],"related-controls":{"control-selections":[{"include-controls":[{"control-id":"AC-2"}]}]},"milestones":[{"uuid":"m-1","title":"Deploy","description":"Ship"}]}]}}`, TypePOAM},
	}
	for _, fx := range fixtures {
		var v any
		if err := json.Unmarshal([]byte(fx.raw), &v); err != nil {
			t.Fatal(err)
		}
		d, err := Parse(v)
		if err != nil {
			t.Fatal(err)
		}
		if d.Type != fx.typ || len(d.ImportRefs) != 1 {
			t.Fatalf("bad doc %#v", d)
		}
		if fx.typ == TypePlan && (len(d.Tasks) != 1 || len(d.Subjects) != 1 || len(d.ReviewedControls) != 1) {
			t.Fatalf("bad plan %#v", d)
		}
		if fx.typ == TypePOAM && (len(d.POAMItems) != 1 || d.POAMItems[0].Severity != "high" || d.POAMItems[0].ScheduledCompletion != "2026-10-01") {
			t.Fatalf("bad poam %#v", d)
		}
	}
}

func TestFallbackKeysStable(t *testing.T) {
	raw := []byte(`{"assessment-results":{"uuid":"x","metadata":{"title":"X"},"results":[{"observations":[{"title":"one"},{"title":"two"}]}]}}`)
	a, _ := ParseJSON(raw)
	b, _ := ParseJSON(raw)
	if a.Observations[0].ItemKey != b.Observations[0].ItemKey || a.Observations[0].ItemKey == a.Observations[1].ItemKey {
		t.Fatal("fallback keys are not stable and unique")
	}
}

func TestParseNormalizesPresentationCopyWithoutMutatingSource(t *testing.T) {
	tests := []struct {
		name  string
		raw   string
		check func(*testing.T, Document)
	}{
		{
			name: "assessment plan subjects and tasks",
			raw:  `{"assessment-plan":{"uuid":"DEMO ap-1","metadata":{"title":"DEMO DEMO Assessment plan"},"assessment-subjects":[{"uuid":"DEMO subject-1","title":"DEMO Subject","description":"DEMO Subject description","include-subjects":[{"subject-uuid":"DEMO asset-1"}]}],"tasks":[{"uuid":"DEMO task-1","title":"DEMO Task","description":"DEMO Task description","props":[{"name":"status","value":"DEMO pending"}],"timing":{"within-date-range":{"start":"2026-09-01","end":"2026-09-02"}}}]}}`,
			check: func(t *testing.T, d Document) {
				assertPresentation(t, "document title", d.Title, "DEMO Assessment plan")
				assertItemPresentation(t, "subject", d.Subjects[0], "Subject", "Subject", "Subject description")
				assertItemPresentation(t, "task", d.Tasks[0], "Task", "Task", "Task description")
				if d.DocumentID != "DEMO ap-1" || d.Subjects[0].UUID != "DEMO subject-1" || d.Subjects[0].SubjectIDs[0] != "DEMO asset-1" || d.Tasks[0].Status != "DEMO pending" {
					t.Fatalf("non-presentation plan fields changed: %#v", d)
				}
			},
		},
		{
			name: "assessment results findings observations and risks",
			raw:  `{"assessment-results":{"uuid":"DEMO ar-1","metadata":{"title":"DEMO Results"},"results":[{"uuid":"DEMO result-1","title":"DEMO Result","description":"DEMO Result description","observations":[{"uuid":"DEMO observation-1","title":"DEMO Observation","description":"DEMO Observation description"}],"findings":[{"uuid":"DEMO finding-1","title":"DEMO Finding","description":"DEMO Finding description","target":{"target-id":"DEMO ac-2_smt.a","type":"objective-id","status":{"state":"DEMO not-satisfied"}}}],"risks":[{"uuid":"DEMO risk-1","title":"DEMO Risk","description":"DEMO Risk description","props":[{"name":"risk","value":"DEMO high"}]}]}]}}`,
			check: func(t *testing.T, d Document) {
				assertPresentation(t, "document title", d.Title, "Results")
				assertItemPresentation(t, "result", d.Results[0], "Result", "Result", "Result description")
				assertItemPresentation(t, "observation", d.Observations[0], "Observation", "Observation", "Observation description")
				assertItemPresentation(t, "finding", d.Findings[0], "Finding", "Finding", "Finding description")
				assertItemPresentation(t, "risk", d.Risks[0], "Risk", "Risk", "Risk description")
				if d.Findings[0].ObjectiveIDs[0] != "DEMO ac-2_smt.a" || d.Findings[0].Status != "DEMO not-satisfied" || d.Risks[0].Risk != "DEMO high" {
					t.Fatalf("non-presentation result fields changed: finding=%#v risk=%#v", d.Findings[0], d.Risks[0])
				}
			},
		},
		{
			name: "poam items remarks and milestones",
			raw:  `{"plan-of-action-and-milestones":{"uuid":"DEMO poam-1","metadata":{"title":"DEMO POA&M"},"poam-items":[{"uuid":"DEMO item-1","title":"DEMO Item","description":"DEMO Item description","remarks":"DEMO Useful remediation context. Parser requires manual part mapping for POA&M items because related-controls statement-ids are not normalized. Customer-approved follow-up remains.","props":[{"name":"status","value":"DEMO open"},{"name":"risk","value":"DEMO moderate"},{"name":"scheduled-completion-date","value":"2026-10-01"}],"related-controls":{"control-selections":[{"include-controls":[{"control-id":"DEMO AC-2"}]}]},"milestones":[{"uuid":"DEMO milestone-1","title":"DEMO Milestone","description":"DEMO Milestone description"}]},{"uuid":"item-2","remarks":"DEMO Keep Parser requires manual part mapping for POA&M items because related-controls statement-ids are not normalized. appendix verbatim."}]}}`,
			check: func(t *testing.T, d Document) {
				assertPresentation(t, "document title", d.Title, "POA&M")
				assertItemPresentation(t, "poam item", d.POAMItems[0], "Item", "Item", "Item description")
				assertItemPresentation(t, "milestone", d.POAMItems[0].Milestones[0], "Milestone", "Milestone", "Milestone description")
				assertPresentation(t, "poam remarks", d.POAMItems[0].Remarks, "Useful remediation context. Customer-approved follow-up remains.")
				assertPresentation(t, "other remarks", d.POAMItems[1].Remarks, "Keep Parser requires manual part mapping for POA&M items because related-controls statement-ids are not normalized. appendix verbatim.")
				item := d.POAMItems[0]
				if d.DocumentID != "DEMO poam-1" || item.UUID != "DEMO item-1" || item.Status != "DEMO open" || item.Risk != "DEMO moderate" || item.ScheduledCompletion != "2026-10-01" || item.ControlIDs[0] != "DEMO AC-2" || item.Milestones[0].UUID != "DEMO milestone-1" {
					t.Fatalf("non-presentation POA&M fields changed: %#v", item)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var source any
			if err := json.Unmarshal([]byte(tt.raw), &source); err != nil {
				t.Fatal(err)
			}
			before, err := json.Marshal(source)
			if err != nil {
				t.Fatal(err)
			}
			d, err := Parse(source)
			if err != nil {
				t.Fatal(err)
			}
			after, err := json.Marshal(source)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatalf("parser mutated immutable source\nbefore: %s\n after: %s", before, after)
			}
			tt.check(t, d)
		})
	}
}

func assertItemPresentation(t *testing.T, path string, item Item, title, summary, description string) {
	t.Helper()
	assertPresentation(t, path+" title", item.Title, title)
	assertPresentation(t, path+" summary", item.Summary, summary)
	assertPresentation(t, path+" description", item.Description, description)
}

func assertPresentation(t *testing.T, path, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %q, want %q", path, got, want)
	}
}
