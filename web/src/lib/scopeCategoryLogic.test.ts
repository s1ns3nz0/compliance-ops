import test from 'node:test'
import assert from 'node:assert/strict'
import { scopeCategoryIdFromSearch, scopeRemovalError, toggleScopeCategoryIds } from './scopeCategoryLogic.ts'

test('scope category selection adds and removes multiple values', () => {
  assert.deepEqual(toggleScopeCategoryIds(['production-1'], 'company', true), ['production-1', 'company'])
  assert.deepEqual(toggleScopeCategoryIds(['production-1', 'company'], 'production-1', false), ['company'])
})

test('implemented parts retain their last scope', () => {
  assert.equal(scopeRemovalError('implemented', 0), 'Implemented parts need at least one scope.')
  assert.equal(scopeRemovalError('implemented', 1), '')
  assert.equal(scopeRemovalError('planned', 0), '')
})

test('scope category filter persists in the URL', () => {
  const search = new URLSearchParams('status=planned&scopeCategoryId=production-2')
  assert.equal(scopeCategoryIdFromSearch(search), 'production-2')
})
