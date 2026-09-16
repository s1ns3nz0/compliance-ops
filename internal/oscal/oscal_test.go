package oscal

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestShortName_Patterns(t *testing.T) {
	cases := map[string]string{
		"NIST Special Publication 800-53 Revision 5: Security and Privacy Controls for Information Systems and Organizations": "NIST SP 800-53",
		"nist sp 800-53 rev 5 low baseline": "NIST SP 800-53",
		"Electronic (OSCAL) Version of Secure Software Development Framework (SSDF): Recommendations for Mitigating the Risk of Software Vulnerabilities": "NIST SP 800-218 SSDF",
		"NIST SP 800-218 v1.1":                   "NIST SP 800-218 SSDF",
		"ssdf tasks":                             "NIST SP 800-218 SSDF",
		"Protecting CUI (NIST SP 800-171 Rev 2)": "NIST SP 800-171",
		"NIST Cybersecurity Framework 2.0":       "NIST CSF",
		"nist csf":                               "NIST CSF",
		"ISO/IEC 27001:2022 Information security management": "ISO/IEC 27001",
		"iso 27002 controls":                  "ISO/IEC 27002",
		"AICPA SOC 2 Trust Services Criteria": "SOC 2",
		"PCI DSS v4.0":                        "PCI DSS",
		"pci-dss":                             "PCI DSS",
		"CIS Critical Security Controls v8":   "CIS Controls",
		"FedRAMP Rev 5 High Baseline":         "FedRAMP High",
		"fedramp moderate baseline profile":   "FedRAMP Moderate",
		"FedRAMP LOW":                         "FedRAMP Low",
		"HIPAA Security Rule":                 "HIPAA",
		"EU GDPR Articles":                    "GDPR",
		"K-ISMS-P Certification Criteria":     "K-ISMS-P",
		"ISMS-P certification criteria":       "ISMS-P",
		"Example OSCAL catalog":               "Example OSCAL catalog",
		"Internal Security Baseline for Payment Processing Systems": "Internal Security Baseline for Payment…",
		"  spaced   out   title  ":                                  "spaced out title",
	}
	for title, want := range cases {
		if got := ShortName(title, nil); got != want {
			t.Errorf("ShortName(%q) = %q, want %q", title, got, want)
		}
	}
}

func TestShortName_TruncatesAtWordBoundary(t *testing.T) {
	got := ShortName(strings.Repeat("a", 50), nil)
	if got != strings.Repeat("a", 40)+"…" {
		t.Fatalf("no-space title = %q", got)
	}
	if n := len([]rune(got)); n != 41 {
		t.Fatalf("rune length = %d", n)
	}
	long := "Corporate Information Handling Standard, Version Three (Draft)"
	got = ShortName(long, nil)
	if !strings.HasSuffix(got, "…") || len([]rune(got)) > 41 || strings.HasSuffix(strings.TrimSuffix(got, "…"), ",") {
		t.Fatalf("truncated = %q", got)
	}
}

func TestShortName_PropsWin(t *testing.T) {
	props := []any{map[string]any{"name": "keywords", "value": "x"}, map[string]any{"name": "short-name", "value": " ACME-SEC "}}
	if got := ShortName("NIST SP 800-53", props); got != "ACME-SEC" {
		t.Fatalf("prop short name = %q", got)
	}
	tooLong := []any{map[string]any{"name": "abbreviation", "value": strings.Repeat("x", 61)}}
	if got := ShortName("PCI DSS", tooLong); got != "PCI DSS" {
		t.Fatalf("oversized prop should be ignored: %q", got)
	}
}

const partsCatalog = `{
  "catalog": {
    "uuid": "8e4e759b-bd43-4d64-91b5-c04d6b1c4a93",
    "metadata": { "title": "Parts catalog", "props": [{"name": "short-name", "value": "PARTS"}] },
    "controls": [{
      "id": "ac-2", "title": "Account Management",
      "links": [
        {"href": "#ac-3", "rel": "related"},
        {"href": "#ac-5", "rel": "related"},
        {"href": "https://example.com", "rel": "reference"}
      ],
      "parts": [
        {"id": "ac-2_smt", "name": "statement", "prose": "Do the following:", "parts": [
          {"id": "ac-2_smt.a", "name": "item", "props": [{"name": "label", "value": "a."}], "prose": "Define account types."},
          {"id": "ac-2_smt.b", "name": "item", "props": [{"name": "label", "value": "b."}], "prose": "Assign managers.", "parts": [
            {"id": "ac-2_smt.b.1", "name": "item", "props": [{"name": "label", "value": "1."}], "prose": "Nested."}
          ]}
        ]},
        {"id": "ac-2_gdn", "name": "guidance", "prose": "Guidance text."},
        {"name": "assessment-objective", "ns": "x", "class": "y"}
      ]
    }]
  }
}`

func TestResolveProfileIncludesChildrenAndExcludesControls(t *testing.T) {
	catalogs, err := ParseJSON([]byte(`{"catalog":{"uuid":"catalog-1","metadata":{"title":"Catalog"},"controls":[{"id":"ac-1","title":"Parent","controls":[{"id":"ac-1.1","title":"Child one"},{"id":"ac-1.2","title":"Child two"}]},{"id":"ac-2","title":"Other"}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := ParseJSON([]byte(`{"profile":{"uuid":"profile-1","metadata":{"title":"Baseline"},"imports":[{"href":"https://example.test/catalog.json#catalog-1","include-controls":[{"with-ids":["ac-1"],"with-child-controls":"yes"}],"exclude-controls":[{"with-ids":["ac-1.2"]}]}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles[0].ProfileImports) != 1 || profiles[0].ProfileImports[0].Href != "https://example.test/catalog.json#catalog-1" {
		t.Fatalf("profile imports = %+v", profiles[0].ProfileImports)
	}
	resolved, err := ResolveProfile(profiles[0], catalogs)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ID != "profile-1" || resolved.Title != "Baseline" || resolved.Type != "profile" {
		t.Fatalf("resolved identity = %+v", resolved)
	}
	var ids []string
	for _, c := range resolved.Controls {
		ids = append(ids, c.ID)
	}
	if !reflect.DeepEqual(ids, []string{"ac-1", "ac-1.1"}) {
		t.Fatalf("resolved controls = %v", ids)
	}
}

func TestResolveProfileIncludeAllAndUnavailableCatalog(t *testing.T) {
	catalogs, err := ParseJSON([]byte(`{"catalog":{"uuid":"catalog-all","metadata":{"title":"Catalog"},"controls":[{"id":"a","title":"A"},{"id":"b","title":"B"}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := ParseJSON([]byte(`{"profile":{"uuid":"profile-all","metadata":{"title":"All"},"imports":[{"href":"#catalog-all","include-all":{},"exclude-controls":[{"with-ids":["b"]}]}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveProfile(profiles[0], catalogs)
	if err != nil || len(resolved.Controls) != 1 || resolved.Controls[0].ID != "a" {
		t.Fatalf("resolved = %+v, %v", resolved, err)
	}
	if _, err := ResolveProfile(profiles[0], nil); err == nil {
		t.Fatal("missing catalog resolved successfully")
	}
}

func TestParse_StructuredPartsAndRelated(t *testing.T) {
	docs, err := ParseJSON([]byte(partsCatalog))
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || len(docs[0].Controls) != 1 {
		t.Fatalf("docs = %+v", docs)
	}
	c := docs[0].Controls[0]
	want := []Part{
		{ID: "ac-2_smt", Name: "statement", Prose: "Do the following:", Parts: []Part{
			{ID: "ac-2_smt.a", Name: "item", Label: "a.", Prose: "Define account types."},
			{ID: "ac-2_smt.b", Name: "item", Label: "b.", Prose: "Assign managers.", Parts: []Part{
				{ID: "ac-2_smt.b.1", Name: "item", Label: "1.", Prose: "Nested."},
			}},
		}},
		{ID: "ac-2_gdn", Name: "guidance", Prose: "Guidance text."},
		{Name: "assessment-objective"},
	}
	if !reflect.DeepEqual(c.Parts, want) {
		t.Fatalf("parts = %+v\nwant %+v", c.Parts, want)
	}
	if !reflect.DeepEqual(c.Related, []string{"ac-3", "ac-5"}) {
		t.Fatalf("related = %v", c.Related)
	}
	if _, ok := FindPart(c.Parts, "ac-2_smt.b.1"); !ok {
		t.Fatalf("FindPart nested failed")
	}
	if _, ok := FindPart(c.Parts, "ac-2_smt.z"); ok {
		t.Fatalf("FindPart found a missing part")
	}
	// Flattened text is unchanged in shape.
	if !strings.HasPrefix(c.Text, "ac-2 Account Management Do the following: statement") {
		t.Fatalf("text = %q", c.Text)
	}
	if got := ShortName(docs[0].Title, docs[0].Props()); got != "PARTS" {
		t.Fatalf("Props() short name = %q", got)
	}
	// Controls without parts get an empty, non-nil slice.
	plain, err := ParseJSON([]byte(`{"catalog":{"uuid":"u","metadata":{"title":"t"},"controls":[{"id":"x-1","title":"X"}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if plain[0].Controls[0].Parts == nil || len(plain[0].Controls[0].Parts) != 0 || plain[0].Controls[0].Related != nil || plain[0].Controls[0].References != nil {
		t.Fatalf("plain control = %+v", plain[0].Controls[0])
	}
}

// TestParse_ControlOwnReferencesResolved covers a control (not a task) whose
// own links reference back-matter: rel=external_reference and rel=reference
// '#uuid' hrefs resolve to title/citation/url with an empty taskId; unknown
// uuids keep uuid + text; rel=related stays a control id.
func TestParse_ControlOwnReferencesResolved(t *testing.T) {
	const cat = `{"catalog":{"uuid":"u","metadata":{"title":"Refs"},
	  "controls":[{"id":"c-1","title":"C","links":[
	    {"href":"#c-2","rel":"related"},
	    {"href":"#r1","rel":"external_reference","text":"SA-11"},
	    {"href":"#r2","rel":"reference"},
	    {"href":"#missing","rel":"external_reference","text":"X.1"},
	    {"href":"#r1","rel":"external_reference","text":"SA-11"}]}],
	  "back-matter":{"resources":[
	    {"uuid":"r1","title":"SP800-53","citation":{"text":"Joint Task Force (2020) SP 800-53 Rev. 5."},"rlinks":[{"href":"https://doi.org/10.6028/NIST.SP.800-53r5"},{"href":"https://second"}]},
	    {"uuid":"r2","title":"BSIMM","rlinks":[{"href":"https://www.bsimm.com"}]}]}}}`
	docs, err := ParseJSON([]byte(cat))
	if err != nil {
		t.Fatal(err)
	}
	c := docs[0].Controls[0]
	if !reflect.DeepEqual(c.Related, []string{"c-2"}) {
		t.Fatalf("related = %v", c.Related)
	}
	want := []Reference{
		{UUID: "r1", Title: "SP800-53", Citation: "Joint Task Force (2020) SP 800-53 Rev. 5.", URL: "https://doi.org/10.6028/NIST.SP.800-53r5", Text: "SA-11"},
		{UUID: "r2", Title: "BSIMM", URL: "https://www.bsimm.com"},
		{UUID: "missing", Text: "X.1"},
	}
	if !reflect.DeepEqual(c.References, want) {
		t.Fatalf("references = %+v\nwant %+v", c.References, want)
	}
	b, _ := json.Marshal(c.References[2])
	if string(b) != `{"uuid":"missing","title":"","text":"X.1"}` {
		t.Fatalf("unresolved json = %s", b)
	}
}

const paramsCatalog = `{
  "catalog": {
    "uuid": "9f1e759b-bd43-4d64-91b5-c04d6b1c4a94",
    "metadata": { "title": "Params catalog" },
    "params": [{"id": "cat_prm", "values": ["catalog-wide value"]}],
    "groups": [{
      "id": "ac", "title": "Access Control",
      "params": [{"id": "grp_prm", "label": "group thing"}],
      "controls": [{
        "id": "ac-1", "title": "Policy for {{ insert: param, ac-01_odp.04 }}",
        "params": [
          {"id": "ac-1_prm_1", "label": "organization-defined personnel or roles"},
          {"id": "ac-01_odp.03", "select": {"how-many": "one-or-more", "choice": ["organization-level", "mission/business process-level", "system-level"]}},
          {"id": "ac-01_odp.04", "label": "official"},
          {"id": "ac-01_odp.05", "label": "frequency", "values": ["annually", "after incidents"]},
          {"id": "ac-1_sel_one", "select": {"choice": ["disable", "remove"]}},
          {"id": "ac-1_nested", "select": {"choice": ["{{ insert: param, ac-1_inner }}", "never"]}},
          {"id": "ac-1_inner", "label": "time period"},
          {"id": "ac-1_empty"}
        ],
        "parts": [
          {"id": "ac-1_smt", "name": "statement", "parts": [
            {"id": "ac-1_smt.a", "name": "item", "props": [{"name": "label", "value": "a."}], "prose": "Develop, document, and disseminate to {{ insert: param, ac-1_prm_1 }}:", "parts": [
              {"id": "ac-1_smt.a.1", "name": "item", "prose": "{{insert: param, ac-01_odp.03}} access control policy that:"}
            ]},
            {"id": "ac-1_smt.b", "name": "item", "props": [{"name": "label", "value": "b."}], "prose": "Designate an {{ insert: param, ac-01_odp.04 }} to manage the development, documentation, and dissemination of the access control policy and procedures; and"},
            {"id": "ac-1_smt.c", "name": "item", "prose": "Review {{ insert: param, ac-01_odp.05 }}; {{ insert: param, ac-1_sel_one }} accounts within {{ insert: param, ac-1_nested }}."},
            {"id": "ac-1_smt.d", "name": "item", "prose": "Unknown {{ insert: param, nope_prm }} and empty {{ insert: param, ac-1_empty }}; group {{ insert: param, grp_prm }}; catalog {{ insert: param, cat_prm }}."}
          ]},
          {"id": "ac-1_gdn", "name": "guidance", "prose": "Plain guidance."}
        ],
        "controls": [{
          "id": "ac-1.1", "title": "Enhancement",
          "params": [{"id": "ac-1.1_prm", "label": "official", "select": {"choice": ["x"]}}],
          "parts": [{"id": "ac-1.1_smt", "name": "statement", "prose": "Inherited {{ insert: param, ac-01_odp.04 }} and own {{ insert: param, ac-1.1_prm }}."}]
        }]
      }, {
        "id": "ac-2", "title": "Sibling",
        "parts": [{"id": "ac-2_smt", "name": "statement", "prose": "Not visible: {{ insert: param, ac-01_odp.04 }}"}]
      }]
    }]
  }
}`

func TestParse_ResolvesParameterPlaceholders(t *testing.T) {
	docs, err := ParseJSON([]byte(paramsCatalog))
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Control{}
	for _, c := range docs[0].Controls {
		byID[c.ID] = c
	}
	ac1 := byID["ac-1"]
	stmt := ac1.Parts[0]
	want := map[string]string{
		"ac-1_smt.a":   "Develop, document, and disseminate to [Assignment: organization-defined personnel or roles]:",
		"ac-1_smt.a.1": "[Selection (one or more): organization-level; mission/business process-level; system-level] access control policy that:",
		"ac-1_smt.b":   "Designate an [Assignment: organization-defined official] to manage the development, documentation, and dissemination of the access control policy and procedures; and",
		"ac-1_smt.c":   "Review annually, after incidents; [Selection: disable; remove] accounts within [Selection: [Assignment: organization-defined time period]; never].",
		"ac-1_smt.d":   "Unknown [Assignment: nope_prm] and empty [Assignment: ac-1_empty]; group [Assignment: organization-defined group thing]; catalog catalog-wide value.",
	}
	for id, prose := range want {
		p, ok := FindPart(ac1.Parts, id)
		if !ok {
			t.Fatalf("part %s missing", id)
		}
		if p.Prose != prose {
			t.Errorf("%s prose = %q\nwant %q", id, p.Prose, prose)
		}
	}
	if stmt.Prose != "" || ac1.Parts[1].Prose != "Plain guidance." {
		t.Fatalf("untouched parts changed: %q / %q", stmt.Prose, ac1.Parts[1].Prose)
	}
	// Nothing machine-readable leaks into the stored tree.
	if b, _ := json.Marshal(ac1.Parts); strings.Contains(string(b), "{{") {
		t.Fatalf("parts still contain placeholders: %s", b)
	}
	if ac1.Title != "Policy for [Assignment: organization-defined official]" {
		t.Fatalf("title = %q", ac1.Title)
	}
	if strings.Contains(ac1.Text, "{{") || !strings.Contains(ac1.Text, "[Assignment: organization-defined official]") {
		t.Fatalf("text = %q", ac1.Text)
	}
	// Exported params keep id/label/values/choices/howMany.
	if len(ac1.Params) != 8 || ac1.Params[0].ID != "ac-1_prm_1" || ac1.Params[0].Label != "organization-defined personnel or roles" {
		t.Fatalf("params = %+v", ac1.Params)
	}
	sel := ac1.Params[1]
	if sel.HowMany != "one-or-more" || len(sel.Choices) != 3 || sel.Label != "" {
		t.Fatalf("select param = %+v", sel)
	}
	if one := ac1.Params[4]; one.HowMany != "one" || len(one.Choices) != 2 {
		t.Fatalf("single select param = %+v", one)
	}
	if vals := ac1.Params[3]; !reflect.DeepEqual(vals.Values, []string{"annually", "after incidents"}) {
		t.Fatalf("values param = %+v", vals)
	}
	// Enhancements see their parent's params; siblings do not. Selection wins over label.
	enh := byID["ac-1.1"]
	if enh.Parts[0].Prose != "Inherited [Assignment: organization-defined official] and own [Selection: x]." {
		t.Fatalf("enhancement prose = %q", enh.Parts[0].Prose)
	}
	if len(enh.Params) != 1 {
		t.Fatalf("enhancement params = %+v", enh.Params)
	}
	if sib := byID["ac-2"]; sib.Parts[0].Prose != "Not visible: [Assignment: ac-01_odp.04]" || sib.Params != nil {
		t.Fatalf("sibling = %+v", sib)
	}
}

func TestResolveParams_RecursionCapAndEdgeCases(t *testing.T) {
	params := []Param{
		{ID: "loop", Choices: []string{"{{ insert: param, loop }}"}},
		{ID: "od", Label: "Organization-Defined  thing"},
	}
	got := ResolveParams("{{ insert: param, loop }}", params)
	if strings.Contains(got, "{{") {
		t.Fatalf("self-referencing selection left a placeholder: %q", got)
	}
	if !strings.HasPrefix(got, "[Selection: [Selection: ") || !strings.HasSuffix(got, "[Assignment: loop]]]]]]") {
		t.Fatalf("depth-capped resolution = %q", got)
	}
	if got := ResolveParams("x {{ insert: param, od }} y", params); got != "x [Assignment: Organization-Defined thing] y" {
		t.Fatalf("existing organization-defined prefix duplicated: %q", got)
	}
	if got := ResolveParams("no placeholders", nil); got != "no placeholders" {
		t.Fatalf("passthrough = %q", got)
	}
	if got := ResolveParams("{{insert:param,a}} {{ insert: param, b }}", nil); got != "[Assignment: a] [Assignment: b]" {
		t.Fatalf("unknown ids = %q", got)
	}
}

func TestTrackableParts(t *testing.T) {
	items := []Part{
		{ID: "ac-2_smt.a", Name: "item", Label: "a.", Prose: "A", Parts: []Part{{ID: "ac-2_smt.a.1", Name: "item"}}},
		{ID: "ac-2_smt.b", Name: "item", Label: "b.", Prose: "B"},
	}
	cases := []struct {
		name      string
		parts     []Part
		controlID string
		wantIDs   []string
	}{
		{"statement with items", []Part{{ID: "ac-2_gdn", Name: "guidance"}, {ID: "ac-2_smt", Name: "statement", Parts: items}}, "ac-2", []string{"ac-2_smt.a", "ac-2_smt.b"}},
		{"statement without items", []Part{{ID: "ac-3_smt", Name: "statement", Prose: "Enforce."}, {ID: "ac-3_gdn", Name: "guidance"}}, "ac-3", []string{"ac-3_smt"}},
		{"statement without id", []Part{{Name: "statement", Prose: "Define."}}, "ac-1", []string{"ac-1_smt"}},
		{"items without ids", []Part{{Name: "statement", Parts: []Part{{Name: "item", Label: "a."}, {Name: "item", Label: "b."}}}}, "x-1", []string{"x-1_smt.1", "x-1_smt.2"}},
		{"no statement", []Part{{ID: "ac-9_gdn", Name: "guidance"}}, "ac-9", []string{"ac-9_smt"}},
		{"no parts", nil, "ac-10", []string{"ac-10_smt"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := TrackableParts(tc.parts, tc.controlID)
			var ids []string
			for _, p := range got {
				ids = append(ids, p.ID)
			}
			if !reflect.DeepEqual(ids, tc.wantIDs) {
				t.Fatalf("ids = %v, want %v", ids, tc.wantIDs)
			}
		})
	}
	// Items keep label/prose/nested parts; the single-statement form drops nested parts (there are none) and keeps prose.
	got := TrackableParts([]Part{{ID: "ac-2_smt", Name: "statement", Parts: items}}, "ac-2")
	if got[0].Label != "a." || got[0].Prose != "A" || len(got[0].Parts) != 1 {
		t.Fatalf("item = %+v", got[0])
	}
	stmt := TrackableParts([]Part{{ID: "ac-3_smt", Name: "statement", Prose: "Enforce."}}, "ac-3")[0]
	if stmt.Name != "statement" || stmt.Prose != "Enforce." {
		t.Fatalf("statement = %+v", stmt)
	}
	// The synthetic part is a statement with no prose.
	if s := TrackableParts(nil, "ac-10")[0]; s.Name != "statement" || s.Prose != "" {
		t.Fatalf("synthetic = %+v", s)
	}
}
