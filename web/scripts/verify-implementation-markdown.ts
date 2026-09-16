import {
  implementationMarkdownBlocks,
  isPlaceholderLine,
  renderableImplementationMarkdown,
} from '../src/lib/implementationMarkdown.js'

const whatWeDo = '**What we do**'
const whatWeDoStarter = '- _We are [briefly state the concrete practice in operation]._'

function assert(condition: unknown, message: string): asserts condition {
  if (!condition) throw new Error(message)
}

const bare = `${whatWeDo}\n${whatWeDoStarter}`
const bareBlocks = implementationMarkdownBlocks(bare)
assert(bareBlocks.length === 1, 'bare template should produce one section block')
assert(bareBlocks[0]?.placeholder, 'bare template starter should be marked as a placeholder')
assert(bareBlocks[0]?.body === whatWeDoStarter, 'bare template starter should remain renderable')
assert(isPlaceholderLine(whatWeDoStarter, whatWeDo), 'known starter should match its exact section')

const completed = `${whatWeDo}\n${whatWeDoStarter}\nThe platform team reviews PRs weekly.`
const completedBlocks = implementationMarkdownBlocks(completed)
assert(!completedBlocks[0]?.placeholder, 'completed section should not be a placeholder block')
assert(!renderableImplementationMarkdown(completed).includes(whatWeDoStarter), 'completed section should hide its starter')
assert(renderableImplementationMarkdown(completed).includes('The platform team reviews PRs weekly.'), 'completed content should remain')

const italic = `${whatWeDo}\n- _An italic user statement._`
assert(renderableImplementationMarkdown(italic).includes('_An italic user statement._'), 'arbitrary italic prose must remain')

const mockBanner = '> **DEMO / MOCK DATA**: This is only a demo.'
assert(renderableImplementationMarkdown(mockBanner).includes(mockBanner), 'mock banner must remain')

const starterVerify = '**Verify**\n- _Add one runnable command or repeatable check._'
const hiddenStarterVerify = renderableImplementationMarkdown(starterVerify)
assert(!hiddenStarterVerify.includes('**Verify**'), 'starter-only Verify heading should disappear')
assert(!hiddenStarterVerify.includes('Add one runnable command'), 'starter-only Verify copy should disappear')

const realVerify = '**Verify**\n- Run `npm test` before each release.'
assert(renderableImplementationMarkdown(realVerify) === realVerify, 'real Verify section must remain unchanged')

const mixedVerify = `${whatWeDo}\n${whatWeDoStarter}\n\n${starterVerify}\n\n**Gaps**\n- None.`
assert(!renderableImplementationMarkdown(mixedVerify).includes('Add one runnable command'), 'starter Verify should disappear between template sections')
assert(renderableImplementationMarkdown(mixedVerify).includes('**Gaps**'), 'sections after starter Verify should remain')

console.log('implementation markdown verification passed: starters handled; starter Verify hidden; real Verify, italic, and mock banner retained')
