import { readFile } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import path from 'node:path';
import { atomicJSON, fail } from './chain-store.mjs';
const digest=value=>createHash('sha256').update(typeof value==='string'?value:JSON.stringify(value)).digest('hex');
const clone=value=>structuredClone(value);
const validText=(value,max)=>typeof value==='string' && value.trim() && Array.from(value).length<=max;
async function writeWorkflowState(file,value) {
  // Windows scanners can briefly hold the destination. Retry only this exact
  // local snapshot, never a model request or document operation.
  for(let attempt=0;;attempt++) {
    try {await atomicJSON(file,value);return;}
    catch(error) {if(!['EPERM','EACCES','EBUSY'].includes(error.code)||attempt>=3)throw error;await new Promise(resolve=>setTimeout(resolve,25*(attempt+1)));}
  }
}
function targetOf(value) {
  if(!value || !Number.isSafeInteger(value.documentId) || value.documentId<=0 || !Number.isSafeInteger(value.revision) || value.revision<0 || !validText(value.text,6000) || !validText(value.hostSessionId,120) || !value.anchor || !value.focus) fail(400,'请提供真实正文选区及当前宿主身份');
  return clone({documentId:value.documentId,revision:value.revision,anchor:value.anchor,focus:value.focus,text:value.text,hostSessionId:value.hostSessionId});
}

export async function createWorkflowStore({directory,chain,model,latestVersion,writeState=writeWorkflowState}) {
  const file=path.join(directory,'controlled-writing.json');
  let state;
  try { state=JSON.parse(await readFile(file,'utf8')); } catch(error) { if(error.code!=='ENOENT')throw error; }
  state ||= {schema:1,projectId:chain.read().projectId,revision:0,candidates:[],operations:{}};
  if(state.schema!==1 || state.projectId!==chain.read().projectId)throw Error('Workflow store identity mismatch');
  let serial=Promise.resolve();
  const visibleCandidate=c=>{ const {modelOperations,...view}=c; return clone(view); };
  const read=()=>({revision:durable.revision,candidates:durable.candidates.map(visibleCandidate)});
  const result=c=>({...read(),candidate:visibleCandidate(durable.candidates.find(saved=>saved.id===c.id))});
  let durable=clone(state);
  const persist=async()=>{
    state.revision++;
    try {await writeState(file,state);durable=clone(state);}
    catch(error) {state=clone(durable);throw error;}
  };
  function contextFor(ids,includeComment) {
    const current=chain.read();
    if(!Array.isArray(ids) || ids.length>4 || new Set(ids).size!==ids.length)fail(400,'请选择最多四份不同资料版本');
    const selected=ids.map(id=>{
      const source=current.sourceVersions[id];
      if(!source || source.prepared || !source.sourceId || !validText(source.text,12000))fail(400,'只能选择已登记且可读取的资料版本');
      return {versionId:id,sourceId:source.sourceId,title:source.title,text:source.text,sha256:digest(source.text),quotedTextHash:source.source?.quoted_text_hash || source.document?.source?.quoted_text_hash};
    });
    if(selected.reduce((n,s)=>n+Array.from(s.text).length,0)>12000 || selected.some(s=>!s.quotedTextHash))fail(400,'资料过长或缺少来源摘要');
    return {sources:selected,comment:includeComment ? clone(current.comment) : null,sourceHead:selected.length ? current.sourceLatest : null};
  }
  const contextCurrent=c=>digest(contextFor(c.context.sources.map(s=>s.versionId),!!c.context.comment))===digest(c.context);
  async function assertCurrent(c) {
    let current=false;
    try {current=contextCurrent(c);if(current)await model.validateSources?.(c.context);} catch(error) {if(error.status!==409 && error.status!==400)throw error;current=false;}
    if(!current) {c.status='stale';c.error='资料或人工意见已变化，请重新生成候选';await persist();fail(409,c.error);}
  }
  async function action(input) {
    if(!input || !/^[a-zA-Z0-9_-]{8,100}$/.test(input.key||''))fail(400,'需要有效的操作编号');
    const fingerprint=digest(input), prior=state.operations[input.key];
    if(prior && prior.fingerprint!==fingerprint)fail(409,'同一操作编号不能用于不同请求');
    if(prior?.done && input.type!=='preflight') {
      const c=state.candidates.find(c=>c.id===prior.candidateId);return result(c);
    }
    let c;
    if(input.type==='generate') {
      if(input.contextConfirmed!==true || !validText(input.instruction,2400) || typeof input.includeComment!=='boolean')fail(400,'请明确修改要求并确认本次AI上下文');
      const target=targetOf(input.target);
      if(prior) c=state.candidates.find(c=>c.id===prior.candidateId);
      else {
        if(input.expectedChainRevision!==chain.read().revision)fail(409,'项目记录已变化，请重新查看并确认本次上下文');
        const context=contextFor(input.sourceVersionIds || [],input.includeComment);
        if(context.comment && Array.from(context.comment.text).length+Array.from(input.instruction).length>3400)fail(400,'人工意见和修改要求过长，请缩短后再发送');
        const saved=await latestVersion();
        c={id:`wf-${digest(input.key).slice(0,32)}`,status:'generating',target,original:target.text,replacement:'',instruction:input.instruction.trim(),context,citations:[],baseSavedVersion:{diskVersion:saved.diskVersion,docSha256:saved.docSha256},createdAt:new Date().toISOString(),history:[],modelOperations:{}};
        state.candidates.push(c);state.operations[input.key]={fingerprint,candidateId:c.id,done:false};await persist();
      }
      await assertCurrent(c);
      c.status='generating';delete c.error;await persist();
      try {
        const output=await model.generate(c,persist);
        if(!validText(output.replacement,12000) || output.modelReceipt?.runMode!=='real')fail(502,'模型没有返回可用的真实候选');
        await assertCurrent(c);
        Object.assign(c,output,{status:'proposed'});
      } catch(error) {
        if(c.status!=='stale')c.status='failed';
        c.error=error.message;await persist();throw error;
      }
    } else {
      c=state.candidates.find(c=>c.id===input.candidateId);if(!c)fail(404,'候选不存在');
      switch(input.type) {
        case 'preflight': {
          if(!['proposed','applying'].includes(c.status))fail(409,'此候选不能再采纳');
          await assertCurrent(c);
          if(digest(targetOf(input.target))!==digest(c.target))fail(409,'正文或选区已变化，请重新生成；未修改正文');
          c.status='applying';c.operationId=c.id;break;
        }
        case 'applied': {
          if(!['applying','applied'].includes(c.status))fail(409,'请先核对候选和当前选区');
          const r=input.receipt;
          if(!r || r.operationId!==c.id || r.status!=='applied' || r.matchesCurrentRevision!==true || r.documentId!==c.target.documentId || r.beforeRevision!==c.target.revision || !Number.isSafeInteger(r.afterRevision) || r.afterRevision<=r.beforeRevision || r.beforeText!==c.original || r.afterText!==c.replacement)fail(409,'原生操作回执与候选不一致或正文已变化');
          c.nativeReceipt=clone(r);c.status='applied';break;
        }
        case 'saved': {
          if(!['applied','saved'].includes(c.status))fail(409,'尚无已应用的原生操作');
          const r=input.receipt;
          if(!r || r.operationId!==c.id || r.status!=='applied' || r.matchesCurrentRevision!==true || r.afterRevision!==c.nativeReceipt.afterRevision || r.documentId!==c.target.documentId || input.savedVersion?.nativeDocumentId!==r.documentId || input.savedVersion?.nativeRevision!==r.afterRevision)fail(409,'当前原生正文与此次修改不一致，请人工核对后再关联保存');
          const saved=await latestVersion();
          if(!input.savedVersion || saved.diskVersion!==input.savedVersion.diskVersion || saved.docSha256!==input.savedVersion.docSha256 || saved.diskVersion<=c.baseSavedVersion.diskVersion || saved.docSha256===c.baseSavedVersion.docSha256)fail(409,'没有匹配的最新正文保存版本');
          // This links the user's save to an observed native receipt; it does not
          // assert semantic completion or close the independent review task.
          c.savedVersion={diskVersion:saved.diskVersion,docSha256:saved.docSha256};c.status='saved';break;
        }
        case 'undone': {
          const r=input.receipt;
          if(!['applied','saved'].includes(c.status) || !r || r.operationId!==c.id || r.status!=='undone' || r.matchesCurrentRevision!==true || r.documentId!==c.target.documentId || r.beforeText!==c.original || r.afterText!==c.replacement || !Number.isSafeInteger(r.afterRevision) || r.afterRevision<=c.nativeReceipt.afterRevision)fail(409,'无法确认此次修改已撤销');
          c.nativeReceipt=clone(r);c.status='undone';break;
        }
        case 'reject': {
          if(!['proposed','failed','stale'].includes(c.status))fail(409,'已开始执行的候选不能当作未采用而丢弃');
          c.status='rejected';break;
        }
        default:fail(400,'未知工作流操作');
      }
    }
    c.history.push({type:input.type,at:new Date().toISOString()});
    state.operations[input.key]={fingerprint,candidateId:c.id,done:true};await persist();return result(c);
  }
  return {read,act(input){const work=serial.then(()=>action(input));serial=work.catch(()=>{});return work;}};
}
