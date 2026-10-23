<script setup lang="ts">
import { computed,nextTick,onBeforeUnmount,onMounted,reactive,ref,watch } from 'vue'
import type { Asset,Chapter,Project } from '@/api/lingdoc/workspace'
import {
 acceptOwnerTransfer,acceptProjectInvitation,changeMemberState,getCollaborationProject,inviteProjectMember,listMemberDirectory,listOwnerTransfers,listProjectInvitations,rejectOwnerTransfer,requestOwnerTransfer,updateMemberAssignment,
 type CollaborationMember,type CollaborationOwnerTransfer,type MemberDirectoryEntry,type MemberStateAction,type ProjectInvitation,
} from '@/api/lingdoc/collaboration'
import { newIdempotencyKey } from '@/api/lingdoc/candidateAdoption'
import { actionableInvitation,assignmentDraft,assignmentFingerprint,cloneValue,collaborationAccess,collaborationAttempt,CollaborationEpoch,functionRoles,governanceRole,lifecycleActions,memberStateLabel,ownerTransferLabel,resourceOptions,serializeAssignment,setFunctionRole,unseenScopes,type AssignmentDraft,type CollaborationAttempt } from './collaborationState'

const props=withDefaults(defineProps<{project:Project;chapters:Chapter[];assets:Asset[];readonly?:boolean;currentUserId?:string}>(),{readonly:false,currentUserId:''})
const emit=defineEmits<{close:[];changed:[];dirty:[dirty:boolean]}>()
const localProject=ref(cloneValue(props.project)),busy=ref(false),loading=ref(false),error=ref(''),notice=ref('')
const section=ref<'members'|'invitations'|'owner'>('members')
const directory=ref<MemberDirectoryEntry[]>([]),invitations=ref<ProjectInvitation[]>([]),transfers=ref<CollaborationOwnerTransfer[]>([])
const projectReadError=ref(''),directoryError=ref(''),invitationError=ref(''),transferError=ref('')
const access=computed(()=>collaborationAccess(localProject.value,props.currentUserId,props.readonly))
const owner=computed(()=>localProject.value.members.find(member=>governanceRole(member)==='owner'))
const names=computed(()=>new Map(directory.value.map(user=>[user.user_id,user.name])))
const nameOf=(id:string)=>names.value.get(id)||id
const scopeName=(scope:string)=>options.value.find(option=>option.id===scope)?.label||`现存范围 · ${scope}`
const roleName=(role:string)=>functionRoles.find(item=>item.id===role)?.label||`未识别职责（${role}）`
const options=computed(()=>resourceOptions(localProject.value,props.chapters,props.assets))
const drafts=reactive<Record<string,AssignmentDraft>>({}),editorKey=ref(''),editorKind=ref<'invite'|'assignment'|'resend'>('invite')
const editor=computed(()=>drafts[editorKey.value])
const stateDrafts=reactive<Record<string,{userId:string;action:MemberStateAction;reason:string;version:number;reviewed:boolean;initial:string}>>({}),stateKey=ref('')
const stateDraft=computed(()=>stateDrafts[stateKey.value])
const handoffCache=reactive<Record<string,{toUserId:string;reviewed:boolean;basedOnVersion:number}>>({})
const handoff=computed(()=>{const key=`${localProject.value.id}/${props.currentUserId}`;return handoffCache[key]??(handoffCache[key]={toUserId:'',reviewed:false,basedOnVersion:localProject.value.project_version})})
const rejectionReasons=reactive<Record<string,string>>({}),rejectionVersions=reactive<Record<string,number>>({})
const drawer=ref<'assignment'|'state'|'handoff'|null>(null),drawerElement=ref<HTMLElement|null>(null),closeConfirm=ref(false)
const closeButton=ref<HTMLButtonElement|null>(null)
let returnFocus:HTMLElement|null=null
const stateFingerprint=(draft:NonNullable<typeof stateDraft.value>)=>JSON.stringify({user:draft.userId,action:draft.action,reason:draft.reason,version:draft.version,reviewed:draft.reviewed})
const hasUnsavedChanges=computed(()=>Object.values(drafts).some(draft=>assignmentFingerprint(draft)!==draft.initial)||Object.values(stateDrafts).some(draft=>stateFingerprint(draft)!==draft.initial)||Object.values(handoffCache).some(draft=>!!draft.toUserId||draft.reviewed)||Object.values(rejectionReasons).some(reason=>!!reason))
watch(hasUnsavedChanges,dirty=>emit('dirty',dirty),{immediate:true})
defineExpose({hasUnsavedChanges,busy})
const epoch=new CollaborationEpoch();epoch.switchContext(`${props.project.id}/${props.currentUserId}`)
let readSequence=0
const attempts=new Map<string,CollaborationAttempt>()
function operationKey(operation:string,input:unknown){const previous=attempts.get(operation);const attempt=collaborationAttempt(previous,input,newIdempotencyKey);attempts.set(operation,attempt);return attempt.key}
function errorText(value:unknown){return value instanceof Error?value.message:'请求失败，请重新读取状态后复核。'}
async function refresh(token=epoch.capture(),includeProject=true){
 const sequence=++readSequence;loading.value=true
 if(includeProject){
  try{
   const result=await getCollaborationProject(localProject.value.id)
   if(!epoch.current(token)||sequence!==readSequence)return
   if(result.data.id===localProject.value.id&&result.data.project_version>=localProject.value.project_version){
    localProject.value=cloneValue(result.data)
    // Only this accepted read can recover its error; mutation failures are independent.
    projectReadError.value=''
   }
  }catch(value:unknown){if(!epoch.current(token)||sequence!==readSequence)return;projectReadError.value=errorText(value)}
 }
 // Directory visibility depends on the newly read role, including an Owner
 // acceptance receipt which contains a transfer and is not a Project DTO.
 const requests=[access.value.manager?listMemberDirectory(localProject.value.id):Promise.resolve({data:[] as MemberDirectoryEntry[]}),listProjectInvitations(localProject.value.id),listOwnerTransfers(localProject.value.id)] as const
 const results=await Promise.allSettled(requests)
 if(!epoch.current(token)||sequence!==readSequence)return
 const [usersResult,inviteResult,transferResult]=results
 if(usersResult.status==='fulfilled'){directory.value=usersResult.value.data;directoryError.value=''}else directoryError.value=errorText(usersResult.reason)
 if(inviteResult.status==='fulfilled'){invitations.value=inviteResult.value.data;invitationError.value=''}else invitationError.value=errorText(inviteResult.reason)
 if(transferResult.status==='fulfilled'){transfers.value=transferResult.value.data;transferError.value=''}else transferError.value=errorText(transferResult.reason)
 loading.value=false
}
async function submit<T>(operation:string,input:unknown,call:(key:string)=>Promise<{data:T;meta?:{replayed?:boolean}}>,label:(data:T)=>string,onRecorded?:()=>void){
 if(busy.value||props.readonly)return
 const token=epoch.capture();const submittedBasis=(input as {expected_project_version?:number}).expected_project_version??localProject.value.project_version;busy.value=true;error.value='';notice.value=''
 try {
 const result=await call(operationKey(`${token.context}/${operation}`,input))
 if(!epoch.current(token))return
 notice.value=label(result.data)+(result.meta?.replayed?'（读取了同一次操作回执）':'')
 const data=result.data as {status?:string;id?:string;project_version?:number;members?:unknown[]}
 const projectResult=typeof data.project_version==='number'&&data.project_version>submittedBasis&&data.id===localProject.value.id&&Array.isArray(data.members)
 const recordResult=['invited','accepted','pending','cancelled','expired'].includes(data.status||'')
 if(projectResult||recordResult)onRecorded?.();else notice.value+=' 输入继续保留，请重新读取并核对真实结果。'
 emit('changed')
 await refresh(token)
 }catch(value:unknown){if(epoch.current(token))error.value=errorText(value)}finally{busy.value=false}
}
function showDrawer(kind:NonNullable<typeof drawer.value>,event?:Event){returnFocus=event?.currentTarget as HTMLElement|null;drawer.value=kind;error.value='';nextTick(()=>drawerElement.value?.querySelector<HTMLElement>('input,select,textarea,button')?.focus())}
function hideDrawer(){if(busy.value)return;drawer.value=null;nextTick(()=>returnFocus?.focus())}
function drawerKeys(event:KeyboardEvent){
 if(event.key==='Escape'){event.preventDefault();event.stopPropagation();hideDrawer();return}
 if(event.key!=='Tab'||!drawerElement.value)return
 const focusable=Array.from(drawerElement.value.querySelectorAll<HTMLElement>('button:not(:disabled),input:not(:disabled),select:not(:disabled),textarea:not(:disabled),[tabindex="0"]'))
 const first=focusable[0],last=focusable[focusable.length-1]
 if(!first){event.preventDefault();drawerElement.value.focus();return}
 if(event.shiftKey&&document.activeElement===first){event.preventDefault();last?.focus()}else if(!event.shiftKey&&document.activeElement===last){event.preventDefault();first?.focus()}
}
function requestClose(){if(busy.value)return;if(hasUnsavedChanges.value){closeConfirm.value=true;nextTick(()=>closeButton.value?.focus())}else emit('close')}
function discardAndClose(){for(const key of Object.keys(drafts))delete drafts[key];for(const key of Object.keys(stateDrafts))delete stateDrafts[key];for(const key of Object.keys(handoffCache))delete handoffCache[key];for(const key of Object.keys(rejectionReasons))delete rejectionReasons[key];emit('close')}
function openInvite(event?:Event){if(!access.value.manager)return;editorKind.value='invite';const key=`${localProject.value.id}/${props.currentUserId}/invite/new`;if(!drafts[key])drafts[key]=assignmentDraft(undefined,localProject.value.project_version);editorKey.value=key;showDrawer('assignment',event)}
function openAssignment(member:CollaborationMember,event?:Event){if(!access.value.manager||!currentEditable(member))return;editorKind.value='assignment';const key=`${localProject.value.id}/${props.currentUserId}/assignment/${member.user_id}`;if(!drafts[key])drafts[key]=assignmentDraft(member,localProject.value.project_version);editorKey.value=key;showDrawer('assignment',event)}
function currentEditable(member:CollaborationMember){return governanceRole(member)!=='owner'&&(!member.status||member.status==='active')}
function openResend(invitation:ProjectInvitation,event?:Event){if(!access.value.manager||invitation.status!=='invited')return;editorKind.value='resend';const key=`${localProject.value.id}/${props.currentUserId}/resend/${invitation.id}`;if(!drafts[key]){const draft=assignmentDraft(invitation.assignment,localProject.value.project_version);draft.invitationId=invitation.id;draft.invitationVersion=invitation.version;draft.initial=assignmentFingerprint(draft);drafts[key]=draft};editorKey.value=key;showDrawer('assignment',event)}
const inviteCandidates=computed(()=>directory.value.filter(user=>user.user_id!==props.currentUserId&&!localProject.value.members.some(member=>member.user_id===user.user_id&&member.status!=='removed')))
function selectInvitee(id:string){if(!editor.value||editor.value.userId===id)return;const removed=localProject.value.members.some(member=>member.user_id===id&&member.status==='removed');const key=`${localProject.value.id}/${props.currentUserId}/invite/${id}${removed?`/removed-${localProject.value.project_version}`:''}`;if(!drafts[key])drafts[key]=assignmentDraft(undefined,localProject.value.project_version,id);editorKey.value=key}
function toggleRole(role:string,event:Event){if(editor.value)setFunctionRole(editor.value,role,(event.target as HTMLInputElement).checked)}
function toggleScope(role:string,id:string,event:Event){const draft=editor.value;if(!draft)return;const checked=(event.target as HTMLInputElement).checked;const scopes=draft.scopes[role]||[];draft.scopes[role]=checked?[...new Set([...scopes,id])]:scopes.filter(scope=>scope!==id)}
function discardUnknownRole(role:string){if(!editor.value)return;editor.value.roles=editor.value.roles.filter(item=>item!==role);delete editor.value.scopes[role]}
function rebaseState(){const draft=stateDraft.value;const member=stateMember.value;if(!draft||!member||!lifecycleActions(member).includes(draft.action)){error.value='当前成员状态不再允许该操作。';return};draft.version=localProject.value.project_version;draft.reviewed=false}
function rebaseHandoff(){handoff.value.basedOnVersion=localProject.value.project_version;handoff.value.reviewed=false}
function rebaseAssignment(){if(!editor.value)return;editor.value.basedOnVersion=localProject.value.project_version;if(editorKind.value==='resend'){const current=invitations.value.find(invite=>invite.id===editor.value?.invitationId);if(!current||current.status!=='invited'){error.value='邀请状态已变化，请重新打开当前邀请。';return};editor.value.invitationVersion=current.version};notice.value='已使用当前项目版本；请逐项核对下面的职责、范围和原因后保存。'}
async function saveAssignment(){
 const draft=editor.value;if(!draft||!access.value.manager)return
 if(draft.basedOnVersion!==localProject.value.project_version){error.value='编辑依据的项目版本已变化，请读取并重新复核基线。';return}
 if(!draft.reason.trim()){error.value='请写明此次邀请或职责变更的原因。';return}
 try {
 const assignment=serializeAssignment(draft);const input={expected_project_version:draft.basedOnVersion,assignment,reason:draft.reason, ...(editorKind.value==='resend'?{invitation_id:draft.invitationId,expected_invitation_version:draft.invitationVersion}:{})}
 if(editorKind.value==='assignment')await submit(`assignment/${draft.userId}`,input,key=>updateMemberAssignment(localProject.value.id,draft.userId,input,key),()=> '服务端已返回当前成员配置。',()=>{draft.initial=assignmentFingerprint(draft);hideAfterWrite()})
 else await submit(`invite/${draft.userId}`,input,key=>inviteProjectMember(localProject.value.id,input,key),result=>`服务端返回：${memberStateLabel(result.status)}；需受邀者本人接受。`,()=>{draft.initial=assignmentFingerprint(draft);hideAfterWrite()})
 }catch(value:unknown){error.value=errorText(value)}
}
function hideAfterWrite(){drawer.value=null;nextTick(()=>returnFocus?.focus())}
const actionLabels:Record<MemberStateAction,string>={suspend:'暂停项目权限',remove:'移除成员',restore:'复核并恢复项目成员',restore_tenant:'复核租户恢复后的项目权限'}
function openState(member:CollaborationMember,action:MemberStateAction,event?:Event){if(!access.value.manager||!lifecycleActions(member).includes(action))return;const key=`${localProject.value.id}/${props.currentUserId}/${member.user_id}/${action}`;if(!stateDrafts[key]){const draft={userId:member.user_id,action,reason:'',version:localProject.value.project_version,reviewed:false,initial:''};draft.initial=stateFingerprint(draft);stateDrafts[key]=draft};stateKey.value=key;showDrawer('state',event)}
const stateMember=computed(()=>localProject.value.members.find(member=>member.user_id===stateDraft.value?.userId))
async function applyState(){const draft=stateDraft.value;if(!draft||!access.value.manager||!draft.reason.trim()||!draft.reviewed)return;if(draft.version!==localProject.value.project_version){error.value='成员状态依据已过期，请重新打开操作并复核。';return};const input={expected_project_version:draft.version,action:draft.action,reason:draft.reason};await submit(`state/${draft.userId}/${draft.action}`,input,key=>changeMemberState(localProject.value.id,draft.userId,input,key),result=>{const member=result.members.find(member=>member.user_id===draft.userId);return `服务端返回：${memberStateLabel(member?.status||'unconfirmed')}。`},()=>{delete stateDrafts[stateKey.value];hideAfterWrite()})}
async function acceptInvitation(invitation:ProjectInvitation){if(!actionableInvitation(invitation.status,invitation.expires_at,props.currentUserId,invitation.user_id))return;const input={expected_invitation_version:invitation.version};await submit(`accept-invitation/${invitation.id}`,input,key=>acceptProjectInvitation(localProject.value.id,invitation.id,invitation.version,key),result=>`服务端返回：${memberStateLabel(result.status)}。`)}
function openHandoff(event?:Event){if(!access.value.owner)return;if(!handoff.value.toUserId&&!handoff.value.reviewed)handoff.value.basedOnVersion=localProject.value.project_version;showDrawer('handoff',event)}
async function beginHandoff(){if(!access.value.owner||!handoff.value.toUserId||!handoff.value.reviewed)return;if(handoff.value.basedOnVersion!==localProject.value.project_version){error.value='交接依据已变化，请重新读取项目并复核接任者。';return};const input={to_user_id:handoff.value.toUserId,expected_project_version:handoff.value.basedOnVersion};await submit('owner-request',input,key=>requestOwnerTransfer(localProject.value.id,input,key),result=>`服务端返回：${ownerTransferLabel(result.status)}；当前 Owner 尚未改变。`,()=>{handoff.value.toUserId='';handoff.value.reviewed=false;hideAfterWrite()})}
async function acceptHandoff(transfer:CollaborationOwnerTransfer){if(transfer.status!=='pending'||transfer.to_user_id!==props.currentUserId)return;await submit(`owner-accept/${transfer.id}`,{transfer_id:transfer.id},key=>acceptOwnerTransfer(localProject.value.id,transfer.id,key),result=>`服务端返回：${ownerTransferLabel(result.status)}；正在重新读取项目成员状态。`)}
function beginReject(transfer:CollaborationOwnerTransfer){rejectionVersions[transfer.id]??=localProject.value.project_version}
function rebaseReject(transfer:CollaborationOwnerTransfer){rejectionVersions[transfer.id]=localProject.value.project_version}
async function rejectHandoff(transfer:CollaborationOwnerTransfer){const version=rejectionVersions[transfer.id]??transfer.expected_project_version;if(version!==localProject.value.project_version){error.value='拒绝操作依据已变化，请重新读取并复核。';return};const reason=rejectionReasons[transfer.id]?.trim();if(!reason||transfer.status!=='pending')return;await submit(`owner-reject/${transfer.id}`,{expected_project_version:version,reason},key=>rejectOwnerTransfer(localProject.value.id,transfer.id,version,reason,key),result=>`服务端返回：${ownerTransferLabel(result.status)}。`,()=>{delete rejectionReasons[transfer.id];delete rejectionVersions[transfer.id]})}
watch([()=>props.project.id,()=>props.currentUserId],()=>{epoch.switchContext(`${props.project.id}/${props.currentUserId}`);readSequence++;localProject.value=cloneValue(props.project);directory.value=[];invitations.value=[];transfers.value=[];drawer.value=null;error.value='';projectReadError.value='';notice.value='';loading.value=false;void refresh(epoch.capture(),false)})
watch(()=>props.project,project=>{if(project.id===localProject.value.id&&project.project_version>=localProject.value.project_version)localProject.value=cloneValue(project)},{deep:true})
onMounted(()=>void refresh(epoch.capture(),false))
onBeforeUnmount(()=>{epoch.dispose();readSequence++})
</script>
<template>
 <section class="collaboration-panel" :aria-busy="busy || loading" aria-labelledby="collaboration-title" @keydown.esc="!drawer && requestClose()">
  <header class="panel-header"><div><p class="eyebrow">{{ localProject.name }}</p><h2 id="collaboration-title">项目协作</h2><p class="muted">项目第 {{ localProject.project_version }} 版 · 当前 Owner：{{ owner ? nameOf(owner.user_id) : '服务端尚未返回' }}</p></div><button type="button" class="icon-button" aria-label="关闭协作面板" :disabled="busy" @click="requestClose">×</button></header>
  <p v-if="notice" class="notice" role="status">{{ notice }}</p><p v-if="error" class="error" role="alert">{{ error }}</p>
  <p v-if="projectReadError" class="error" role="alert">{{ projectReadError }}</p>
  <p v-if="readonly" class="muted">当前为只读查看。服务端权限变化后，请重新读取项目。</p><p v-else-if="!currentUserId" class="muted">当前账号身份尚未提供，人工操作保持关闭。</p>
  <div v-if="closeConfirm" class="close-warning" role="alert"><p>还有未提交的协作编辑。保留编辑可继续复核，关闭并丢弃会清空这些输入。</p><div class="actions"><button ref="closeButton" @click="closeConfirm = false">保留编辑</button><button class="danger" @click="discardAndClose">关闭并丢弃</button></div></div>
  <div class="toolbar"><nav aria-label="协作内容"><button type="button" :aria-pressed="section === 'members'" @click="section = 'members'">当前成员</button><button type="button" :aria-pressed="section === 'invitations'" @click="section = 'invitations'">邀请</button><button type="button" :aria-pressed="section === 'owner'" @click="section = 'owner'">Owner 交接</button></nav><button type="button" :disabled="busy || loading" @click="refresh()">{{ loading ? '正在读取…' : '重新读取' }}</button></div>
  <div v-if="section === 'members'" class="stack">
   <div class="section-header"><div><h3>成员与当前授权</h3><p class="muted">治理身份和功能职责分别记录。每位成员独立保存，待接受和已移除成员不会被批量覆盖。</p></div><button v-if="access.manager" :disabled="busy" @click="openInvite($event)">邀请成员</button></div>
   <p v-if="directoryError" class="error" role="alert">用户目录读取失败：{{ directoryError }}</p>
   <article v-for="member in localProject.members" :key="member.user_id" class="member-card">
    <div class="member-heading"><div><strong>{{ nameOf(member.user_id) }}</strong><small>{{ member.user_id }}{{ member.user_id === currentUserId ? ' · 当前账号' : '' }}</small></div><div class="badges"><span>{{ governanceRole(member) === 'owner' ? 'Owner' : governanceRole(member) === 'admin' ? '项目治理管理员' : governanceRole(member) === 'member' ? '项目成员' : `未识别身份（${governanceRole(member)}）` }}</span><span>{{ memberStateLabel(member.status) }}</span></div></div>
    <ul v-if="member.function_roles?.length" class="scope-summary"><li v-for="role in member.function_roles" :key="role"><b>{{ roleName(role) }}</b><span>{{ member.function_scopes?.[role]?.length ? member.function_scopes[role].map(scopeName).join('、') : '未分配对象范围（不授予章节或资料写权限）' }}</span></li></ul><p v-else class="muted">没有展开的功能职责；当前实际权限由服务端判定。</p>
    <div v-if="access.manager" class="actions"><button v-if="currentEditable(member)" :disabled="busy" @click="openAssignment(member,$event)">编辑职责与范围</button><button v-for="action in lifecycleActions(member)" :key="action" :class="{ danger: action === 'remove' }" :disabled="busy" @click="openState(member,action,$event)">{{ actionLabels[action] }}</button><button v-if="member.status === 'removed'" :disabled="busy" @click="openInvite($event)">重新邀请</button><span v-if="governanceRole(member) === 'owner'" class="muted">Owner 变更需要双方交接。</span></div>
   </article>
  </div>
  <div v-else-if="section === 'invitations'" class="stack">
   <div class="section-header"><div><h3>邀请记录</h3><p class="muted">新邀请有效期为 72 小时。重发会生成新的邀请版本，旧版本不能用于接受。</p></div><button v-if="access.manager" :disabled="busy" @click="openInvite($event)">邀请成员</button></div>
   <p v-if="invitationError" class="error" role="alert">邀请读取失败：{{ invitationError }}</p><p v-if="!invitations.length && !invitationError" class="muted">当前账号可见范围内暂无邀请记录。</p>
   <article v-for="invitation in invitations" :key="invitation.id" class="member-card"><div class="member-heading"><div><strong>{{ nameOf(invitation.user_id) }}</strong><small>邀请第 {{ invitation.version }} 版 · {{ invitation.id }}</small></div><span class="badge">{{ memberStateLabel(invitation.status) }}</span></div><p>原因：{{ invitation.reason }}</p><p class="muted">有效期至 {{ new Date(invitation.expires_at).toLocaleString() }}</p><ul><li v-for="role in invitation.assignment.function_roles" :key="role">{{ roleName(role) }} · {{ invitation.assignment.function_scopes?.[role]?.length ? invitation.assignment.function_scopes[role].map(scopeName).join('、') : '未分配对象范围（不授予章节或资料写权限）' }}</li></ul><div class="actions"><button v-if="!readonly && actionableInvitation(invitation.status,invitation.expires_at,currentUserId,invitation.user_id)" :disabled="busy" @click="acceptInvitation(invitation)">本人接受邀请</button><button v-if="access.manager && invitation.status === 'invited'" :disabled="busy" @click="openResend(invitation,$event)">重新复核并重发</button></div></article>
  </div>
  <div v-else class="stack"><div class="section-header"><div><h3>Owner 双方交接</h3><p class="muted">提出交接不会立即变更 Owner；只有指定接任者接受当前交接后才切换。租户管理员身份不会自动成为项目 Owner。</p></div><button v-if="access.owner" :disabled="busy" @click="openHandoff($event)">提出 Owner 交接</button></div><p v-if="transferError" class="error" role="alert">交接记录读取失败：{{ transferError }}</p><p v-if="!transfers.length && !transferError" class="muted">当前账号可见范围内暂无交接记录。</p><article v-for="transfer in transfers" :key="transfer.id" class="member-card"><div class="member-heading"><strong>{{ nameOf(transfer.from_user_id) }} → {{ nameOf(transfer.to_user_id) }}</strong><span class="badge">{{ ownerTransferLabel(transfer.status) }}</span></div><p class="muted">依据项目第 {{ transfer.expected_project_version }} 版 · {{ transfer.created_at }}</p><div v-if="transfer.status === 'pending' && !readonly" class="stack"><button v-if="transfer.to_user_id === currentUserId" :disabled="busy || transfer.expected_project_version !== localProject.project_version" @click="acceptHandoff(transfer)">本人接受 Owner 交接</button><p v-if="transfer.expected_project_version !== localProject.project_version" class="error">项目版本已变化，该交接需要重新复核。</p><form v-if="access.owner || transfer.to_user_id === currentUserId" class="inline-form" @submit.prevent="rejectHandoff(transfer)"><label>拒绝原因<input v-model="rejectionReasons[transfer.id]" :disabled="busy" required @focus="beginReject(transfer)" /></label><button v-if="(rejectionVersions[transfer.id] ?? transfer.expected_project_version) !== localProject.project_version" type="button" :disabled="busy" @click="rebaseReject(transfer)">以当前版本复核拒绝</button><button class="danger" :disabled="busy || !rejectionReasons[transfer.id]?.trim() || (rejectionVersions[transfer.id] ?? transfer.expected_project_version) !== localProject.project_version">拒绝此次交接</button></form></div></article></div>
  <p v-if="hasUnsavedChanges" class="draft-notice">未提交输入已保留在本面板中；重新读取不会覆盖编辑内容。</p>
  <div v-if="drawer" class="drawer-backdrop" @click.self="hideDrawer">
   <section ref="drawerElement" class="collaboration-drawer" role="dialog" aria-modal="true" aria-labelledby="collaboration-drawer-title" tabindex="-1" @keydown="drawerKeys">
    <header class="drawer-header"><h3 id="collaboration-drawer-title">{{ drawer === 'assignment' ? editorKind === 'assignment' ? '编辑当前成员' : editorKind === 'resend' ? '复核并重发邀请' : '邀请成员' : drawer === 'state' && stateDraft ? actionLabels[stateDraft.action] : '提出 Owner 交接' }}</h3><button type="button" aria-label="关闭编辑抽屉并保留输入" :disabled="busy" @click="hideDrawer">×</button></header>
    <p v-if="error" class="error" role="alert">{{ error }}</p>
    <fieldset class="interaction-boundary" :disabled="busy || readonly">
     <form v-if="drawer === 'assignment' && editor" class="stack" @submit.prevent="saveAssignment">
      <label v-if="editorKind === 'invite'">同租户当前用户<select :value="editor.userId" required @change="selectInvitee(($event.target as HTMLSelectElement).value)"><option value="">请选择真实用户</option><option v-for="user in inviteCandidates" :key="user.user_id" :value="user.user_id">{{ user.name }} · {{ user.user_id }}</option></select></label><p v-else><b>{{ nameOf(editor.userId) }}</b><br /><small>{{ editor.userId }}</small></p>
      <p v-if="editorKind === 'invite'" class="muted">可选用户来自服务端同租户当前目录，本次最多返回 200 人。</p><p v-if="directoryError && editorKind === 'invite'" class="error">{{ directoryError }}。未获得真实用户目录前不能发出邀请。</p>
      <div v-if="editor.basedOnVersion !== localProject.project_version" class="basis-warning"><p>此编辑依据项目第 {{ editor.basedOnVersion }} 版，当前为第 {{ localProject.project_version }} 版。请核对当前成员后明确重新复核。</p><button type="button" @click="rebaseAssignment">使用当前基线重新复核</button></div>
      <label>项目治理身份<select v-model="editor.governance"><option value="member">项目成员</option><option value="admin">项目治理管理员</option><option v-if="!['member','admin'].includes(editor.governance)" :value="editor.governance">未识别身份（{{ editor.governance }}）</option></select></label><p v-if="editor.governance === 'admin'" class="muted">治理管理员可参与成员和项目治理；下方功能范围不会缩小其治理身份本身。</p>
      <fieldset><legend>功能职责</legend><div class="role-options"><label v-for="role in functionRoles" :key="role.id" class="checkbox"><input :checked="editor.roles.includes(role.id)" type="checkbox" @change="toggleRole(role.id,$event)" />{{ role.label }}</label></div></fieldset>
      <fieldset v-for="role in editor.roles" :key="role" class="scope-group"><legend>{{ roleName(role) }}的对象范围</legend><template v-if="functionRoles.some(item=>item.id === role)"><p class="muted">仅授予明确选择的章节或资料范围。空范围不授予这些对象的写入权限；实际权限仍由服务端判定。</p><div class="scope-options"><label v-for="option in options" :key="option.id" class="checkbox"><input type="checkbox" :checked="editor.scopes[role]?.includes(option.id)" @change="toggleScope(role,option.id,$event)" />{{ option.label }}</label><label v-for="scope in unseenScopes(editor,role,options)" :key="scope" class="checkbox retained-scope"><input type="checkbox" :checked="true" disabled />现存范围（暂未识别）：{{ scope }} · 后端管理，原样保留</label><p v-if="!(editor.scopes[role]?.length)" class="scope-warning">尚未分配对象范围：保存空范围不会赋予章节或资料写入权限。</p></div></template><template v-else><p class="error">此职责暂未识别，内容保持原样。请重新读取，或明确移除后重新复核。</p><button type="button" class="danger" @click="discardUnknownRole(role)">明确移除此职责及其范围</button></template></fieldset>
      <div v-for="role in Object.keys(editor.scopes).filter(role=>!editor.roles.includes(role) && !functionRoles.some(item=>item.id === role))" :key="role" class="basis-warning"><p>保留的未识别范围分组：{{ role }} · {{ editor.scopes[role].join('、') }}</p><button type="button" class="danger" @click="discardUnknownRole(role)">明确移除此范围分组</button></div><label>此次变更原因<textarea v-model="editor.reason" rows="3" required /></label><p class="muted">依据项目第 {{ editor.basedOnVersion }} 版。只保存此成员；服务端会再次验证当前权限和范围。</p><button :disabled="editor.basedOnVersion !== localProject.project_version || !editor.reason.trim() || !editor.userId || (editorKind === 'invite' && !!directoryError)">{{ editorKind === 'assignment' ? '保存该成员配置' : editorKind === 'resend' ? '提交新版邀请' : '发出邀请，等待本人接受' }}</button>
     </form>
     <form v-else-if="drawer === 'state' && stateDraft" class="stack" @submit.prevent="applyState"><p><strong>{{ nameOf(stateDraft.userId) }}</strong> · {{ memberStateLabel(stateMember?.status) }}</p><p v-if="stateDraft.action === 'remove'" class="error">移除会清空该成员的对象范围。后续需要重新邀请，界面不会恢复旧授权。</p><p v-else-if="stateDraft.action === 'restore_tenant'" class="scope-warning">租户恢复后，项目权限仍需此次人工复核；确认的是当前记录，服务端还会检查租户成员状态。</p><ul v-if="stateMember"><li v-for="role in stateMember.function_roles" :key="role">{{ roleName(role) }} · {{ stateMember.function_scopes?.[role]?.length ? stateMember.function_scopes[role].map(scopeName).join('、') : '未分配对象范围（不授予章节或资料写权限）' }}</li></ul><div v-if="stateDraft.version !== localProject.project_version" class="basis-warning"><p>当前项目版本已变化，请重新复核此操作。</p><button type="button" @click="rebaseState">以当前成员状态重新复核</button></div><label>操作原因<textarea v-model="stateDraft.reason" rows="3" required /></label><label class="checkbox"><input v-model="stateDraft.reviewed" type="checkbox" />我已复核当前职责、范围和此操作的影响。</label><button :class="{danger:stateDraft.action === 'remove'}" :disabled="!stateDraft.reviewed || !stateDraft.reason.trim() || stateDraft.version !== localProject.project_version">{{ actionLabels[stateDraft.action] }}</button></form>
     <form v-else-if="drawer === 'handoff'" class="stack" @submit.prevent="beginHandoff"><p>当前 Owner：{{ owner ? nameOf(owner.user_id) : '尚未返回' }}。此次提交只提出交接，接任者需要本人接受。</p><label>指定接任者<select v-model="handoff.toUserId" required><option value="">请选择同租户当前用户</option><option v-for="user in directory.filter(user=>user.user_id !== currentUserId)" :key="user.user_id" :value="user.user_id">{{ user.name }} · {{ user.user_id }}</option></select></label><div v-if="handoff.basedOnVersion !== localProject.project_version" class="basis-warning"><p>交接依据项目第 {{ handoff.basedOnVersion }} 版，项目已变化。</p><button type="button" @click="rebaseHandoff">以当前项目重新复核</button></div><label class="checkbox"><input v-model="handoff.reviewed" type="checkbox" />我已核对接任者，并了解本人接受后 Owner 才会切换。</label><button :disabled="!handoff.toUserId || !handoff.reviewed || handoff.basedOnVersion !== localProject.project_version || !!directoryError">提出交接，等待接任者确认</button></form>
    </fieldset>
   </section>
  </div>
 </section>
</template>

<style scoped>
.collaboration-panel{background:#fff;color:#1f2c40;padding:24px;min-width:0;font-size:14px}.panel-header,.section-header,.member-heading,.drawer-header,.toolbar,.actions,.role-options{display:flex;align-items:center;gap:12px}.panel-header,.section-header,.member-heading,.drawer-header,.toolbar{justify-content:space-between}.panel-header{align-items:flex-start}.eyebrow{margin:0;color:#596c84}h2{margin:6px 0;font-size:24px}h3{margin:0 0 6px}p{line-height:1.6;margin:8px 0}small,.muted{color:#64758c}.member-heading small{display:block;margin-top:4px;overflow-wrap:anywhere}.stack{display:grid;gap:16px}.toolbar{margin:22px 0;flex-wrap:wrap}nav{display:flex;gap:8px;flex-wrap:wrap}button{border:1px solid #c8d3e1;border-radius:7px;background:#fff;color:#243c5c;padding:9px 13px;cursor:pointer;font:inherit}button[aria-pressed=true]{background:#e9f1ff;border-color:#9bb8eb}button:disabled{opacity:.5;cursor:not-allowed}button:focus-visible,input:focus-visible,textarea:focus-visible,select:focus-visible{outline:3px solid #3976d3;outline-offset:2px}.icon-button{font-size:24px;padding:4px 10px}.danger{color:#9b2c32;border-color:#e5b7bd}.member-card{padding:16px;border:1px solid #dce4ee;border-radius:10px}.badges,.actions,.role-options{flex-wrap:wrap}.badges{display:flex;gap:7px}.badge,.badges span{background:#eef2f7;border-radius:5px;padding:5px 8px}.scope-summary{display:grid;gap:8px;padding-left:18px}.scope-summary span{margin-left:12px;color:#63768e}.error{background:#fff2f1;color:#933c3c;padding:10px 12px;border-radius:7px}.notice,.basis-warning,.scope-warning,.close-warning{background:#edf4ff;border:1px solid #d7e4f8;border-radius:7px;padding:12px}.scope-warning{background:#fff8eb;border-color:#ebdbb9}.draft-notice{color:#775a30;font-size:13px}.drawer-backdrop{position:fixed;inset:0;z-index:40;background:#142b4c55;display:flex;justify-content:flex-end}.collaboration-drawer{width:min(540px,94vw);height:100%;overflow:auto;box-sizing:border-box;padding:22px;background:#fff;box-shadow:-8px 0 40px #153b6122}.drawer-header{margin-bottom:20px}.interaction-boundary{border:0;margin:0;padding:0;min-width:0}.stack label,.inline-form label{display:grid;gap:7px}.stack label.checkbox,label.checkbox{display:flex;align-items:flex-start;gap:8px}.checkbox input{width:auto;min-height:0;margin-top:3px}input,select,textarea{border:1px solid #bac8da;border-radius:6px;padding:8px 10px;min-width:0;box-sizing:border-box;font:inherit;max-width:100%}textarea{resize:vertical}fieldset{border:1px solid #dce4ee;border-radius:7px;padding:12px;min-width:0}legend{padding:0 6px;font-weight:600}.scope-options{display:grid;gap:9px;margin-top:12px}.retained-scope{background:#fff8eb;padding:8px;overflow-wrap:anywhere}.inline-form{display:flex;gap:12px;align-items:flex-end;flex-wrap:wrap}.inline-form label{flex:1;min-width:160px}@media(max-width:650px){.collaboration-panel{padding:16px}.member-heading,.section-header{align-items:flex-start;flex-direction:column}.scope-summary span{display:block;margin-left:0}.toolbar{align-items:flex-start}}
</style>
