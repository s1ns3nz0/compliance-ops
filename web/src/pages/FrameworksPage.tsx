import { useRef, useState, type DragEvent, type FormEvent } from 'react'
import { Link } from 'react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Check, Copy, Download, ExternalLink, FileJson, Pencil, RefreshCw, Trash2, Upload, X } from 'lucide-react'
import { ApiError, api, errorMessage } from '@/api'
import type { Framework, FrameworkImportInput, ImportResult, OscalUpload } from '@/types'
import { PageHeader } from '@/components/Layout'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Dialog } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { EmptyState, ErrorState, LoadingState, Spinner } from '@/components/ui/feedback'
import { cn, formatBytes, formatDate, formatDateTime } from '@/lib/utils'

const MAX_OSCAL_UPLOAD_BYTES = 10 * 1024 * 1024

function isImportable(type: string) {
  const normalized = type.toLowerCase()
  return normalized === 'catalog' || normalized === 'profile'
}

export function FrameworksPage() {
  const [importOpen, setImportOpen] = useState(false)
  const q = useQuery({ queryKey: ['frameworks'], queryFn: api.listFrameworks })

  return (
    <div>
      <PageHeader
        title="Frameworks"
        subtitle="Imported OSCAL catalogs and profiles with requirement counts."
        actions={
          <>
            <Button variant="outline" size="sm" onClick={() => q.refetch()} disabled={q.isFetching} aria-label="Refresh">
              <RefreshCw className={q.isFetching ? 'animate-spin' : ''} />
            </Button>
            <Button onClick={() => setImportOpen(true)}>
              <Download /> Import OSCAL
            </Button>
          </>
        }
      />
      <Card>
        <CardContent className="p-0">
          {q.isPending && <LoadingState />}
          {q.isError && <ErrorState error={q.error} className="m-4" />}
          {q.data && q.data.items.length === 0 && <EmptyState>No frameworks imported yet.</EmptyState>}
          {q.data && q.data.items.length > 0 && (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Framework</TableHead>
                  <TableHead>Type</TableHead>
                  <TableHead>OSCAL document</TableHead>
                  <TableHead className="text-right">Requirements</TableHead>
                  <TableHead>Last modified</TableHead>
                  <TableHead>Imported</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {q.data.items.map((f) => (
                  <TableRow key={f.id}>
                    <TableCell className="max-w-md"><FrameworkName framework={f} /></TableCell>
                    <TableCell><Badge variant="outline">{f.type}</Badge></TableCell>
                    <TableCell className="font-mono text-xs text-slate-600">{f.oscalDocumentId}</TableCell>
                    <TableCell className="text-right tabular-nums">{f.requirementCount}</TableCell>
                    <TableCell className="text-slate-600">{formatDate(f.lastModified)}</TableCell>
                    <TableCell className="text-slate-600">{formatDateTime(f.importedAt)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
      <ImportDialog open={importOpen} onClose={() => setImportOpen(false)} />
    </div>
  )
}

function FrameworkName({ framework: f }: { framework: Framework }) {
  const qc = useQueryClient()
  const [editing, setEditing] = useState(false)
  const [value, setValue] = useState(f.shortName)
  const mutation = useMutation({
    mutationFn: (shortName: string) => api.patchFramework(f.id, { shortName }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['frameworks'] })
      qc.invalidateQueries({ queryKey: ['dashboard'] })
      qc.invalidateQueries({ queryKey: ['audit'] })
      setEditing(false)
    },
  })
  const start = () => {
    setValue(f.shortName)
    mutation.reset()
    setEditing(true)
  }
  const submit = (e: FormEvent) => {
    e.preventDefault()
    const v = value.trim()
    if (!v) return
    if (v === f.shortName) {
      setEditing(false)
      return
    }
    mutation.mutate(v)
  }

  if (editing) {
    return (
      <form onSubmit={submit} className="flex items-center gap-1.5">
        <Input aria-label={`Short name for ${f.title}`} value={value} autoFocus onChange={(e) => setValue(e.target.value)} onKeyDown={(e) => e.key === 'Escape' && setEditing(false)} className="h-8 max-w-xs" disabled={mutation.isPending} />
        <Button type="submit" size="icon" variant="ghost" aria-label="Save short name" disabled={!value.trim() || mutation.isPending}>{mutation.isPending ? <Spinner /> : <Check />}</Button>
        <Button size="icon" variant="ghost" aria-label="Cancel rename" onClick={() => setEditing(false)} disabled={mutation.isPending}><X /></Button>
        {mutation.isError && <span className="text-xs text-red-700">{errorMessage(mutation.error)}</span>}
      </form>
    )
  }

  return (
    <div className="group min-w-0">
      <div className="flex items-center gap-1.5">
        <Link to={`/frameworks/${encodeURIComponent(f.id)}`} className="truncate font-medium hover:underline" title={f.title}>{f.shortName || f.title}</Link>
        <Button size="icon" variant="ghost" className="h-6 w-6 text-slate-400 hover:text-slate-900" aria-label={`Rename ${f.shortName || f.title}`} onClick={start}><Pencil className="!size-3.5" /></Button>
      </div>
      {f.shortName && f.shortName !== f.title && <div className="truncate text-xs text-slate-500" title={f.title}>{f.title}</div>}
    </div>
  )
}

function ImportSummary({ results }: { results: ImportResult[] | null }) {
  if (!results) return null
  return (
    <div className="rounded-md border border-emerald-200 bg-emerald-50 p-3 text-sm text-emerald-900">
      <p className="font-medium">Import finished</p>
      <ul className="mt-1 space-y-0.5">
        {results.length === 0 && <li>No documents were imported.</li>}
        {results.map((r) => (
          <li key={r.framework.id}>
            {r.framework.shortName || r.framework.title}: <span className="tabular-nums">{r.created} created, {r.updated} updated</span>
            <span className="text-emerald-700"> ({r.framework.requirementCount} requirements)</span>
          </li>
        ))}
      </ul>
    </div>
  )
}

function ImportDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const qc = useQueryClient()
  const fileInput = useRef<HTMLInputElement>(null)
  const [results, setResults] = useState<ImportResult[] | null>(null)
  const [busyImport, setBusyImport] = useState<string | null>(null)
  const [uploadError, setUploadError] = useState<string | null>(null)
  const [uploadNotice, setUploadNotice] = useState<string | null>(null)
  const [dragging, setDragging] = useState(false)
  const [rawUploadId, setRawUploadId] = useState<string | null>(null)
  const [copiedSha, setCopiedSha] = useState<string | null>(null)

  const sources = useQuery({ queryKey: ['oscal-sources'], queryFn: api.listOscalSources, enabled: open })
  const uploads = useQuery({ queryKey: ['oscal-uploads'], queryFn: api.listOscalUploads, enabled: open })
  const raw = useQuery({ queryKey: ['oscal-upload', rawUploadId], queryFn: () => api.getOscalUpload(rawUploadId!), enabled: rawUploadId !== null })

  const importMutation = useMutation({
    mutationFn: (input: FrameworkImportInput) => api.importFrameworks(input),
    onMutate: (input) => setBusyImport(input.documentId ?? input.uploadId),
    onSuccess: (data) => {
      setResults(data.items)
      qc.invalidateQueries({ queryKey: ['frameworks'] })
      qc.invalidateQueries({ queryKey: ['requirements'] })
      qc.invalidateQueries({ queryKey: ['oscal-uploads'] })
      qc.invalidateQueries({ queryKey: ['dashboard'] })
    },
    onSettled: () => setBusyImport(null),
  })

  const uploadMutation = useMutation({
    mutationFn: api.uploadOscal,
    onSuccess: (item) => {
      setUploadError(null)
      setUploadNotice(`Uploaded ${item.title} (${item.type}).`)
      qc.invalidateQueries({ queryKey: ['oscal-uploads'] })
    },
    onError: (error) => {
      qc.invalidateQueries({ queryKey: ['oscal-uploads'] })
      setUploadNotice(null)
      setUploadError(error instanceof ApiError && error.status === 409 ? 'Already uploaded.' : errorMessage(error))
    },
  })

  const deleteMutation = useMutation({
    mutationFn: api.deleteOscalUpload,
    onSuccess: () => qc.invalidateQueries({ queryKey: ['oscal-uploads'] }),
  })

  const chooseFile = (file?: File) => {
    setUploadError(null)
    setUploadNotice(null)
    uploadMutation.reset()
    if (!file) return
    if (file.size > MAX_OSCAL_UPLOAD_BYTES) {
      setUploadError('The file exceeds the 10 MiB limit.')
      return
    }
    uploadMutation.mutate(file)
  }

  const onDrop = (event: DragEvent<HTMLDivElement>) => {
    event.preventDefault()
    setDragging(false)
    const files = event.dataTransfer.files
    if (files.length !== 1) {
      setUploadError('Choose exactly one JSON file.')
      return
    }
    chooseFile(files[0])
  }

  const copySha = async (upload: OscalUpload) => {
    await navigator.clipboard.writeText(upload.sha256)
    setCopiedSha(upload.id)
    window.setTimeout(() => setCopiedSha((id) => id === upload.id ? null : id), 2000)
  }

  const removeUpload = (upload: OscalUpload) => {
    if (!window.confirm(`Delete uploaded OSCAL document “${upload.title}”?`)) return
    deleteMutation.mutate(upload.id)
  }

  const close = () => {
    setResults(null)
    setUploadError(null)
    setUploadNotice(null)
    setRawUploadId(null)
    importMutation.reset()
    uploadMutation.reset()
    onClose()
  }

  return (
    <>
      <Dialog open={open} onClose={close} title="Import OSCAL" description="Import catalogs and profiles from configured sources or uploaded OSCAL JSON." className="max-w-5xl" footer={<Button variant="outline" onClick={close}>Close</Button>}>
        <div className="space-y-8">
          <section aria-labelledby="configured-sources-heading">
            <h3 id="configured-sources-heading" className="mb-3 font-semibold">Configured sources</h3>
            {sources.isPending && <LoadingState label="Loading configured sources…" />}
            {sources.isError && <ErrorState error={sources.error} />}
            {sources.data?.items.length === 0 && <EmptyState>No OSCAL sources are configured.</EmptyState>}
            <div className="space-y-3">
              {sources.data?.items.map((source) => (
                <div key={source.id} className="rounded-lg border border-slate-200">
                  <div className="flex flex-wrap items-center gap-2 border-b border-slate-100 bg-slate-50 px-3 py-2">
                    <a href={source.url} target="_blank" rel="noopener noreferrer" className="min-w-0 flex-1 truncate text-sm font-medium hover:underline" title={source.url}>{source.url} <ExternalLink className="inline size-3" aria-hidden /></a>
                    <Badge variant={source.status === 'available' ? 'success' : 'danger'}>{source.status}</Badge>
                  </div>
                  {source.status === 'unavailable' ? (
                    <div className="px-3 py-3 text-sm text-red-700">This source is unavailable{source.errorCode ? ` (${source.errorCode})` : ''}.</div>
                  ) : source.documents.length === 0 ? (
                    <div className="px-3 py-3 text-sm text-slate-500">No documents found.</div>
                  ) : (
                    <Table>
                      <TableHeader><TableRow><TableHead>Document</TableHead><TableHead>Type</TableHead><TableHead>Version</TableHead><TableHead className="text-right">Controls</TableHead><TableHead className="w-28" /></TableRow></TableHeader>
                      <TableBody>
                        {source.documents.map((document) => (
                          <TableRow key={document.id}>
                            <TableCell><div className="font-medium">{document.title}</div><div className="font-mono text-xs text-slate-500">{document.id}</div></TableCell>
                            <TableCell><Badge variant="outline">{document.type}</Badge></TableCell>
                            <TableCell>{document.version || 'No version'}</TableCell>
                            <TableCell className="text-right tabular-nums">{document.controlCount}</TableCell>
                            <TableCell className="text-right">
                              {isImportable(document.type) ? <Button size="sm" variant="secondary" onClick={() => importMutation.mutate({ documentId: document.id })} disabled={importMutation.isPending}>{busyImport === document.id && <Spinner />} Import</Button> : <span className="text-xs text-slate-500">View only</span>}
                            </TableCell>
                          </TableRow>
                        ))}
                      </TableBody>
                    </Table>
                  )}
                </div>
              ))}
            </div>
          </section>

          <section aria-labelledby="upload-oscal-heading">
            <h3 id="upload-oscal-heading" className="mb-3 font-semibold">Upload OSCAL JSON</h3>
            <div
              onDragEnter={(e) => { e.preventDefault(); setDragging(true) }}
              onDragOver={(e) => e.preventDefault()}
              onDragLeave={() => setDragging(false)}
              onDrop={onDrop}
              className={cn('rounded-lg border-2 border-dashed px-6 py-7 text-center transition-colors', dragging ? 'border-slate-500 bg-slate-50' : 'border-slate-300')}
            >
              <Upload className="mx-auto mb-2 size-6 text-slate-500" aria-hidden />
              <p className="text-sm font-medium">Drop one OSCAL JSON file here</p>
              <p className="mt-1 text-xs text-slate-500">JSON only, maximum 10 MiB. The server validates the OSCAL content.</p>
              <input ref={fileInput} type="file" accept="application/json,.json" className="sr-only" aria-label="Choose OSCAL JSON file" onChange={(e) => { chooseFile(e.target.files?.[0]); e.currentTarget.value = '' }} />
              <Button className="mt-3" size="sm" variant="outline" onClick={() => fileInput.current?.click()} disabled={uploadMutation.isPending}>{uploadMutation.isPending ? <Spinner /> : <FileJson />} {uploadMutation.isPending ? 'Uploading…' : 'Choose file'}</Button>
            </div>
            {uploadError && <div role="alert" className="mt-2 text-sm text-red-700">{uploadError}</div>}
            {uploadNotice && <div role="status" className="mt-2 text-sm text-emerald-700">{uploadNotice}</div>}
          </section>

          <section aria-labelledby="uploaded-documents-heading">
            <div className="mb-3 flex items-center justify-between gap-2">
              <h3 id="uploaded-documents-heading" className="font-semibold">Uploaded documents</h3>
              <Button size="sm" variant="ghost" aria-label="Refresh uploaded documents" onClick={() => uploads.refetch()} disabled={uploads.isFetching}><RefreshCw className={uploads.isFetching ? 'animate-spin' : ''} /></Button>
            </div>
            {uploads.isPending && <LoadingState label="Loading uploaded documents…" />}
            {uploads.isError && <ErrorState error={uploads.error} />}
            {uploads.data?.items.length === 0 && <EmptyState>No OSCAL documents uploaded yet.</EmptyState>}
            {uploads.data && uploads.data.items.length > 0 && (
              <div className="overflow-x-auto rounded-lg border border-slate-200">
                <Table>
                  <TableHeader><TableRow><TableHead>Document</TableHead><TableHead>Type / version</TableHead><TableHead>SHA-256</TableHead><TableHead>Size</TableHead><TableHead>Uploaded</TableHead><TableHead>State</TableHead><TableHead className="text-right">Actions</TableHead></TableRow></TableHeader>
                  <TableBody>
                    {uploads.data.items.map((upload) => (
                      <TableRow key={upload.id}>
                        <TableCell className="max-w-64"><div className="truncate font-medium" title={upload.title}>{upload.title}</div></TableCell>
                        <TableCell><Badge variant="outline">{upload.type}</Badge><div className="mt-1 text-xs text-slate-500">{upload.version || 'No version'}</div></TableCell>
                        <TableCell><button type="button" onClick={() => void copySha(upload)} className="inline-flex items-center gap-1 font-mono text-xs hover:underline" title={upload.sha256} aria-label={`Copy SHA-256 for ${upload.title}`}>{upload.sha256.slice(0, 12)}… {copiedSha === upload.id ? <Check className="size-3 text-emerald-600" /> : <Copy className="size-3" />}</button></TableCell>
                        <TableCell className="whitespace-nowrap">{formatBytes(upload.sizeBytes)}</TableCell>
                        <TableCell className="whitespace-nowrap text-slate-600">{formatDateTime(upload.uploadedAt)}</TableCell>
                        <TableCell>{upload.importedFrameworkId ? <Badge variant="success">Imported</Badge> : <Badge variant="muted">Not imported</Badge>}</TableCell>
                        <TableCell>
                          <div className="flex justify-end gap-1">
                            <Button size="sm" variant="ghost" onClick={() => setRawUploadId(upload.id)}>View raw</Button>
                            {isImportable(upload.type) ? <Button size="sm" variant="secondary" onClick={() => importMutation.mutate({ uploadId: upload.id })} disabled={importMutation.isPending}>{busyImport === upload.id && <Spinner />} Import</Button> : <span className="self-center px-2 text-xs text-slate-500">View only</span>}
                            <span title={upload.importedFrameworkId ? 'Referenced by an imported framework' : undefined}>
                              <Button size="icon" variant="ghost" aria-label={`Delete ${upload.title}`} onClick={() => removeUpload(upload)} disabled={Boolean(upload.importedFrameworkId) || deleteMutation.isPending}><Trash2 className="text-red-600" /></Button>
                            </span>
                          </div>
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            )}
            {deleteMutation.isError && <ErrorState error={deleteMutation.error} className="mt-3" />}
          </section>

          {importMutation.isError && <ErrorState error={importMutation.error} />}
          <ImportSummary results={results} />
        </div>
      </Dialog>

      <Dialog open={rawUploadId !== null} onClose={() => setRawUploadId(null)} title={raw.data?.title || 'Raw OSCAL JSON'} description="Exact uploaded document stored by the server." className="max-w-4xl" footer={<Button variant="outline" onClick={() => setRawUploadId(null)}>Close</Button>}>
        {raw.isPending && <LoadingState label="Loading raw OSCAL JSON…" />}
        {raw.isError && <ErrorState error={raw.error} />}
        {raw.data && <pre className="max-h-[65vh] overflow-auto rounded-md bg-slate-950 p-4 text-xs leading-relaxed text-slate-100"><code>{JSON.stringify(raw.data.document, null, 2)}</code></pre>}
      </Dialog>
    </>
  )
}
