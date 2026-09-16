export function formatAssessmentTitle(title: string): string {
  return title.startsWith('DEMO ') ? title.slice('DEMO '.length) : title
}
