import { get, post, put } from '@/utils/request'

export interface Project {
  id: string
  name: string
  status: 'draft' | 'active'
  project_version: number
  spec_revision: number
  spec: Record<string, string>
  template_id: string
  template_version: string
  members: Array<{ user_id: string; role: 'owner' | 'collaborator' }>
}

export interface ReviewItem {
  id: string
  statement: string
  origin_candidate_id: string
}

export interface ReviewDecision {
  review_item_id: string
  disposition: 'resolved' | 'retained_warning'
  reason: string
}

export interface Confirmation {
  id: string
  chapter_id: string
  chapter_version_id: string
  spec_revision: number
  asset_versions: Array<{ asset_id: string; asset_revision: number }>
  template_version: string
  actor_user_id: string
  created_at: string
  valid: boolean
  review_decisions: ReviewDecision[]
}

export interface Chapter {
  id: string
  project_id: string
  section_id: string
  title: string
  current_version_id: string | null
  body_markdown: string
  source_ids: string[]
  review_items: ReviewItem[]
  confirmation_valid: boolean
}

export interface Asset {
  id: string
  project_id: string
  knowledge_id: string
  title: string
  asset_revision: number
  processing_state: 'pending' | 'processing' | 'ready' | 'failed' | 'replaced'
}

export interface Source {
  id: string
  project_id: string
  asset_id: string
  asset_revision: number
  locator: string
  quoted_text: string
  quoted_text_hash: string
  status: 'available' | 'stale' | 'unavailable'
}

export interface Result<T> {
  data: T
  request_id: string
  meta: { replayed: boolean; refresh_required: boolean }
}

const base = '/api/v1/lingdoc/projects'
const segment = (id: string) => encodeURIComponent(id)
const keyHeader = (key: string) => ({ headers: { 'Idempotency-Key': key } })

export const listProjects = () => get<Result<{ items: Project[]; truncated: boolean }>>(base)
export const createProject = (name: string, key: string) =>
  post<Result<Project>>(base, { name, template_id: 'template-demo' }, keyHeader(key))
export const getProject = (id: string) => get<Result<Project>>(`${base}/${segment(id)}`)
export const saveSpec = (id: string, expected: number, fields: Record<string, string>, key: string) =>
  put<Result<Project>>(`${base}/${segment(id)}/spec`,
    { expected_spec_revision: expected, fields }, keyHeader(key))
export const activateProject = (id: string, expected: number, key: string) =>
  post<Result<Project>>(`${base}/${segment(id)}/activate`,
    { expected_spec_revision: expected }, keyHeader(key))
export const listChapters = (id: string) => get<Result<Chapter[]>>(`${base}/${segment(id)}/chapters`)
export const listAssets = (id: string) => get<Result<Asset[]>>(`${base}/${segment(id)}/assets`)
export const bindAsset = (id: string, knowledgeId: string, key: string) =>
  post<Result<Asset>>(`${base}/${segment(id)}/assets`, { knowledge_id: knowledgeId }, keyHeader(key))
export const retrieveSources = (id: string, query: string, assetIds: string[]) =>
  post<Result<Source[]>>(`${base}/${segment(id)}/retrieval`, { query, asset_ids: assetIds })
export const getSource = (projectId: string, sourceId: string) =>
  get<Result<Source>>(`${base}/${segment(projectId)}/sources/${segment(sourceId)}`)
export const saveChapter = (projectId: string, chapterId: string, input: {
  expected_chapter_version_id: string | null
  expected_spec_revision: number
  body_markdown: string
  source_ids: string[]
}, key: string) => post<Result<Chapter>>(
  `${base}/${segment(projectId)}/chapters/${segment(chapterId)}/versions`, input, keyHeader(key),
)
export const confirmChapter = (projectId: string, chapterId: string, input: {
  expected_chapter_version_id: string
  expected_spec_revision: number
  review_decisions: ReviewDecision[]
}, key: string) => post<Result<Confirmation>>(
  `${base}/${segment(projectId)}/chapters/${segment(chapterId)}/confirmations`, input, keyHeader(key),
)
