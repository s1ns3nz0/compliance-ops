import { ChevronLeft, ChevronRight } from 'lucide-react'
import { Button } from './button'

interface PaginationProps {
  offset: number
  limit: number
  total: number
  onChange: (offset: number) => void
}

export function Pagination({ offset, limit, total, onChange }: PaginationProps) {
  const from = total === 0 ? 0 : offset + 1
  const to = Math.min(offset + limit, total)
  const pageCount = Math.max(1, Math.ceil(total / limit))
  const page = Math.min(pageCount, Math.floor(offset / limit) + 1)
  return (
    <nav className="flex items-center justify-between gap-4 text-sm text-slate-600" aria-label="Pagination">
      <span>
        {from}–{to} of {total} · Page {page} of {pageCount}
      </span>
      <div className="flex gap-2">
        <Button aria-label="Previous page" variant="outline" size="sm" disabled={offset === 0} onClick={() => onChange(Math.max(0, offset - limit))}>
          <ChevronLeft /> Previous
        </Button>
        <Button aria-label="Next page" variant="outline" size="sm" disabled={to >= total} onClick={() => onChange(offset + limit)}>
          Next <ChevronRight />
        </Button>
      </div>
    </nav>
  )
}
