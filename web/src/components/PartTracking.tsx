import { useCallback, useEffect, useRef, useState, type KeyboardEvent } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { AlertCircle, Check, Pencil, X } from 'lucide-react'
import { api, errorMessage } from '@/api'
import {
  REQUIREMENT_STATUSES,
  type RequirementDetail,
  type RequirementStatus,
  type TrackablePart,
  type TrackablePartPatch,
  type TrackablePartPatchResult,
} from '@/types'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'
import { ErrorState, Spinner } from '@/components/ui/feedback'
import { ImplementationMarkdown } from '@/components/ImplementationMarkdown'
import { OscalProse } from '@/components/OscalProse'
import { ScopeCategoryMultiSelect } from '@/components/ScopeCategoryMultiSelect'
import { scopeRemovalError } from '@/lib/scopeCategoryLogic'
import { STATUS_LABEL, cn, formatDateTime, isOverdue, relativeTime, toDateInput } from '@/lib/utils'

// ---------------------------------------------------------------------------
// Shared mutation: PATCH /v1/requirements/{id}/parts/{partId}
// ---------------------------------------------------------------------------

/**
 * Applies the response of a part PATCH to the requirement detail query cache: the returned part replaces
 * the matching trackable part and the returned requirement (effective status, derived owner/due, updatedAt)
 * is merged over the cached detail so the header badge and roll-up refresh without a refetch.
 */
function applyPartResult(
  qc: ReturnType<typeof useQueryClient>,
  requirementId: string,
  { part, requirement }: TrackablePartPatchResult,
) {
  qc.setQueryData<RequirementDetail>(['requirement', requirementId], (old) => {
    if (!old) return old
    const parts = old.trackableParts ?? []
    const trackableParts = parts.some((p) => p.partId === part.partId)
      ? parts.map((p) => (p.partId === part.partId ? part : p))
      : [...parts, part]
    return { ...old, ...requirement, trackableParts }
  })
  qc.invalidateQueries({ queryKey: ['requirements'] })
  qc.invalidateQueries({ queryKey: ['dashboard'] })
  qc.invalidateQueries({ queryKey: ['audit'] })
}

function usePartMutation(requirementId: string, partId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (patch: TrackablePartPatch) => api.patchRequirementPart(requirementId, partId, patch),
    onSuccess: (res) => applyPartResult(qc, requirementId, res),
  })
}

// ---------------------------------------------------------------------------
// Tracking row (auto-save)
// ---------------------------------------------------------------------------

type SaveState = 'idle' | 'saving' | 'saved' | 'error'

function SaveIndicator({ state, error }: { state: SaveState; error?: unknown }) {
  if (state === 'idle') return <span className="w-16" aria-hidden />
  return (
    <span
      role="status"
      className={cn(
        'inline-flex w-16 items-center gap-1 text-[11px]',
        state === 'saving' && 'text-slate-500',
        state === 'saved' && 'text-emerald-700',
        state === 'error' && 'text-red-700',
      )}
      title={state === 'error' ? errorMessage(error) : undefined}
    >
      {state === 'saving' && (
        <>
          <Spinner className="size-3" /> Saving…
        </>
      )}
      {state === 'saved' && (
        <>
          <Check className="size-3" aria-hidden /> Saved
        </>
      )}
      {state === 'error' && (
        <>
          <AlertCircle className="size-3" aria-hidden /> Failed
        </>
      )}
    </span>
  )
}

const AUTOSAVE_DEBOUNCE_MS = 600

/** Compact status · owner · due row for one statement part. Saves on change (select/date) or after a typing pause (owner). */
export function PartTrackingRow({ requirementId, part }: { requirementId: string; part: TrackablePart }) {
  const mutation = usePartMutation(requirementId, part.partId)
  const [status, setStatus] = useState<RequirementStatus>(part.status)
  const [scopes, setScopes] = useState(part.scopes ?? [])
  const [owner, setOwner] = useState(part.owner ?? '')
  const [dueDate, setDueDate] = useState(toDateInput(part.dueDate))
  const [state, setState] = useState<SaveState>('idle')
  const [scopeError, setScopeError] = useState('')
  const ownerDirty = useRef(false)
  const ownerTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const savedTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const saveQueue = useRef<Promise<void>>(Promise.resolve())
  const pendingSaves = useRef(0)

  // Keep optimistic controls steady while queued writes are applying server responses.
  useEffect(() => {
    if (pendingSaves.current === 0) {
      setStatus(part.status)
      setScopes(part.scopes ?? [])
      setDueDate(toDateInput(part.dueDate))
    }
    if (!ownerDirty.current) setOwner(part.owner ?? '')
  }, [part.status, part.scopes, part.dueDate, part.owner, part.updatedAt])

  useEffect(
    () => () => {
      if (ownerTimer.current) clearTimeout(ownerTimer.current)
      if (savedTimer.current) clearTimeout(savedTimer.current)
    },
    [],
  )

  const save = useCallback(
    (patch: TrackablePartPatch, onError?: () => void) => {
      pendingSaves.current += 1
      setState('saving')
      const run = async () => {
        try {
          await mutation.mutateAsync(patch)
          pendingSaves.current -= 1
          if (pendingSaves.current > 0) return
          setState('saved')
          if (savedTimer.current) clearTimeout(savedTimer.current)
          savedTimer.current = setTimeout(() => setState((s) => (s === 'saved' ? 'idle' : s)), 2500)
        } catch {
          pendingSaves.current -= 1
          setState('error')
          onError?.()
        }
      }
      saveQueue.current = saveQueue.current.then(run, run)
    },
    [mutation],
  )

  const flushOwner = useCallback(
    (value: string) => {
      if (ownerTimer.current) {
        clearTimeout(ownerTimer.current)
        ownerTimer.current = null
      }
      ownerDirty.current = false
      if (value === (part.owner ?? '')) return
      save({ owner: value })
    },
    [part.owner, save],
  )

  const onOwnerChange = (value: string) => {
    setOwner(value)
    ownerDirty.current = true
    if (ownerTimer.current) clearTimeout(ownerTimer.current)
    ownerTimer.current = setTimeout(() => flushOwner(value), AUTOSAVE_DEBOUNCE_MS)
  }

  const onStatusChange = (value: RequirementStatus) => {
    if (value === 'implemented' && scopes.length === 0) {
      setScopeError('Choose at least one scope before marking this part implemented.')
      return
    }
    setScopeError('')
    setStatus(value)
    save({ status: value }, () => setStatus(part.status))
  }

  const onScopesChange = (nextScopes: typeof scopes) => {
    const guardError = scopeRemovalError(status, nextScopes.length)
    if (guardError) {
      setScopeError(guardError)
      return
    }
    const previous = scopes
    setScopeError('')
    setScopes(nextScopes)
    save({ scopeCategoryIds: nextScopes.map((scope) => scope.id) }, () => setScopes(previous))
  }

  const onDueChange = (value: string) => {
    setDueDate(value)
    save({ dueDate: value || null })
  }

  const overdue = isOverdue(dueDate || null, status)
  const idBase = `part-${part.partId}`

  return (
    <div className="space-y-2 text-xs">
      <div className="space-y-1">
        <span id={`${idBase}-scope-label`} className="block text-slate-500">Scope</span>
        <ScopeCategoryMultiSelect selected={scopes} onChange={onScopesChange} invalid={Boolean(scopeError)} />
      </div>
      {scopeError && <p role="alert" className="text-xs text-red-700">{scopeError}</p>}
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5">
        <label className="flex items-center gap-1.5">
          <span className="text-slate-500">Status</span>
        <Select
          id={`${idBase}-status`}
          value={status}
          onChange={(e) => onStatusChange(e.target.value as RequirementStatus)}
          className="w-36 [&>select]:h-7 [&>select]:text-xs"
          aria-label="Part status"
        >
          {REQUIREMENT_STATUSES.map((s) => (
            <option key={s} value={s}>
              {STATUS_LABEL[s]}
            </option>
          ))}
        </Select>
      </label>
      <label className="flex items-center gap-1.5">
        <span className="text-slate-500">Owner</span>
        <Input
          id={`${idBase}-owner`}
          value={owner}
          onChange={(e) => onOwnerChange(e.target.value)}
          onBlur={() => {
            if (ownerDirty.current) flushOwner(owner)
          }}
          placeholder="Team or person"
          className="h-7 w-40 text-xs"
          aria-label="Part owner"
        />
      </label>
      <label className="flex items-center gap-1.5">
        <span className="text-slate-500">Due</span>
        <Input
          id={`${idBase}-due`}
          type="date"
          value={dueDate}
          onChange={(e) => onDueChange(e.target.value)}
          className={cn('h-7 w-36 text-xs', overdue && 'border-red-300 text-red-700')}
          aria-label="Part due date"
        />
        <button
          type="button"
          onClick={() => onDueChange('')}
          disabled={!dueDate}
          aria-label="Clear due date"
          className="rounded p-0.5 text-slate-400 hover:bg-slate-100 hover:text-slate-700 disabled:invisible"
        >
          <X className="size-3.5" aria-hidden />
        </button>
      </label>
      <SaveIndicator state={state} error={mutation.error} />
        {part.updatedAt && (
          <span className="ml-auto text-[11px] text-slate-400" title={formatDateTime(part.updatedAt)}>
            updated {relativeTime(part.updatedAt)}
          </span>
        )}
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Description (Markdown) editor
// ---------------------------------------------------------------------------

const PLACEHOLDER = 'Describe this implementation…'

export function PartDescription({ requirementId, part }: { requirementId: string; part: TrackablePart }) {
  const mutation = usePartMutation(requirementId, part.partId)
  const [editing, setEditing] = useState(false)
  const [tab, setTab] = useState<'write' | 'preview'>('write')
  const [draft, setDraft] = useState(part.description ?? '')
  const textareaRef = useRef<HTMLTextAreaElement>(null)

  const original = part.description ?? ''
  const dirty = draft !== original

  const open = () => {
    setDraft(original)
    setTab('write')
    mutation.reset()
    setEditing(true)
  }

  const close = () => {
    setEditing(false)
    setDraft(original)
    mutation.reset()
  }

  const cancel = () => {
    if (dirty && !window.confirm('Discard unsaved changes to this description?')) return
    close()
  }

  const save = () => {
    if (!dirty) {
      close()
      return
    }
    mutation.mutate({ description: draft }, { onSuccess: () => setEditing(false) })
  }

  // Autosize the textarea to its content (min 6 rows).
  useEffect(() => {
    const el = textareaRef.current
    if (!el || !editing || tab !== 'write') return
    el.style.height = 'auto'
    el.style.height = `${Math.max(el.scrollHeight, 6 * 24)}px`
  }, [draft, editing, tab])

  const onKeyDown = (e: KeyboardEvent<HTMLElement>) => {
    if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
      e.preventDefault()
      save()
    } else if (e.key === 'Escape') {
      e.preventDefault()
      cancel()
    }
  }

  if (!editing) {
    if (!original.trim()) {
      return (
        <button
          type="button"
          onClick={open}
          className="mt-2 w-full rounded-md border border-dashed border-slate-300 px-3 py-2 text-left text-sm text-slate-400 hover:border-slate-400 hover:text-slate-600"
        >
          {PLACEHOLDER}
        </button>
      )
    }
    return (
      <div className="group relative mt-2 rounded-md border border-slate-200 bg-slate-50/60 px-3 py-2">
        <ImplementationMarkdown source={original} />
        <Button
          variant="ghost"
          size="sm"
          onClick={open}
          className="absolute right-1 top-1 h-7 px-2 text-xs text-slate-500 opacity-70 hover:text-slate-900 group-hover:opacity-100"
          aria-label="Edit description"
        >
          <Pencil className="size-3.5" /> Edit
        </Button>
      </div>
    )
  }

  const tabId = `desc-${part.partId}`
  return (
    <div className="mt-2 rounded-md border border-slate-300 bg-white" onKeyDown={onKeyDown}>
      <div className="flex items-center gap-1 border-b border-slate-200 px-2 py-1" role="tablist" aria-label="Description editor mode">
        {(
          [
            ['write', 'Write'],
            ['preview', 'Preview'],
          ] as const
        ).map(([k, label]) => (
          <button
            key={k}
            type="button"
            role="tab"
            id={`${tabId}-tab-${k}`}
            aria-selected={tab === k}
            aria-controls={`${tabId}-panel`}
            onClick={() => setTab(k)}
            className={cn(
              'rounded px-2 py-1 text-xs font-medium',
              tab === k ? 'bg-slate-900 text-white' : 'text-slate-600 hover:bg-slate-100',
            )}
          >
            {label}
          </button>
        ))}
        <span className="ml-auto text-[11px] text-slate-400">Markdown · ⌘/Ctrl+Enter to save · Esc to cancel</span>
      </div>
      <div id={`${tabId}-panel`} role="tabpanel" aria-labelledby={`${tabId}-tab-${tab}`} className="p-2">
        {tab === 'write' ? (
          <Textarea
            ref={textareaRef}
            autoFocus
            rows={6}
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            placeholder={PLACEHOLDER}
            className="min-h-[9rem] resize-y font-mono text-xs leading-relaxed"
            aria-label="Description (Markdown)"
          />
        ) : draft.trim() ? (
          <ImplementationMarkdown source={draft} className="min-h-[9rem] px-1" />
        ) : (
          <p className="min-h-[9rem] px-1 text-sm text-slate-400">Nothing to preview.</p>
        )}
      </div>
      {mutation.isError && <ErrorState error={mutation.error} className="mx-2 mb-2" />}
      <div className="flex items-center justify-end gap-2 border-t border-slate-200 px-2 py-1.5">
        <Button variant="outline" size="sm" onClick={cancel} disabled={mutation.isPending}>
          Cancel
        </Button>
        <Button size="sm" onClick={save} disabled={!dirty || mutation.isPending}>
          {mutation.isPending && <Spinner />} Save
        </Button>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Part block: header row + tracking row + description
// ---------------------------------------------------------------------------

export function PartBlock({ requirementId, part }: { requirementId: string; part: TrackablePart }) {
  const headingId = `part-${part.partId}`
  return (
    <section aria-labelledby={headingId} className="py-4 first:pt-0 last:pb-0">
      <p id={headingId} className="flex items-start gap-2 text-sm leading-relaxed text-slate-800">
        <span className="mt-0.5 shrink-0 rounded-md bg-slate-100 px-1.5 py-0.5 font-mono text-[11px] font-semibold text-slate-600">
          {part.label || 'Statement'}
        </span>
        <span className="min-w-0 flex-1">
          <OscalProse prose={part.prose} />
          {!part.prose && <span className="text-slate-400">(no statement text)</span>}
        </span>
      </p>
      <div className="mt-2 ml-3 border-l-2 border-slate-200 pl-3">
        <PartTrackingRow requirementId={requirementId} part={part} />
        <PartDescription requirementId={requirementId} part={part} />
      </div>
    </section>
  )
}
