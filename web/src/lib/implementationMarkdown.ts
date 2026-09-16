/** Exact starter copy used by implementation description templates, keyed by section header. */
export const IMPLEMENTATION_TEMPLATE_STARTERS: Readonly<Record<string, string>> = {
  '**Policy Basis**': '- _State the governing policy, standard, or procedure, such as IAM-POL-02 v3 §3.2, approved by the CISO on 2026-08-12._',
  '**What we do**': '- _We are [briefly state the concrete practice in operation]._',
  '**Who (role / owner)**': '- _Name the accountable role and any approver or reviewer._',
  '**How (solution / tooling / ticket / committee)**': '- _State the system, workflow, ticket, committee, or runbook used to do this work._',
  '**Period (cadence)**': '- _State when this happens, such as continuously in CI, monthly, quarterly, or annually._',
  '**Evidence**': '- _Name the report, file, ticket, or link that shows the work happened._',
  '**Gaps**': '- _State any missing coverage, enforcement gap, owner, and target date._',
}

const LEGACY_VERIFY_HEADER = '**Verify**'
const LEGACY_VERIFY_STARTER = '- _Add one runnable command or repeatable check._'

export type ImplementationMarkdownBlock = {
  /** Source to render after hiding a starter from a completed section. */
  source: string
  /** Known section header, when this block represents a template section. */
  header?: string
  /** Section body before display transformation. */
  body?: string
  /** True only when the section body contains no content besides its exact starter. */
  placeholder: boolean
}

function normalizedLine(line: string) {
  return line.trim()
}

function withoutStarterOnlyVerify(source: string): string {
  const lines = source.split(/\r?\n/)
  const visible: string[] = []
  for (let index = 0; index < lines.length;) {
    if (normalizedLine(lines[index] ?? '') !== LEGACY_VERIFY_HEADER) {
      visible.push(lines[index] ?? '')
      index += 1
      continue
    }
    let nextHeader = index + 1
    while (nextHeader < lines.length && !/^\*\*[^*]+\*\*$/.test(normalizedLine(lines[nextHeader] ?? ''))) nextHeader += 1
    const content = lines.slice(index + 1, nextHeader).filter((line) => normalizedLine(line) !== '')
    if (content.length === 1 && normalizedLine(content[0] ?? '') === LEGACY_VERIFY_STARTER) {
      index = nextHeader
      continue
    }
    visible.push(...lines.slice(index, nextHeader))
    index = nextHeader
  }
  return visible.join('\n')
}

/** Matches only an exact, known starter for the supplied exact template header. */
export function isPlaceholderLine(line: string, sectionHeader: string): boolean {
  const starter = IMPLEMENTATION_TEMPLATE_STARTERS[normalizedLine(sectionHeader)]
  return starter !== undefined && normalizedLine(line) === starter
}

/**
 * Splits known template sections for display. Source is never mutated; callers keep
 * the original Markdown in the editor and storage. A starter is removed only from a
 * section that has other non-whitespace content.
 */
export function implementationMarkdownBlocks(source: string): ImplementationMarkdownBlock[] {
  const displaySource = withoutStarterOnlyVerify(source)
  const lines = displaySource.split(/\r?\n/)
  const headerIndexes = lines
    .map((line, index) => ({ line, index }))
    .filter(({ line }) => Object.hasOwn(IMPLEMENTATION_TEMPLATE_STARTERS, normalizedLine(line)))

  if (headerIndexes.length === 0) return [{ source: displaySource, placeholder: false }]

  const blocks: ImplementationMarkdownBlock[] = []
  let foundStarter = false
  const firstHeaderIndex = headerIndexes[0]?.index ?? 0
  if (firstHeaderIndex > 0) blocks.push({ source: lines.slice(0, firstHeaderIndex).join('\n'), placeholder: false })

  for (let index = 0; index < headerIndexes.length; index += 1) {
    const section = headerIndexes[index]
    if (!section) continue
    const nextHeaderIndex = headerIndexes[index + 1]?.index ?? lines.length
    const header = section.line
    const bodyLines = lines.slice(section.index + 1, nextHeaderIndex)
    const starterIndexes = bodyLines.flatMap((line, bodyIndex) => (isPlaceholderLine(line, header) ? [bodyIndex] : []))
    foundStarter ||= starterIndexes.length > 0

    const hasRealContent = bodyLines.some((line, bodyIndex) => normalizedLine(line) !== '' && !starterIndexes.includes(bodyIndex))
    const visibleBodyLines = hasRealContent ? bodyLines.filter((_, bodyIndex) => !starterIndexes.includes(bodyIndex)) : bodyLines
    const body = visibleBodyLines.join('\n')
    blocks.push({
      source: [header, ...visibleBodyLines].join('\n'),
      header,
      body,
      placeholder: starterIndexes.length > 0 && !hasRealContent,
    })
  }

  // Preserve ordinary Markdown exactly when it merely happens to contain a template-like heading.
  return foundStarter ? blocks : [{ source: displaySource, placeholder: false }]
}

/** Markdown suitable for normal rendering after completed-section starters are removed. */
export function renderableImplementationMarkdown(source: string): string {
  return implementationMarkdownBlocks(source)
    .map((block) => block.source)
    .join('\n')
}
