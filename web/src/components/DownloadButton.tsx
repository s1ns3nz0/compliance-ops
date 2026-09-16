import { useState } from 'react'
import { Download } from 'lucide-react'
import { api, errorMessage } from '@/api'
import type { Evidence } from '@/types'
import { Button } from '@/components/ui/button'
import { Spinner } from '@/components/ui/feedback'

export function DownloadButton({ evidence, size = 'sm' }: { evidence: Evidence; size?: 'sm' | 'default' | 'icon' }) {
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)
  const run = async () => {
    setBusy(true)
    setErr(null)
    try {
      await api.downloadEvidence(evidence.id, evidence.fileName)
    } catch (e) {
      setErr(errorMessage(e))
    } finally {
      setBusy(false)
    }
  }
  return (
    <span className="inline-flex flex-col">
      <Button variant="outline" size={size} onClick={run} disabled={busy} aria-label={`Download ${evidence.fileName}`} title={err ?? undefined}>
        {busy ? <Spinner /> : <Download />} {size !== 'icon' && 'Download'}
      </Button>
      {err && <span className="mt-1 text-xs text-red-700">{err}</span>}
    </span>
  )
}
