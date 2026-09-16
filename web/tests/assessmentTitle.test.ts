import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { test } from 'node:test'
import { formatAssessmentTitle } from '../src/lib/assessmentTitle.ts'

test('strips exactly one leading DEMO prefix from an assessment presentation title', () => {
  assert.equal(
    formatAssessmentTitle('DEMO NIST SSDF Portfolio Assessment Plan'),
    'NIST SSDF Portfolio Assessment Plan',
  )
  assert.equal(formatAssessmentTitle('DEMO DEMO Assessment Results'), 'DEMO Assessment Results')
})

test('leaves internal DEMO text and other assessment titles unchanged', () => {
  assert.equal(formatAssessmentTitle('NIST DEMO Assessment Results'), 'NIST DEMO Assessment Results')
  assert.equal(formatAssessmentTitle('Demo Assessment Plan'), 'Demo Assessment Plan')
  assert.equal(formatAssessmentTitle('DEMO'), 'DEMO')
  assert.equal(formatAssessmentTitle('NIST SSDF Assessment Plan'), 'NIST SSDF Assessment Plan')
})

test('formats the shared AssessmentSummary card title for every assessment tab', async () => {
  const source = await readFile(new URL('../src/pages/AssessmentPage.tsx', import.meta.url), 'utf8')

  assert.match(source, /const presentationTitle = formatAssessmentTitle\(a\.title\)/)
  assert.match(source, /title=\{presentationTitle\}/)
  assert.match(source, /\{presentationTitle \|\| 'Untitled OSCAL assessment'\}/)
  assert.doesNotMatch(source, /title=\{a\.title\}|\{a\.title \|\|/)
})
