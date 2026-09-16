import { AlertCircle, Loader2 } from 'lucide-react'
import { cn } from '@/lib/utils'
import { errorMessage } from '@/api'

export function Spinner({ className }: { className?: string }) {
  return <Loader2 className={cn('size-4 animate-spin', className)} aria-hidden />
}

export function LoadingState({ label = 'Loading…' }: { label?: string }) {
  return (
    <div className="flex items-center gap-2 py-8 text-sm text-slate-500" role="status">
      <Spinner /> {label}
    </div>
  )
}

export function ErrorState({ error, className }: { error: unknown; className?: string }) {
  return (
    <div
      role="alert"
      className={cn('flex items-start gap-2 rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-800', className)}
    >
      <AlertCircle className="mt-0.5 size-4 shrink-0" aria-hidden />
      <span>{errorMessage(error)}</span>
    </div>
  )
}

export function EmptyState({ children }: { children: React.ReactNode }) {
  return <div className="py-8 text-center text-sm text-slate-500">{children}</div>
}
