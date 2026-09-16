import * as React from 'react'
import { X } from 'lucide-react'
import { cn } from '@/lib/utils'
import { Button } from './button'

interface DialogProps {
  open: boolean
  onClose: () => void
  title: string
  description?: string
  children: React.ReactNode
  footer?: React.ReactNode
  className?: string
}

/** Minimal accessible modal dialog built on the native <dialog> element. */
export function Dialog({ open, onClose, title, description, children, footer, className }: DialogProps) {
  const ref = React.useRef<HTMLDialogElement>(null)
  const titleId = React.useId()
  const descId = React.useId()

  React.useEffect(() => {
    const el = ref.current
    if (!el) return
    if (open && !el.open) el.showModal()
    else if (!open && el.open) el.close()
  }, [open])

  React.useEffect(() => {
    if (!open) return
    const prev = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    return () => {
      document.body.style.overflow = prev
    }
  }, [open])

  return (
    <dialog
      ref={ref}
      aria-labelledby={titleId}
      aria-describedby={description ? descId : undefined}
      onCancel={(e) => {
        e.preventDefault()
        onClose()
      }}
      onClick={(e) => {
        if (e.target === ref.current) onClose()
      }}
      className={cn(
        'fixed inset-0 m-auto w-full max-w-lg rounded-xl border border-slate-200 bg-white p-0 text-slate-900 shadow-xl backdrop:bg-slate-900/40 open:flex open:flex-col',
        'max-h-[90vh]',
        className,
      )}
    >
      {open && (
        <>
          <div className="flex items-start justify-between gap-4 border-b border-slate-100 px-6 py-4">
            <div>
              <h2 id={titleId} className="text-lg font-semibold leading-tight">
                {title}
              </h2>
              {description && (
                <p id={descId} className="mt-1 text-sm text-slate-500">
                  {description}
                </p>
              )}
            </div>
            <Button variant="ghost" size="icon" aria-label="Close" onClick={onClose}>
              <X />
            </Button>
          </div>
          <div className="overflow-y-auto px-6 py-4">{children}</div>
          {footer && <div className="flex justify-end gap-2 border-t border-slate-100 px-6 py-4">{footer}</div>}
        </>
      )}
    </dialog>
  )
}
