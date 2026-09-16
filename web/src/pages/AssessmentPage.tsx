import { useMemo, useState } from 'react'
import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { FileJson, Link2, Search, Trash2 } from 'lucide-react'
import { Link, useSearchParams } from 'react-router'
import { api } from '@/api'
import type {
  AssessmentDetail,
  AssessmentMapping,
  AssessmentSummary,
  AssessmentType,
  Framework,
  Requirement,
} from '@/types'
import { PageHeader } from '@/components/Layout'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Dialog } from '@/components/ui/dialog'
import { EmptyState, ErrorState, LoadingState, Spinner } from '@/components/ui/feedback'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select } from '@/components/ui/select'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { formatAssessmentTitle } from '@/lib/assessmentTitle'
import { cn, formatDate, formatDateTime } from '@/lib/utils'

type Tab = 'plan' | 'results' | 'poam'

const TABS: { key: Tab; label: string; type: AssessmentType }[] = [
  { key: 'plan', label: 'Plan', type: 'assessment-plan' },
  { key: 'results', label: 'Results', type: 'assessment-results' },
  { key: 'poam', label: 'POA&M', type: 'plan-of-action-and-milestones' },
]

function record(value: unknown): Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {}
}
function records(value: unknown): Record<string, unknown>[] {
  return Array.isArray(value) ? value.map(record).filter((x) => Object.keys(x).length > 0) : []
}
function text(value: unknown): string {
  if (typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean') return String(value)
  if (Array.isArray(value)) return value.map(text).filter(Boolean).join(', ')
  const valueRecord = record(value)
  if (Object.keys(valueRecord).length === 0) return ''
  return pick(valueRecord, 'summary', 'title', 'label', 'name', 'itemKey', 'id')
}
function pick(source: Record<string, unknown>, ...keys: string[]): string {
  for (const key of keys) {
    const value = text(source[key])
    if (value) return value
  }
  return ''
}
function arrayFrom(source: Record<string, unknown>, ...keys: string[]): Record<string, unknown>[] {
  for (const key of keys) {
    const value = records(source[key])
    if (value.length) return value
  }
  return []
}
function summaryRecord(a: AssessmentSummary | AssessmentDetail): Record<string, unknown> {
  return record(a.counts ?? a.summary ?? a.content ?? {})
}
function effectiveFrameworkId(a: AssessmentSummary | AssessmentDetail): string {
  return text(a.effectiveFrameworkId) || text(a.frameworkId)
}
function frameworkName(a: AssessmentSummary | AssessmentDetail, frameworks: Framework[]): string {
  const id = effectiveFrameworkId(a)
  return text(a.effectiveFrameworkShortName) || text(a.frameworkShortName) || frameworks.find((f) => f.id === id)?.shortName || 'Unlinked'
}
function linkSourceBadge(source: unknown) {
  if (source === 'automatic') return <Badge variant="info">Automatically matched</Badge>
  if (source === 'manual') return <Badge variant="secondary">Manually linked</Badge>
  return <Badge variant="warning">Unlinked</Badge>
}
function itemKey(item: Record<string, unknown>, index: number): string {
  return pick(item, 'itemKey', 'key', 'uuid', 'id') || `item-${index + 1}`
}
function assessmentItems(detail: AssessmentDetail): Record<string, unknown>[] {
  const direct = [
    ...records(detail.reviewedControls), ...records(detail.subjects), ...records(detail.tasks),
    ...records(detail.results), ...records(detail.observations), ...records(detail.findings),
    ...records(detail.risks), ...records(detail.attestations), ...records(detail.resultLog),
    ...records(detail.poamItems),
  ]
  if (direct.length) return direct
  const own = [...records(detail.mappedItems), ...records(detail.unmappedItems)]
  if (own.length) return own
  const content = record(detail.content)
  const summary = record(detail.summary)
  return arrayFrom(content, 'items', 'findings', 'observations', 'risks', 'poamItems', 'results')
    .concat(arrayFrom(summary, 'items', 'findings', 'observations', 'risks', 'poamItems', 'results'))
}
function mappings(detail: AssessmentDetail): AssessmentMapping[] {
  return Array.isArray(detail.mappings) ? detail.mappings : []
}

export function AssessmentPage() {
  const [sp, setSp] = useSearchParams()
  const requestedTab = sp.get('tab')
  const tab: Tab = requestedTab === 'results' || requestedTab === 'poam' ? requestedTab : 'plan'
  const frameworkId = sp.get('frameworkId') ?? ''
  const selected = TABS.find((x) => x.key === tab) ?? TABS[0]
  const detailId = sp.get('uploadId')

  const update = (patch: Record<string, string>) => {
    const next = new URLSearchParams(sp)
    for (const [key, value] of Object.entries(patch)) value ? next.set(key, value) : next.delete(key)
    setSp(next, { replace: true })
  }
  const frameworks = useQuery({ queryKey: ['frameworks'], queryFn: api.listFrameworks })
  const list = useQuery({
    queryKey: ['assessments', selected.type, frameworkId],
    queryFn: () => api.listAssessments({ type: selected.type, frameworkId: frameworkId || undefined, limit: 50, offset: 0 }),
    placeholderData: keepPreviousData,
  })

  return (
    <div>
      <PageHeader title="Assessment" subtitle="Uploaded OSCAL assessment documents." />
      <div className="mb-4 flex flex-wrap items-end justify-between gap-3 border-b border-slate-200">
        <div role="tablist" aria-label="Assessment type" className="flex gap-1">
          {TABS.map((item) => (
            <button
              key={item.key}
              type="button"
              role="tab"
              aria-selected={tab === item.key}
              onClick={() => update({ tab: item.key })}
              className={cn('border-b-2 px-4 py-2 text-sm font-medium', tab === item.key ? 'border-slate-900 text-slate-900' : 'border-transparent text-slate-500 hover:text-slate-900')}
            >
              {item.label}
            </button>
          ))}
        </div>
        <div className="mb-2 w-full max-w-xs space-y-1">
          <Label htmlFor="assessment-framework">Framework</Label>
          <Select id="assessment-framework" value={frameworkId} onChange={(e) => update({ frameworkId: e.target.value })}>
            <option value="">All frameworks</option>
            {frameworks.data?.items.map((f) => <option key={f.id} value={f.id}>{f.shortName}</option>)}
            <option value="unlinked">Unlinked</option>
          </Select>
        </div>
      </div>

      {list.isPending && <LoadingState label={`Loading ${selected.label.toLowerCase()} assessments…`} />}
      {list.isError && <ErrorState error={list.error} />}
      {list.data?.items.length === 0 && (
        <EmptyState>
          No {selected.label.toLowerCase()} documents match this filter. Go to <Link className="font-medium underline" to="/frameworks">Frameworks</Link> to import OSCAL.
        </EmptyState>
      )}
      {list.data && list.data.items.length > 0 && (
        <div className="grid gap-4 lg:grid-cols-2">
          {list.data.items.map((assessment) => (
            <AssessmentCard key={assessment.uploadId} assessment={assessment} tab={tab} frameworks={frameworks.data?.items ?? []} onOpen={() => update({ uploadId: assessment.uploadId })} />
          ))}
        </div>
      )}
      <AssessmentDetailDialog uploadId={detailId} onClose={() => update({ uploadId: '' })} frameworks={frameworks.data?.items ?? []} />
    </div>
  )
}

function AssessmentCard({ assessment: a, tab, frameworks, onOpen }: { assessment: AssessmentSummary; tab: Tab; frameworks: Framework[]; onOpen: () => void }) {
  const summary = summaryRecord(a)
  const presentationTitle = formatAssessmentTitle(a.title)
  return (
    <Card className="flex flex-col">
      <CardHeader>
        <div className="flex items-start justify-between gap-2">
          <div className="min-w-0">
            <CardTitle className="truncate" title={presentationTitle}>{presentationTitle || 'Untitled OSCAL assessment'}</CardTitle>
            <CardDescription>{a.version ? `Version ${a.version} · ` : ''}{formatDateTime(a.uploadedAt)}</CardDescription>
          </div>
          {linkSourceBadge(a.linkSource)}
        </div>
      </CardHeader>
      <CardContent className="flex flex-1 flex-col gap-4">
        <div className="text-sm"><span className="text-slate-500">Framework:</span> <span className="font-medium">{frameworkName(a, frameworks)}</span></div>
        {tab === 'plan' && <PlanPreview summary={summary} />}
        {tab === 'results' && <ResultsPreview summary={summary} />}
        {tab === 'poam' && <PoamPreview summary={summary} />}
        <div className="mt-auto flex items-center justify-between border-t border-slate-100 pt-3 text-xs text-slate-500">
          <span>{a.mappedCount ?? Math.max(0, Object.entries(a.counts ?? {}).filter(([key]) => key !== 'unmappedItems').reduce((sum, [, value]) => sum + value, 0) - (a.counts?.unmappedItems ?? 0))} mapped · {a.unmappedCount ?? a.counts?.unmappedItems ?? 0} unmapped</span>
          <Button size="sm" onClick={onOpen}>View details</Button>
        </div>
      </CardContent>
    </Card>
  )
}

function Fact({ label, value }: { label: string; value: string }) {
  return <div><dt className="text-xs font-medium uppercase tracking-wide text-slate-400">{label}</dt><dd className="mt-0.5 text-sm text-slate-800">{value || 'Not provided'}</dd></div>
}
function PlanPreview({ summary }: { summary: Record<string, unknown> }) {
  return <dl className="grid grid-cols-2 gap-3"><Fact label="Reviewed controls / objectives" value={pick(summary, 'reviewedControls', 'controls', 'objectives')} /><Fact label="Subjects / assets" value={pick(summary, 'subjects', 'assets')} /><Fact label="Tasks / methods" value={pick(summary, 'tasks', 'methods')} /></dl>
}
function ResultsPreview({ summary }: { summary: Record<string, unknown> }) {
  return <dl className="grid grid-cols-2 gap-3"><Fact label="Findings" value={pick(summary, 'findings', 'findingCount')} /><Fact label="Observations" value={pick(summary, 'observations', 'observationCount')} /><Fact label="Risks" value={pick(summary, 'risks', 'riskCount')} /><Fact label="Unmapped" value={pick(summary, 'unmappedItems', 'unmappedCount', 'unmapped')} /></dl>
}
function PoamPreview({ summary }: { summary: Record<string, unknown> }) {
  return <dl className="grid grid-cols-2 gap-3"><Fact label="Items" value={pick(summary, 'poamItems', 'itemCount', 'items')} /><Fact label="Unmapped" value={pick(summary, 'unmappedItems', 'unmappedCount')} /></dl>
}

function AssessmentDetailDialog({ uploadId, onClose, frameworks }: { uploadId: string | null; onClose: () => void; frameworks: Framework[] }) {
  const [rawOpen, setRawOpen] = useState(false)
  const q = useQuery({ queryKey: ['assessment', uploadId], queryFn: () => api.getAssessment(uploadId!), enabled: uploadId !== null })
  const raw = useQuery({ queryKey: ['oscal-upload', uploadId], queryFn: () => api.getOscalUpload(uploadId!), enabled: uploadId !== null && rawOpen })
  const close = () => { setRawOpen(false); onClose() }
  return (
    <>
      <Dialog open={uploadId !== null} onClose={close} title={q.data?.title || 'Assessment details'} description="OSCAL metadata, records, framework link, and mappings." className="max-w-6xl" footer={<><Button variant="outline" onClick={() => setRawOpen(true)} disabled={!q.data}><FileJson /> View raw OSCAL JSON</Button><Button variant="outline" onClick={close}>Close</Button></>}>
        {q.isPending && <LoadingState label="Loading assessment…" />}
        {q.isError && <ErrorState error={q.error} />}
        {q.data && <AssessmentDetailContent detail={q.data} frameworks={frameworks} />}
      </Dialog>
      <Dialog open={rawOpen} onClose={() => setRawOpen(false)} title={raw.data?.title || 'Raw OSCAL JSON'} description="Exact uploaded document stored by the server." className="max-w-5xl" footer={<Button variant="outline" onClick={() => setRawOpen(false)}>Close</Button>}>
        {raw.isPending && <LoadingState label="Loading raw OSCAL JSON…" />}
        {raw.isError && <ErrorState error={raw.error} />}
        {raw.data && <pre className="max-h-[65vh] overflow-auto rounded-md bg-slate-950 p-4 text-xs leading-relaxed text-slate-100"><code>{JSON.stringify(raw.data.document, null, 2)}</code></pre>}
      </Dialog>
    </>
  )
}

function AssessmentDetailContent({ detail, frameworks }: { detail: AssessmentDetail; frameworks: Framework[] }) {
  return (
    <div className="space-y-6">
      <FrameworkLinker detail={detail} frameworks={frameworks} />
      {detail.type === 'assessment-plan' && <PlanDetail detail={detail} />}
      {detail.type === 'assessment-results' && <ResultsDetail detail={detail} frameworks={frameworks} />}
      {detail.type === 'plan-of-action-and-milestones' && <PoamDetail detail={detail} frameworks={frameworks} />}
      <MappingsSection detail={detail} frameworks={frameworks} />
    </div>
  )
}

function FrameworkLinker({ detail, frameworks }: { detail: AssessmentDetail; frameworks: Framework[] }) {
  const qc = useQueryClient()
  const [choice, setChoice] = useState(effectiveFrameworkId(detail))
  const mutation = useMutation({
    mutationFn: (frameworkId: string | null) => api.linkAssessmentFramework(detail.uploadId, frameworkId),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['assessment', detail.uploadId] }); qc.invalidateQueries({ queryKey: ['assessments'] }) },
  })
  const linked = Boolean(effectiveFrameworkId(detail))
  return (
    <section className="rounded-lg border border-slate-200 bg-slate-50 p-4">
      <div className="flex flex-wrap items-center gap-2"><h3 className="font-semibold">Framework link</h3>{linkSourceBadge(detail.linkSource)}<span className="text-sm text-slate-600">Effective framework: <strong>{frameworkName(detail, frameworks)}</strong></span></div>
      <div className="mt-3 flex flex-wrap items-end gap-2">
        <div className="min-w-64 flex-1 space-y-1"><Label htmlFor="detail-framework">{linked ? (detail.linkSource === 'manual' ? 'Change framework' : 'Manual override') : 'Select framework'}</Label><Select id="detail-framework" value={choice} onChange={(e) => setChoice(e.target.value)}><option value="">Select a framework…</option>{frameworks.map((f) => <option key={f.id} value={f.id}>{f.shortName}</option>)}</Select></div>
        <Button onClick={() => mutation.mutate(choice || null)} disabled={mutation.isPending || (!choice && !linked)}>{mutation.isPending ? <Spinner /> : <Link2 />}{linked ? 'Change' : 'Link'}</Button>
        {detail.linkSource === 'manual' && <Button variant="outline" onClick={() => { setChoice(''); mutation.mutate(null) }} disabled={mutation.isPending}>Clear</Button>}
      </div>
      {mutation.isError && <ErrorState error={mutation.error} className="mt-2" />}
    </section>
  )
}

function itemValues(items: unknown, ...keys: string[]): string {
  return records(items).map((item) => pick(item, ...keys)).filter(Boolean).join(', ')
}
function PlanDetail({ detail }: { detail: AssessmentDetail }) {
  const tasks = records(detail.tasks)
  const roles = tasks.flatMap((item) => Array.isArray(item.responsibleRoles) ? item.responsibleRoles.map(text) : []).filter(Boolean).join(', ')
  const schedules = tasks.map((item) => text(item.schedule)).filter(Boolean).join(', ')
  return <Card><CardHeader><CardTitle>Assessment plan</CardTitle><CardDescription>Plan details and coverage.</CardDescription></CardHeader><CardContent><dl className="grid gap-4 md:grid-cols-2"><Fact label="Metadata" value={[detail.version, detail.lastModified].filter(Boolean).join(' · ')} /><Fact label="Reviewed controls / objectives" value={itemValues(detail.reviewedControls, 'controlIds', 'objectiveIds', 'partIds', 'summary')} /><Fact label="Subjects / assets" value={itemValues(detail.subjects, 'subjectIds', 'title', 'summary')} /><Fact label="Tasks / methods" value={itemValues(detail.tasks, 'methods', 'title', 'summary')} /><Fact label="Responsible roles" value={roles} /><Fact label="Schedule" value={schedules} /></dl></CardContent></Card>
}

function mappingFor(detail: AssessmentDetail, key: string): AssessmentMapping | undefined {
  const explicit = mappings(detail).find((m) => m.itemKey === key)
  if (explicit) return explicit
  const item = assessmentItems(detail).find((candidate, index) => itemKey(candidate, index) === key)
  const embedded = record(item?.mapping)
  const requirementId = pick(embedded, 'requirementId')
  if (!requirementId) return undefined
  return {
    itemKey: key,
    requirementId,
    partId: pick(embedded, 'partId') || undefined,
    controlId: pick(embedded, 'controlId') || undefined,
    source: pick(embedded, 'source') as AssessmentMapping['source'],
    frameworkId: effectiveFrameworkId(detail),
  }
}
function mappingLabel(mapping: AssessmentMapping | undefined, frameworks: Framework[]): string {
  if (!mapping) return 'Unmapped'
  const shortName = mapping.frameworkShortName || frameworks.find((f) => f.id === mapping.frameworkId)?.shortName || 'Framework'
  const part = mapping.partLabel || mapping.partId
  return `${shortName} → ${(mapping.controlId || mapping.requirementId).toUpperCase()}${part ? ` → ${part}` : ''}`
}
function ResultsDetail({ detail, frameworks }: { detail: AssessmentDetail; frameworks: Framework[] }) {
  const resultItems = [...records(detail.findings), ...records(detail.observations), ...records(detail.risks)]
  const items = resultItems.length ? resultItems : assessmentItems(detail)
  const mapped = items.map((item, index) => ({ item, key: itemKey(item, index), mapping: mappingFor(detail, itemKey(item, index)) })).filter((x) => x.mapping)
  const grouped = useMemo(() => {
    const out = new Map<string, typeof mapped>()
    for (const row of mapped) {
      const key = `${row.mapping?.controlId || row.mapping?.requirementId}|${row.mapping?.partLabel || ''}`
      out.set(key, [...(out.get(key) ?? []), row])
    }
    return [...out.values()]
  }, [mapped])
  return <Card><CardHeader><CardTitle>Results by control</CardTitle><CardDescription>Framework → control → statement part → findings.</CardDescription></CardHeader><CardContent className="space-y-4">{grouped.length === 0 && <p className="text-sm text-slate-500">No mapped result records.</p>}{grouped.map((rows) => <section key={rows[0].key} className="rounded-lg border border-slate-200"><h4 className="border-b bg-slate-50 px-3 py-2 text-sm font-semibold">{mappingLabel(rows[0].mapping, frameworks)}</h4><ResultRows rows={rows.map((x) => ({ key: x.key, item: x.item }))} /></section>)}</CardContent></Card>
}
function ResultRows({ rows }: { rows: { key: string; item: Record<string, unknown> }[] }) {
  return <Table><TableHeader><TableRow><TableHead>Observation / finding</TableHead><TableHead>Risk / severity</TableHead><TableHead>Result status</TableHead><TableHead>Evidence</TableHead></TableRow></TableHeader><TableBody>{rows.map(({ key, item }) => <TableRow key={key}><TableCell className="max-w-md">{pick(item, 'observation', 'finding', 'description', 'title', 'summary') || key}</TableCell><TableCell>{pick(item, 'risk', 'severity', 'likelihood')}</TableCell><TableCell>{pick(item, 'resultStatus', 'status', 'state')}</TableCell><TableCell>{pick(item, 'evidenceRefs', 'evidence', 'evidenceTitle', 'links')}</TableCell></TableRow>)}</TableBody></Table>
}
function PoamDetail({ detail, frameworks }: { detail: AssessmentDetail; frameworks: Framework[] }) {
  const items = records(detail.poamItems).length ? records(detail.poamItems) : assessmentItems(detail)
  return <Card><CardHeader><CardTitle>Plan of action and milestones</CardTitle><CardDescription>Remediation items linked to this assessment.</CardDescription></CardHeader><CardContent className="p-0"><Table><TableHeader><TableRow><TableHead>Item / title</TableHead><TableHead>Related control</TableHead><TableHead>Risk / severity</TableHead><TableHead>Status</TableHead><TableHead>Owner</TableHead><TableHead>Scheduled completion</TableHead><TableHead>Milestones</TableHead><TableHead>Observations / evidence</TableHead></TableRow></TableHeader><TableBody>{items.map((item, index) => { const key = itemKey(item, index); return <TableRow key={key}><TableCell>{pick(item, 'title', 'item', 'description') || key}</TableCell><TableCell>{mappingLabel(mappingFor(detail, key), frameworks)}</TableCell><TableCell>{pick(item, 'risk', 'severity')}</TableCell><TableCell>{pick(item, 'status', 'state')}</TableCell><TableCell>{pick(item, 'owner', 'responsibleRole')}</TableCell><TableCell>{formatDate(pick(item, 'scheduledCompletion', 'completionDate', 'dueDate'))}</TableCell><TableCell>{pick(item, 'milestones', 'milestone')}</TableCell><TableCell>{pick(item, 'relatedObservationIds', 'relatedFindingIds', 'evidenceRefs', 'observations', 'evidence', 'links')}</TableCell></TableRow> })}</TableBody></Table>{items.length === 0 && <p className="p-4 text-sm text-slate-500">No POA&amp;M items.</p>}</CardContent></Card>
}

function MappingsSection({ detail, frameworks }: { detail: AssessmentDetail; frameworks: Framework[] }) {
  const items = assessmentItems(detail)
  const rows = items.map((item, index) => ({ item, key: itemKey(item, index), mapping: mappingFor(detail, itemKey(item, index)) }))
  const mapped = rows.filter((x) => x.mapping)
  const serverUnmapped = records(detail.unmappedItems)
  const unmapped = (serverUnmapped.length ? serverUnmapped : rows.filter((x) => !x.mapping).map((x) => x.item))
    .map((item, index) => ({ item, key: itemKey(item, index), mapping: undefined }))
  const qc = useQueryClient()
  const remove = useMutation({ mutationFn: (key: string) => api.deleteAssessmentMapping(detail.uploadId, key), onSuccess: () => { qc.invalidateQueries({ queryKey: ['assessment', detail.uploadId] }); qc.invalidateQueries({ queryKey: ['assessments'] }) } })
  return <div className="space-y-4"><Card><CardHeader><CardTitle>Mapped records</CardTitle><CardDescription>Assessment mappings are informational and never change implementation status.</CardDescription></CardHeader><CardContent>{mapped.length === 0 ? <p className="text-sm text-slate-500">No records mapped.</p> : <ul className="divide-y divide-slate-100">{mapped.map((row) => <MappedRecordRow key={row.key} detail={detail} row={row} frameworks={frameworks} onRemove={() => remove.mutate(row.key)} removing={remove.isPending} />)}</ul>}{remove.isError && <ErrorState error={remove.error} />}</CardContent></Card><Card><CardHeader><CardTitle>Unmapped queue</CardTitle><CardDescription>Search the linked framework and map each record to a control and optional statement part.</CardDescription></CardHeader><CardContent className="space-y-3">{unmapped.length === 0 ? <p className="text-sm text-slate-500">All assessment records are mapped.</p> : unmapped.map((row) => <MappingEditor key={row.key} detail={detail} item={row.item} itemKeyValue={row.key} />)}</CardContent></Card></div>
}

function MappedRecordRow({ detail, row, frameworks, onRemove, removing }: {
  detail: AssessmentDetail
  row: { item: Record<string, unknown>; key: string; mapping: AssessmentMapping | undefined }
  frameworks: Framework[]
  onRemove: () => void
  removing: boolean
}) {
  const [overriding, setOverriding] = useState(false)
  return (
    <li className="py-2">
      <div className="flex items-center justify-between gap-3">
        <div><p className="text-sm font-medium">{pick(row.item, 'title', 'summary', 'description') || row.key}</p><p className="text-xs text-slate-500">{mappingLabel(row.mapping, frameworks)}</p></div>
        <div className="flex items-center gap-2">
          {row.mapping?.source === 'manual' ? <Button size="icon" variant="ghost" aria-label={`Remove mapping for ${row.key}`} onClick={onRemove} disabled={removing}><Trash2 className="text-red-600" /></Button> : <><Badge variant="info">Automatically matched</Badge><Button size="sm" variant="outline" onClick={() => setOverriding((value) => !value)}>Override</Button></>}
        </div>
      </div>
      {overriding && <div className="mt-2"><MappingEditor detail={detail} item={row.item} itemKeyValue={row.key} /></div>}
    </li>
  )
}

function MappingEditor({ detail, item, itemKeyValue }: { detail: AssessmentDetail; item: Record<string, unknown>; itemKeyValue: string }) {
  const qc = useQueryClient()
  const frameworkId = effectiveFrameworkId(detail)
  const [query, setQuery] = useState('')
  const [requirementId, setRequirementId] = useState('')
  const [partId, setPartId] = useState('')
  const requirements = useQuery({ queryKey: ['requirements', 'assessment-map', frameworkId, query], queryFn: () => api.listRequirements({ frameworkId, q: query || undefined, limit: 50, offset: 0 }), enabled: Boolean(frameworkId) })
  const requirement = useQuery({ queryKey: ['requirement', requirementId], queryFn: () => api.getRequirement(requirementId), enabled: Boolean(requirementId) })
  const mutation = useMutation({ mutationFn: () => api.createAssessmentMapping(detail.uploadId, { itemKey: itemKeyValue, requirementId, partId: partId || undefined }), onSuccess: () => { qc.invalidateQueries({ queryKey: ['assessment', detail.uploadId] }); qc.invalidateQueries({ queryKey: ['assessments'] }) } })
  const selected = requirements.data?.items.find((r: Requirement) => r.id === requirementId)
  return <div className="rounded-lg border border-slate-200 p-3"><div className="mb-3"><p className="text-sm font-medium">{pick(item, 'title', 'summary', 'description', 'observation') || itemKeyValue}</p><p className="font-mono text-[11px] text-slate-400">{itemKeyValue}</p></div>{!frameworkId ? <p className="text-sm text-amber-700">Link a framework before mapping records.</p> : <div className="grid gap-2 md:grid-cols-[1fr_1fr_1fr_auto]"><div className="space-y-1"><Label>Search controls</Label><div className="relative"><Search className="absolute left-2.5 top-2.5 size-4 text-slate-400" /><Input className="pl-8" value={query} onChange={(e) => setQuery(e.target.value)} placeholder="Control id or title" /></div></div><div className="space-y-1"><Label>Control</Label><Select value={requirementId} onChange={(e) => { setRequirementId(e.target.value); setPartId('') }}><option value="">Select…</option>{requirements.data?.items.map((r) => <option key={r.id} value={r.id}>{r.controlId.toUpperCase()}: {r.title}</option>)}</Select></div><div className="space-y-1"><Label>Statement part (optional)</Label><Select value={partId} onChange={(e) => setPartId(e.target.value)} disabled={!requirementId || requirement.isPending}><option value="">Whole control</option>{requirement.data?.trackableParts.map((part) => <option key={part.partId} value={part.partId}>{part.label || part.partId}</option>)}</Select></div><Button className="self-end" onClick={() => mutation.mutate()} disabled={!selected || mutation.isPending}>{mutation.isPending && <Spinner />}Map</Button></div>}{mutation.isError && <ErrorState error={mutation.error} className="mt-2" />}</div>
}
