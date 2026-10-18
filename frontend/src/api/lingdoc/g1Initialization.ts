import { get, post } from '@/utils/request'
import type { Project,Result } from './workspace'
export interface ActivationDiff {project_id:string;current_project_version:number;current_spec_revision:number;changed_since_version:number;changed_fields:Array<{key:string;value:string;origin:string;status:string;modified_by:string;modified_at:string}>;pending_ai_fields:Array<{key:string;value:string;origin:string;status:string}>}
export interface ActivationReadReceipt {id:string;actor_id:string;project_id:string;project_version:number;spec_revision:number;read_at:string}
const path=(id:string)=>`/api/v1/lingdoc/projects/${encodeURIComponent(id)}`
export const getActivationDiff=(id:string,since=0)=>get<Result<ActivationDiff>>(`${path(id)}/activation-diff`,{params:{since_project_version:since}})
export const recordActivationRead=(id:string,version:number)=>post<Result<ActivationReadReceipt>>(`${path(id)}/activation-read`,{expected_project_version:version})

export const listDiscardedProjects=()=>get<Result<Project[]>>('/api/v1/lingdoc/discarded-projects')
export const discardDraftProject=(id:string,version:number,key:string)=>post<Result<Project>>(`${path(id)}/discard`,{expected_project_version:version},{headers:{'Idempotency-Key':key}})
export const restoreDraftProject=(id:string,version:number,key:string)=>post<Result<Project>>(`${path(id)}/restore`,{expected_project_version:version},{headers:{'Idempotency-Key':key}})

export interface DraftCandidate {id:string;project_id:string;kind:string;title:string;content:string;level:string;based_on_context_revision:number;created_by:string;created_at:string;provenance?:{source_type:string;source_id?:string;locator?:string}}
export interface DraftCandidateInput {kind:string;title:string;content:string;level:'background'|'candidate_evidence'|'discovery'}
export const listDraftCandidates=(id:string)=>get<Result<DraftCandidate[]>>(`${path(id)}/draft-candidates`)
export const createDraftCandidate=(id:string,input:DraftCandidateInput,key:string)=>post<Result<DraftCandidate>>(`${path(id)}/draft-candidates`,input,{headers:{'Idempotency-Key':key}})
