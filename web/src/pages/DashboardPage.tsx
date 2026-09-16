import { useEffect, useState } from 'react'
import { Link } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { AlertTriangle, CalendarClock, CheckCircle2, Gauge } from 'lucide-react'
import { api } from '@/api'
import { REQUIREMENT_STATUSES, type FrameworkSummary, type StatusCounts } from '@/types'
import { PageHeader } from '@/components/Layout'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge, STATUS_VARIANT, StatusBadge } from '@/components/ui/badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { EmptyState, ErrorState, LoadingState } from '@/components/ui/feedback'
import { Pagination } from '@/components/ui/pagination'
import { STATUS_LABEL, cn, dueLabel, formatDate, formatDateTime } from '@/lib/utils'

function Stat({ label, value, hint, icon: Icon, tone }: { label: string; value: string | number; hint?: string; icon: typeof Gauge; tone?: string }) {
  return (
    <Card>
      <CardContent className="flex items-center gap-4 p-5">
        <div className={`rounded-lg p-2.5 ${tone ?? 'bg-slate-100 text-slate-700'}`}>
          <Icon className="size-5" aria-hidden />
        </div>
        <div>
          <p className="text-xs font-medium uppercase tracking-wide text-slate-500">{label}</p>
          <p className="text-2xl font-semibold leading-tight">{value}</p>
          {hint && <p className="text-xs text-slate-500">{hint}</p>}
        </div>
      </CardContent>
    </Card>
  )
}

function CoverageBar({ percent }: { percent: number }) {
  const p = Math.max(0, Math.min(100, Math.round(percent)))
  return (
    <div className="flex items-center gap-2" aria-label={`Coverage ${p}%`}>
      <div className="h-2 w-28 overflow-hidden rounded-full bg-slate-200">
        <div className="h-full rounded-full bg-emerald-500" style={{ width: `${p}%` }} />
      </div>
      <span className="w-10 text-right tabular-nums">{p}%</span>
    </div>
  )
}

/** One chip per OSCAL implementation-status, coloured like the status badge. */
function StatusChips({ byStatus }: { byStatus: StatusCounts }) {
  return (
    <div className="flex flex-wrap gap-1">
      {REQUIREMENT_STATUSES.map((k) => (
        <Badge key={k} variant={STATUS_VARIANT[k]} className="gap-1 font-normal" title={STATUS_LABEL[k]}>
          <span className="opacity-80">{STATUS_LABEL[k]}</span>
          <span className="font-semibold tabular-nums">{byStatus?.[k] ?? 0}</span>
        </Badge>
      ))}
    </div>
  )
}

export function DashboardPage() {
  const [upcomingOffset, setUpcomingOffset] = useState(0)
  const q = useQuery({ queryKey: ['dashboard'], queryFn: api.dashboard, refetchInterval: 60_000 })
  const frameworks = useQuery({ queryKey: ['frameworks'], queryFn: api.listFrameworks })
  const upcomingTotal = Math.min(q.data?.upcomingRequirements?.length ?? 0, 20)
  useEffect(() => {
    const lastOffset = Math.max(0, Math.floor(Math.max(0, upcomingTotal - 1) / 10) * 10)
    setUpcomingOffset((offset) => Math.min(offset, lastOffset))
  }, [upcomingTotal])

  if (q.isPending) return <LoadingState label="Loading dashboard…" />
  if (q.isError) return <ErrorState error={q.error} />
  const d = q.data
  const upcoming = d.upcomingRequirements ?? []
  const upcomingPage = upcoming.slice(upcomingOffset, upcomingOffset + 10)

  return (
    <div className="space-y-6">
      <PageHeader title="Dashboard" subtitle={`Data generated ${formatDateTime(d.generatedAt)}`} />

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Stat label="Coverage" value={`${Math.round(d.totals.coveragePercent)}%`} hint="implemented of applicable" icon={Gauge} tone="bg-emerald-100 text-emerald-700" />
        <Stat label="Implemented" value={`${d.totals.implemented} / ${d.totals.applicable}`} hint={`${d.totals.total} total requirements`} icon={CheckCircle2} />
        <Stat label="Due soon" value={d.totals.dueSoon ?? 0} hint="due within 30 days" icon={CalendarClock} tone={(d.totals.dueSoon ?? 0) > 0 ? 'bg-amber-100 text-amber-700' : undefined} />
        <Stat label="Overdue" value={d.totals.overdue} hint="past due date, not implemented" icon={AlertTriangle} tone={d.totals.overdue > 0 ? 'bg-red-100 text-red-700' : undefined} />
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Status by framework</CardTitle>
        </CardHeader>
        <CardContent>
          {d.frameworks.length === 0 ? (
            <EmptyState>
              No frameworks imported yet. <Link className="underline" to="/frameworks">Import one</Link>.
            </EmptyState>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Framework</TableHead>
                  <TableHead>Coverage</TableHead>
                  <TableHead>Implemented</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead className="text-right">Due soon</TableHead>
                  <TableHead className="text-right">Overdue</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {d.frameworks.map((f: FrameworkSummary) => (
                  <TableRow key={f.frameworkId}>
                    <TableCell>
                      <Link to={`/frameworks/${encodeURIComponent(f.frameworkId)}`} className="font-medium hover:underline" title={f.title}>
                        {frameworks.data?.items.find((x) => x.id === f.frameworkId)?.shortName ?? f.shortName ?? f.title}
                      </Link>
                    </TableCell>
                    <TableCell><CoverageBar percent={f.coveragePercent} /></TableCell>
                    <TableCell className="tabular-nums">{f.implemented} / {f.applicable} <span className="text-slate-400">({f.total})</span></TableCell>
                    <TableCell><StatusChips byStatus={f.byStatus} /></TableCell>
                    <TableCell className="text-right tabular-nums">
                      {(f.dueSoon ?? 0) > 0 ? <Badge variant="warning">{f.dueSoon}</Badge> : <span className="text-slate-400">0</span>}
                    </TableCell>
                    <TableCell className="text-right tabular-nums">
                      {f.overdue > 0 ? <Badge variant="danger">{f.overdue}</Badge> : <span className="text-slate-400">0</span>}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Upcoming deadlines</CardTitle>
        </CardHeader>
        <CardContent>
          {upcoming.length === 0 ? (
            <EmptyState>No requirements with a due date.</EmptyState>
          ) : (
            <ul className="divide-y divide-slate-100">
              {upcomingPage.map((r) => (
                <li key={r.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 py-2">
                  <Badge variant="secondary" className="shrink-0 font-normal" title={r.frameworkShortName}>
                    {r.frameworkShortName}
                  </Badge>
                  <div className="min-w-0 flex-1">
                    <Link to={`/requirements/${encodeURIComponent(r.id)}`} className="block truncate font-medium hover:underline" title={r.title}>
                      <span className="mr-2 font-mono text-xs text-slate-500">{r.controlId.toUpperCase()}</span>
                      {r.title}
                    </Link>
                    <p className="text-xs text-slate-500">{r.owner || 'unassigned'}</p>
                  </div>
                  <StatusBadge status={r.status} />
                  <div className="w-36 shrink-0 text-right">
                    <time dateTime={r.dueDate ?? undefined} className="block text-sm tabular-nums text-slate-700">{formatDate(r.dueDate)}</time>
                    <span className={cn('text-xs', r.overdue ? 'font-medium text-red-700' : r.daysUntilDue <= 30 ? 'text-amber-700' : 'text-slate-500')}>
                      {dueLabel(r.daysUntilDue)}
                    </span>
                  </div>
                </li>
              ))}
              {upcomingTotal > 10 && (
                <li className="pt-3">
                  <Pagination offset={upcomingOffset} limit={10} total={upcomingTotal} onChange={setUpcomingOffset} />
                </li>
              )}
            </ul>
          )}
        </CardContent>
      </Card>

    </div>
  )
}
