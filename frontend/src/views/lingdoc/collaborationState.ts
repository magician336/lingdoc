import type { Project,Chapter,Asset } from '@/api/lingdoc/workspace'
import type { CollaborationMember,MemberAssignment,MemberStateAction } from '@/api/lingdoc/collaboration'
export const functionRoles=[{id:'author',label:'写作者'},{id:'researcher',label:'调研者'},{id:'reviewer',label:'复核者'},{id:'observer',label:'观察者'}]
const knownRoles=new Set(functionRoles.map(role=>role.id))
export const cloneValue=<T>(value:T):T=>JSON.parse(JSON.stringify(value))
export function memberStateLabel(status?:string):string {
 const labels:Record<string,string>={active:'当前成员',invited:'待本人接受',suspended:'项目内已暂停',suspended_by_tenant:'租户暂停；待项目复核恢复',removed:'已移除',expired:'已过期',accepted:'邀请已接受',pending:'等待接任者确认',rejected:'已拒绝',cancelled:'已取消'}
 if(!status)return '当前成员（兼容旧记录）'
 return labels[status]||`待识别状态（${status}）`
}
export function governanceRole(member:CollaborationMember):string{return member.governance_role||(member.role==='owner'?'owner':'member')}
export function currentMember(member?:CollaborationMember):boolean{return !!member&&(!member.status||member.status==='active')}
export function collaborationAccess(project:Project,currentUserId?:string,readonly=false){
 const member=project.members.find(member=>member.user_id===currentUserId)
 const role=member?governanceRole(member):''
 return {owner:!readonly&&currentMember(member)&&role==='owner',manager:!readonly&&currentMember(member)&&(role==='owner'||role==='admin')}
}
export function lifecycleActions(member:CollaborationMember):MemberStateAction[]{
 if(governanceRole(member)==='owner')return []
 if(!member.status||member.status==='active')return ['suspend','remove']
 if(member.status==='suspended')return ['restore','remove']
 if(member.status==='suspended_by_tenant')return ['restore_tenant','remove']
 if(member.status==='invited')return ['remove']
 return []
}
export interface ScopeOption {id:string;label:string}
export function resourceOptions(project:Project,chapters:Chapter[],assets:Asset[]):ScopeOption[]{
 const options:ScopeOption[]=[]
 if(project.status!=='draft')for(const chapter of chapters.filter(chapter=>chapter.project_id===project.id))options.push({id:chapter.id,label:`章节 · ${chapter.title}`})
 for(const asset of assets.filter(asset=>asset.project_id===project.id))options.push({id:`asset:${asset.id}`,label:`资料 · ${asset.title}`})
 return options.filter((option,index)=>options.findIndex(other=>other.id===option.id)===index)
}
export interface AssignmentDraft {
 userId:string;governance:string;roles:string[];scopes:Record<string,string[]>
 basedOnVersion:number;reason:string;initial:string;invitationId?:string;invitationVersion?:number
}
export function assignmentFingerprint(draft:AssignmentDraft):string{return JSON.stringify({userId:draft.userId,governance:draft.governance,roles:draft.roles,scopes:draft.scopes,basis:draft.basedOnVersion,reason:draft.reason,invitation:draft.invitationId,invitationVersion:draft.invitationVersion})}
export function assignmentDraft(member:CollaborationMember|undefined,version:number,userId=''):AssignmentDraft{
 const fresh=!member||member.status==='removed'
 const roles=fresh?[]:cloneValue(member.function_roles||[])
 const scopes=fresh?{}:cloneValue(member.function_scopes||{})
 const draft:AssignmentDraft={userId:member?.user_id||userId,governance:fresh?'member':governanceRole(member),roles,scopes,basedOnVersion:version,reason:'',initial:''}
 draft.initial=assignmentFingerprint(draft);return draft
}
export function setFunctionRole(draft:AssignmentDraft,role:string,enabled:boolean){
 if(enabled&&!draft.roles.includes(role)){draft.roles.push(role);draft.scopes[role]??=[]}
 if(!enabled)draft.roles=draft.roles.filter(value=>value!==role)
}
export function serializeAssignment(draft:AssignmentDraft):MemberAssignment{
 if(!draft.userId.trim()||!['member','admin'].includes(draft.governance))throw new Error('请选择真实成员与项目治理身份。')
 if(draft.roles.some(role=>!knownRoles.has(role)))throw new Error('存在未识别职责，请保留编辑并重新读取，或明确移除该职责后再复核。')
 const scopes:Record<string,string[]>={}
 // Empty scope is a deliberate absence of object-level write grants.
 for(const role of draft.roles)scopes[role]=[...new Set(draft.scopes[role]||[])]
 // Server-supplied orphan scopes are retained until a curator explicitly
 // resolves them; no serializer silently drops unknown grants.
 for(const [role,values] of Object.entries(draft.scopes)){if(!knownRoles.has(role)&&!draft.roles.includes(role))scopes[role]=cloneValue(values)}
 return {user_id:draft.userId,role:'collaborator',governance_role:draft.governance as 'member'|'admin',function_roles:[...draft.roles],function_scopes:scopes}
}
export function unseenScopes(draft:AssignmentDraft,role:string,options:ScopeOption[]):string[]{const ids=new Set(options.map(option=>option.id));return (draft.scopes[role]||[]).filter(scope=>!ids.has(scope))}
export function actionableInvitation(status:string,expiresAt:string,currentUserId:string|undefined,userId:string,now=Date.now()):boolean{return status==='invited'&&userId===currentUserId&&Number.isFinite(Date.parse(expiresAt))&&Date.parse(expiresAt)>now}
export interface EpochToken {context:string;generation:number}
export class CollaborationEpoch {
 private context='';private generation=0
 switchContext(context:string){this.context=context;this.generation++;return this.capture()}
 capture():EpochToken{return {context:this.context,generation:this.generation}}
 current(token:EpochToken){return token.context===this.context&&token.generation===this.generation}
 dispose(){this.generation++}
}
export interface CollaborationAttempt {signature:string;key:string}
export function collaborationAttempt(previous:CollaborationAttempt|undefined,input:unknown,mint:()=>string):CollaborationAttempt{
 const signature=JSON.stringify(input);return previous?.signature===signature?previous:{signature,key:mint()}
}

export function ownerTransferLabel(status:string):string {
 const labels:Record<string,string>={pending:'等待接任者确认',accepted:'交接已接受',cancelled:'交接已取消',expired:'交接已过期'}
 return labels[status]||`待识别状态（${status}）`
}
