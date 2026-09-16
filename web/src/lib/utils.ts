import { clsx, type ClassValue } from 'clsx'
import { twMerge } from 'tailwind-merge'

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

/** Render an ISO date-time or date string as YYYY-MM-DD (local date for date-only inputs). */
export function formatDate(value?: string | null): string {
  if (!value) return 'Not provided'
  if (/^\d{4}-\d{2}-\d{2}$/.test(value)) return value
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return value
  return d.toISOString().slice(0, 10)
}

/** Value suitable for <input type="date">. */
export function toDateInput(value?: string | null): string {
  if (!value) return ''
  return formatDate(value)
}

export function formatDateTime(value?: string | null): string {
  if (!value) return 'Not provided'
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return value
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

export function relativeTime(value?: string | null, now: number = Date.now()): string {
  if (!value) return 'Not provided'
  const t = new Date(value).getTime()
  if (Number.isNaN(t)) return value
  const diff = Math.round((t - now) / 1000)
  const abs = Math.abs(diff)
  const rtf = new Intl.RelativeTimeFormat('en', { numeric: 'auto' })
  if (abs < 60) return rtf.format(diff, 'second')
  if (abs < 3600) return rtf.format(Math.round(diff / 60), 'minute')
  if (abs < 86400) return rtf.format(Math.round(diff / 3600), 'hour')
  if (abs < 86400 * 30) return rtf.format(Math.round(diff / 86400), 'day')
  if (abs < 86400 * 365) return rtf.format(Math.round(diff / (86400 * 30)), 'month')
  return rtf.format(Math.round(diff / (86400 * 365)), 'year')
}

export function formatBytes(n: number): string {
  if (!Number.isFinite(n)) return 'Not provided'
  if (n < 1024) return `${n} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let v = n / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(v < 10 ? 1 : 0)} ${units[i]}`
}

export function isOverdue(dueDate?: string | null, status?: string): boolean {
  if (!dueDate) return false
  if (status === 'implemented' || status === 'not_applicable') return false
  const today = new Date().toISOString().slice(0, 10)
  return formatDate(dueDate) < today
}

/** Labels for OSCAL implementation-status values. */
export const STATUS_LABEL: Record<string, string> = {
  implemented: 'Implemented',
  partial: 'Partial',
  planned: 'Planned',
  alternative: 'Alternative',
  not_applicable: 'N/A',
}

export const EVIDENCE_KIND_LABEL: Record<string, string> = {
  file: 'File',
  link: 'Link',
}

/** Hostname of a URL for compact link labels; falls back to the raw string. */
export function urlHost(url?: string | null): string {
  if (!url) return 'No linked URL'
  try {
    return new URL(url).hostname
  } catch {
    return url
  }
}

export function isHttpUrl(value: string): boolean {
  try {
    const u = new URL(value)
    return u.protocol === 'http:' || u.protocol === 'https:'
  } catch {
    return false
  }
}

/** Derive an OSCAL group id from a control id ("ac-2" → "AC", "ac-2.1" → "AC"). */
export function controlGroup(controlId: string): string | null {
  const m = /^([a-z]+)[-.]/i.exec(controlId.trim())
  return m ? m[1].toUpperCase() : null
}

/** Human label for a signed day offset: "in 12 days", "due today", "overdue by 3 days". */
export function dueLabel(daysUntilDue: number): string {
  if (daysUntilDue === 0) return 'due today'
  const n = Math.abs(daysUntilDue)
  const unit = n === 1 ? 'day' : 'days'
  return daysUntilDue < 0 ? `overdue by ${n} ${unit}` : `in ${n} ${unit}`
}
