import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { test } from 'node:test'

import { ASSESSMENT_NOTICE_HEADING, ASSESSMENT_NOTICE_ITEMS } from '../src/lib/uiCopy.ts'

const expectedItems = [
  'NIST SP 800-53: Entirely mock data.',
  'NIST SP 800-218 SSDF: Mock data plus an assessment of the node-operator sample application.',
  'Node Validator Key Management Policy: Company-specific policy authored for the node-operator project.',
]

test('assessment notice preserves the approved copy exactly', () => {
  assert.equal(ASSESSMENT_NOTICE_HEADING, 'Assessment notice: This portfolio includes example data for each framework.')
  assert.deepEqual(ASSESSMENT_NOTICE_ITEMS, expectedItems)
})

test('authenticated layout renders the assessment notice as an accessible list', async () => {
  const source = await readFile(new URL('../src/components/Layout.tsx', import.meta.url), 'utf8')
  assert.match(source, /role="alert"/)
  assert.match(source, /aria-labelledby="assessment-notice-heading"/)
  assert.match(source, /<p id="assessment-notice-heading"/)
  assert.match(source, /<ul className=/)
  assert.match(source, /ASSESSMENT_NOTICE_ITEMS\.map/)
})
