import { writeFile } from 'node:fs/promises'
import process from 'node:process'

const baseURL = process.env.BASE_URL || 'http://localhost:3000'
const token = process.env.COMPLIANCE_OPS_TOKEN
if (!token?.trim()) {
  console.error('COMPLIANCE_OPS_TOKEN is required')
  process.exit(1)
}
const cdpURL = process.env.CDP_URL || 'http://127.0.0.1:9223'
const outputPath = process.env.SMOKE_OUTPUT || '/tmp/assessment-blank-fix/summary.json'
const consolePath = process.env.CONSOLE_OUTPUT || '/tmp/assessment-blank-fix/console-after.log'
const testErrorBoundary = process.env.TEST_ERROR_BOUNDARY === '1'
const tabs = [
  {
    key: 'plan',
    title: 'DEMO SSDF Assessment Plan',
    detail: 'Assessment plan',
    proof: ['REVIEWED CONTROLS / OBJECTIVES', 'Unmapped queue'],
  },
  {
    key: 'results',
    title: 'DEMO SSDF Assessment Results',
    detail: 'Results by control',
    proof: ['Manually linked', 'NIST SP 800-218 SSDF → PO.2 → PO.2.1'],
  },
  {
    key: 'poam',
    title: 'DEMO SSDF Plan of Action and Milestones',
    detail: 'Plan of action and milestones',
    proof: ['SCHEDULED COMPLETION', 'Mapped records'],
  },
]

const target = await fetch(`${cdpURL}/json/new?about%3Ablank`, { method: 'PUT' }).then((response) => response.json())
const ws = new WebSocket(target.webSocketDebuggerUrl)
await new Promise((resolve, reject) => {
  ws.addEventListener('open', resolve, { once: true })
  ws.addEventListener('error', reject, { once: true })
})
let nextId = 0
const pending = new Map()
const consoleEntries = []
const exceptions = []
const failedRequests = []
const responses = []
ws.addEventListener('message', ({ data }) => {
  const message = JSON.parse(data)
  if (message.id) {
    const callback = pending.get(message.id)
    pending.delete(message.id)
    if (message.error) callback?.reject(new Error(message.error.message))
    else callback?.resolve(message.result)
    return
  }
  if (message.method === 'Runtime.consoleAPICalled') {
    consoleEntries.push({ type: message.params.type, text: message.params.args.map((arg) => arg.value ?? arg.description ?? '').join(' ') })
  }
  if (message.method === 'Runtime.exceptionThrown') {
    const detail = message.params.exceptionDetails
    exceptions.push({
      text: detail.exception?.description || detail.text,
      url: detail.url,
      lineNumber: detail.lineNumber,
      columnNumber: detail.columnNumber,
      stack: detail.stackTrace?.callFrames?.slice(0, 20).map((frame) => `${frame.functionName || '<anonymous>'} (${frame.url}:${frame.lineNumber + 1}:${frame.columnNumber + 1})`) || [],
    })
  }
  if (message.method === 'Network.loadingFailed') failedRequests.push({ errorText: message.params.errorText, canceled: message.params.canceled, blockedReason: message.params.blockedReason })
  if (message.method === 'Network.responseReceived' && message.params.response.url.includes('/v1/')) {
    responses.push({ url: message.params.response.url, status: message.params.response.status })
  }
})
function send(method, params = {}) {
  const id = ++nextId
  ws.send(JSON.stringify({ id, method, params }))
  return new Promise((resolve, reject) => pending.set(id, { resolve, reject }))
}
async function evaluate(expression) {
  const result = await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true })
  if (result.exceptionDetails) throw new Error(result.exceptionDetails.exception?.description || result.exceptionDetails.text)
  return result.result.value
}
async function waitFor(expression, description, timeoutMs = 8_000) {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    if (await evaluate(expression)) return
    await new Promise((resolve) => setTimeout(resolve, 100))
  }
  throw new Error(`Timed out waiting for ${description}`)
}
async function navigate(url) {
  await send('Page.navigate', { url })
  await waitFor(`document.readyState === 'complete'`, `load: ${url}`)
}
function assert(condition, message) {
  if (!condition) throw new Error(message)
}

let report
try {
  await Promise.all([send('Runtime.enable'), send('Page.enable'), send('Network.enable')])
  await navigate(baseURL)
  await evaluate(`sessionStorage.setItem('compliance-ops.auth', ${JSON.stringify(JSON.stringify({ token }))})`)

  const tabProof = []
  for (const tab of tabs) {
    const query = tab.key === 'plan' ? '' : `?tab=${tab.key}`
    await navigate(`${baseURL}/assessment${query}`)
    await waitFor(`document.body.innerText.includes(${JSON.stringify(tab.title)})`, `${tab.key} card`)
    const listState = await evaluate(`({
      url: location.href,
      bodyText: document.body.innerText,
      frameworkValue: document.getElementById('assessment-framework')?.value ?? null,
      selectedTab: document.querySelector('[role="tab"][aria-selected="true"]')?.textContent?.trim() ?? null,
      viewDetailsButtons: [...document.querySelectorAll('button')].filter((button) => button.textContent?.trim() === 'View details').length,
    })`)
    assert(!new URL(listState.url).searchParams.has('frameworkId'), `${tab.key}: frameworkId unexpectedly present`)
    assert(listState.frameworkValue === '', `${tab.key}: framework did not default to All frameworks`)
    assert(listState.viewDetailsButtons > 0, `${tab.key}: detail button missing`)
    await evaluate(`[...document.querySelectorAll('button')].find((button) => button.textContent?.trim() === 'View details')?.click()`)
    await waitFor(`document.querySelector('dialog[open]')?.innerText.includes(${JSON.stringify(tab.detail)})`, `${tab.key} detail`)
    const detailText = await evaluate(`document.querySelector('dialog[open]')?.innerText ?? ''`)
    for (const expected of tab.proof) assert(detailText.includes(expected), `${tab.key} detail missing: ${expected}`)
    tabProof.push({
      tab: tab.key,
      selectedTab: listState.selectedTab,
      title: tab.title,
      allFrameworks: listState.frameworkValue === '' && !new URL(listState.url).searchParams.has('frameworkId'),
      detail: tab.detail,
      proof: tab.proof.filter((text) => detailText.includes(text)),
    })
  }

  let errorBoundary = { tested: false }
  if (testErrorBoundary) {
    await navigate(baseURL)
    await evaluate(`(async () => {
      const [boundaryModule, reactModule, reactDOMModule] = await Promise.all([
        import('/src/components/ErrorBoundary.tsx'),
        import('/node_modules/.vite/deps/react.js'),
        import('/node_modules/.vite/deps/react-dom_client.js'),
      ])
      const { ErrorBoundary } = boundaryModule
      const React = reactModule.default
      const ReactDOM = reactDOMModule.default
      const host = document.createElement('div')
      host.id = 'error-boundary-smoke'
      document.body.appendChild(host)
      function Boom() { throw new Error('Boundary smoke failure') }
      ReactDOM.createRoot(host).render(React.createElement(ErrorBoundary, null, React.createElement(Boom)))
    })()`)
    await waitFor(`document.getElementById('error-boundary-smoke')?.innerText.includes('Something went wrong')`, 'error boundary fallback')
    const boundaryText = await evaluate(`document.getElementById('error-boundary-smoke')?.innerText ?? ''`)
    assert(boundaryText.includes('Boundary smoke failure'), 'Error boundary message missing')
    assert(boundaryText.includes('Reload page'), 'Error boundary reload button missing')
    errorBoundary = { tested: true, heading: 'Something went wrong', sanitizedMessage: 'Boundary smoke failure', reloadButton: true }
  }

  const apiFailures = responses.filter((response) => response.status >= 400)
  assert(exceptions.length === 0, `Browser exceptions: ${exceptions.map((entry) => entry.text).join('; ')}`)
  assert(failedRequests.length === 0, `Failed requests: ${failedRequests.map((entry) => entry.errorText).join('; ')}`)
  assert(apiFailures.length === 0, `API failures: ${apiFailures.map((entry) => `${entry.status} ${entry.url}`).join('; ')}`)
  report = {
    ok: true,
    baseURL,
    tabProof,
    errorBoundary,
    apiResponses: responses,
    exceptions,
    failedRequests,
    consoleErrorCount: consoleEntries.filter((entry) => entry.type === 'error').length,
  }
} catch (error) {
  report = {
    ok: false,
    baseURL,
    error: error instanceof Error ? error.message : String(error),
    exceptions,
    failedRequests,
    apiResponses: responses,
  }
  process.exitCode = 1
} finally {
  await writeFile(outputPath, `${JSON.stringify(report, null, 2)}\n`)
  await writeFile(consolePath, `${[
    ...consoleEntries.map((entry) => `[${entry.type}] ${entry.text}`),
    ...exceptions.map((entry) => `[exception] ${entry.text}\n${entry.stack.join('\n')}`),
    ...failedRequests.map((entry) => `[request-failed] ${entry.errorText}`),
  ].join('\n')}\n`)
  console.log(JSON.stringify(report, null, 2))
  ws.close()
  await fetch(`${cdpURL}/json/close/${target.id}`)
}
