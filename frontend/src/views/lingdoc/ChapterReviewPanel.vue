<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import type { Chapter, ChapterVersion, Project } from '@/api/lingdoc/workspace'
import * as api from '@/api/lingdoc/chapterReview'
import { newIdempotencyKey } from '@/api/lingdoc/candidateAdoption'
const props=withDefaults(defineProps<{project:Project;chapter:Chapter;currentUserId:string;selection?:api.CommentSelection;readonly?:boolean;formalCurrent:boolean}>(),{readonly:true})
const emit=defineEmits<{close:[];changed:[];dirty:[boolean];locate:[selection:api.CommentSelection]}>()
interface DraftBasis {projectId:string;chapterId:string;versionId:string;projectVersion:number;contextRevision:number;specRevision:number;reviewVersion:number}
interface ReviewDraft {body:string;reason:string;basis:DraftBasis|null;selection?:api.CommentSelection}
const drafts=reactive<Record<string,ReviewDraft>>({})
const identity=computed(()=>`${props.project.id}/${props.chapter.id}/${props.currentUserId}`)
const draft=computed(()=>drafts[identity.value]??(drafts[identity.value]={body:'',reason:'',basis:null}))
const state=ref<api.ChapterReviewState|null>(null),comments=ref<api.ChapterComment[]>([]),versions=ref<ChapterVersion[]>([])
const busy=ref(false),loading=ref(false),error=ref(''),notice=ref(''),comparisonId=ref(''),acknowledgedBasis=ref(''),formalText=ref<HTMLElement|null>(null)
let epoch=0,readSequence=0
const keys=new Map<string,{payload:string;key:string}>()
const parentSelectionKey=()=>JSON.stringify([props.project.id,props.chapter.id,props.currentUserId,props.chapter.current_version_id,props.project.project_version,props.project.current_context_revision,props.project.spec_revision])
const openingSelectionKey=parentSelectionKey()
const ownMember=computed(()=>props.project.members.find(m=>m.user_id===props.currentUserId&&(!m.status||m.status==='active')))
const owner=computed(()=>ownMember.value?.role==='owner')
const functionalScope=(role:'author'|'reviewer')=>ownMember.value?.function_roles?.includes(role)&&ownMember.value?.function_scopes?.[role]?.includes(props.chapter.id)
const canAuthor=computed(()=>owner.value||!!functionalScope('author'))
const canReview=computed(()=>owner.value||!!functionalScope('reviewer'))
const currentVersion=computed(()=>versions.value.find(v=>v.id===props.chapter.current_version_id&&v.chapter_id===props.chapter.id&&v.project_id===props.project.id))
const comparedVersion=computed(()=>versions.value.find(v=>v.id===comparisonId.value))
const currentBasis=():DraftBasis=>({projectId:props.project.id,chapterId:props.chapter.id,versionId:props.chapter.current_version_id||'',projectVersion:props.project.project_version,contextRevision:props.project.current_context_revision,specRevision:props.project.spec_revision,reviewVersion:state.value?.version??0})
const basisKey=(basis:DraftBasis)=>JSON.stringify(basis)
const draftStale=computed(()=>!!draft.value.basis&&basisKey(draft.value.basis)!==basisKey(currentBasis()))
const formalAcknowledged=computed({get:()=>acknowledgedBasis.value===basisKey(currentBasis()),set:(value:boolean)=>{acknowledgedBasis.value=value?basisKey(currentBasis()):''}})
const ready=computed(()=>!loading.value&&!!state.value&&state.value.chapter_id===props.chapter.id&&Number.isInteger(state.value.version)&&state.value.version>=0&&!!currentVersion.value)
function selectionFits(selection:api.CommentSelection|undefined,version:ChapterVersion|undefined):boolean{
 if(!selection||!version||!Number.isInteger(selection.start_utf16)||!Number.isInteger(selection.end_utf16)||selection.start_utf16<0||selection.end_utf16<=selection.start_utf16||selection.end_utf16>version.body_markdown.length)return false
 const splitsSurrogate=(at:number)=>{const before=version.body_markdown.charCodeAt(at-1),after=version.body_markdown.charCodeAt(at);return before>=0xd800&&before<=0xdbff&&after>=0xdc00&&after<=0xdfff}
 return !splitsSurrogate(selection.start_utf16)&&!splitsSurrogate(selection.end_utf16)&&version.body_markdown.slice(selection.start_utf16,selection.end_utf16)===selection.quote_markdown
}
function pinDraft(){if(draft.value.basis)return;draft.value.basis=currentBasis();if(props.formalCurrent&&parentSelectionKey()===openingSelectionKey&&selectionFits(props.selection,currentVersion.value))draft.value.selection={...props.selection!}}
const body=computed({get:()=>draft.value.body,set:(value:string)=>{if(value)pinDraft();draft.value.body=value}})
const reason=computed({get:()=>draft.value.reason,set:(value:string)=>{if(value)pinDraft();draft.value.reason=value}})
const hasUnsavedChanges=computed(()=>Object.values(drafts).some(d=>!!d.body.trim()||!!d.reason.trim()))
watch(hasUnsavedChanges,d=>emit('dirty',d),{immediate:true})
defineExpose({hasUnsavedChanges,busy})
const statusLabel=computed(()=>{const status=state.value?.status;if(!status)return loading.value?'正在读取':'状态尚未读取';return ({DRAFT:'待撰写 / 已退回',IN_REVIEW:'待评审',STALE:'正文或所依赖内容已变化',CONFIRMED:'当前版本已确认',ARCHIVED:'项目已归档'} as Record<string,string>)[status]||`待识别状态（${status}）`})
function opKey(operation:string,input:unknown){const payload=JSON.stringify(input),name=`${identity.value}/${operation}`,old=keys.get(name);if(old?.payload===payload)return old.key;const key=newIdempotencyKey();keys.set(name,{payload,key});return key}
function forgetKey(operation:string){keys.delete(`${identity.value}/${operation}`)}
function fail(cause:unknown){const item=cause as {error?:{message?:string};message?:string};error.value=item.error?.message??item.message??'操作未完成；输入已保留，请重新读取当前版本。'}
async function load():Promise<boolean>{
 const at=epoch,sequence=++readSequence,p=props.project.id,c=props.chapter.id;loading.value=true;error.value=''
 try{const [review,history,notes]=await Promise.all([api.getChapterReview(p,c),api.listReviewChapterVersions(p,c),api.listChapterComments(p,c)]);if(at!==epoch||sequence!==readSequence)return false
 if(!history.data||!Array.isArray(history.data.items)||!Array.isArray(notes.data))throw new Error('章节版本或批注回执格式无法确认。')
 state.value=review.data;versions.value=history.data.items.filter(v=>v.project_id===p&&v.chapter_id===c);comments.value=notes.data
 if(comparisonId.value&&!versions.value.some(v=>v.id===comparisonId.value))comparisonId.value=''
 return true
 }catch(cause){if(at===epoch&&sequence===readSequence){state.value=null;comments.value=[];versions.value=[];acknowledgedBasis.value='';fail(cause)}return false}
 finally{if(at===epoch&&sequence===readSequence)loading.value=false}
}
watch(()=>[props.project.id,props.chapter.id,props.currentUserId,props.chapter.current_version_id,props.project.current_context_revision,props.project.spec_revision,props.project.project_version],()=>{epoch++;state.value=null;comments.value=[];versions.value=[];comparisonId.value='';acknowledgedBasis.value='';notice.value=draft.value.body||draft.value.reason?'版本或权限基线已变化，输入保留；请读当前正式正文后明确重新关联。':'';void load()},{immediate:true,flush:'sync'})
async function rebaseDraft(){if(busy.value||props.readonly)return;const at=epoch;if(!await load()||at!==epoch||!currentVersion.value)return;draft.value.basis=currentBasis();draft.value.selection=undefined;acknowledgedBasis.value='';notice.value='输入已保留并关联当前正式版本。旧选段已解除，请在下方当前正式正文中重新选择，或保留为章节批注。'}
function selectFormalRange(){
 if(props.readonly||busy.value||!ready.value||draftStale.value)return
 const element=formalText.value,selection=window.getSelection();if(!element||!selection?.rangeCount||selection.isCollapsed)return
 const range=selection.getRangeAt(0);if(!element.contains(range.startContainer)||!element.contains(range.endContainer))return
 const prefix=range.cloneRange();prefix.selectNodeContents(element);prefix.setEnd(range.startContainer,range.startOffset)
 const end=range.cloneRange();end.selectNodeContents(element);end.setEnd(range.endContainer,range.endOffset)
 const chosen={start_utf16:prefix.toString().length,end_utf16:end.toString().length,quote_markdown:range.toString()}
 if(!selectionFits(chosen,currentVersion.value)){notice.value='所选范围不能准确对应当前正式正文，未建立关联。';return}
 pinDraft();draft.value.selection=chosen;notice.value='已关联当前正式正文中的确切选段。'
}
function clearSelection(){draft.value.selection=undefined}
function locateComment(item:api.ChapterComment){if(item.chapter_version_id!==props.chapter.current_version_id||!props.formalCurrent||!selectionFits(item.selection,currentVersion.value)){notice.value='历史或已变化的批注不在当前工作正文猜测位置，请查看其固定正式版本。';return}emit('locate',item.selection!)}
function showCommentVersion(item:api.ChapterComment){if(versions.value.some(v=>v.id===item.chapter_version_id))comparisonId.value=item.chapter_version_id;else notice.value='当前授权读取未返回该历史版本；未用当前正文代替。'}
async function review(action:'submit'|'return'){
 if(busy.value||props.readonly||!ready.value||draftStale.value||!formalAcknowledged.value||action==='submit'&&(!canAuthor.value||!props.formalCurrent)||action==='return'&&(!canReview.value||state.value?.status!=='IN_REVIEW'||!reason.value.trim()))return
 pinDraft();const basis={...draft.value.basis!},at=epoch,submittedReason=reason.value,input={expected_project_version:basis.projectVersion,expected_context_revision:basis.contextRevision,expected_review_version:basis.reviewVersion,chapter_version_id:basis.versionId,action,reason:submittedReason}
 busy.value=true;error.value=''
 try{const result=await api.setChapterReview(basis.projectId,basis.chapterId,input,opKey(action,input));if(at!==epoch)return
 if(result.data.chapter_version_id!==basis.versionId||result.data.chapter_id!==basis.chapterId||result.data.status!==(action==='submit'?'IN_REVIEW':'DRAFT')||result.data.context_revision!==basis.contextRevision||!Number.isInteger(result.data.version)||result.data.version<=basis.reviewVersion){notice.value='回执状态尚未确认，输入保留，请重新读取。';return}
 state.value=result.data;if(reason.value===submittedReason)reason.value='';forgetKey(action);if(!draft.value.body)draft.value.basis=null;else if(draft.value.basis)draft.value.basis.reviewVersion=result.data.version
 notice.value=action==='submit'?'当前正式版本已提交评审。':'当前正式版本已退回，理由已记录。';acknowledgedBasis.value='';emit('changed')
 }catch(cause){if(at===epoch)fail(cause)}finally{busy.value=false}
}
async function comment(item?:api.ChapterComment){
 if(busy.value||props.readonly||!ready.value||!(canAuthor.value||canReview.value)||!item&&(draftStale.value||!body.value.trim())||item&&(item.resolved||item.actor_id!==props.currentUserId&&!owner.value))return
 if(!item)pinDraft();const basis=item?currentBasis():{...draft.value.basis!},at=epoch,text=body.value,selected=item?item.selection:draft.value.selection
 if(!item&&selected&&!selectionFits(selected,currentVersion.value)){notice.value='批注选段已变化，请重新关联。';return}
 const input={expected_project_version:basis.projectVersion,expected_context_revision:basis.contextRevision,chapter_version_id:item?.chapter_version_id??basis.versionId,expected_version:item?.version??0,body:item?.body??text,id:item?.id,selection:selected,resolve:!!item},operation=item?.id??'comment'
 busy.value=true;error.value=''
 try{const result=await api.saveChapterComment(basis.projectId,basis.chapterId,input,opKey(operation,input));if(at!==epoch)return
 if(!result.data.id||result.data.chapter_id!==basis.chapterId||result.data.chapter_version_id!==input.chapter_version_id||!Number.isInteger(result.data.version)||result.data.version<=input.expected_version||item&&(!result.data.resolved||result.data.id!==item.id)){notice.value='批注回执尚未确认，输入保留，请重新读取。';return}
 if(!item&&body.value===text){body.value='';draft.value.selection=undefined;if(!draft.value.reason)draft.value.basis=null}forgetKey(operation);notice.value=item?'批注已解决，历史版本关联保留。':'批注已保存到所选正式版本。';await load();if(at===epoch)emit('changed')
 }catch(cause){if(at===epoch)fail(cause)}finally{busy.value=false}
}
</script>
<template>
 <section class="chapter-review-panel" aria-label="章节评审与批注" :aria-busy="busy||loading">
  <header><div><h2>章节评审与批注</h2><p>{{chapter.title}} · <strong>{{statusLabel}}</strong></p></div><button type="button" :disabled="busy" @click="emit('close')">返回文稿</button></header>
  <p v-if="error" role="alert" class="error">{{error}}</p><p v-if="notice" role="status">{{notice}}</p>
  <button type="button" :disabled="busy||loading" @click="load">重新读取</button>
  <p v-if="!chapter.current_version_id">请先提交正式章节版本；普通自动保存只更新工作副本。</p>
  <p v-else-if="!formalCurrent" class="warning">工作副本与正式正文有差异。下方评审针对固定正式版本；不能将工作副本的选段直接关联到它。作者提交评审前需先提交正式版本，Reviewer 可核对固定正文后退回。</p>
  <p v-if="state?.reason" class="return-reason"><strong>{{state.status==='DRAFT'?'最近退回 / 评审理由':'最近评审理由'}}：</strong>{{state.reason}}<small>{{state.actor_id}} · 正式版本 {{state.chapter_version_id}} · {{state.updated_at}}</small></p>
  <div v-if="draftStale" class="warning" role="alert"><p>输入依据的正式版本或项目基线已经变化。内容保留，旧选段不会自动写入新版本。</p><button type="button" :disabled="busy||loading||readonly" @click="rebaseDraft">读当前正文并明确重新关联输入</button></div>
  <section v-if="currentVersion" class="formal-comparison" aria-label="正式版本正文对照">
   <div><h3>当前正式正文</h3><small>{{currentVersion.id}} · 研究条件 {{currentVersion.spec_revision}} · {{currentVersion.created_at}}</small><pre ref="formalText" tabindex="0" @mouseup="selectFormalRange" @keyup="selectFormalRange">{{currentVersion.body_markdown}}</pre></div>
   <div><label>只读对照版本<select v-model="comparisonId"><option value="">选择实际版本</option><option v-for="version in versions" :key="version.id" :value="version.id">{{version.created_at}} · {{version.id}}</option></select></label><template v-if="comparedVersion"><small>{{comparedVersion.id}} · 研究条件 {{comparedVersion.spec_revision}}</small><pre>{{comparedVersion.body_markdown}}</pre></template><p v-else class="muted">读取历史不会恢复工作副本或移动正式版本指针。</p></div>
  </section>
  <div v-if="canAuthor||canReview" class="review-actions">
   <label class="acknowledgement"><input v-model="formalAcknowledged" type="checkbox" :disabled="busy||readonly||!ready||draftStale" />我已核对上方当前正式版本正文。</label>
   <label>评审 / 退回原因<textarea v-model="reason" :disabled="busy||readonly||loading" maxlength="2000" rows="3" /></label>
   <button v-if="canAuthor" type="button" :disabled="busy||readonly||!ready||draftStale||!formalAcknowledged||!formalCurrent" @click="review('submit')">提交当前正式版本评审</button>
   <button v-if="canReview" type="button" :disabled="busy||readonly||!ready||draftStale||!formalAcknowledged||state?.status!=='IN_REVIEW'||!reason.trim()" @click="review('return')">带理由退回当前正式版本</button>
  </div>
  <div v-if="canAuthor||canReview" class="comment-form"><h3>新增批注</h3><blockquote v-if="draft.selection&&!draftStale&&ready">关联当前正式选段：{{draft.selection.quote_markdown}} <button type="button" :disabled="busy" @click="clearSelection">解除选段</button></blockquote><p v-else class="muted">章节批注。可在上方当前正式正文中选取文字，绑定确切版本与范围。</p><label>批注内容<textarea v-model="body" :disabled="busy||readonly||loading" maxlength="4000" rows="4" placeholder="说明具体问题或修改建议" /></label><button type="button" :disabled="busy||readonly||!ready||draftStale||!body.trim()" @click="comment()">保存当前正式版本批注</button></div>
  <h3>批注历史</h3><p v-if="!comments.length&&!loading&&!error" class="muted">此章节暂无批注。</p>
  <article v-for="item in comments" :key="item.id" :class="{resolved:item.resolved}"><small>{{item.actor_id}} · {{item.created_at}} · {{item.chapter_version_id===chapter.current_version_id?'当前正式版本':'历史正式版本'}} {{item.chapter_version_id}} · {{item.resolved?'已解决':'待处理'}}</small><blockquote v-if="item.selection">{{item.selection.quote_markdown}}</blockquote><p class="comment-body">{{item.body}}</p><button type="button" @click="showCommentVersion(item)">查看所属正式正文</button><button v-if="item.selection&&item.chapter_version_id===chapter.current_version_id&&formalCurrent&&selectionFits(item.selection,currentVersion)" type="button" @click="locateComment(item)">定位当前原文</button><p v-else-if="item.selection" class="muted">此批注保留固定版本的原文关联，不在当前工作正文猜测位置。</p><button v-if="!item.resolved&&(item.actor_id===currentUserId||owner)&&(canAuthor||canReview)" type="button" :disabled="busy||readonly||!ready" @click="comment(item)">标记已解决</button></article>
 </section>
</template>
<style scoped>
.chapter-review-panel{padding:24px;color:#243d3c;min-width:0}header{display:flex;justify-content:space-between;gap:16px;align-items:start}h2{margin:0;font-size:22px}h3{font-size:16px;margin-top:20px}.review-actions,.comment-form{display:grid;gap:12px}label{display:grid;gap:8px}textarea,select{width:100%;box-sizing:border-box;padding:10px;resize:vertical}textarea{min-height:80px}button{padding:9px 14px;border:1px solid #c9d9d7;border-radius:7px;background:#fff;cursor:pointer;justify-self:start}button:disabled{opacity:.55;cursor:default}article{padding:16px 0;border-bottom:1px solid #e1e8e6}blockquote{margin:12px 0;padding:12px 16px;border-left:3px solid #298779;background:#eff7f4;white-space:pre-wrap;overflow-wrap:anywhere}.comment-body,pre,.return-reason{white-space:pre-wrap;overflow-wrap:anywhere}.muted,small{color:#647674}small{display:block}.warning,.return-reason{background:#fff8e9;padding:12px;color:#805c1d}.error{color:#a83131}.resolved{opacity:.75}.formal-comparison{display:grid;grid-template-columns:1fr 1fr;gap:16px;margin:20px 0}.formal-comparison>div{min-width:0}pre{background:#f4f7f5;border:1px solid #d9e4dd;border-radius:6px;padding:16px;max-height:400px;overflow:auto;font:14px/1.8 "Songti SC","SimSun",Georgia,serif}.acknowledgement{display:flex;gap:8px;align-items:start}button:focus-visible,pre:focus-visible{outline:3px solid #3976d3;outline-offset:2px}@media(max-width:700px){.formal-comparison{grid-template-columns:1fr}.chapter-review-panel{padding:16px}}
</style>