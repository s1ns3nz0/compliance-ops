/** Format application-authored evidence titles without changing stored evidence. */
export function formatEvidenceTitle(title: string): string {
  return title.replace(/\s+\u2014\s+/gu, ': ')
}