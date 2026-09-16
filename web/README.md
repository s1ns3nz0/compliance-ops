# Compliance Ops web UI

React 19 + TypeScript + Vite frontend for the Compliance Ops dashboard.
The production build is written to `../internal/webui/dist` so the Go binary
can embed it.

## Run

```sh
cd web
npm install
npm run dev        # http://localhost:5173, proxies /v1 and /healthz to http://localhost:3000
npm run typecheck  # tsc -b --noEmit
npm run build      # tsc -b && vite build  -> ../internal/webui/dist
npm run preview    # serve the production build locally
```

Or from the repo root: `npm --prefix web run build`.

Any token from the server's named runtime token set can be entered in the UI
(connect screen) and is kept in `sessionStorage` under `compliance-ops.auth`,
scoped to the current tab and browser session. Token labels are not sent to the
API and do not select an identity. The server enforces token68-safe values of at
least 12 characters; use randomly generated values of at least 43 characters
for production. Audit records use the trusted server-side
`COMPLIANCE_AUDIT_ACTOR`; the UI does not
accept an actor name. A `401` from the API clears the token and returns to the
connect screen.

## Routes

| Path | Screen |
| --- | --- |
| `/` | Dashboard: coverage / implemented / due soon (30d) / overdue stats, per-framework coverage table, upcoming deadlines (soonest first, overdue in red), recent changes (audit) |
| `/frameworks` | Imported frameworks (short name primary, full title muted, inline rename via `PATCH /v1/frameworks/{id}`) + import dialog (`GET /v1/oscal/documents`, `POST /v1/frameworks/import`) |
| `/frameworks/:frameworkId` | Requirements filtered to one framework |
| `/requirements` | Requirements with framework/status/query filters |
| `/requirements/:id` | Control page: Implementation card with one block per statement part (label chip + prose, auto-saving status · owner · due row, Markdown description with Edit / Write · Preview / Save), then a single *Evidence* section with *Add evidence*; Control guidance card (guidance / assessment objectives / related); *Tracking (roll-up)* card (effective status with override toggle, derived owner/due, notes); *History* card with the control's audit entries |
| `/evidence` | Evidence list with kind/query filters, download (file) or external link (link), multi-requirement add |

The server must serve `index.html` for unknown paths (SPA fallback) because the
app uses `BrowserRouter`.

## Structure

- `src/api.ts`: typed API client (`ApiError`, header injection, 401 handling)
- `src/types.ts`: API contract types
- `src/auth.tsx`: auth context backed by tab-scoped `sessionStorage`
- `src/components/ui`: small shadcn-style primitives (button, card, badge, input, select, table, dialog, textarea)
- `src/pages`: one file per screen
- `src/components/EvidenceLink.tsx`: kind icon, external link and download/open action shared by evidence lists
- `src/components/OscalProse.tsx`: renders control prose; server-resolved OSCAL parameters (`[Assignment: …]`, `[Selection…: …]`) get a subtle highlight, with a `raw` toggle when `rawProse` is present
- `src/components/UploadEvidenceDialog.tsx`: adds file or link evidence to one or more requirements (`lockRequirements` pins it to the current control)
- `src/components/Markdown.tsx`: `react-markdown` and `remark-gfm` renderer; raw HTML is skipped (never injected), and links open in a new tab with `rel="noopener noreferrer"`
- `src/components/ImplementationMarkdown.tsx`: shows exact template starter lines in gray until a Markdown section has content; completed sections hide only their starter in display and preview, while the textarea and stored Markdown stay unchanged
- `src/components/PartTracking.tsx`: per-statement-part block with an auto-saving tracking row (`PATCH /v1/requirements/{id}/parts/{partId}`; owner typing debounced 600 ms, select/date saved immediately, per-row *Saving… / Saved ✓ / Failed*) and Markdown description editor (⌘/Ctrl+Enter saves, Esc cancels with a confirmation when dirty). Responses update the `['requirement', id]` query cache directly so the header badge and roll-up update without a refetch

## Contract notes

- Requirement status is the OSCAL implementation-status enum: `implemented`, `partial`, `planned`, `alternative`, `not_applicable`.
- Frameworks carry `shortName`; the UI names frameworks by it everywhere and only shows the full title as secondary text.
- Evidence has `kind: 'file' | 'link'`. File evidence is uploaded as multipart; link evidence is created with a JSON body (`kind`, `url`, …) and opens in a new tab instead of downloading.
- Tracking lives on statement parts. Requirement detail returns `trackableParts[]` (`partId`, `label`, `prose`, `status`, `owner`, `dueDate`, `description` in Markdown, `updatedAt`); `PATCH /v1/requirements/{id}/parts/{partId}` accepts `{status?, owner?, dueDate?: 'YYYY-MM-DD' | null, description?}` and returns `{part, requirement}`.
- Requirement `status` is the effective status: `statusOverride` when set, else `derivedStatus` (rolled up from parts). `owner` and `dueDate` are derived, read-only roll-ups (most common part owner, earliest open part due date). `PATCH /v1/requirements/{id}` accepts only `{notes?, statusOverride?: Status | null}`; sending `status`/`owner`/`dueDate` is a 400.
- There are no implementation activities any more (no `/activities` routes, no `activityIds` on evidence). Evidence is linked to requirements only (`requirementIds`). There is no review workflow (no `reviewState`, no `/review` endpoint).
- Dashboard exposes `upcomingRequirements` (with `daysUntilDue`, `overdue`, `frameworkShortName`) and `dueSoon` counts; `overdueRequirements` / `expiringEvidence` no longer exist.
- The control page's History card has no per-entity audit endpoint; it fetches `GET /v1/audit?limit=200` and filters client-side (entries whose `entityId` or `detail.requirementId` match, or evidence whose `detail.requirementIds` include the control). `part.update` rows are shown as “part a. updated” via a `detail.partId` → label lookup.
