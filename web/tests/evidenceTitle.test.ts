import assert from 'node:assert/strict'
import { test } from 'node:test'
import { formatEvidenceTitle } from '../src/lib/evidenceTitle.ts'

test('formats a spaced em dash in an application-authored evidence title as an appositive', () => {
  assert.equal(
    formatEvidenceTitle('Jira SEC-1 \u2014 access-control policy draft'),
    'Jira SEC-1: access-control policy draft',
  )
})

test('leaves hyphens and titles without a spaced em dash unchanged', () => {
  assert.equal(formatEvidenceTitle('2025-01 access-control review'), '2025-01 access-control review')
  assert.equal(formatEvidenceTitle('Evidence title'), 'Evidence title')
})
