import { ExternalLink, FileText, Link2 } from 'lucide-react'
import type { Evidence, EvidenceKind } from '@/types'
import { buttonVariants } from '@/components/ui/button'
import { DownloadButton } from '@/components/DownloadButton'
import { cn, urlHost } from '@/lib/utils'

/** Small icon that tells file evidence from link evidence. */
export function KindIcon({ kind, className }: { kind: EvidenceKind; className?: string }) {
  const Icon = kind === 'link' ? Link2 : FileText
  return <Icon className={cn('size-4 shrink-0 text-slate-500', className)} aria-label={kind === 'link' ? 'Link evidence' : 'File evidence'} role="img" />
}

/** External link for link evidence: hostname label, opens in a new tab. */
export function ExternalEvidenceLink({ url, className, iconOnly = false }: { url: string; className?: string; iconOnly?: boolean }) {
  const host = urlHost(url)
  return (
    <a
      href={url}
      target="_blank"
      rel="noopener noreferrer"
      title={url}
      aria-label={`Open ${host} in a new tab`}
      className={cn('inline-flex items-center gap-1 text-sm text-sky-700 hover:underline', className)}
    >
      <ExternalLink className="size-4 shrink-0" aria-hidden />
      {!iconOnly && <span className="truncate">{host}</span>}
    </a>
  )
}

/** Primary action for an evidence row: download for files, open-externally for links. */
export function EvidencePrimaryAction({ evidence, size = 'sm' }: { evidence: Evidence; size?: 'sm' | 'icon' }) {
  if (evidence.kind === 'link' && evidence.url) {
    const host = urlHost(evidence.url)
    return (
      <a
        href={evidence.url}
        target="_blank"
        rel="noopener noreferrer"
        aria-label={`Open ${host} in a new tab`}
        title={evidence.url}
        className={buttonVariants({ variant: 'outline', size })}
      >
        <ExternalLink /> {size !== 'icon' && 'Open'}
      </a>
    )
  }
  return <DownloadButton evidence={evidence} size={size} />
}
