import { useEffect, useMemo, useState, type FormEvent } from 'react'
import { Link, useParams } from 'react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ChevronDown, ChevronRight, Paperclip } from 'lucide-react'
import { api } from '@/api'
import {
  REQUIREMENT_STATUSES,
  type AuditEntry,
  type Evidence,
  type Framework,
  type Part,
  type Reference,
  type Requirement,
  type RequirementAssessmentRecord,
  type RequirementDetail,
  type RequirementPatch,
  type RequirementStatus,
  type TrackablePart,
} from '@/types'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Label } from '@/components/ui/label'
import { Select } from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'
import { Badge, StatusBadge } from '@/components/ui/badge'
import { EmptyState, ErrorState, LoadingState, Spinner } from '@/components/ui/feedback'
import { EvidencePrimaryAction, KindIcon } from '@/components/EvidenceLink'
import { OscalProse } from '@/components/OscalProse'
import { PartBlock } from '@/components/PartTracking'
import { UploadEvidenceDialog } from '@/components/UploadEvidenceDialog'
import { STATUS_LABEL, cn, controlGroup, formatBytes, formatDate, formatDateTime, isOverdue, relativeTime, urlHost } from '@/lib/utils'
import { formatEvidenceTitle } from '@/lib/evidenceTitle'

// ---------------------------------------------------------------------------
// Part helpers
// ---------------------------------------------------------------------------

function partsNamed(parts: Part[] | undefined, name: string): Part[] {
  return (parts ?? []).filter((p) => p.name === name)
}

function hasProse(parts: Part[] | undefined): boolean {
  return (parts ?? []).some((p) => !!p.prose || hasProse(p.parts))
}

/** For section parts (guidance, objectives): own prose first (label-less), then their nested items. */
function sectionItems(parts: Part[]): Part[] {
  const own: Part[] = parts.filter((p) => !!p.prose).map((p) => ({ name: p.name, id: p.id, prose: p.prose, rawProse: p.rawProse }))
  const nested: Part[] = parts.flatMap((p) => p.parts ?? [])
  return [...own, ...nested]
}

/**
 * Fallback when the server sends no trackable parts (e.g. older API): one synthetic, read-only part per
 * statement item so the prose is still shown. Tracking rows on such parts would 404, so they are not rendered.
 */
function fallbackParts(r: RequirementDetail): TrackablePart[] {
  const statements = partsNamed(r.parts, 'statement')
  const items: Part[] = []
  for (const st of statements) {
    if (st.parts && st.parts.length > 0) items.push(...st.parts)
    else items.push(st)
  }
  return items.map((p, i) => ({
    partId: p.id ?? `${r.controlId}_smt.${i}`,
    label: p.label,
    prose: p.prose ?? '',
    scopes: [],
    status: 'planned',
    owner: '',
    description: '',
  }))
}

// ---------------------------------------------------------------------------
// Page
// ---------------------------------------------------------------------------

export function RequirementDetailPage() {
  const { id = '' } = useParams<{ id: string }>()
  const q = useQuery({ queryKey: ['requirement', id], queryFn: () => api.getRequirement(id), enabled: !!id })
  const frameworks = useQuery({ queryKey: ['frameworks'], queryFn: api.listFrameworks })
  const assessmentRecords = useQuery({ queryKey: ['requirement-assessments', id], queryFn: () => api.listRequirementAssessments(id), enabled: !!id })

  if (q.isPending) return <LoadingState label="Loading control…" />
  if (q.isError) return <ErrorState error={q.error} />
  const r = q.data
  const framework = frameworks.data?.items.find((f) => f.id === r.frameworkId)
  const group = controlGroup(r.controlId)
  const controlId = r.controlId.toUpperCase()
  const evidenceCount = (r.evidence ?? []).length

  return (
    <div className="space-y-6">
      <div>
        <nav aria-label="Breadcrumb" className="flex flex-wrap items-center gap-1 text-xs text-slate-500">
          <Link to={framework ? `/frameworks/${encodeURIComponent(framework.id)}` : '/requirements'} className="hover:text-slate-900 hover:underline">
            {framework?.shortName ?? framework?.title ?? 'Requirements'}
          </Link>
          {group && (
            <>
              <span aria-hidden>›</span>
              <Link to={`${framework ? `/frameworks/${encodeURIComponent(framework.id)}` : '/requirements'}?q=${encodeURIComponent(group.toLowerCase())}`} className="hover:text-slate-900 hover:underline">
                {group}
              </Link>
            </>
          )}
          <span aria-hidden>›</span>
          <span className="font-mono text-slate-700" aria-current="page">{controlId}</span>
        </nav>
        <div className="mt-1 min-w-0">
          <h1 className="flex flex-wrap items-center gap-2 text-2xl font-semibold tracking-tight">
            <span>
              <span className="font-mono">{controlId}</span> {r.title}
            </span>
            <StatusBadge status={r.status} />
            {r.statusOverride && <Badge variant="outline" title={`Derived status: ${STATUS_LABEL[r.derivedStatus] ?? r.derivedStatus}`}>override</Badge>}
            {isOverdue(r.dueDate, r.status) && <Badge variant="danger">Overdue</Badge>}
          </h1>
          <p className="mt-1 text-sm text-slate-500">
            Owner {r.owner || <span className="text-slate-400">unassigned</span>} · Due {formatDate(r.dueDate)} · Updated {formatDate(r.updatedAt)} · {evidenceCount} evidence
          </p>
        </div>
      </div>

      <div className="grid gap-6 lg:grid-cols-[minmax(0,1.6fr)_minmax(0,1fr)]">
        <div className="space-y-6">
          <ImplementationCard requirement={r} />
          <ControlCard requirement={r} framework={framework} />
        </div>
        <div className="space-y-6">
          <TrackingCard requirement={r} />
        </div>
      </div>

      <AssessmentRecordsCard query={assessmentRecords} requirement={r} />
      <AuditCard requirement={r} />
    </div>
  )
}

function assessmentTab(type: RequirementAssessmentRecord['type']): string {
  if (type === 'assessment-results') return 'results'
  if (type === 'plan-of-action-and-milestones') return 'poam'
  return 'plan'
}

function AssessmentRecordsCard({ query, requirement }: {
  query: { isPending: boolean; isError: boolean; error: unknown; data?: { items: RequirementAssessmentRecord[] } }
  requirement: RequirementDetail
}) {
  const groups = useMemo(() => {
    const grouped = new Map<string, RequirementAssessmentRecord[]>()
    for (const item of query.data?.items ?? []) {
      const partLabel = requirement.trackableParts.find((part) => part.partId === item.partId)?.label
      const key = item.partLabel || partLabel || item.partId || 'Whole control'
      grouped.set(key, [...(grouped.get(key) ?? []), item])
    }
    return [...grouped.entries()]
  }, [query.data, requirement.trackableParts])
  return (
    <Card>
      <CardHeader>
        <CardTitle>Assessment records</CardTitle>
        <CardDescription>Assessment records do not change implementation status.</CardDescription>
      </CardHeader>
      <CardContent>
        {query.isPending && <LoadingState label="Loading assessment records…" />}
        {query.isError && <ErrorState error={query.error} />}
        {!query.isPending && !query.isError && groups.length === 0 && <p className="text-sm text-slate-500">No assessment records are mapped to this control.</p>}
        <div className="space-y-4">
          {groups.map(([part, items]) => (
            <section key={part}>
              <h3 className="mb-1 text-xs font-semibold uppercase tracking-wide text-slate-500">{part}</h3>
              <ul className="divide-y divide-slate-100 rounded-md border border-slate-200">
                {items.map((item, index) => (
                  <li key={`${item.uploadId}-${item.itemKey ?? index}`} className="flex flex-wrap items-center justify-between gap-3 px-3 py-2">
                    <div className="min-w-0">
                      <div className="flex flex-wrap items-center gap-1.5"><Badge variant="outline">{item.type}</Badge>{(item.status || item.risk || item.severity) && <Badge variant="secondary">{item.status || item.risk || item.severity}</Badge>}</div>
                      <p className="mt-1 text-sm text-slate-700">{item.summary || item.title || 'Assessment record'}</p>
                    </div>
                    <Link className="text-sm font-medium underline decoration-slate-300 underline-offset-2" to={`/assessment?tab=${assessmentTab(item.type)}&uploadId=${encodeURIComponent(item.uploadId)}`}>View in Assessment</Link>
                  </li>
                ))}
              </ul>
            </section>
          ))}
        </div>
      </CardContent>
    </Card>
  )
}

// ---------------------------------------------------------------------------
// Control guidance (catalog)
// ---------------------------------------------------------------------------

function PartList({ parts, depth = 0, compact = false }: { parts: Part[]; depth?: number; compact?: boolean }) {
  return (
    <ol className={cn('space-y-1.5', depth > 0 && 'mt-1.5 ml-5')}>
      {parts.map((p, i) => (
        <li key={p.id ?? `${p.name}-${i}`} className={cn('text-slate-800', compact ? 'text-xs leading-relaxed' : 'text-sm leading-relaxed')}>
          {(p.label || p.prose) && (
            <p>
              {p.label && <span className="mr-1.5 font-semibold text-slate-500">{p.label}</span>}
              <OscalProse prose={p.prose} rawProse={p.rawProse} />
            </p>
          )}
          {p.parts && p.parts.length > 0 && <PartList parts={p.parts} depth={depth + 1} compact={compact} />}
        </li>
      ))}
    </ol>
  )
}

function Collapsible({ title, count, children, defaultOpen = false }: { title: string; count?: number; children: React.ReactNode; defaultOpen?: boolean }) {
  const [open, setOpen] = useState(defaultOpen)
  return (
    <div className="border-t border-slate-100 pt-3 first:border-t-0 first:pt-0">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
        className="flex w-full items-center gap-1.5 text-left text-sm font-medium text-slate-700 hover:text-slate-900"
      >
        {open ? <ChevronDown className="size-4" aria-hidden /> : <ChevronRight className="size-4" aria-hidden />}
        {title}
        {count !== undefined && <span className="text-xs font-normal text-slate-400">({count})</span>}
      </button>
      {open && <div className="mt-2 pl-5">{children}</div>}
    </div>
  )
}

/** Catalog material other than the statement (which lives in the Implementation card). */
function ControlCard({ requirement: r, framework }: { requirement: RequirementDetail; framework?: Framework }) {
  const parts = r.parts ?? []
  const guidance = partsNamed(parts, 'guidance')
  const objectives = partsNamed(parts, 'assessment-objective')
  const methods = partsNamed(parts, 'assessment-method')
  const related = r.related ?? []
  const references = r.references ?? []
  const empty = !hasProse(guidance) && !hasProse(objectives) && !hasProse(methods) && related.length === 0 && references.length === 0

  return (
    <Card>
      <CardHeader>
        <CardTitle>Control guidance</CardTitle>
        <CardDescription>Catalog guidance, assessment details, related controls, and references.</CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        {empty && <p className="text-sm text-slate-500">No guidance in the catalog for this control.</p>}
        {hasProse(guidance) && (
          <Collapsible title="Guidance" defaultOpen>
            <PartList parts={sectionItems(guidance)} />
          </Collapsible>
        )}
        {hasProse(objectives) && (
          <Collapsible title="Assessment objectives">
            <PartList parts={sectionItems(objectives)} compact />
          </Collapsible>
        )}
        {hasProse(methods) && (
          <Collapsible title="Assessment methods">
            <PartList parts={methods} compact />
          </Collapsible>
        )}
        {related.length > 0 && (
          <Collapsible title="Related controls" count={related.length} defaultOpen>
            <div className="flex flex-wrap gap-1.5">
              {related.map((cid) => (
                <RelatedChip key={cid} controlId={cid} frameworkId={framework?.id ?? r.frameworkId} />
              ))}
            </div>
          </Collapsible>
        )}
        {references.length > 0 && (
          <Collapsible title="References" count={references.length}>
            <ReferenceList references={references} />
          </Collapsible>
        )}
      </CardContent>
    </Card>
  )
}

/** External references grouped by the folded task that carried them (or "Practice" / the control itself). */
function ReferenceList({ references }: { references: Reference[] }) {
  const groups = useMemo(() => {
    const byTask = new Map<string, Reference[]>()
    for (const ref of references) {
      const key = ref.taskId ?? ''
      const list = byTask.get(key)
      if (list) list.push(ref)
      else byTask.set(key, [ref])
    }
    return Array.from(byTask.entries())
  }, [references])
  return (
    <div className="space-y-3">
      {groups.map(([taskId, refs]) => (
        <div key={taskId || '_own'}>
          {groups.length > 1 || taskId ? (
            <p className="mb-1 font-mono text-xs font-semibold text-slate-500">{taskId || 'Practice'}</p>
          ) : null}
          <ul className="space-y-1">
            {refs.map((ref, i) => (
              <ReferenceItem key={`${ref.uuid}|${ref.text ?? ''}|${i}`} reference={ref} />
            ))}
          </ul>
        </div>
      ))}
    </div>
  )
}

function ReferenceItem({ reference: ref }: { reference: Reference }) {
  const title = ref.title || ref.url || ref.uuid || 'Unresolved reference'
  return (
    <li className="text-sm leading-relaxed text-slate-800">
      <span className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
        {ref.url ? (
          <a href={ref.url} target="_blank" rel="noopener noreferrer" className="font-medium text-slate-900 underline decoration-slate-300 underline-offset-2 hover:decoration-slate-700">
            {title}
          </a>
        ) : (
          <span className="font-medium text-slate-900">{title}</span>
        )}
        {ref.text && <span className="rounded-md border border-slate-300 bg-white px-1.5 py-0.5 font-mono text-xs text-slate-700">{ref.text}</span>}
      </span>
      {ref.citation && <p className="text-xs text-slate-500">{ref.citation}</p>}
    </li>
  )
}

/** Chip for a related control; links to the matching requirement in the same framework when one exists. */
function RelatedChip({ controlId, frameworkId }: { controlId: string; frameworkId: string }) {
  const q = useQuery({
    queryKey: ['requirements', 'lookup', frameworkId, controlId.toLowerCase()],
    queryFn: () => api.listRequirements({ frameworkId, q: controlId, limit: 5 }),
    staleTime: 5 * 60_000,
  })
  const match = q.data?.items.find((x) => x.controlId.toLowerCase() === controlId.toLowerCase())
  const label = controlId.toUpperCase()
  if (match) {
    return (
      <Link to={`/requirements/${encodeURIComponent(match.id)}`} title={match.title} className="rounded-md border border-slate-300 bg-white px-2 py-0.5 font-mono text-xs text-slate-800 hover:bg-slate-100">
        {label}
      </Link>
    )
  }
  return (
    <span className="rounded-md border border-transparent bg-slate-100 px-2 py-0.5 font-mono text-xs text-slate-600" title={q.isPending ? 'Resolving…' : 'Not imported in this framework'}>
      {label}
    </span>
  )
}

// ---------------------------------------------------------------------------
// Implementation: statement part › tracking row › description, then evidence
// ---------------------------------------------------------------------------

function EvidenceItem({ evidence: e }: { evidence: Evidence }) {
  return (
    <li className="flex items-start justify-between gap-2 py-1.5">
      <div className="min-w-0">
        <div className="flex min-w-0 items-center gap-1.5 text-sm">
          <KindIcon kind={e.kind} className="size-3.5" />
          <span className="truncate font-medium text-slate-800" title={formatEvidenceTitle(e.title)}>{formatEvidenceTitle(e.title)}</span>
        </div>
        <p className="truncate text-xs text-slate-500">
          {e.kind === 'link' ? (
            <a href={e.url} target="_blank" rel="noopener noreferrer" className="hover:underline" title={e.url}>{urlHost(e.url)}</a>
          ) : (
            <>{e.fileName} · {formatBytes(e.sizeBytes)}</>
          )}
          {e.validUntil && <> · valid until {formatDate(e.validUntil)}</>}
        </p>
        {e.description && <p className="text-xs text-slate-600">{e.description}</p>}
      </div>
      <EvidencePrimaryAction evidence={e} size="icon" />
    </li>
  )
}

/** Requirement-level evidence list with a single "Add evidence" action bound to this requirement. */
function EvidenceSection({ requirement: r }: { requirement: RequirementDetail }) {
  const [addOpen, setAddOpen] = useState(false)
  const evidence = r.evidence ?? []
  return (
    <section aria-labelledby="evidence-heading" className="pt-4">
      <div className="flex items-center justify-between gap-2">
        <h3 id="evidence-heading" className="text-sm font-medium text-slate-700">
          Evidence <span className="text-xs font-normal text-slate-400">({evidence.length})</span>
        </h3>
        <Button variant="outline" size="sm" onClick={() => setAddOpen(true)}>
          <Paperclip className="size-3.5" /> Add evidence
        </Button>
      </div>
      {evidence.length === 0 ? (
        <p className="py-2 text-xs text-slate-400">No evidence yet.</p>
      ) : (
        <ul className="mt-1 divide-y divide-slate-100">
          {evidence.map((e) => (
            <EvidenceItem key={e.id} evidence={e} />
          ))}
        </ul>
      )}
      <UploadEvidenceDialog
        open={addOpen}
        onClose={() => setAddOpen(false)}
        initialRequirements={[{ id: r.id, controlId: r.controlId, title: r.title }]}
        lockRequirements
      />
    </section>
  )
}

function ImplementationCard({ requirement: r }: { requirement: RequirementDetail }) {
  const hasTrackable = Array.isArray(r.trackableParts) && r.trackableParts.length > 0
  const parts = useMemo(() => (hasTrackable ? r.trackableParts : fallbackParts(r)), [hasTrackable, r])

  return (
    <Card>
      <CardHeader>
        <CardTitle>Implementation</CardTitle>
        <CardDescription>
          Track each statement part separately. Status, owner, and due date save automatically. Add implementation details in Markdown. Evidence applies to the full control.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <div className="divide-y divide-slate-200">
          {parts.length === 0 && <p className="py-2 text-sm text-slate-500">This control has no statement parts to track.</p>}
          {hasTrackable
            ? parts.map((p) => <PartBlock key={p.partId} requirementId={r.id} part={p} />)
            : parts.map((p) => (
                <section key={p.partId} className="py-4 first:pt-0 last:pb-0">
                  <p className="flex items-start gap-2 text-sm leading-relaxed text-slate-800">
                    <span className="mt-0.5 shrink-0 rounded-md bg-slate-100 px-1.5 py-0.5 font-mono text-[11px] font-semibold text-slate-600">{p.label || 'Statement'}</span>
                    <span className="min-w-0 flex-1"><OscalProse prose={p.prose} /></span>
                  </p>
                  <p className="mt-1 ml-3 text-xs text-slate-400">Per-part tracking is not available from this API version.</p>
                </section>
              ))}
          <EvidenceSection requirement={r} />
        </div>
      </CardContent>
    </Card>
  )
}

// ---------------------------------------------------------------------------
// History: audit log entries for this requirement
// ---------------------------------------------------------------------------

function concernsRequirement(a: AuditEntry, id: string): boolean {
  if (a.entityId === id) return true
  const d = a.detail ?? {}
  if (d.requirementId === id) return true
  const t = a.entityType.toLowerCase()
  if (t === 'evidence') {
    const ids = d.requirementIds
    if (Array.isArray(ids) && ids.includes(id)) return true
  }
  return false
}

function detailSummary(detail: Record<string, unknown>): string {
  const skip = new Set(['requirementId', 'requirementIds', 'id', 'partId'])
  const bits: string[] = []
  for (const [k, v] of Object.entries(detail ?? {})) {
    if (skip.has(k)) continue
    if (v === null || v === undefined) continue
    if (typeof v === 'object') continue
    bits.push(`${k} → ${String(v)}`)
    if (bits.length >= 3) break
  }
  return bits.join(' · ')
}

/** "part a. updated" for `part.update` rows, using the requirement's trackable parts for the label. */
function partActionLabel(a: AuditEntry, labels: Map<string, string>): string | null {
  const partId = typeof a.detail?.partId === 'string' ? a.detail.partId : null
  if (!partId) return null
  const label = labels.get(partId)
  const verb = a.action.endsWith('.update') ? 'updated' : a.action.split('.').pop() ?? 'changed'
  return label ? `part ${label} ${verb}` : `part ${partId} ${verb}`
}

function AuditCard({ requirement: r }: { requirement: RequirementDetail }) {
  const requirementId = r.id
  const q = useQuery({ queryKey: ['audit', 200], queryFn: () => api.audit(200) })
  const entries = useMemo(() => (q.data?.items ?? []).filter((a) => concernsRequirement(a, requirementId)), [q.data, requirementId])
  const partLabels = useMemo(() => {
    const m = new Map<string, string>()
    for (const p of r.trackableParts ?? []) m.set(p.partId, p.label || 'statement')
    return m
  }, [r.trackableParts])

  return (
    <Card>
      <CardHeader>
        <CardTitle>History</CardTitle>
        <CardDescription>Audit entries for this control, its statement parts, and its evidence. The list checks the latest 200 entries.</CardDescription>
      </CardHeader>
      <CardContent>
        {q.isPending && <LoadingState />}
        {q.isError && <ErrorState error={q.error} />}
        {q.data && entries.length === 0 && <EmptyState>No audit entries for this control yet.</EmptyState>}
        {entries.length > 0 && (
          <ul className="divide-y divide-slate-100">
            {entries.map((a) => {
              const summary = detailSummary(a.detail)
              const partLabel = partActionLabel(a, partLabels)
              return (
                <li key={a.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 py-2 text-sm">
                  <time className="w-24 shrink-0 text-xs text-slate-500" dateTime={a.at} title={formatDateTime(a.at)}>
                    {relativeTime(a.at)}
                  </time>
                  <span className="w-28 shrink-0 truncate font-medium text-slate-700">{a.actor || 'system'}</span>
                  <Badge variant="outline" className="font-mono">{a.action}</Badge>
                  {partLabel && <span className="text-xs font-medium text-slate-700">{partLabel}</span>}
                  {summary && <span className="truncate text-xs text-slate-500">{summary}</span>}
                </li>
              )
            })}
          </ul>
        )}
      </CardContent>
    </Card>
  )
}

// ---------------------------------------------------------------------------
// Tracking (roll-up): effective status + override, derived owner/due, notes
// ---------------------------------------------------------------------------

function useRequirementPatch(r: RequirementDetail) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (patch: RequirementPatch) => api.patchRequirement(r.id, patch),
    onSuccess: (updated: Requirement) => {
      qc.setQueryData<RequirementDetail>(['requirement', r.id], (old) => (old ? { ...old, ...updated } : old))
      qc.invalidateQueries({ queryKey: ['requirements'] })
      qc.invalidateQueries({ queryKey: ['dashboard'] })
      qc.invalidateQueries({ queryKey: ['audit'] })
    },
  })
}

function StatusOverride({ requirement: r }: { requirement: RequirementDetail }) {
  const mutation = useRequirementPatch(r)
  const overridden = !!r.statusOverride
  const [enabled, setEnabled] = useState(overridden)
  const [choice, setChoice] = useState<RequirementStatus>(r.statusOverride ?? r.status)

  useEffect(() => {
    setEnabled(overridden)
    setChoice(r.statusOverride ?? r.status)
  }, [overridden, r.statusOverride, r.status])

  const setOverride = (status: RequirementStatus) => {
    setChoice(status)
    mutation.mutate({ statusOverride: status })
  }
  const clear = () => {
    setEnabled(false)
    if (overridden) mutation.mutate({ statusOverride: null })
  }

  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-xs text-slate-500">Status</span>
        <StatusBadge status={r.status} />
        {overridden && <span className="text-xs text-slate-500">(override)</span>}
        {mutation.isPending && <Spinner className="size-3 text-slate-400" />}
      </div>
      {overridden && (
        <p className="text-xs text-slate-400">
          Derived from parts: <span className="line-through">{STATUS_LABEL[r.derivedStatus] ?? r.derivedStatus}</span>
        </p>
      )}
      <label className="flex items-center gap-2 text-xs text-slate-700">
        <input
          type="checkbox"
          role="switch"
          aria-checked={enabled}
          checked={enabled}
          onChange={(e) => (e.target.checked ? setEnabled(true) : clear())}
          disabled={mutation.isPending}
          className="size-3.5 accent-slate-900"
        />
        Override status
      </label>
      {enabled && (
        <div className="flex items-center gap-2">
          <Select
            aria-label="Override status"
            value={choice}
            onChange={(e) => setOverride(e.target.value as RequirementStatus)}
            className="flex-1 [&>select]:h-8 [&>select]:text-xs"
            disabled={mutation.isPending}
          >
            {REQUIREMENT_STATUSES.map((s) => (
              <option key={s} value={s}>{STATUS_LABEL[s]}</option>
            ))}
          </Select>
          {!overridden && (
            <Button size="sm" onClick={() => setOverride(choice)} disabled={mutation.isPending}>
              Apply
            </Button>
          )}
          {overridden && (
            <Button variant="outline" size="sm" onClick={clear} disabled={mutation.isPending}>
              Clear override
            </Button>
          )}
        </div>
      )}
      {mutation.isError && <ErrorState error={mutation.error} />}
    </div>
  )
}

function NotesForm({ requirement: r }: { requirement: RequirementDetail }) {
  const mutation = useRequirementPatch(r)
  const [notes, setNotes] = useState(r.notes ?? '')
  const [savedAt, setSavedAt] = useState<number | null>(null)
  useEffect(() => setNotes(r.notes ?? ''), [r.id, r.notes])
  const dirty = notes !== (r.notes ?? '')

  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (!dirty) return
    mutation.mutate({ notes }, { onSuccess: () => setSavedAt(Date.now()) })
  }

  return (
    <form onSubmit={submit} className="space-y-1.5">
      <Label htmlFor="rq-notes">Notes</Label>
      <Textarea id="rq-notes" rows={4} value={notes} onChange={(e) => setNotes(e.target.value)} placeholder="Control-level notes…" />
      {mutation.isError && <ErrorState error={mutation.error} />}
      <div className="flex items-center justify-between">
        <span className="text-xs text-slate-500" role="status">
          {mutation.isPending ? 'Saving…' : dirty ? 'Unsaved changes' : savedAt ? 'Saved' : ''}
        </span>
        <div className="flex gap-2">
          <Button variant="outline" size="sm" disabled={!dirty || mutation.isPending} onClick={() => setNotes(r.notes ?? '')}>
            Reset
          </Button>
          <Button type="submit" size="sm" disabled={!dirty || mutation.isPending}>
            {mutation.isPending && <Spinner />} Save
          </Button>
        </div>
      </div>
    </form>
  )
}

function TrackingCard({ requirement: r }: { requirement: RequirementDetail }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Tracking (roll-up)</CardTitle>
        <CardDescription>Status, owner, and due date come from the statement parts under Implementation.</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <StatusOverride requirement={r} />
        <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-sm">
          <dt className="text-xs text-slate-500">Owner</dt>
          <dd className="text-slate-800">{r.owner || <span className="text-slate-400">unassigned</span>}</dd>
          <dt className="text-xs text-slate-500">Due</dt>
          <dd className={cn('text-slate-800', isOverdue(r.dueDate, r.status) && 'font-medium text-red-700')}>{formatDate(r.dueDate)}</dd>
        </dl>
        <p className="-mt-2 text-[11px] text-slate-400">From parts: most common owner and earliest open due date</p>
        <NotesForm requirement={r} />
      </CardContent>
    </Card>
  )
}
