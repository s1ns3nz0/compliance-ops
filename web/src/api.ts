import type {
  ApiErrorBody,
  AssessmentDetail,
  AssessmentMapping,
  AssessmentQuery,
  AssessmentSummary,
  AuditEntry,
  Dashboard,
  Evidence,
  EvidenceQuery,
  EvidenceUpload,
  Framework,
  FrameworkPatch,
  FrameworkImportInput,
  ImportResult,
  LinkEvidenceCreate,
  OscalDocument,
  OscalSource,
  OscalUpload,
  OscalUploadDetail,
  Page,
  Requirement,
  RequirementDetail,
  RequirementPatch,
  RequirementQuery,
  RequirementAssessmentRecord,
  ScopeCategory,
  TrackablePartPatch,
  TrackablePartPatchResult,
} from './types'

export const AUTH_STORAGE_KEY = 'compliance-ops.auth'

export interface AuthState {
  token: string
}

export function loadAuth(): AuthState | null {
  try {
    // Keep bearer credentials scoped to this tab and browser session. This
    // storage is not shared with new tabs or later browser sessions.
    const raw = sessionStorage.getItem(AUTH_STORAGE_KEY)
    if (!raw) return null
    const parsed = JSON.parse(raw) as Partial<AuthState>
    if (!parsed.token) return null
    return { token: parsed.token }
  } catch {
    return null
  }
}

export function saveAuth(auth: AuthState | null) {
  // A 401 and explicit disconnect both remove the tab-scoped credential.
  if (auth) sessionStorage.setItem(AUTH_STORAGE_KEY, JSON.stringify(auth))
  else sessionStorage.removeItem(AUTH_STORAGE_KEY)
}

export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly body: Readonly<ApiErrorBody>

  constructor(status: number, code: string, message?: string, body: ApiErrorBody = { code }) {
    super(message || code)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.body = body
  }

  get isUnauthorized() {
    return this.status === 401
  }
}

const UNAUTHORIZED_EVENT = 'compliance-ops:unauthorized'

export function onUnauthorized(handler: () => void): () => void {
  window.addEventListener(UNAUTHORIZED_EVENT, handler)
  return () => window.removeEventListener(UNAUTHORIZED_EVENT, handler)
}

function authHeaders(): Record<string, string> {
  const auth = loadAuth()
  const headers: Record<string, string> = {}
  if (auth?.token) headers.Authorization = `Bearer ${auth.token}`
  return headers
}

async function toApiError(res: Response): Promise<ApiError> {
  let body: ApiErrorBody | null = null
  try {
    body = (await res.json()) as ApiErrorBody
  } catch {
    // non-JSON error body
  }
  const code = body?.code || `HTTP_${res.status}`
  return new ApiError(res.status, code, body?.message || friendlyMessage(res.status, code), body ?? { code })
}

function friendlyMessage(status: number, code: string): string {
  switch (code) {
    case 'UNAUTHORIZED':
      return 'Unauthorized: token is missing or invalid.'
    case 'NOT_FOUND':
      return 'Not found.'
    case 'INVALID_QUERY':
      return 'Invalid query parameters.'
    case 'INVALID_BODY':
      return 'Invalid request body.'
    case 'PAYLOAD_TOO_LARGE':
      return 'File is too large.'
    case 'TRACKING_UNAVAILABLE':
      return 'Tracking store is unavailable (503). Try again later.'
    case 'OSCAL_SOURCE_UNAVAILABLE':
      return 'OSCAL source is unavailable (503). Check the configured OSCAL source.'
    default:
      return `Request failed (${status} ${code}).`
  }
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  for (const [k, v] of Object.entries(authHeaders())) headers.set(k, v)
  if (init.body && !(init.body instanceof FormData) && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json')
  }
  headers.set('Accept', 'application/json')

  const res = await fetch(path, { ...init, headers })
  if (res.status === 401) {
    saveAuth(null)
    window.dispatchEvent(new Event(UNAUTHORIZED_EVENT))
  }
  if (!res.ok) throw await toApiError(res)
  if (res.status === 204) return undefined as T
  return (await res.json()) as T
}

function qs(params: object): string {
  const sp = new URLSearchParams()
  for (const [k, v] of Object.entries(params as Record<string, string | number | undefined | null>)) {
    if (v === undefined || v === null || v === '') continue
    sp.set(k, String(v))
  }
  const s = sp.toString()
  return s ? `?${s}` : ''
}

export const api = {
  health: () => fetch('/healthz').then((r) => r.ok),

  // Frameworks
  listFrameworks: () => request<{ items: Framework[] }>('/v1/frameworks'),
  importFrameworks: (input: FrameworkImportInput) =>
    request<{ items: ImportResult[] }>('/v1/frameworks/import', {
      method: 'POST',
      body: JSON.stringify(input),
    }),
  patchFramework: (id: string, patch: FrameworkPatch) =>
    request<Framework>(`/v1/frameworks/${encodeURIComponent(id)}`, {
      method: 'PATCH',
      body: JSON.stringify(patch),
    }),
  listOscalDocuments: () => request<{ items: OscalDocument[] }>('/v1/oscal/documents'),
  listOscalSources: () => request<{ items: OscalSource[] }>('/v1/oscal/sources'),
  listOscalUploads: () => request<{ items: OscalUpload[] }>('/v1/oscal/uploads'),
  getOscalUpload: (id: string) => request<OscalUploadDetail>(`/v1/oscal/uploads/${encodeURIComponent(id)}`),
  uploadOscal: (file: File) => {
    const fd = new FormData()
    fd.append('file', file)
    return request<OscalUpload>('/v1/oscal/uploads', { method: 'POST', body: fd })
  },
  deleteOscalUpload: (id: string) =>
    request<void>(`/v1/oscal/uploads/${encodeURIComponent(id)}`, { method: 'DELETE' }),

  // Requirements
  listRequirements: (query: RequirementQuery = {}) =>
    request<Page<Requirement>>(`/v1/requirements${qs(query)}`),
  getRequirement: (id: string) =>
    request<RequirementDetail>(`/v1/requirements/${encodeURIComponent(id)}`),
  patchRequirement: (id: string, patch: RequirementPatch) =>
    request<Requirement>(`/v1/requirements/${encodeURIComponent(id)}`, {
      method: 'PATCH',
      body: JSON.stringify(patch),
    }),

  patchRequirementPart: (requirementId: string, partId: string, patch: TrackablePartPatch) =>
    request<TrackablePartPatchResult>(
      `/v1/requirements/${encodeURIComponent(requirementId)}/parts/${encodeURIComponent(partId)}`,
      { method: 'PATCH', body: JSON.stringify(patch) },
    ),
  listScopeCategories: () => request<{ items: ScopeCategory[] }>('/v1/scope-categories?limit=100'),
  createScopeCategory: (name: string) =>
    request<ScopeCategory>('/v1/scope-categories', { method: 'POST', body: JSON.stringify({ name }) }),
  renameScopeCategory: (id: string, name: string) =>
    request<ScopeCategory>(`/v1/scope-categories/${encodeURIComponent(id)}`, {
      method: 'PATCH', body: JSON.stringify({ name }),
    }),
  deleteScopeCategory: (id: string) =>
    request<void>(`/v1/scope-categories/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  listRequirementAssessments: (id: string) =>
    request<{ items: RequirementAssessmentRecord[] }>(`/v1/requirements/${encodeURIComponent(id)}/assessments`),

  // Assessments
  listAssessments: (query: AssessmentQuery) =>
    request<Page<AssessmentSummary>>(`/v1/assessments${qs(query)}`),
  getAssessment: (uploadId: string) =>
    request<AssessmentDetail>(`/v1/assessments/${encodeURIComponent(uploadId)}`),
  linkAssessmentFramework: (uploadId: string, frameworkId: string | null) =>
    request<AssessmentDetail>(`/v1/assessments/${encodeURIComponent(uploadId)}/framework`, {
      method: 'PATCH', body: JSON.stringify({ frameworkId }),
    }),
  createAssessmentMapping: (uploadId: string, input: { itemKey: string; requirementId: string; partId?: string }) =>
    request<AssessmentMapping>(`/v1/assessments/${encodeURIComponent(uploadId)}/mappings`, {
      method: 'POST', body: JSON.stringify(input),
    }),
  deleteAssessmentMapping: (uploadId: string, itemKey: string) =>
    request<void>(`/v1/assessments/${encodeURIComponent(uploadId)}/mappings/${encodeURIComponent(itemKey)}`, {
      method: 'DELETE',
    }),

  // Evidence
  listEvidence: (query: EvidenceQuery = {}) => request<Page<Evidence>>(`/v1/evidence${qs(query)}`),
  getEvidence: (id: string) => request<Evidence>(`/v1/evidence/${encodeURIComponent(id)}`),
  uploadEvidence: (input: EvidenceUpload) => {
    const fd = new FormData()
    fd.append('file', input.file)
    fd.append('title', input.title)
    if (input.description) fd.append('description', input.description)
    if (input.validFrom) fd.append('validFrom', input.validFrom)
    if (input.validUntil) fd.append('validUntil', input.validUntil)
    for (const id of input.requirementIds ?? []) fd.append('requirementIds', id)
    return request<Evidence>('/v1/evidence', { method: 'POST', body: fd })
  },
  /** Link evidence is created with a JSON body (no file). */
  createLinkEvidence: (input: LinkEvidenceCreate) =>
    request<Evidence>('/v1/evidence', {
      method: 'POST',
      body: JSON.stringify({
        kind: 'link',
        url: input.url,
        title: input.title,
        description: input.description,
        validFrom: input.validFrom,
        validUntil: input.validUntil,
        requirementIds: input.requirementIds?.length ? input.requirementIds : undefined,
      }),
    }),
  /** Fetches the binary with the bearer header and triggers a browser download via a blob URL. */
  downloadEvidence: async (id: string, fileName: string) => {
    const res = await fetch(`/v1/evidence/${encodeURIComponent(id)}/download`, {
      headers: authHeaders(),
    })
    if (res.status === 401) {
      saveAuth(null)
      window.dispatchEvent(new Event(UNAUTHORIZED_EVENT))
    }
    if (!res.ok) throw await toApiError(res)
    const blob = await res.blob()
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = fileName || 'evidence'
    document.body.appendChild(a)
    a.click()
    a.remove()
    setTimeout(() => URL.revokeObjectURL(url), 10_000)
  },

  // Dashboard & audit
  dashboard: () => request<Dashboard>('/v1/dashboard'),
  audit: (limit = 50) => request<{ items: AuditEntry[] }>(`/v1/audit${qs({ limit })}`),
}

export function errorMessage(err: unknown): string {
  if (err instanceof ApiError) return err.message
  if (err instanceof Error) return err.message
  return String(err)
}
