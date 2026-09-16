import { useState } from 'react'
import { cn } from '@/lib/utils'

/** Matches server-resolved OSCAL parameter text such as `[Assignment: organization-defined official]`
 *  or `[Selection (one or more): a; b]`. */
const PARAM_RE = /\[(Assignment|Selection)[^\]]*\]/g

/** Split prose into plain and parameter segments. */
export function splitOscalProse(prose: string): { text: string; param: boolean }[] {
  const out: { text: string; param: boolean }[] = []
  let last = 0
  for (const m of prose.matchAll(PARAM_RE)) {
    const i = m.index ?? 0
    if (i > last) out.push({ text: prose.slice(last, i), param: false })
    out.push({ text: m[0], param: true })
    last = i + m[0].length
  }
  if (last < prose.length) out.push({ text: prose.slice(last), param: false })
  return out
}

/** Control prose with resolved OSCAL parameters subtly highlighted. */
export function OscalProse({ prose, rawProse, className }: { prose?: string; rawProse?: string; className?: string }) {
  const [showRaw, setShowRaw] = useState(false)
  if (!prose) return null
  const hasRaw = !!rawProse && rawProse !== prose
  return (
    <span className={cn('whitespace-pre-wrap', className)}>
      {showRaw ? (
        <span className="font-mono text-[0.9em] text-slate-600">{rawProse}</span>
      ) : (
        splitOscalProse(prose).map((seg, i) =>
          seg.param ? (
            <mark
              key={i}
              title="OSCAL parameter"
              className="rounded-sm bg-slate-100 px-0.5 text-slate-700 underline decoration-slate-400 decoration-dotted underline-offset-2"
            >
              {seg.text}
            </mark>
          ) : (
            <span key={i}>{seg.text}</span>
          ),
        )
      )}
      {hasRaw && (
        <>
          {' '}
          <button
            type="button"
            onClick={() => setShowRaw((v) => !v)}
            className="align-baseline text-[11px] text-slate-400 hover:text-slate-700 hover:underline"
            title={showRaw ? 'Show resolved prose' : 'Show machine form with {{ insert: param }} placeholders'}
            aria-pressed={showRaw}
          >
            {showRaw ? 'resolved' : 'raw'}
          </button>
        </>
      )}
    </span>
  )
}
