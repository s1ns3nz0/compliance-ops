import { useEffect, useState } from 'react'
import { Link, useSearchParams } from 'react-router'
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { Search, Upload } from 'lucide-react'
import { api } from '@/api'
import { EVIDENCE_KINDS, type EvidenceKind } from '@/types'
import { PageHeader } from '@/components/Layout'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select } from '@/components/ui/select'
import { Badge } from '@/components/ui/badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Pagination } from '@/components/ui/pagination'
import { EmptyState, ErrorState, LoadingState } from '@/components/ui/feedback'
import { EvidencePrimaryAction, ExternalEvidenceLink, KindIcon } from '@/components/EvidenceLink'
import { UploadEvidenceDialog } from '@/components/UploadEvidenceDialog'
import { EVIDENCE_KIND_LABEL, cn, formatBytes, formatDate, formatDateTime } from '@/lib/utils'
import { formatEvidenceTitle } from '@/lib/evidenceTitle'

const LIMIT = 25

export function EvidencePage() {
  const [sp, setSp] = useSearchParams()
  const kind = (sp.get('kind') ?? '') as EvidenceKind | ''
  const requirementId = sp.get('requirementId') ?? ''
  const q = sp.get('q') ?? ''
  const offset = Number(sp.get('offset') ?? 0) || 0
  const [uploadOpen, setUploadOpen] = useState(false)

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

  const list = useQuery({
    queryKey: ['evidence', { kind, requirementId, q, offset, limit: LIMIT }],
    queryFn: () =>
      api.listEvidence({
        kind: kind || undefined,
        requirementId: requirementId || undefined,
        q: q || undefined,
        limit: LIMIT,
        offset,
      }),
    placeholderData: keepPreviousData,
  })

  const today = new Date().toISOString().slice(0, 10)

  return (
    <div>
      <PageHeader
        title="Evidence"
        subtitle="Uploaded files and external links with validity dates and linked requirements."
        actions={
          <Button onClick={() => setUploadOpen(true)}>
            <Upload /> Add evidence
          </Button>
        }
      />

      <div className="mb-4 grid gap-3 sm:grid-cols-[minmax(0,0.6fr)_minmax(0,2.4fr)]">
        <div className="space-y-1">
          <Label htmlFor="e-kind">Type</Label>
          <Select id="e-kind" value={kind} onChange={(e) => update({ kind: e.target.value, offset: 0 })}>
            <option value="">All types</option>
            {EVIDENCE_KINDS.map((k) => (
              <option key={k} value={k}>{EVIDENCE_KIND_LABEL[k]}</option>
            ))}
          </Select>
        </div>
        <div className="space-y-1">
          <Label htmlFor="e-q">Search</Label>
          <div className="relative">
            <Search className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-slate-400" aria-hidden />
            <Input id="e-q" className="pl-8" placeholder="Title, file name, or description…" value={qInput} onChange={(e) => setQInput(e.target.value)} />
          </div>
        </div>
      </div>
      {requirementId && (
        <p className="mb-3 text-sm text-slate-600">
          Filtered by requirement <code className="rounded bg-slate-100 px-1 text-xs">{requirementId}</code>{' '}
          <button className="underline" onClick={() => update({ requirementId: '', offset: 0 })}>clear</button>
        </p>
      )}

      <Card>
        <CardContent className="p-0">
          {list.isPending && <LoadingState />}
          {list.isError && <ErrorState error={list.error} className="m-4" />}
          {list.data && list.data.items.length === 0 && <EmptyState>No evidence matches the current filters.</EmptyState>}
          {list.data && list.data.items.length > 0 && (
            <Table className={cn(list.isFetching && 'opacity-60')}>
              <TableHeader>
                <TableRow>
                  <TableHead>Title</TableHead>
                  <TableHead>Source</TableHead>
                  <TableHead>Valid</TableHead>
                  <TableHead>Requirements</TableHead>
                  <TableHead>Uploaded</TableHead>
                  <TableHead className="text-right">Actions</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {list.data.items.map((e) => {
                  const expired = !!e.validUntil && formatDate(e.validUntil) < today
                  return (
                    <TableRow key={e.id}>
                      <TableCell className="max-w-xs">
                        <div className="flex items-center gap-1.5">
                          <KindIcon kind={e.kind} />
                          <span className="truncate font-medium" title={formatEvidenceTitle(e.title)}>{formatEvidenceTitle(e.title)}</span>
                        </div>
                        {e.description && <div className="truncate text-xs text-slate-500" title={e.description}>{e.description}</div>}
                      </TableCell>
                      <TableCell className="max-w-[12rem]">
                        {e.kind === 'link' && e.url ? (
                          <ExternalEvidenceLink url={e.url} className="max-w-full text-xs" />
                        ) : (
                          <>
                            <div className="truncate text-xs" title={e.fileName}>{e.fileName}</div>
                            <div className="text-xs text-slate-500">{formatBytes(e.sizeBytes)}</div>
                          </>
                        )}
                      </TableCell>
                      <TableCell className="text-xs text-slate-700">
                        {e.validFrom || e.validUntil ? (
                          <span className="whitespace-nowrap">
                            {formatDate(e.validFrom)} → {formatDate(e.validUntil)}
                            {expired && <Badge variant="danger" className="ml-1">Expired</Badge>}
                          </span>
                        ) : (
                          <span className="text-slate-400">Not provided</span>
                        )}
                      </TableCell>
                      <TableCell>
                        <div className="flex max-w-[10rem] flex-wrap gap-1">
                          {e.requirementIds.slice(0, 3).map((id) => (
                            <Link key={id} to={`/requirements/${encodeURIComponent(id)}`} className="rounded bg-slate-100 px-1 font-mono text-[11px] hover:bg-slate-200" title={id}>
                              {id.length > 10 ? `${id.slice(0, 8)}…` : id}
                            </Link>
                          ))}
                          {e.requirementIds.length > 3 && <span className="text-xs text-slate-500">+{e.requirementIds.length - 3}</span>}
                        </div>
                      </TableCell>
                      <TableCell className="text-xs text-slate-500">
                        <div>{formatDateTime(e.createdAt)}</div>
                        <div>{e.uploadedBy}</div>
                      </TableCell>
                      <TableCell>
                        <div className="flex items-center justify-end">
                          <EvidencePrimaryAction evidence={e} size="icon" />
                        </div>
                      </TableCell>
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

      <UploadEvidenceDialog open={uploadOpen} onClose={() => setUploadOpen(false)} />
    </div>
  )
}
