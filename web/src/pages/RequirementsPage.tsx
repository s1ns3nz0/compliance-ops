import { useEffect, useState } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router'
import { useQuery, keepPreviousData } from '@tanstack/react-query'
import { Search } from 'lucide-react'
import { api } from '@/api'
import { REQUIREMENT_STATUSES, type RequirementStatus } from '@/types'
import { PageHeader } from '@/components/Layout'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select } from '@/components/ui/select'
import { StatusBadge, Badge } from '@/components/ui/badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Pagination } from '@/components/ui/pagination'
import { EmptyState, ErrorState, LoadingState } from '@/components/ui/feedback'
import { scopeCategoryIdFromSearch } from '@/lib/scopeCategoryLogic'
import { STATUS_LABEL, cn, formatDate, formatDateTime, isOverdue } from '@/lib/utils'

const LIMIT = 25

export function RequirementsPage() {
  const params = useParams<{ frameworkId?: string }>()
  const [sp, setSp] = useSearchParams()
  const navigate = useNavigate()

  const frameworkId = params.frameworkId ?? sp.get('frameworkId') ?? ''
  const status = (sp.get('status') ?? '') as RequirementStatus | ''
  const q = sp.get('q') ?? ''
  const scopeCategoryId = scopeCategoryIdFromSearch(sp)
  const offset = Number(sp.get('offset') ?? 0) || 0

  const [qInput, setQInput] = useState(q)
  useEffect(() => setQInput(q), [q])
  useEffect(() => {
    const t = setTimeout(() => {
      if (qInput !== q) update({ q: qInput, offset: 0 })
    }, 300)
    return () => clearTimeout(t)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [qInput])

  const update = (patch: Record<string, string | number | undefined>) => {
    const next = new URLSearchParams(sp)
    for (const [k, v] of Object.entries(patch)) {
      if (v === undefined || v === '' || v === 0) next.delete(k)
      else next.set(k, String(v))
    }
    setSp(next, { replace: true })
  }

  const frameworks = useQuery({ queryKey: ['frameworks'], queryFn: api.listFrameworks })
  const scopeCategories = useQuery({ queryKey: ['scope-categories'], queryFn: api.listScopeCategories })
  const list = useQuery({
    queryKey: ['requirements', { frameworkId, status, q, scopeCategoryId, offset, limit: LIMIT }],
    queryFn: () => api.listRequirements({ frameworkId: frameworkId || undefined, status: status || undefined, q: q || undefined, scopeCategoryId: scopeCategoryId || undefined, limit: LIMIT, offset }),
    placeholderData: keepPreviousData,
  })

  const currentFramework = frameworks.data?.items.find((f) => f.id === frameworkId)

  const onFrameworkChange = (id: string) => {
    if (params.frameworkId !== undefined) {
      // route-based framework selection: switch route
      const rest = new URLSearchParams(sp)
      rest.delete('offset')
      const suffix = rest.toString() ? `?${rest}` : ''
      navigate(id ? `/frameworks/${encodeURIComponent(id)}${suffix}` : `/requirements${suffix}`)
    } else {
      update({ frameworkId: id, offset: 0 })
    }
  }

  return (
    <div>
      <PageHeader
        title="Requirements"
        subtitle={currentFramework ? `${currentFramework.shortName || currentFramework.title} · ${currentFramework.requirementCount} requirements` : 'All frameworks'}
      />

      <div className="mb-4 grid gap-3 sm:grid-cols-2 lg:grid-cols-[minmax(0,1.1fr)_minmax(0,0.8fr)_minmax(0,1.2fr)_minmax(0,1.2fr)]">
        <div className="space-y-1">
          <Label htmlFor="f-framework">Framework</Label>
          <Select id="f-framework" value={frameworkId} onChange={(e) => onFrameworkChange(e.target.value)}>
            <option value="">All frameworks</option>
            {frameworks.data?.items.map((f) => (
              <option key={f.id} value={f.id}>{f.shortName || f.title}</option>
            ))}
          </Select>
        </div>
        <div className="space-y-1">
          <Label htmlFor="f-status">Status</Label>
          <Select id="f-status" value={status} onChange={(e) => update({ status: e.target.value, offset: 0 })}>
            <option value="">All statuses</option>
            {REQUIREMENT_STATUSES.map((s) => (
              <option key={s} value={s}>{STATUS_LABEL[s]}</option>
            ))}
          </Select>
        </div>
        <div className="space-y-1">
          <Label htmlFor="f-scope">Scope</Label>
          <Select id="f-scope" value={scopeCategoryId} onChange={(e) => update({ scopeCategoryId: e.target.value, offset: 0 })}>
            <option value="">All scopes</option>
            {scopeCategories.data?.items.map((category) => (
              <option key={category.id} value={category.id}>{category.name}</option>
            ))}
          </Select>
        </div>
        <div className="space-y-1">
          <Label htmlFor="f-q">Search</Label>
          <div className="relative">
            <Search className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-slate-400" aria-hidden />
            <Input id="f-q" className="pl-8" placeholder="Control ID, title, or text…" value={qInput} onChange={(e) => setQInput(e.target.value)} />
          </div>
        </div>
      </div>

      <Card>
        <CardContent className="p-0">
          {list.isPending && <LoadingState />}
          {list.isError && <ErrorState error={list.error} className="m-4" />}
          {list.data && list.data.items.length === 0 && <EmptyState>No requirements match the current filters.</EmptyState>}
          {list.data && list.data.items.length > 0 && (
            <Table className={cn(list.isFetching && 'opacity-60')}>
              <TableHeader>
                <TableRow>
                  <TableHead>Control</TableHead>
                  <TableHead>Title</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead>Owner</TableHead>
                  <TableHead>Due</TableHead>
                  <TableHead>Updated</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {list.data.items.map((r) => {
                  const overdue = isOverdue(r.dueDate, r.status)
                  return (
                    <TableRow
                      key={r.id}
                      className="cursor-pointer"
                      tabIndex={0}
                      role="link"
                      onClick={() => navigate(`/requirements/${encodeURIComponent(r.id)}`)}
                      onKeyDown={(e) => {
                        if (e.key === 'Enter') navigate(`/requirements/${encodeURIComponent(r.id)}`)
                      }}
                    >
                      <TableCell className="font-mono text-xs">{r.controlId}</TableCell>
                      <TableCell className="max-w-md">
                        <div className="truncate font-medium" title={r.title}>{r.title}</div>
                        {!frameworkId && frameworks.data && (
                          <div className="truncate text-xs text-slate-500">{(() => { const f = frameworks.data.items.find((x) => x.id === r.frameworkId); return f ? f.shortName || f.title : r.frameworkId })()}</div>
                        )}
                      </TableCell>
                      <TableCell>
                        <span className="inline-flex items-center gap-1">
                          <StatusBadge status={r.status} />
                          {r.statusOverride && (
                            <Badge variant="outline" className="px-1 text-[10px]" title={`Status overridden (derived: ${STATUS_LABEL[r.derivedStatus] ?? r.derivedStatus})`}>
                              override
                            </Badge>
                          )}
                        </span>
                      </TableCell>
                      <TableCell className="text-slate-700">{r.owner || <span className="text-slate-400">No owner</span>}</TableCell>
                      <TableCell>
                        {overdue ? <Badge variant="danger">{formatDate(r.dueDate)}</Badge> : <span className="text-slate-700">{formatDate(r.dueDate)}</span>}
                      </TableCell>
                      <TableCell className="text-xs text-slate-500">{formatDateTime(r.updatedAt)}</TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
      {list.data && (
        <div className="mt-3">
          <Pagination offset={offset} limit={LIMIT} total={list.data.total} onChange={(o) => update({ offset: o })} />
        </div>
      )}
    </div>
  )
}
