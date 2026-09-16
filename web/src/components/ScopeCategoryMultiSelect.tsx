import { useEffect, useId, useMemo, useRef, useState, type KeyboardEvent } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Check, ChevronDown, Pencil, Plus, Trash2, X } from 'lucide-react'
import { ApiError, api, errorMessage } from '@/api'
import type { ScopeCategory } from '@/types'
import { toggleScopeCategoryIds } from '@/lib/scopeCategoryLogic'
import { cn } from '@/lib/utils'
import { Input } from '@/components/ui/input'
import { Spinner } from '@/components/ui/feedback'

interface ScopeCategoryMultiSelectProps {
  selected: ScopeCategory[]
  onChange: (categories: ScopeCategory[]) => void
  disabled?: boolean
  invalid?: boolean
}

export function ScopeCategoryMultiSelect({ selected, onChange, disabled, invalid }: ScopeCategoryMultiSelectProps) {
  const id = useId()
  const rootRef = useRef<HTMLDivElement>(null)
  const queryClient = useQueryClient()
  const categoriesQuery = useQuery({ queryKey: ['scope-categories'], queryFn: api.listScopeCategories })
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const [editingId, setEditingId] = useState<string | null>(null)
  const [renameValue, setRenameValue] = useState('')
  const [actionError, setActionError] = useState('')

  const categories = categoriesQuery.data?.items ?? []
  const selectedIds = selected.map((category) => category.id)
  const selectionForIds = (ids: string[], candidates: ScopeCategory[] = categories) =>
    ids.map((categoryId) => candidates.find((category) => category.id === categoryId))
      .filter((category): category is ScopeCategory => Boolean(category))
  const visibleSelected = selected.map((category) => categories.find((candidate) => candidate.id === category.id) ?? category)
  const normalizedQuery = query.trim().toLocaleLowerCase()
  const filtered = useMemo(
    () => categories.filter((category) => category.name.toLocaleLowerCase().includes(normalizedQuery)),
    [categories, normalizedQuery],
  )
  const canCreate = Boolean(query.trim()) && !categories.some((category) => category.name.toLocaleLowerCase() === normalizedQuery)

  useEffect(() => {
    if (!open) return
    const closeOutside = (event: MouseEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', closeOutside)
    return () => document.removeEventListener('mousedown', closeOutside)
  }, [open])

  const updateCategoryCache = (category: ScopeCategory) => {
    queryClient.setQueryData<{ items: ScopeCategory[] }>(['scope-categories'], (old) => {
      if (!old) return { items: [category] }
      const exists = old.items.some((candidate) => candidate.id === category.id)
      return { items: exists ? old.items.map((candidate) => candidate.id === category.id ? category : candidate) : [...old.items, category] }
    })
  }

  const createMutation = useMutation({
    mutationFn: (name: string) => api.createScopeCategory(name),
    onSuccess: (category) => {
      updateCategoryCache(category)
      queryClient.invalidateQueries({ queryKey: ['scope-categories'] })
      onChange(selectionForIds(toggleScopeCategoryIds(selectedIds, category.id, true), [...categories, ...selected, category]))
      setQuery('')
      setActionError('')
    },
    onError: (error) => setActionError(errorMessage(error)),
  })
  const renameMutation = useMutation({
    mutationFn: ({ categoryId, name }: { categoryId: string; name: string }) => api.renameScopeCategory(categoryId, name),
    onSuccess: (category) => {
      updateCategoryCache(category)
      queryClient.invalidateQueries({ queryKey: ['scope-categories'] })
      setEditingId(null)
      setActionError('')
    },
    onError: (error) => setActionError(errorMessage(error)),
  })
  const deleteMutation = useMutation({
    mutationFn: api.deleteScopeCategory,
    onSuccess: (_, categoryId) => {
      queryClient.setQueryData<{ items: ScopeCategory[] }>(['scope-categories'], (old) =>
        old ? { items: old.items.filter((category) => category.id !== categoryId) } : old,
      )
      queryClient.invalidateQueries({ queryKey: ['scope-categories'] })
      setActionError('')
    },
    onError: (error) => setActionError(
      error instanceof ApiError && error.status === 409
        ? 'This scope is in use and cannot be deleted.'
        : errorMessage(error),
    ),
  })

  const create = () => {
    const name = query.trim()
    if (name && canCreate && !createMutation.isPending) createMutation.mutate(name)
  }

  const saveRename = (categoryId: string) => {
    const name = renameValue.trim()
    if (name && !renameMutation.isPending) renameMutation.mutate({ categoryId, name })
  }

  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === 'Escape') {
      event.preventDefault()
      setOpen(false)
      setEditingId(null)
    }
  }

  return (
    <div ref={rootRef} className="relative" onKeyDown={onKeyDown}>
      <div
        className={cn(
          'flex min-h-9 w-full flex-wrap items-center gap-1 rounded-md border bg-white px-2 py-1 shadow-sm',
          invalid ? 'border-red-400' : 'border-slate-300',
          disabled && 'cursor-not-allowed opacity-50',
        )}
      >
        {visibleSelected.map((category) => (
          <span key={category.id} className="inline-flex items-center gap-1 rounded-full bg-slate-100 px-2 py-0.5 text-xs text-slate-700">
            {category.name}
            <button
              type="button"
              className="rounded-full text-slate-400 hover:text-slate-900 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-slate-400"
              aria-label={`Remove ${category.name}`}
              disabled={disabled}
              onClick={() => onChange(selectionForIds(toggleScopeCategoryIds(selectedIds, category.id, false), [...categories, ...selected]))}
            >
              <X className="size-3" aria-hidden />
            </button>
          </span>
        ))}
        <button
          type="button"
          id={`${id}-trigger`}
          className="flex min-w-28 flex-1 items-center justify-between gap-2 px-1 py-0.5 text-left text-xs text-slate-500 focus-visible:outline-none"
          aria-haspopup="listbox"
          aria-label="Scope"
          aria-expanded={open}
          aria-controls={`${id}-listbox`}
          disabled={disabled}
          onClick={() => setOpen((value) => !value)}
        >
          <span>{selected.length ? 'Add scope…' : 'Choose scopes…'}</span>
          <ChevronDown className="size-3.5 shrink-0" aria-hidden />
        </button>
      </div>

      {open && (
        <div className="absolute z-30 mt-1 w-full min-w-72 rounded-md border border-slate-200 bg-white p-2 shadow-lg">
          <Input
            autoFocus
            value={query}
            onChange={(event) => { setQuery(event.target.value); setActionError('') }}
            onKeyDown={(event) => {
              if (event.key === 'Enter' && canCreate) {
                event.preventDefault()
                create()
              }
            }}
            placeholder="Search scopes…"
            className="h-8 text-xs"
            aria-label="Search scopes"
          />
          <div id={`${id}-listbox`} role="listbox" aria-multiselectable="true" aria-labelledby={`${id}-trigger`} className="mt-2 max-h-60 overflow-y-auto">
            {categoriesQuery.isPending && <div className="flex items-center gap-2 px-2 py-2 text-xs text-slate-500"><Spinner /> Loading scopes…</div>}
            {categoriesQuery.isError && <p className="px-2 py-2 text-xs text-red-700">{errorMessage(categoriesQuery.error)}</p>}
            {filtered.map((category) => {
              const checked = selectedIds.includes(category.id)
              const editing = editingId === category.id
              return (
                <div key={category.id} role="option" aria-selected={checked} className="flex min-h-8 items-center gap-1 rounded px-1 hover:bg-slate-50">
                  {editing ? (
                    <form className="flex min-w-0 flex-1 items-center gap-1" onSubmit={(event) => { event.preventDefault(); saveRename(category.id) }}>
                      <Input autoFocus value={renameValue} onChange={(event) => setRenameValue(event.target.value)} className="h-7 text-xs" aria-label={`Rename ${category.name}`} />
                      <button type="submit" className="rounded p-1 text-emerald-700 hover:bg-emerald-50" aria-label="Save rename"><Check className="size-3.5" /></button>
                      <button type="button" className="rounded p-1 text-slate-500 hover:bg-slate-100" onClick={() => setEditingId(null)} aria-label="Cancel rename"><X className="size-3.5" /></button>
                    </form>
                  ) : (
                    <>
                      <label className="flex min-w-0 flex-1 cursor-pointer items-center gap-2 py-1 text-xs text-slate-700">
                        <input
                          type="checkbox"
                          checked={checked}
                          onChange={(event) => onChange(selectionForIds(toggleScopeCategoryIds(selectedIds, category.id, event.target.checked), [...categories, ...selected]))}
                          className="size-3.5 rounded border-slate-300"
                        />
                        <span className="truncate">{category.name}</span>
                      </label>
                      <button type="button" className="rounded p-1 text-slate-400 hover:bg-slate-100 hover:text-slate-700" onClick={() => { setEditingId(category.id); setRenameValue(category.name); setActionError('') }} aria-label={`Rename ${category.name}`}><Pencil className="size-3.5" /></button>
                      <button
                        type="button"
                        className="rounded p-1 text-slate-400 hover:bg-red-50 hover:text-red-700"
                        onClick={() => {
                          if (window.confirm(`Delete “${category.name}”?`)) deleteMutation.mutate(category.id)
                        }}
                        aria-label={`Delete ${category.name}`}
                      ><Trash2 className="size-3.5" /></button>
                    </>
                  )}
                </div>
              )
            })}
            {!categoriesQuery.isPending && filtered.length === 0 && !canCreate && <p className="px-2 py-2 text-xs text-slate-500">No scopes found.</p>}
          </div>
          {canCreate && (
            <button type="button" onClick={create} disabled={createMutation.isPending} className="mt-1 flex w-full items-center gap-2 rounded px-2 py-1.5 text-left text-xs font-medium text-slate-700 hover:bg-slate-100 disabled:opacity-50">
              {createMutation.isPending ? <Spinner className="size-3.5" /> : <Plus className="size-3.5" />}
              Create “{query.trim()}”
            </button>
          )}
          {actionError && <p role="alert" className="mt-1 px-2 text-xs text-red-700">{actionError}</p>}
        </div>
      )}
    </div>
  )
}
