import { get, post, put } from '@/utils/request'
import type { Project, Result } from './workspace'

export interface CollaborationMember {
 user_id: string
 role: string
 governance_role?: string
 function_roles?: string[]
 function_scopes?: Record<string,string[]>
 status?: string
}
export interface MemberAssignment {
 user_id: string
 role: 'collaborator'
 governance_role: 'admin'|'member'
 function_roles: string[]
 function_scopes: Record<string,string[]>
}
export interface MemberDirectoryEntry { user_id:string; name:string; tenant_role:string }
export interface ProjectInvitation {
 id:string; project_id:string; user_id:string; version:number; assignment:CollaborationMember
 status:string; inviter:string; reason:string; expires_at:string; created_at:string; accepted_at?:string
}
export interface InviteMemberInput {
 expected_project_version:number; assignment:MemberAssignment; reason:string
 invitation_id?:string; expected_invitation_version?:number
}
export interface MemberAssignmentInput { expected_project_version:number; assignment:MemberAssignment; reason:string }
export type MemberStateAction='suspend'|'remove'|'restore'|'restore_tenant'
export interface MemberStateInput {expected_project_version:number;action:MemberStateAction;reason:string}
export interface CollaborationOwnerTransfer {
 id:string;project_id:string;from_user_id:string;to_user_id:string;status:string
 expected_project_version:number;created_at:string;accepted_at?:string
}
export interface OwnerTransferRequest {to_user_id:string;expected_project_version:number}
const segment=(value:string)=>encodeURIComponent(value)
const base=(projectId:string)=>`/api/v1/lingdoc/projects/${segment(projectId)}`
const headers=(key:string)=>({headers:{'Idempotency-Key':key}})
export const listMemberDirectory=(id:string)=>get<Result<MemberDirectoryEntry[]>>(`${base(id)}/members/directory`)
export const listMyProjectInvitations=()=>get<Result<ProjectInvitation[]>>('/api/v1/lingdoc/project-invitations')
export const listProjectInvitations=(id:string)=>get<Result<ProjectInvitation[]>>(`${base(id)}/invitations`)
export const inviteProjectMember=(id:string,input:InviteMemberInput,key:string)=>post<Result<ProjectInvitation>>(`${base(id)}/invitations`,input,headers(key))
export const acceptProjectInvitation=(id:string,invitationId:string,version:number,key:string)=>post<Result<ProjectInvitation>>(`${base(id)}/invitations/${segment(invitationId)}/accept`,{expected_invitation_version:version},headers(key))
export const updateMemberAssignment=(id:string,userId:string,input:MemberAssignmentInput,key:string)=>put<Result<Project>>(`${base(id)}/members/${segment(userId)}/assignment`,input,headers(key))
export const changeMemberState=(id:string,userId:string,input:MemberStateInput,key:string)=>post<Result<Project>>(`${base(id)}/members/${segment(userId)}/state`,input,headers(key))
export const getCollaborationProject=(id:string)=>get<Result<Project>>(base(id))
export const listOwnerTransfers=(id:string)=>get<Result<CollaborationOwnerTransfer[]>>(`${base(id)}/owner-transfers`)
export const requestOwnerTransfer=(id:string,input:OwnerTransferRequest,key:string)=>post<Result<CollaborationOwnerTransfer>>(`${base(id)}/owner-transfer`,input,headers(key))
export const acceptOwnerTransfer=(id:string,transferId:string,key:string)=>post<Result<CollaborationOwnerTransfer>>(`${base(id)}/owner-transfer/${segment(transferId)}/accept`,{},headers(key))
export const rejectOwnerTransfer=(id:string,transferId:string,expectedProjectVersion:number,reason:string,key:string)=>post<Result<CollaborationOwnerTransfer>>(`${base(id)}/owner-transfer/${segment(transferId)}/reject`,{expected_project_version:expectedProjectVersion,reason},headers(key))
