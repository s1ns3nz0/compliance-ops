import * as React from 'react'
import { cva, type VariantProps } from 'class-variance-authority'
import { cn } from '@/lib/utils'
import type { RequirementStatus } from '@/types'
import { STATUS_LABEL } from '@/lib/utils'

const badgeVariants = cva(
  'inline-flex items-center rounded-md border px-2 py-0.5 text-xs font-medium whitespace-nowrap transition-colors',
  {
    variants: {
      variant: {
        default: 'border-transparent bg-slate-900 text-white',
        secondary: 'border-transparent bg-slate-100 text-slate-700',
        outline: 'border-slate-300 text-slate-700',
        success: 'border-transparent bg-emerald-100 text-emerald-800',
        warning: 'border-transparent bg-amber-100 text-amber-800',
        danger: 'border-transparent bg-red-100 text-red-800',
        info: 'border-transparent bg-sky-100 text-sky-800',
        muted: 'border-transparent bg-slate-100 text-slate-500',
        slate: 'border-transparent bg-slate-200 text-slate-800',
        blue: 'border-transparent bg-blue-100 text-blue-800',
        gray: 'border-transparent bg-gray-100 text-gray-600',
      },
    },
    defaultVariants: { variant: 'default' },
  },
)

export interface BadgeProps extends React.HTMLAttributes<HTMLSpanElement>, VariantProps<typeof badgeVariants> {}

function Badge({ className, variant, ...props }: BadgeProps) {
  return <span className={cn(badgeVariants({ variant }), className)} {...props} />
}

/** OSCAL implementation-status → colour: implemented=green, partial=amber, planned=slate, alternative=blue, not_applicable=gray. */
const STATUS_VARIANT: Record<RequirementStatus, BadgeProps['variant']> = {
  implemented: 'success',
  partial: 'warning',
  planned: 'slate',
  alternative: 'blue',
  not_applicable: 'gray',
}

function StatusBadge({ status, className }: { status: RequirementStatus; className?: string }) {
  return (
    <Badge variant={STATUS_VARIANT[status] ?? 'secondary'} className={className}>
      {STATUS_LABEL[status] ?? status}
    </Badge>
  )
}

export { Badge, badgeVariants, StatusBadge, STATUS_VARIANT }
