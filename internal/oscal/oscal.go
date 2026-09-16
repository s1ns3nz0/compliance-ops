// Package oscal parses OSCAL JSON documents and extracts controls. It is a port
// of the previous TypeScript implementation and keeps the same shape.
package oscal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// DocumentTypes are the supported OSCAL root keys, in detection order.
var DocumentTypes = []string{"catalog", "profile", "component-definition", "system-security-plan", "assessment-plan", "assessment-results", "plan-of-action-and-milestones"}

// Part is a structured OSCAL control part (statement, guidance, item, ...).
// Only id, name, label (from the "label" prop), prose and nested parts are
// kept; document order is preserved.
type Part struct {
	ID    string `json:"id,omitempty"`
	Name  string `json:"name"`
	Label string `json:"label,omitempty"`
	// Prose has every `{{ insert: param, <id> }}` placeholder resolved to
	// NIST-style text at parse time (see ResolveParams); the machine form is
	// not kept so stored parts never contain placeholders.
	Prose string `json:"prose,omitempty"`
	Parts []Part `json:"parts,omitempty"`
}

// Param is an OSCAL control parameter (organization-defined parameter).
type Param struct {
	ID      string   `json:"id"`
	Label   string   `json:"label,omitempty"`
	Values  []string `json:"values,omitempty"`
	Choices []string `json:"choices,omitempty"`
	// HowMany is the select cardinality ("one" or "one-or-more"); empty
	// when the parameter has no select.
	HowMany string `json:"howMany,omitempty"`
}

// Reference is an external reference of a control (OSCAL link with
// rel=external_reference, or rel=reference pointing at a back-matter
// resource), resolved against the document's back-matter resources.
type Reference struct {
	// UUID is the back-matter resource uuid ('#' stripped); empty when the
	// link href was a plain URL.
	UUID string `json:"uuid"`
	// Title is the resource title; empty when the uuid is unresolved.
	Title    string `json:"title"`
	Citation string `json:"citation,omitempty"`
	URL      string `json:"url,omitempty"`
	// Text is the link text, e.g. "SA-11" or "CR1.5" (the referenced
	// section inside the external document).
	Text string `json:"text,omitempty"`
	// TaskID names the folded task (SSDF) that carried the link; empty when
	// the control's own links did.
	TaskID string `json:"taskId,omitempty"`
}

// Control is a flattened OSCAL control.
type Control struct {
	ID     string  `json:"id"`
	Title  string  `json:"title"`
	Text   string  `json:"text"`
	Parts  []Part  `json:"parts"`
	Params []Param `json:"params,omitempty"`
	// Related holds control ids from links with rel=related only.
	Related []string `json:"related,omitempty"`
	// References holds the resolved external references (rel=external_reference,
	// plus rel=reference links into back-matter), including those of folded tasks.
	References []Reference    `json:"references,omitempty"`
	Raw        map[string]any `json:"raw"`
}

// ControlSelection is an OSCAL profile include/exclude control selection.
type ControlSelection struct {
	WithIDs           []string `json:"withIds,omitempty"`
	Matching          []string `json:"matching,omitempty"`
	WithChildControls bool     `json:"withChildControls,omitempty"`
}

// ProfileImport retains one profile import and its control selections.
type ProfileImport struct {
	Href            string             `json:"href"`
	IncludeAll      bool               `json:"includeAll,omitempty"`
	IncludeControls []ControlSelection `json:"includeControls,omitempty"`
	ExcludeControls []ControlSelection `json:"excludeControls,omitempty"`
}

// ---- parameter resolution ---------------------------------------------------

// paramPlaceholder matches OSCAL prose insertions such as
// `{{ insert: param, ac-01_odp.04 }}`.
var paramPlaceholder = regexp.MustCompile(`\{\{\s*insert:\s*param,\s*([^}\s]+)\s*\}\}`)

// maxParamDepth caps recursive resolution of placeholders nested inside
// selection choices.
const maxParamDepth = 5

// paramScope is the set of parameters visible to a control: catalog and
// group params plus the params of every ancestor control and the control
// itself. Later (nearer) definitions shadow earlier ones.
type paramScope map[string]Param

// extend returns a copy of s with params added.
func (s paramScope) extend(params []Param) paramScope {
	if len(params) == 0 {
		return s
	}
	out := make(paramScope, len(s)+len(params))
	for k, v := range s {
		out[k] = v
	}
	for _, p := range params {
		out[p.ID] = p
	}
	return out
}

// ResolveParams replaces every parameter placeholder in s using params.
// Resolution order per parameter: fixed values (joined with ", "), then a
// selection (`[Selection: a; b]` / `[Selection (one or more): a; b]`, choices
// resolved recursively), then `[Assignment: organization-defined <label>]`,
// and `[Assignment: <id>]` for unknown or empty parameters.
func ResolveParams(s string, params []Param) string {
	return resolveParams(s, paramScope{}.extend(params), 0)
}

func resolveParams(s string, scope paramScope, depth int) string {
	if !strings.Contains(s, "{{") {
		return s
	}
	return paramPlaceholder.ReplaceAllStringFunc(s, func(m string) string {
		id := paramPlaceholder.FindStringSubmatch(m)[1]
		return paramText(id, scope, depth)
	})
}

func paramText(id string, scope paramScope, depth int) string {
	p, ok := scope[id]
	if !ok || depth >= maxParamDepth {
		return "[Assignment: " + id + "]"
	}
	switch {
	case len(p.Values) > 0:
		return strings.Join(p.Values, ", ")
	case len(p.Choices) > 0:
		choices := make([]string, len(p.Choices))
		for i, c := range p.Choices {
			choices[i] = resolveParams(c, scope, depth+1)
		}
		prefix := "[Selection: "
		if p.HowMany == "one-or-more" {
			prefix = "[Selection (one or more): "
		}
		return prefix + strings.Join(choices, "; ") + "]"
	case p.Label != "":
		label := strings.Join(strings.Fields(p.Label), " ")
		if !strings.HasPrefix(strings.ToLower(label), "organization-defined") {
			label = "organization-defined " + label
		}
		return "[Assignment: " + label + "]"
	default:
		return "[Assignment: " + id + "]"
	}
}

// paramsOf converts raw OSCAL params into Params (order preserved).
func paramsOf(v any) []Param {
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]Param, 0, len(list))
	for _, item := range list {
		pm := asMap(item)
		if pm == nil {
			continue
		}
		id := asString(pm["id"])
		if id == "" {
			continue
		}
		p := Param{ID: id, Label: strings.TrimSpace(asString(pm["label"])), Values: stringsOf(pm["values"])}
		if sel := asMap(pm["select"]); sel != nil {
			p.Choices = stringsOf(sel["choice"])
			p.HowMany = asString(sel["how-many"])
			if p.HowMany == "" && len(p.Choices) > 0 {
				p.HowMany = "one"
			}
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func stringsOf(v any) []string {
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, item := range list {
		if s := asString(item); s != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

// FindPart walks parts depth-first and returns the part with the given id.
func FindPart(parts []Part, id string) (Part, bool) {
	for _, p := range parts {
		if p.ID == id {
			return p, true
		}
		if sub, ok := FindPart(p.Parts, id); ok {
			return sub, true
		}
	}
	return Part{}, false
}

// TrackableParts returns the parts of a control that carry implementation
// tracking, in document order: the nested items of the part named
// "statement"; the statement part itself when it has no nested items; or a
// synthetic "<controlID>_smt" statement when the control has no statement
// part at all. Parts without an id get a deterministic fallback id
// ("<controlID>_smt" for the statement, "<statementID>.<n>" for items) so
// every returned part is addressable. The result is never empty and never
// aliases the input.
func TrackableParts(parts []Part, controlID string) []Part {
	stmtID := controlID + "_smt"
	for _, p := range parts {
		if p.Name != "statement" {
			continue
		}
		if p.ID != "" {
			stmtID = p.ID
		}
		if len(p.Parts) == 0 {
			stmt := p
			stmt.ID = stmtID
			stmt.Parts = nil
			return []Part{stmt}
		}
		out := make([]Part, len(p.Parts))
		for i, item := range p.Parts {
			out[i] = item
			if out[i].ID == "" {
				out[i].ID = fmt.Sprintf("%s.%d", stmtID, i+1)
			}
		}
		return out
	}
	return []Part{{ID: stmtID, Name: "statement"}}
}

// Document is a parsed OSCAL document with flattened controls.
type Document struct {
	ID             string          `json:"id"`
	Type           string          `json:"type"`
	Title          string          `json:"title"`
	LastModified   string          `json:"lastModified,omitempty"`
	Version        string          `json:"version,omitempty"`
	Controls       []Control       `json:"controls"`
	ProfileImports []ProfileImport `json:"profileImports,omitempty"`
	Raw            map[string]any  `json:"raw"`
}

// Props returns the raw metadata props of a parsed document (may be nil).
func (d Document) Props() []any {
	root := asMap(d.Raw[d.Type])
	meta := asMap(root["metadata"])
	list, _ := meta["props"].([]any)
	return list
}

// shortNamePatterns maps case-insensitive title patterns to a canonical
// framework short name; checked in order, first match wins.
var shortNamePatterns = []struct {
	re   *regexp.Regexp
	name string
}{
	{regexp.MustCompile(`(?i)800[-\s]?218|\bSSDF\b|secure\s+software\s+development\s+framework`), "NIST SP 800-218 SSDF"},
	{regexp.MustCompile(`(?i)800[-\s]?53`), "NIST SP 800-53"},
	{regexp.MustCompile(`(?i)800[-\s]?171`), "NIST SP 800-171"},
	{regexp.MustCompile(`(?i)nist\s*csf|cybersecurity\s+framework`), "NIST CSF"},
	{regexp.MustCompile(`(?i)27001`), "ISO/IEC 27001"},
	{regexp.MustCompile(`(?i)27002`), "ISO/IEC 27002"},
	{regexp.MustCompile(`(?i)\bsoc\s*2\b`), "SOC 2"},
	{regexp.MustCompile(`(?i)pci[\s-]*dss`), "PCI DSS"},
	{regexp.MustCompile(`(?i)\bcis\b.*controls`), "CIS Controls"},
	{regexp.MustCompile(`(?i)fedramp.*\b(high|moderate|low)\b`), "FedRAMP $1"},
	{regexp.MustCompile(`(?i)\bhipaa\b`), "HIPAA"},
	{regexp.MustCompile(`(?i)\bgdpr\b`), "GDPR"},
	{regexp.MustCompile(`(?i)k-isms-p`), "K-ISMS-P"},
	{regexp.MustCompile(`(?i)isms-p`), "ISMS-P"},
}

// shortNameProps are metadata prop names that carry an explicit short name.
var shortNameProps = []string{"short-name", "shortname", "abbreviation"}

const shortNameMax = 40

// ShortName derives a display short name for a framework. An explicit
// metadata prop (short-name, abbreviation, label) wins; otherwise the title is
// matched against known framework patterns; otherwise the title is cut to 40
// characters at a word boundary with an ellipsis.
func ShortName(title string, props []any) string {
	for _, n := range shortNameProps {
		if v := strings.TrimSpace(propValue(props, n)); v != "" && len(v) <= 60 {
			return v
		}
	}
	title = strings.Join(strings.Fields(title), " ")
	for _, p := range shortNamePatterns {
		if m := p.re.FindStringSubmatch(title); m != nil {
			name := p.name
			if len(m) > 1 {
				name = strings.Replace(name, "$1", capitalize(m[1]), 1)
			}
			return name
		}
	}
	return truncateWords(title, shortNameMax)
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + strings.ToLower(s[1:])
}

// truncateWords cuts s to at most max runes at a word boundary and appends an
// ellipsis when something was removed.
func truncateWords(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	cut := string(runes[:max])
	if i := strings.LastIndex(cut, " "); i > 0 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,;:-") + "…"
}

// Repository lists OSCAL documents.
type Repository interface {
	List(ctx context.Context) ([]Document, error)
}

// SourceRepository exposes configured sources for independent discovery.
type SourceRepository interface {
	Repository
	Sources() []string
	FetchSource(ctx context.Context, sourceURL string) ([]Document, error)
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func asString(v any) string {
	s, ok := v.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return ""
	}
	return s
}

func directText(control map[string]any) string {
	parts := []string{}
	if id := asString(control["id"]); id != "" {
		parts = append(parts, id)
	}
	if title := asString(control["title"]); title != "" {
		parts = append(parts, title)
	}
	if list, ok := control["parts"].([]any); ok {
		for _, p := range list {
			pm := asMap(p)
			if pm == nil {
				continue
			}
			if prose := asString(pm["prose"]); prose != "" {
				parts = append(parts, prose)
			}
			if name := asString(pm["name"]); name != "" {
				parts = append(parts, name)
			}
		}
	}
	return strings.Join(parts, " ")
}

// propValue returns the value of the first prop named name.
func propValue(props any, name string) string {
	list, ok := props.([]any)
	if !ok {
		return ""
	}
	for _, p := range list {
		pm := asMap(p)
		if pm != nil && asString(pm["name"]) == name {
			return asString(pm["value"])
		}
	}
	return ""
}

// partsOf converts raw OSCAL parts into the structured Part tree, resolving
// parameter placeholders in prose against scope.
func partsOf(v any, scope paramScope) []Part {
	list, ok := v.([]any)
	if !ok {
		return []Part{}
	}
	out := make([]Part, 0, len(list))
	for _, p := range list {
		pm := asMap(p)
		if pm == nil {
			continue
		}
		part := Part{ID: asString(pm["id"]), Name: asString(pm["name"]), Label: propValue(pm["props"], "label"), Prose: resolveParams(asString(pm["prose"]), scope, 0)}
		if sub := partsOf(pm["parts"], scope); len(sub) > 0 {
			part.Parts = sub
		}
		out = append(out, part)
	}
	return out
}

// relatedOf collects the targets of links whose rel is one of rels,
// stripping a leading '#'.
func relatedOf(v any, rels ...string) []string {
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, l := range list {
		lm := asMap(l)
		if lm == nil {
			continue
		}
		rel := asString(lm["rel"])
		match := false
		for _, r := range rels {
			if rel == r {
				match = true
				break
			}
		}
		if !match {
			continue
		}
		href := strings.TrimPrefix(strings.TrimSpace(asString(lm["href"])), "#")
		if href != "" {
			out = append(out, href)
		}
	}
	return out
}

// referencesOf collects unresolved references from links: every link with
// rel=external_reference (a '#uuid' href yields UUID, any other href yields
// URL) and links with rel=reference whose href is a '#uuid'. taskID is
// recorded on each entry.
func referencesOf(v any, taskID string) []Reference {
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []Reference
	for _, l := range list {
		lm := asMap(l)
		if lm == nil {
			continue
		}
		rel := asString(lm["rel"])
		href := strings.TrimSpace(asString(lm["href"]))
		if href == "" {
			continue
		}
		ref := Reference{Text: strings.TrimSpace(asString(lm["text"])), TaskID: taskID}
		switch {
		case rel == "external_reference" && strings.HasPrefix(href, "#"):
			ref.UUID = strings.TrimPrefix(href, "#")
		case rel == "external_reference":
			ref.URL = href
		case rel == "reference" && strings.HasPrefix(href, "#"):
			ref.UUID = strings.TrimPrefix(href, "#")
		default:
			continue
		}
		if ref.UUID == "" && ref.URL == "" {
			continue
		}
		out = append(out, ref)
	}
	return out
}

// appendUniqueRefs appends each of add to dst unless an entry with the same
// (uuid, url, text, taskId) is already present.
func appendUniqueRefs(dst []Reference, add []Reference) []Reference {
	for _, r := range add {
		dup := false
		for _, d := range dst {
			if d.UUID == r.UUID && d.URL == r.URL && d.Text == r.Text && d.TaskID == r.TaskID {
				dup = true
				break
			}
		}
		if !dup {
			dst = append(dst, r)
		}
	}
	return dst
}

// resource is one back-matter resource.
type resource struct {
	title, citation, url string
}

// backMatterResources indexes the back-matter resources of a document root
// by uuid.
func backMatterResources(root map[string]any) map[string]resource {
	bm := asMap(root["back-matter"])
	list, _ := bm["resources"].([]any)
	out := make(map[string]resource, len(list))
	for _, item := range list {
		rm := asMap(item)
		if rm == nil {
			continue
		}
		id := strings.TrimSpace(asString(rm["uuid"]))
		if id == "" {
			continue
		}
		res := resource{title: strings.TrimSpace(asString(rm["title"]))}
		if cit := asMap(rm["citation"]); cit != nil {
			res.citation = strings.TrimSpace(asString(cit["text"]))
		}
		if rlinks, ok := rm["rlinks"].([]any); ok {
			for _, rl := range rlinks {
				if href := strings.TrimSpace(asString(asMap(rl)["href"])); href != "" {
					res.url = href
					break
				}
			}
		}
		out[id] = res
	}
	return out
}

// resolveReferences fills title, citation and url of every reference whose
// uuid is a known back-matter resource. Unresolved uuids keep uuid and text.
func resolveReferences(controls []Control, resources map[string]resource) {
	if len(resources) == 0 {
		return
	}
	for i := range controls {
		for j := range controls[i].References {
			ref := &controls[i].References[j]
			if res, ok := resources[ref.UUID]; ok && ref.UUID != "" {
				ref.Title, ref.Citation = res.title, res.citation
				if ref.URL == "" {
					ref.URL = res.url
				}
			}
		}
	}
}

// appendUnique appends each of add to dst unless already present.
func appendUnique(dst []string, add []string) []string {
	for _, s := range add {
		dup := false
		for _, d := range dst {
			if d == s {
				dup = true
				break
			}
		}
		if !dup {
			dst = append(dst, s)
		}
	}
	return dst
}

// isTaskControl reports whether a nested control is an SSDF-style "task":
// class == "task" (case-insensitive) or a prop role=task. Tasks are folded
// into their parent practice instead of becoming controls of their own.
func isTaskControl(cm map[string]any) bool {
	if strings.EqualFold(strings.TrimSpace(asString(cm["class"])), "task") {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(propValue(cm["props"], "role")), "task")
}

// foldTask merges a task control into its parent: the task statement becomes
// an item of the parent's statement (created as "<parentId>_smt" when
// missing), the task's example parts are collected under an "examples" part
// the task's rel=related links join the parent's Related list and its
// external references join the parent's References (tagged with the task id).
func foldTask(parent *Control, tm map[string]any, scope paramScope) {
	taskID := asString(tm["id"])
	if taskID == "" {
		return
	}
	label := propValue(tm["props"], "label")
	if label == "" {
		label = taskID
	}
	item := Part{ID: taskID, Name: "item", Label: label}
	var examples []Part
	rawParts, _ := tm["parts"].([]any)
	for _, rp := range rawParts {
		pm := asMap(rp)
		if pm == nil {
			continue
		}
		p := partsOf([]any{rp}, scope)[0]
		switch p.Name {
		case "statement":
			if item.Prose == "" {
				item.Prose = p.Prose
			} else {
				item.Prose += " " + p.Prose
			}
			if len(p.Parts) > 0 {
				item.Parts = append(item.Parts, p.Parts...)
			}
		case "example":
			// Examples carry a title ("Example 1:") instead of a label prop.
			if p.Label == "" {
				p.Label = strings.TrimSpace(asString(pm["title"]))
			}
			examples = append(examples, p)
		}
	}
	if item.Prose == "" {
		item.Prose = asString(tm["title"])
	}

	stmtIdx := -1
	for i := range parent.Parts {
		if parent.Parts[i].Name == "statement" {
			stmtIdx = i
			break
		}
	}
	if stmtIdx < 0 {
		parent.Parts = append(parent.Parts, Part{ID: parent.ID + "_smt", Name: "statement"})
		stmtIdx = len(parent.Parts) - 1
	}
	parent.Parts[stmtIdx].Parts = append(parent.Parts[stmtIdx].Parts, item)

	if len(examples) > 0 {
		parent.Parts = append(parent.Parts, Part{ID: taskID + "_ex", Name: "examples", Label: label, Parts: examples})
	}
	parent.Related = appendUnique(parent.Related, relatedOf(tm["links"], "related"))
	parent.References = appendUniqueRefs(parent.References, referencesOf(tm["links"], taskID))

	var text []string
	if parent.Text != "" {
		text = append(text, parent.Text)
	}
	text = append(text, taskID)
	if item.Prose != "" {
		text = append(text, item.Prose)
	}
	for _, ex := range examples {
		if ex.Prose != "" {
			text = append(text, ex.Prose)
		}
	}
	parent.Text = strings.Join(text, " ")
}

// controlsOf flattens the controls under v (a catalog, group or control),
// threading the parameter scope down so enhancements can reference their
// parent control's parameters. Nested "task" controls (SSDF) are folded into
// their parent instead of being emitted; group-level parts such as the SSDF
// "overview" are ignored.
func controlsOf(v any, scope paramScope, out *[]Control) {
	item := asMap(v)
	if item == nil {
		return
	}
	scope = scope.extend(paramsOf(item["params"]))
	if list, ok := item["controls"].([]any); ok {
		for _, c := range list {
			cm := asMap(c)
			if cm == nil {
				continue
			}
			params := paramsOf(cm["params"])
			cs := scope.extend(params)
			id, title := asString(cm["id"]), asString(cm["title"])
			if id == "" || title == "" {
				controlsOf(cm, cs, out)
				continue
			}
			*out = append(*out, Control{
				ID:      id,
				Title:   resolveParams(title, cs, 0),
				Text:    resolveParams(directText(cm), cs, 0),
				Parts:   partsOf(cm["parts"], cs),
				Params:  params,
				Related: relatedOf(cm["links"], "related"),
				// (uuid, text, taskId="") dedupe for the control's own links.
				References: appendUniqueRefs(nil, referencesOf(cm["links"], "")),
				Raw:        cm,
			})
			// Fold task children into the control just emitted; recurse into
			// the remaining nested controls (e.g. 800-53 enhancements) as
			// separate controls. Folding never appends to out, so the parent
			// pointer stays valid until the recursion below.
			parent := &(*out)[len(*out)-1]
			var rest []any
			if nested, ok := cm["controls"].([]any); ok {
				for _, n := range nested {
					if nm := asMap(n); nm != nil && isTaskControl(nm) {
						foldTask(parent, nm, cs.extend(paramsOf(nm["params"])))
						continue
					}
					rest = append(rest, n)
				}
			}
			if len(rest) > 0 {
				controlsOf(map[string]any{"controls": rest}, cs, out)
			}
		}
	}
	if groups, ok := item["groups"].([]any); ok {
		for _, g := range groups {
			controlsOf(g, scope, out)
		}
	}
}

func profileSelections(v any) []ControlSelection {
	var out []ControlSelection
	for _, raw := range asSlice(v) {
		m := asMap(raw)
		if m == nil {
			continue
		}
		s := ControlSelection{WithIDs: stringsOf(m["with-ids"]), WithChildControls: strings.EqualFold(asString(m["with-child-controls"]), "yes")}
		for _, match := range asSlice(m["matching"]) {
			if pattern := asString(asMap(match)["pattern"]); pattern != "" {
				s.Matching = append(s.Matching, pattern)
			}
		}
		out = append(out, s)
	}
	return out
}

func profileImportsOf(raw map[string]any) []ProfileImport {
	var out []ProfileImport
	for _, value := range asSlice(raw["imports"]) {
		m := asMap(value)
		if m == nil {
			continue
		}
		_, includeAll := m["include-all"]
		out = append(out, ProfileImport{
			Href:            strings.TrimSpace(asString(m["href"])),
			IncludeAll:      includeAll,
			IncludeControls: profileSelections(m["include-controls"]),
			ExcludeControls: profileSelections(m["exclude-controls"]),
		})
	}
	return out
}

func asSlice(v any) []any {
	out, _ := v.([]any)
	return out
}

func profileRefID(href string) string {
	href = strings.TrimSpace(href)
	if parsed, err := url.Parse(href); err == nil && parsed.Fragment != "" {
		return parsed.Fragment
	}
	return strings.TrimPrefix(href, "#")
}

func catalogChildMap(raw map[string]any) map[string][]string {
	children := map[string][]string{}
	var walk func(map[string]any, string)
	walk = func(node map[string]any, parent string) {
		for _, value := range asSlice(node["controls"]) {
			control := asMap(value)
			if control == nil {
				continue
			}
			id := asString(control["id"])
			if parent != "" && id != "" {
				children[parent] = append(children[parent], id)
			}
			walk(control, id)
		}
		for _, value := range asSlice(node["groups"]) {
			if group := asMap(value); group != nil {
				walk(group, parent)
			}
		}
	}
	root := asMap(raw["catalog"])
	walk(root, "")
	return children
}

func addDescendants(selected map[string]bool, children map[string][]string, id string) {
	for _, child := range children[id] {
		if selected[child] {
			continue
		}
		selected[child] = true
		addDescendants(selected, children, child)
	}
}

func applyProfileSelections(dst map[string]bool, selections []ControlSelection, controls []Control, children map[string][]string) error {
	for _, selection := range selections {
		var patterns []*regexp.Regexp
		for _, raw := range selection.Matching {
			pattern, err := regexp.Compile(raw)
			if err != nil {
				return fmt.Errorf("invalid profile control pattern %q: %w", raw, err)
			}
			patterns = append(patterns, pattern)
		}
		for _, control := range controls {
			matched := false
			for _, id := range selection.WithIDs {
				if control.ID == id {
					matched = true
					break
				}
			}
			if !matched {
				for _, pattern := range patterns {
					if pattern.MatchString(control.ID) {
						matched = true
						break
					}
				}
			}
			if matched {
				dst[control.ID] = true
				if selection.WithChildControls {
					addDescendants(dst, children, control.ID)
				}
			}
		}
	}
	return nil
}

// ResolveProfile resolves a parsed profile against available parsed catalogs.
// Imports are matched by catalog UUID, including UUID URL fragments. The
// returned document keeps the profile identity and contains selected catalog
// controls in stable import/catalog order.
func ResolveProfile(profile Document, catalogs []Document) (Document, error) {
	if profile.Type != "profile" || len(profile.ProfileImports) == 0 {
		return Document{}, errors.New("profile has no resolvable imports")
	}
	resolved := profile
	resolved.Controls = []Control{}
	seen := map[string]bool{}
	for _, imp := range profile.ProfileImports {
		refID := profileRefID(imp.Href)
		var catalog *Document
		for i := range catalogs {
			if catalogs[i].Type == "catalog" && catalogs[i].ID == refID {
				if catalog != nil {
					return Document{}, fmt.Errorf("profile import %q matches multiple catalogs", imp.Href)
				}
				catalog = &catalogs[i]
			}
		}
		if catalog == nil {
			return Document{}, fmt.Errorf("profile import %q requires an available catalog", imp.Href)
		}
		children := catalogChildMap(catalog.Raw)
		selected := map[string]bool{}
		if imp.IncludeAll {
			for _, control := range catalog.Controls {
				selected[control.ID] = true
			}
		} else if len(imp.IncludeControls) > 0 {
			if err := applyProfileSelections(selected, imp.IncludeControls, catalog.Controls, children); err != nil {
				return Document{}, err
			}
		} else {
			return Document{}, fmt.Errorf("profile import %q has no supported include selection", imp.Href)
		}
		excluded := map[string]bool{}
		if err := applyProfileSelections(excluded, imp.ExcludeControls, catalog.Controls, children); err != nil {
			return Document{}, err
		}
		for _, control := range catalog.Controls {
			key := catalog.ID + "\x00" + control.ID
			if selected[control.ID] && !excluded[control.ID] && !seen[key] {
				resolved.Controls = append(resolved.Controls, control)
				seen[key] = true
			}
		}
	}
	if len(resolved.Controls) == 0 {
		return Document{}, errors.New("profile resolved to zero controls")
	}
	return resolved, nil
}

// Parse accepts one OSCAL document object or an array of them.
func Parse(input any) ([]Document, error) {
	var inputs []any
	switch t := input.(type) {
	case []any:
		inputs = t
	default:
		inputs = []any{input}
	}
	if len(inputs) == 0 {
		return nil, errors.New("OSCAL source is empty")
	}
	docs := make([]Document, 0, len(inputs))
	for _, v := range inputs {
		container := asMap(v)
		if container == nil {
			return nil, errors.New("OSCAL document must be an object")
		}
		var typ string
		var raw map[string]any
		for _, candidate := range DocumentTypes {
			if m := asMap(container[candidate]); m != nil {
				typ, raw = candidate, m
				break
			}
		}
		metadata := asMap(raw["metadata"])
		id := asString(raw["uuid"])
		title := asString(metadata["title"])
		if typ == "" || raw == nil || metadata == nil || id == "" || title == "" {
			return nil, errors.New("OSCAL document requires a typed root, uuid, and metadata.title")
		}
		doc := Document{ID: id, Type: typ, Title: title, LastModified: asString(metadata["last-modified"]), Version: asString(metadata["version"]), Controls: []Control{}, Raw: container}
		if typ == "catalog" {
			controlsOf(raw, paramScope{}, &doc.Controls)
			resolveReferences(doc.Controls, backMatterResources(raw))
		} else if typ == "profile" {
			doc.ProfileImports = profileImportsOf(raw)
		}
		docs = append(docs, doc)
	}
	return docs, nil
}

// ParseJSON decodes JSON bytes and parses them.
func ParseJSON(data []byte) ([]Document, error) {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("OSCAL source is not valid JSON: %w", err)
	}
	return Parse(v)
}

// HTTPRepository fetches documents from the configured HTTPS sources. Each
// source is fetched sequentially with the same client, timeout and size limit;
// the documents are concatenated in source order. Any failing source fails the
// whole listing (fail closed). It never follows redirects and never logs
// document bodies.
type HTTPRepository struct {
	SourceURLs []string
	Client     *http.Client
	MaxBytes   int64
}

// NewHTTPRepository builds a repository with a non-redirecting client.
func NewHTTPRepository(sourceURLs ...string) *HTTPRepository {
	return &HTTPRepository{SourceURLs: append([]string(nil), sourceURLs...), MaxBytes: 64 << 20, Client: &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("redirects are not followed for the OSCAL source")
		},
	}}
}

// Sources returns configured URLs in operator order.
func (r *HTTPRepository) Sources() []string { return append([]string(nil), r.SourceURLs...) }

// List implements Repository.
func (r *HTTPRepository) List(ctx context.Context) ([]Document, error) {
	if len(r.SourceURLs) == 0 {
		return nil, errors.New("no OSCAL source configured")
	}
	var all []Document
	for _, u := range r.SourceURLs {
		docs, err := r.FetchSource(ctx, u)
		if err != nil {
			return nil, err
		}
		all = append(all, docs...)
	}
	return all, nil
}

// FetchSource downloads and parses one configured source independently.
func (r *HTTPRepository) FetchSource(ctx context.Context, sourceURL string) ([]Document, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := r.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("OSCAL source request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OSCAL source request failed: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, r.MaxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > r.MaxBytes {
		return nil, errors.New("OSCAL source exceeds the size limit")
	}
	return ParseJSON(body)
}

// StaticRepository serves fixed documents (tests, fixtures).
type StaticRepository struct{ Documents []Document }

// List implements Repository.
func (s StaticRepository) List(context.Context) ([]Document, error) { return s.Documents, nil }

// FailingRepository always errors (tests).
type FailingRepository struct{ Err error }

// List implements Repository.
func (f FailingRepository) List(context.Context) ([]Document, error) { return nil, f.Err }
