/** OSCAL implementation-status values. */
export type RequirementStatus = 'implemented' | 'partial' | 'planned' | 'alternative' | 'not_applicable'
export type EvidenceKind = 'file' | 'link'

export const REQUIREMENT_STATUSES: RequirementStatus[] = [
  'implemented',
  'partial',
  'planned',
  'alternative',
  'not_applicable',
]
export const EVIDENCE_KINDS: EvidenceKind[] = ['file', 'link']

export interface ApiErrorBody {
  code: string
  message?: string
  [key: string]: unknown
}

export interface Framework {
  id: string
  oscalDocumentId: string
  type: string
  title: string
  /** Short display name, e.g. "NIST SP 800-53". Used wherever a framework is named. */
  shortName: string
  lastModified?: string
  importedAt: string
  requirementCount: number
}

export interface FrameworkPatch {
  shortName: string
}

export interface ScopeCategory {
  id: string
  name: string
  createdAt: string
  updatedAt: string
}

export interface OscalDocument {
  id: string
  type: string
  title: string
  lastModified?: string
  controlCount: number
  version?: string
}

export interface OscalSource {
  id: string
  url: string
  status: 'available' | 'unavailable'
  documents: OscalDocument[]
  errorCode?: string
}

export interface OscalUpload {
  id: string
  documentId: string
  title: string
  type: string
  version?: string
  lastModified?: string
  sha256: string
  sizeBytes: number
  uploadedBy: string
  uploadedAt: string
  importedFrameworkId?: string | null
}

export type OscalUploadDetail = OscalUpload & {
  document: Record<string, unknown>
}

export type FrameworkImportInput =
  | { documentId: string; uploadId?: never }
  | { uploadId: string; documentId?: never }

export interface ImportResult {
  framework: Framework
  created: number
  updated: number
}

/** OSCAL control part (statement, guidance, assessment-objective, …); recursive. */
export interface Part {
  id?: string
  name: string
  label?: string
  /** Prose with OSCAL parameter placeholders resolved server-side into bracketed text,
   *  e.g. `[Assignment: organization-defined official]` or `[Selection (one or more): a; b]`. */
  prose?: string
  /** Machine form of `prose` with unresolved `{{ insert: param, … }}` placeholders, when it differs. */
  rawProse?: string
  parts?: Part[]
}

/** Resolved OSCAL parameter (present on the detail endpoint). */
export interface OscalParam {
  id: string
  label?: string
  values?: string[]
  choices?: string[]
  howMany?: string
}

/** External reference of a control (OSCAL link with rel=external_reference), resolved against back-matter. */
export interface Reference {
  /** Back-matter resource uuid; empty when the link was a plain URL. */
  uuid: string
  /** Resource title, e.g. "BSIMM"; empty when unresolved. */
  title: string
  citation?: string
  url?: string
  /** Link text naming the referenced section, e.g. "SA-11" or "CR1.5". */
  text?: string
  /** Folded SSDF task (e.g. "PO.1.1") that carried the link; absent for the control's own links. */
  taskId?: string
}

export interface Requirement {
  id: string
  frameworkId: string
  controlId: string
  title: string
  text: string
  /** Effective status: `statusOverride` when set, otherwise `derivedStatus`. */
  status: RequirementStatus
  /** Roll-up of the trackable parts' statuses. */
  derivedStatus: RequirementStatus
  /** Manual override of the roll-up; absent/null when not overridden. */
  statusOverride?: RequirementStatus | null
  /** Derived (read-only): most common part owner. */
  owner: string
  /** Derived (read-only): earliest open part due date. */
  dueDate?: string | null
  notes: string
  updatedAt: string
  /** Present on the detail endpoint; the list endpoint may omit it. */
  parts?: Part[]
  /** Related control ids (e.g. "ac-3"); rel=related links only. */
  related?: string[]
  params?: OscalParam[]
  /** Present on the detail endpoint; the list endpoint omits it. */
  references?: Reference[]
}

/** Per-statement-part tracking record (detail endpoint). */
export interface TrackablePart {
  /** OSCAL statement part id, e.g. "ac-2_smt.a". */
  partId: string
  /** Catalog label such as "a.", absent for a single unlabelled statement. */
  label?: string
  prose: string
  /** Organization-defined boundaries for which this implementation claim applies. */
  scopes: ScopeCategory[]
  status: RequirementStatus
  owner: string
  dueDate?: string | null
  /** Markdown. */
  description: string
  updatedAt?: string
}

export interface TrackablePartPatch {
  scopeCategoryIds?: string[]
  status?: RequirementStatus
  owner?: string
  dueDate?: string | null
  description?: string
}

export interface TrackablePartPatchResult {
  part: TrackablePart
  requirement: Requirement
}

export interface Evidence {
  id: string
  kind: EvidenceKind
  title: string
  description: string
  /** Link evidence only. */
  url?: string
  fileName: string
  contentType: string
  sizeBytes: number
  sha256: string
  validFrom?: string | null
  validUntil?: string | null
  uploadedBy: string
  createdAt: string
  requirementIds: string[]
}

export type RequirementDetail = Requirement & {
  parts: Part[]
  related: string[]
  params: OscalParam[]
  references: Reference[]
  trackableParts: TrackablePart[]
  /** Requirement-level evidence. */
  evidence: Evidence[]
}

/** Only notes and the status override are writable on a requirement; owner/due date/status are derived from parts. */
export interface RequirementPatch {
  notes?: string
  statusOverride?: RequirementStatus | null
}

export interface AuditEntry {
  id: string
  at: string
  actor: string
  action: string
  entityType: string
  entityId: string
  detail: Record<string, unknown>
}

export type StatusCounts = Record<RequirementStatus, number>

export interface FrameworkSummary {
  frameworkId: string
  title: string
  shortName?: string
  total: number
  applicable: number
  implemented: number
  coveragePercent: number
  byStatus: StatusCounts
  overdue: number
  /** Due within the next 30 days (not overdue). */
  dueSoon: number
}

export interface DashboardTotals {
  total: number
  applicable: number
  implemented: number
  coveragePercent: number
  byStatus: StatusCounts
  overdue: number
  dueSoon: number
}

/** Requirement with a due date, soonest first; overdue ones have negative `daysUntilDue`. */
export type UpcomingRequirement = Requirement & {
  daysUntilDue: number
  overdue: boolean
  frameworkShortName: string
}

export interface Dashboard {
  generatedAt: string
  frameworks: FrameworkSummary[]
  totals: DashboardTotals
  upcomingRequirements: UpcomingRequirement[]
  /** Latest audit entries (API field name is fixed). */
  recentActivity: AuditEntry[]
}

export interface Page<T> {
  items: T[]
  total: number
}

export interface RequirementQuery {
  frameworkId?: string
  status?: RequirementStatus | ''
  q?: string
  scopeCategoryId?: string
  limit?: number
  offset?: number
}

export type AssessmentType = 'assessment-plan' | 'assessment-results' | 'plan-of-action-and-milestones'
export type AssessmentLinkSource = 'automatic' | 'manual' | 'unlinked' | null

export interface AssessmentMapping {
  itemKey: string
  requirementId: string
  partId?: string | null
  controlId?: string
  partLabel?: string | null
  frameworkId?: string
  frameworkShortName?: string
  source?: 'automatic' | 'manual' | 'unmapped'
  item?: Record<string, unknown>
}

export interface AssessmentItem extends Record<string, unknown> {
  itemKey: string
  kind: string
  title?: string
  summary?: string
  description?: string
  status?: string
  risk?: string
  severity?: string
  owner?: string
  controlIds?: string[]
  objectiveIds?: string[]
  partIds?: string[]
  evidenceRefs?: string[]
  relatedObservationIds?: string[]
  relatedFindingIds?: string[]
  relatedRiskIds?: string[]
  responsibleRoles?: string[]
  methods?: string[]
  subjectIds?: string[]
  scheduledCompletion?: string
  milestones?: AssessmentItem[]
  mapping?: Omit<AssessmentMapping, 'itemKey'>
}

/** Assessment payload content varies by OSCAL model and version, so model-specific fields remain unknown. */
export interface AssessmentSummary {
  uploadId: string
  type: AssessmentType
  title: string
  version?: string
  uploadedAt?: string
  frameworkId?: string | null
  frameworkShortName?: string | null
  effectiveFrameworkId?: string | null
  effectiveFrameworkShortName?: string | null
  linkSource?: AssessmentLinkSource
  mappedCount?: number
  unmappedCount?: number
  counts?: Record<string, number>
  summary?: Record<string, unknown>
  [key: string]: unknown
}

export interface AssessmentDetail extends AssessmentSummary {
  importRefs?: string[]
  reviewedControls?: AssessmentItem[]
  subjects?: AssessmentItem[]
  tasks?: AssessmentItem[]
  results?: AssessmentItem[]
  observations?: AssessmentItem[]
  findings?: AssessmentItem[]
  risks?: AssessmentItem[]
  attestations?: AssessmentItem[]
  resultLog?: AssessmentItem[]
  poamItems?: AssessmentItem[]
  mappings?: AssessmentMapping[]
  mappedItems?: Record<string, unknown>[]
  unmappedItems?: AssessmentItem[]
  document?: Record<string, unknown>
  content?: Record<string, unknown>
}

export interface AssessmentQuery {
  type: AssessmentType
  frameworkId?: string
  limit?: number
  offset?: number
}

export interface RequirementAssessmentRecord {
  uploadId: string
  type: AssessmentType
  title?: string
  itemKey?: string
  partId?: string | null
  partLabel?: string | null
  status?: string
  risk?: string
  severity?: string
  summary?: string
  record?: Record<string, unknown>
  [key: string]: unknown
}

export interface EvidenceQuery {
  requirementId?: string
  kind?: EvidenceKind | ''
  q?: string
  limit?: number
  offset?: number
}

interface EvidenceCommon {
  title: string
  description?: string
  validFrom?: string
  validUntil?: string
  requirementIds?: string[]
}

export interface EvidenceUpload extends EvidenceCommon {
  file: File
}

export interface LinkEvidenceCreate extends EvidenceCommon {
  url: string
}
