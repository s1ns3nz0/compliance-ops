import { useEffect, useMemo, useState, type FormEvent } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { FileText, Link2, X } from 'lucide-react'
import { api } from '@/api'
import type { EvidenceKind, Requirement } from '@/types'
import { Button } from '@/components/ui/button'
import { Dialog } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { Badge } from '@/components/ui/badge'
import { ErrorState, Spinner } from '@/components/ui/feedback'
import { cn, isHttpUrl, urlHost } from '@/lib/utils'

interface Props {
  open: boolean
  onClose: () => void
  /** Pre-linked requirements; when `lockRequirements` is true the picker is hidden. */
  initialRequirements?: Pick<Requirement, 'id' | 'controlId' | 'title'>[]
  lockRequirements?: boolean
}

type ReqRef = Pick<Requirement, 'id' | 'controlId' | 'title'>

function useDebounced<T>(value: T, ms: number): T {
  const [v, setV] = useState(value)
  useEffect(() => {
    const t = setTimeout(() => setV(value), ms)
    return () => clearTimeout(t)
  }, [value, ms])
  return v
}

/** Add evidence: either upload a file (multipart) or register an external link (JSON). */
export function UploadEvidenceDialog({ open, onClose, initialRequirements = [], lockRequirements = false }: Props) {
  const qc = useQueryClient()
  const [kind, setKind] = useState<EvidenceKind>('file')
  const [file, setFile] = useState<File | null>(null)
  const [url, setUrl] = useState('')
  const [title, setTitle] = useState('')
  const [description, setDescription] = useState('')
  const [validFrom, setValidFrom] = useState('')
  const [validUntil, setValidUntil] = useState('')
  const [selected, setSelected] = useState<ReqRef[]>(initialRequirements)
  const [search, setSearch] = useState('')
  const debounced = useDebounced(search, 250)
  const fileInputKey = useMemo(() => (open ? Date.now() : 0), [open])

  useEffect(() => {
    if (open) {
      setKind('file')
      setSelected(initialRequirements)
      setFile(null)
      setUrl('')
      setTitle('')
      setDescription('')
      setValidFrom('')
      setValidUntil('')
      setSearch('')
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])

  const searchQuery = useQuery({
    queryKey: ['requirements', 'picker', debounced],
    queryFn: () => api.listRequirements({ q: debounced, limit: 20 }),
    enabled: open && !lockRequirements && debounced.trim().length > 0,
  })

  const mutation = useMutation({
    mutationFn: () => {
      const common = {
        title: title.trim(),
        description: description.trim() || undefined,
        validFrom: validFrom || undefined,
        validUntil: validUntil || undefined,
        requirementIds: selected.map((r) => r.id),
      }
      return kind === 'link'
        ? api.createLinkEvidence({ ...common, url: url.trim() })
        : api.uploadEvidence({ ...common, file: file! })
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['evidence'] })
      qc.invalidateQueries({ queryKey: ['requirement'] })
      qc.invalidateQueries({ queryKey: ['dashboard'] })
      qc.invalidateQueries({ queryKey: ['audit'] })
      onClose()
    },
  })

  const urlTrimmed = url.trim()
  const urlValid = urlTrimmed.length > 0 && isHttpUrl(urlTrimmed)
  const sourceOk = kind === 'link' ? urlValid : !!file
  const linked = selected.length > 0
  const canSubmit = sourceOk && title.trim().length > 0 && linked && !mutation.isPending

  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (canSubmit) mutation.mutate()
  }

  const toggle = (r: ReqRef) => {
    setSelected((prev) => (prev.some((p) => p.id === r.id) ? prev.filter((p) => p.id !== r.id) : [...prev, r]))
  }

  const switchKind = (k: EvidenceKind) => {
    setKind(k)
    mutation.reset()
  }

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="Add evidence"
      description={
        lockRequirements
          ? 'Upload a file or register an external link as evidence for this control.'
          : 'Upload a file or register an external link, then link it to one or more requirements.'
      }
      footer={
        <>
          <Button variant="outline" onClick={onClose} disabled={mutation.isPending}>
            Cancel
          </Button>
          <Button type="submit" form="upload-evidence-form" disabled={!canSubmit}>
            {mutation.isPending && <Spinner />} {kind === 'link' ? 'Add link' : 'Upload'}
          </Button>
        </>
      }
    >
      <form id="upload-evidence-form" onSubmit={submit} className="space-y-4">
        <div className="space-y-1.5">
          <span className="text-sm font-medium leading-none" id="ev-kind-label">Evidence type</span>
          <div role="radiogroup" aria-labelledby="ev-kind-label" className="inline-flex rounded-md border border-slate-300 p-0.5 text-sm">
            {(
              [
                { k: 'file', label: 'File', Icon: FileText },
                { k: 'link', label: 'Link', Icon: Link2 },
              ] as const
            ).map(({ k, label, Icon }) => (
              <button
                key={k}
                type="button"
                role="radio"
                aria-checked={kind === k}
                onClick={() => switchKind(k)}
                className={cn(
                  'inline-flex items-center gap-1.5 rounded px-3 py-1 transition-colors',
                  kind === k ? 'bg-slate-900 text-white' : 'text-slate-700 hover:bg-slate-100',
                )}
              >
                <Icon className="size-4" aria-hidden /> {label}
              </button>
            ))}
          </div>
        </div>

        {kind === 'file' ? (
          <div className="space-y-1.5">
            <Label htmlFor="ev-file">File</Label>
            <Input
              key={fileInputKey}
              id="ev-file"
              type="file"
              required
              onChange={(e) => {
                const f = e.target.files?.[0] ?? null
                setFile(f)
                if (f && !title) setTitle(f.name.replace(/\.[^.]+$/, ''))
              }}
            />
          </div>
        ) : (
          <div className="space-y-1.5">
            <Label htmlFor="ev-url">URL</Label>
            <Input
              id="ev-url"
              type="url"
              required
              inputMode="url"
              placeholder="https://…"
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              onBlur={() => {
                if (urlValid && !title) setTitle(urlHost(urlTrimmed))
              }}
              aria-invalid={urlTrimmed.length > 0 && !urlValid}
              aria-describedby="ev-url-hint"
            />
            <p id="ev-url-hint" className={cn('text-xs', urlTrimmed.length > 0 && !urlValid ? 'text-red-700' : 'text-slate-500')}>
              {urlTrimmed.length > 0 && !urlValid ? 'Enter an absolute http(s) URL.' : 'Absolute http(s) URL of the external document or system.'}
            </p>
          </div>
        )}
        <div className="space-y-1.5">
          <Label htmlFor="ev-title">Title</Label>
          <Input id="ev-title" required value={title} onChange={(e) => setTitle(e.target.value)} />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="ev-desc">Description</Label>
          <Textarea id="ev-desc" value={description} onChange={(e) => setDescription(e.target.value)} />
        </div>
        <div className="grid grid-cols-2 gap-3">
          <div className="space-y-1.5">
            <Label htmlFor="ev-from">Valid from</Label>
            <Input id="ev-from" type="date" value={validFrom} onChange={(e) => setValidFrom(e.target.value)} />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="ev-until">Valid until</Label>
            <Input id="ev-until" type="date" value={validUntil} onChange={(e) => setValidUntil(e.target.value)} />
          </div>
        </div>

        <div className="space-y-2">
          <Label htmlFor="ev-req-search">Linked requirements ({selected.length})</Label>
          {selected.length > 0 && (
            <div className="flex flex-wrap gap-1.5">
              {selected.map((r) => (
                <Badge key={r.id} variant="secondary" className="gap-1 pr-1">
                  <span className="font-mono">{r.controlId}</span>
                  {!lockRequirements && (
                    <button
                      type="button"
                      className="rounded p-0.5 hover:bg-slate-300"
                      aria-label={`Remove ${r.controlId}`}
                      onClick={() => toggle(r)}
                    >
                      <X className="size-3" />
                    </button>
                  )}
                </Badge>
              ))}
            </div>
          )}
          {!lockRequirements && (
            <>
              <Input
                id="ev-req-search"
                placeholder="Search requirements by control id or title…"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
              />
              {searchQuery.isFetching && <p className="text-xs text-slate-500">Searching…</p>}
              {searchQuery.isError && <ErrorState error={searchQuery.error} />}
              {searchQuery.data && (
                <ul className="max-h-48 divide-y divide-slate-100 overflow-y-auto rounded-md border border-slate-200 text-sm">
                  {searchQuery.data.items.length === 0 && <li className="px-3 py-2 text-slate-500">No matches.</li>}
                  {searchQuery.data.items.map((r) => {
                    const checked = selected.some((s) => s.id === r.id)
                    return (
                      <li key={r.id}>
                        <label className="flex cursor-pointer items-center gap-2 px-3 py-1.5 hover:bg-slate-50">
                          <input type="checkbox" checked={checked} onChange={() => toggle(r)} />
                          <span className="font-mono text-xs text-slate-600">{r.controlId}</span>
                          <span className="truncate">{r.title}</span>
                        </label>
                      </li>
                    )
                  })}
                </ul>
              )}
            </>
          )}
          {selected.length === 0 && <p className="text-xs text-amber-700">Select at least one requirement.</p>}
        </div>
        {mutation.isError && <ErrorState error={mutation.error} />}
      </form>
    </Dialog>
  )
}
