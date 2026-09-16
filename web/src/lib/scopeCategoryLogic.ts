import type { RequirementStatus } from '@/types'

export function toggleScopeCategoryIds(ids: readonly string[], id: string, selected: boolean): string[] {
  if (selected) return ids.includes(id) ? [...ids] : [...ids, id]
  return ids.filter((candidate) => candidate !== id)
}

export function scopeRemovalError(status: RequirementStatus, nextCount: number): string {
  return status === 'implemented' && nextCount === 0 ? 'Implemented parts need at least one scope.' : ''
}

export function scopeCategoryIdFromSearch(search: Pick<URLSearchParams, 'get'>): string {
  return search.get('scopeCategoryId') ?? ''
}
