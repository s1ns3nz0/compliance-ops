// Package assessment normalizes uploaded OSCAL assessment documents without
// changing their canonical JSON.
package assessment

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	TypePlan    = "assessment-plan"
	TypeResults = "assessment-results"
	TypePOAM    = "plan-of-action-and-milestones"
)

var Types = []string{TypePlan, TypeResults, TypePOAM}

func ValidType(s string) bool { return s == TypePlan || s == TypeResults || s == TypePOAM }

type MappingInfo struct {
	Source        string `json:"source"`
	RequirementID string `json:"requirementId,omitempty"`
	PartID        string `json:"partId,omitempty"`
	ControlID     string `json:"controlId,omitempty"`
}

type Item struct {
	ItemKey               string         `json:"itemKey"`
	Kind                  string         `json:"kind"`
	UUID                  string         `json:"uuid,omitempty"`
	Title                 string         `json:"title,omitempty"`
	Summary               string         `json:"summary,omitempty"`
	Description           string         `json:"description,omitempty"`
	Remarks               string         `json:"remarks,omitempty"`
	Status                string         `json:"status,omitempty"`
	Risk                  string         `json:"risk,omitempty"`
	Severity              string         `json:"severity,omitempty"`
	Owner                 string         `json:"owner,omitempty"`
	ControlIDs            []string       `json:"controlIds"`
	ObjectiveIDs          []string       `json:"objectiveIds"`
	PartIDs               []string       `json:"partIds"`
	EvidenceRefs          []string       `json:"evidenceRefs"`
	RelatedObservationIDs []string       `json:"relatedObservationIds"`
	RelatedFindingIDs     []string       `json:"relatedFindingIds"`
	RelatedRiskIDs        []string       `json:"relatedRiskIds"`
	ResponsibleRoles      []string       `json:"responsibleRoles"`
	Methods               []string       `json:"methods"`
	SubjectIDs            []string       `json:"subjectIds"`
	ScheduledCompletion   string         `json:"scheduledCompletion,omitempty"`
	Schedule              any            `json:"schedule,omitempty"`
	Milestones            []Item         `json:"milestones,omitempty"`
	Mapping               MappingInfo    `json:"mapping"`
	Raw                   map[string]any `json:"fields,omitempty"`
}

type Document struct {
	DocumentID       string   `json:"documentId"`
	Type             string   `json:"type"`
	Title            string   `json:"title"`
	Version          string   `json:"version,omitempty"`
	LastModified     string   `json:"lastModified,omitempty"`
	ImportRefs       []string `json:"importRefs"`
	ReviewedControls []Item   `json:"reviewedControls"`
	Subjects         []Item   `json:"subjects"`
	Tasks            []Item   `json:"tasks"`
	Results          []Item   `json:"results"`
	Observations     []Item   `json:"observations"`
	Findings         []Item   `json:"findings"`
	Risks            []Item   `json:"risks"`
	Attestations     []Item   `json:"attestations"`
	LogEntries       []Item   `json:"resultLog"`
	POAMItems        []Item   `json:"poamItems"`
	UnmappedItems    []Item   `json:"unmappedItems"`
}

type CountSet struct {
	ReviewedControls int `json:"reviewedControls"`
	Subjects         int `json:"subjects"`
	Tasks            int `json:"tasks"`
	Results          int `json:"results"`
	Findings         int `json:"findings"`
	Observations     int `json:"observations"`
	Risks            int `json:"risks"`
	POAMItems        int `json:"poamItems"`
	UnmappedItems    int `json:"unmappedItems"`
}

func (d Document) Counts() CountSet {
	return CountSet{len(d.ReviewedControls), len(d.Subjects), len(d.Tasks), len(d.Results), len(d.Findings), len(d.Observations), len(d.Risks), len(d.POAMItems), len(d.UnmappedItems)}
}

func ParseJSON(raw []byte) (Document, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return Document{}, err
	}
	return Parse(v)
}
func Parse(v any) (Document, error) {
	container := obj(v)
	if container == nil {
		return Document{}, errors.New("assessment must be an object")
	}
	var typ string
	var root map[string]any
	for _, t := range Types {
		if m := obj(container[t]); m != nil {
			typ, root = t, m
			break
		}
	}
	if root == nil {
		return Document{}, errors.New("unsupported assessment document")
	}
	meta := obj(root["metadata"])
	d := Document{DocumentID: str(root["uuid"]), Type: typ, Title: normalizePresentation(str(meta["title"]), false), Version: str(meta["version"]), LastModified: str(meta["last-modified"])}
	if d.DocumentID == "" || d.Title == "" {
		return Document{}, errors.New("assessment requires uuid and metadata.title")
	}
	for _, name := range []string{"import-ssp", "import-ap", "import-profile"} {
		if m := obj(root[name]); m != nil {
			if h := ref(str(m["href"])); h != "" {
				d.ImportRefs = appendUnique(d.ImportRefs, h)
			}
		}
	}
	parseReviewed(root["reviewed-controls"], typ, "", &d)
	parseSubjects(root["assessment-subjects"], typ, &d)
	parseTasks(root["tasks"], typ, &d)
	if typ == TypeResults {
		for i, r := range arr(root["results"]) {
			rm := obj(r)
			rid := str(rm["uuid"])
			if rid == "" {
				rid = fmt.Sprintf("index-%d", i+1)
			}
			prefix := typ + "/" + rid
			ri := makeItem(prefix, "result", rm)
			d.Results = append(d.Results, ri)
			parseReviewed(rm["reviewed-controls"], typ, prefix, &d)
			parseItems(rm["observations"], typ, prefix, "observation", &d.Observations)
			parseItems(rm["findings"], typ, prefix, "finding", &d.Findings)
			parseItems(rm["risks"], typ, prefix, "risk", &d.Risks)
			parseItems(rm["attestations"], typ, prefix, "attestation", &d.Attestations)
			if l := obj(rm["log"]); l != nil {
				parseItems(l["entries"], typ, prefix, "log-entry", &d.LogEntries)
			}
		}
	}
	if typ == TypePOAM {
		parseItems(root["poam-items"], typ, "", "poam-item", &d.POAMItems)
	}
	return d, nil
}

func parseReviewed(v any, typ, prefix string, d *Document) {
	m := obj(v)
	if m == nil {
		return
	}
	idx := 0
	for _, sel := range arr(m["control-selections"]) {
		sm := obj(sel)
		for _, inc := range arr(sm["include-controls"]) {
			im := obj(inc)
			it := makeItem(itemKey(typ, prefix+"/reviewed-control", "", idx), "reviewed-control", im)
			it.ControlIDs = appendUnique(it.ControlIDs, str(im["control-id"]))
			it.PartIDs = append(it.PartIDs, stringsList(im["statement-ids"])...)
			it.ObjectiveIDs = append(it.ObjectiveIDs, stringsList(im["assessment-objective-ids"])...)
			d.ReviewedControls = append(d.ReviewedControls, it)
			idx++
		}
	}
}
func parseSubjects(v any, typ string, d *Document) {
	for i, x := range arr(v) {
		m := obj(x)
		it := makeItem(itemKey(typ, "subject", "", i), "subject", m)
		for _, s := range arr(m["include-subjects"]) {
			it.SubjectIDs = appendUnique(it.SubjectIDs, str(obj(s)["subject-uuid"]))
		}
		d.Subjects = append(d.Subjects, it)
	}
}
func parseTasks(v any, typ string, d *Document) { parseItems(v, typ, "", "task", &d.Tasks) }
func parseItems(v any, typ, prefix, kind string, out *[]Item) {
	for i, x := range arr(v) {
		m := obj(x)
		*out = append(*out, makeItem(itemKey(typ, prefix+"/"+kind, m, i), kind, m))
	}
}
func itemKey(typ, prefix string, m any, index int) string {
	p := strings.Trim(prefix, "/")
	p = strings.TrimPrefix(p, typ+"/")
	if mm := obj(m); mm != nil {
		if id := str(mm["uuid"]); id != "" {
			return typ + "/" + p + "/" + id
		}
	}
	return fmt.Sprintf("%s/%s/index-%d", typ, p, index+1)
}
func makeItem(key, kind string, m map[string]any) Item {
	it := Item{ItemKey: key, Kind: kind, UUID: str(m["uuid"]), Title: normalizePresentation(str(m["title"]), false), Description: normalizePresentation(str(m["description"]), false), Remarks: normalizePresentation(str(m["remarks"]), kind == "poam-item"), ControlIDs: []string{}, ObjectiveIDs: []string{}, PartIDs: []string{}, EvidenceRefs: []string{}, RelatedObservationIDs: []string{}, RelatedFindingIDs: []string{}, RelatedRiskIDs: []string{}, ResponsibleRoles: []string{}, Methods: stringsList(m["methods"]), SubjectIDs: []string{}, Mapping: MappingInfo{Source: "unmapped"}, Raw: m}
	it.Summary = it.Title
	if it.Summary == "" {
		it.Summary = it.Description
	}
	for _, k := range []string{"control-id", "control_id", "controlId"} {
		it.ControlIDs = appendUnique(it.ControlIDs, str(m[k]))
	}
	for _, k := range []string{"target-id", "target_id", "targetId"} {
		if s := str(m[k]); s != "" {
			it.ObjectiveIDs = appendUnique(it.ObjectiveIDs, s)
		}
	}
	if target := obj(m["target"]); target != nil {
		id := str(target["target-id"])
		typ := str(target["type"])
		if strings.Contains(typ, "objective") {
			it.ObjectiveIDs = appendUnique(it.ObjectiveIDs, id)
		} else {
			it.ControlIDs = appendUnique(it.ControlIDs, id)
		}
		if st := obj(target["status"]); st != nil {
			it.Status = str(st["state"])
		}
	}
	for _, p := range arr(m["props"]) {
		pm := obj(p)
		name, val := str(pm["name"]), str(pm["value"])
		switch name {
		case "status":
			it.Status = val
		case "risk":
			it.Risk = val
		case "severity":
			it.Severity = val
		case "owner":
			it.Owner = val
		case "scheduled-completion-date":
			it.ScheduledCompletion = val
		}
	}
	if it.Status == "" {
		it.Status = str(m["status"])
	}
	for _, e := range arr(m["relevant-evidence"]) {
		it.EvidenceRefs = appendUnique(it.EvidenceRefs, ref(str(obj(e)["href"])))
	}
	for _, r := range arr(m["related-observations"]) {
		it.RelatedObservationIDs = appendUnique(it.RelatedObservationIDs, str(obj(r)["observation-uuid"]))
	}
	for _, r := range arr(m["related-findings"]) {
		it.RelatedFindingIDs = appendUnique(it.RelatedFindingIDs, str(obj(r)["finding-uuid"]))
	}
	for _, r := range arr(m["related-risks"]) {
		it.RelatedRiskIDs = appendUnique(it.RelatedRiskIDs, str(obj(r)["risk-uuid"]))
	}
	for _, r := range stringsList(m["associated-risks"]) {
		it.RelatedRiskIDs = appendUnique(it.RelatedRiskIDs, r)
	}
	for _, r := range arr(m["responsible-roles"]) {
		it.ResponsibleRoles = appendUnique(it.ResponsibleRoles, str(obj(r)["role-id"]))
	}
	for _, s := range arr(m["subjects"]) {
		it.SubjectIDs = appendUnique(it.SubjectIDs, str(obj(s)["subject-uuid"]))
	}
	if tm := m["timing"]; tm != nil {
		it.Schedule = tm
	}
	for i, x := range arr(m["milestones"]) {
		mm := obj(x)
		it.Milestones = append(it.Milestones, makeItem(itemKey("milestone", kind, mm, i), "milestone", mm))
	}
	if rc := obj(m["related-controls"]); rc != nil {
		for _, s := range arr(rc["control-selections"]) {
			for _, c := range arr(obj(s)["include-controls"]) {
				it.ControlIDs = appendUnique(it.ControlIDs, str(obj(c)["control-id"]))
			}
		}
	}
	return it
}

func normalizePresentation(s string, removePOAMParserRemark bool) string {
	s = strings.TrimPrefix(s, "DEMO ")
	if removePOAMParserRemark {
		const parserRemark = " Parser requires manual part mapping for POA&M items because related-controls statement-ids are not normalized."
		if i := strings.Index(s, parserRemark); i > 0 && strings.ContainsRune(".!?", rune(s[i-1])) {
			end := i + len(parserRemark)
			if end == len(s) || s[end] == ' ' {
				s = s[:i] + s[end:]
			}
		}
	}
	return s
}

func obj(v any) map[string]any { m, _ := v.(map[string]any); return m }
func arr(v any) []any          { a, _ := v.([]any); return a }
func str(v any) string         { s, _ := v.(string); return strings.TrimSpace(s) }
func stringsList(v any) []string {
	var o []string
	for _, x := range arr(v) {
		if s := str(x); s != "" {
			o = append(o, s)
		}
	}
	return o
}
func appendUnique(a []string, s string) []string {
	if s == "" {
		return a
	}
	for _, x := range a {
		if x == s {
			return a
		}
	}
	return append(a, s)
}
func ref(s string) string { return strings.TrimPrefix(strings.TrimSpace(s), "#") }
