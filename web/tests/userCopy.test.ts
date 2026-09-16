import assert from 'node:assert/strict'
import { readdir, readFile } from 'node:fs/promises'
import path from 'node:path'
import { test } from 'node:test'
import { fileURLToPath } from 'node:url'

const webRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const sourceRoot = path.join(webRoot, 'src')
const checkedExtensions = new Set(['.ts', '.tsx'])
const bannedPhrases = [
  ['at its', 'core'],
  ['circle', 'back'],
  ['deep', 'dive'],
  ['game', 'changer'],
  ['ground', 'breaking'],
  ['here is what', 'you need to know'],
  ['i hope', 'this helps'],
  ['in a world', 'where'],
  ['let us', 'break this down'],
  ['let us', 'dive'],
  ['let us', 'explore'],
  ['moving', 'forward'],
  ['not just', 'but'],
  ['serves as', 'a testament'],
  ['without further', 'ado'],
  ['would you', 'like'],
].map((parts) => parts.join('[- ’\']*'))

async function sourceFiles(directory: string): Promise<string[]> {
  const entries = await readdir(directory, { withFileTypes: true })
  const files = await Promise.all(entries.map(async (entry) => {
    const target = path.join(directory, entry.name)
    if (entry.isDirectory()) return sourceFiles(target)
    return checkedExtensions.has(path.extname(entry.name)) && !entry.name.endsWith('.test.ts') ? [target] : []
  }))
  return files.flat()
}

test('first-party user copy avoids em-dash glyphs and high-signal AI phrases', async () => {
  const files = [...await sourceFiles(sourceRoot), path.join(webRoot, 'README.md')]
  const findings: string[] = []
  for (const file of files) {
    const text = await readFile(file, 'utf8')
    text.split(/\r?\n/).forEach((line, index) => {
      if (/[\u2014\u2015\u2e3a\u2e3b]/u.test(line)) findings.push(`${path.relative(webRoot, file)}:${index + 1}: em-dash or horizontal-bar glyph`)
      for (const phrase of bannedPhrases) {
        if (new RegExp(`\\b${phrase}\\b`, 'iu').test(line)) findings.push(`${path.relative(webRoot, file)}:${index + 1}: banned phrase`)
      }
    })
  }
  assert.deepEqual(findings, [])
})
