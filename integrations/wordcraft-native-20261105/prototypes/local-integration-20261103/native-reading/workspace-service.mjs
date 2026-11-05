// Native workspace data adapter. Only project materials reach the existing Go QA path.
// No document/selection API is used here and model output is never executed.
import {createHash, randomUUID} from 'node:crypto';
import {readFile} from 'node:fs/promises';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {atomicJSON, fail} from '../chain-store.mjs';
const here=path.dirname(fileURLToPath(import.meta.url));
const hash=x=>createHash('sha256').update(typeof x==='string'?x:JSON.stringify(x)).digest('hex');
const enc=encodeURIComponent;
const rows=x=>Array.isArray(x)?x:Array.isArray(x?.items)?x.items:[];
const clone=x=>structuredClone(x);
// Only stable local messages cross the UI boundary; upstream error details may contain credentials.
function turnFailureMessage(turn) {
  const messages={
    provider_failure:'AI 服务本轮未能完成回答。问题已保留，可以稍后手动重试或换一个问题；系统不会自动重发。',
    timeout:'AI 回答超时。问题已保留，可以稍后手动重试；系统不会自动重发。',
    cancelled:'本轮 AI 请求已取消。问题已保留，可以重新提问。',
    incomplete_response:'AI 回答未完整返回，本轮未作为有效回答展示。可以缩短问题后手动重试。',
    stale_context:'回答期间资料或项目状态发生变化，本轮回答未采用。请核对资料后重新提问。',
    stale_basis:'本轮依据版本已变化，请核对资料后重新提问。',
    source_access_denied:'本轮参考资料已不可访问，请移除失效资料或重新导入后提问。',
    outcome_unknown:'本轮请求结果尚未确定，记录已保留；为避免重复调用，请新建会话继续。'
  };
  return messages[turn?.failure_code]||'本轮模型未返回可用回答，问题和请求记录已保留；可以稍后手动重试。';
}


export async function createWorkspaceService({directory,goOrigin,origin,projectId,sources,imports=null,fetchImpl=fetch,pdfFixture=undefined}) {
  for(const value of [goOrigin,origin]) {const u=new URL(value);if(u.protocol!=='http:'||!['localhost','127.0.0.1'].includes(u.hostname)||u.origin!==value)throw Error('Exact loopback origin required');}
  if(!projectId||!sources?.readSource||!sources?.listAssets)throw Error('Project material adapter required');
  const base='/api/v1/lingdoc/projects/'+enc(projectId), file=path.join(directory,'native-workspace.json');
  async function request(route,{method='GET',body,key}={}) {
    let response;
    try { response=await fetchImpl(goOrigin+route,{method,headers:{Origin:origin,'Content-Type':'application/json',...(key?{'Idempotency-Key':key}:{})},body:body===undefined?undefined:JSON.stringify(body),redirect:'error',signal:AbortSignal.timeout(75000)}); }
    catch { fail(502,'服务连接中断或超时，请稍后重试。'); }
    let value;try{value=await response.json()}catch{fail(502,'服务响应无法读取，请保留本轮记录，不要重复提交。')}
    if(!value||typeof value!=='object'||Array.isArray(value))fail(502,'服务响应格式无效，请保留本轮记录，不要重复提交。');
    if(!response.ok||value.success===false){
      const form=value&&typeof value.error==='object'&&value.error?value.error:value;
      const rawCode=form?.code||(typeof value?.error==='string'?value.error:undefined);
      const code=['source_access_denied','provider_failure','timeout','stale_context','stale_basis','outcome_unknown','forbidden','validation_error','version_conflict','conflict'].includes(rawCode)?rawCode:undefined;
      const message=code==='source_access_denied'?'资料暂不可访问，请核对权限或移除失效资料。':response.status===409?'项目或资料状态已变化，请刷新后重新操作。':response.status===401||response.status===403?'服务暂未授权此操作，请核对访问配置。':'资料问答服务暂不可用，本轮记录已保留。';
      throw Object.assign(new Error(message),{status:response.status,code});
    }
    return Object.hasOwn(value,'data')?value.data:value;
  }
  const req=(suffix,options)=>request(base+suffix,options);
  let db;try{db=JSON.parse(await readFile(file,'utf8'))}catch(e){if(e.code!=='ENOENT')throw e;}
  if(!db)db={schema:1,projectId,currentSessionId:null,sessions:[],operations:{},pdf:null};
  if(db.schema!==1||db.projectId!==projectId)throw Error('Native workspace project identity mismatch');
  const save=()=>{db.revision=(db.revision||0)+1;return atomicJSON(file,db);};
  for(const session of db.sessions)if(session.busy){session.busy=false;session.uncertain=true;session.error='上次请求中断，结果尚未核对；请新建会话继续，不会自动重复调用模型。';}
  if(pdfFixture===undefined){pdfFixture={manifest:JSON.parse(await readFile(path.join(here,'fixtures','manifest.json'),'utf8')),payload:JSON.parse(await readFile(path.join(here,'fixtures','source.json'),'utf8'))};}
  if(pdfFixture&&!db.pdf){
    const text=pdfFixture.manifest.pages.map(p=>`【第 ${p.page} 页】\n${p.text}`).join('\n\n');
    const uploaded=await sources.uploadText({title:'研究说明-PDF原文-v1.txt',text,key:'native-pdf-'+pdfFixture.manifest.pdfSha256.slice(0,48)});
    db.pdf={assetId:uploaded.asset.id,sourceId:uploaded.sourceId,pdfSha256:pdfFixture.manifest.pdfSha256,textHash:hash(uploaded.document.text)};
  }
  if(db.pdf&&pdfFixture&&db.pdf.pdfSha256!==pdfFixture.manifest.pdfSha256)throw Error('PDF fixture changed; import a new explicit version');
  let catalog=[],details=new Map(),assetIds=[],importRecords=[],importBusy=false,importError='',catalogWarnings=[];
  async function refreshCatalog(){
    const assets=rows(await sources.listAssets());const records=imports?await imports.list():[];const next=[];const map=new Map();const warnings=[];
    for(const asset of assets.slice(0,50)){
      if(!asset.id||!asset.knowledge_id)continue;
      const failedImport=records.find(r=>r.assetId===asset.id&&(r.status!=='ready'||r.searchable!==true));
      if(failedImport){warnings.push({assetId:asset.id,title:failedImport.displayTitle||failedImport.name||asset.title||'项目资料',message:'保留原件或导入状态尚未通过核验，未加入问答范围。'});continue;}
      const chunks=rows(await request(`/api/v1/chunks/${enc(asset.knowledge_id)}?page=1&page_size=100`));
      const first=chunks.find(c=>c.id&&(!c.chunk_type||c.chunk_type==='text'));if(!first)continue;
      let document;try{document=await sources.readSource(first.id)}catch(error){if(error.status!==403||error.code!=='source_access_denied')throw error;
        let local=records.some(r=>r.unavailableAssetId===asset.id);
        if(!local){const knowledge=await request(`/api/v1/knowledge/${enc(asset.knowledge_id)}`);let meta=knowledge.metadata||{};if(typeof meta==='string'){try{meta=JSON.parse(meta)}catch{meta={}}}
          local=records.some(r=>r.extractedTextSha256&&meta.local_operation_key==='import-'+r.id&&meta.local_content_sha256===r.extractedTextSha256);}
        if(!local)throw error;warnings.push({assetId:asset.id,title:asset.title||'项目资料',message:'检索原文暂不可核验或不可访问，未加入问答范围。'});continue;}
      if(document.source.asset_id!==asset.id)throw Error('Source/project binding mismatch');
      const imported=records.find(r=>r.assetId===asset.id&&r.searchable&&r.sourceId);
      const pdf=asset.id===db.pdf?.assetId;
      const item={id:first.id,title:pdf?'研究说明.pdf':asset.title||document.source.locator||'项目资料',version:String(asset.asset_revision||1),folder:pdf?'申报要求与模板':'前期研究材料',assetId:asset.id,pdf};
      if(imported)Object.assign(item,{title:imported.displayTitle||imported.name,version:String(imported.version),folder:imported.folder||'前期研究材料',importId:imported.id,searchable:true,rendering:imported.rendering});
      next.push(item);map.set(item.id,{...item,document});
    }
    for(const r of records.filter(r=>r.status==='readable_only'&&r.sourceId)){const item={id:r.sourceId,title:r.displayTitle||r.name,version:String(r.version),folder:r.folder||'前期研究材料',importId:r.id,searchable:false,rendering:r.rendering};next.push(item);map.set(item.id,item);}
    catalog=next;details=map;assetIds=assets.map(a=>a.id);importRecords=records;catalogWarnings=warnings;
  }
  await refreshCatalog();
  const newSession=()=>{const s={id:randomUUID(),goId:null,title:'新会话',messages:[],context:[],excluded:[],excludedAssets:[],busy:false,error:'',uncertain:false};db.sessions.push(s);db.currentSessionId=s.id;return s;};
  if(!db.sessions.length)newSession();
  if(!db.sessions.some(s=>s.id===db.currentSessionId))db.currentSessionId=db.sessions[0].id;
  await save();
  const current=()=>db.sessions.find(s=>s.id===db.currentSessionId);
  async function resolve(id){
    if(details.has(id))return details.get(id);
    const document=await sources.readSource(id),assetId=document.source.asset_id;
    const sibling=catalog.find(s=>s.assetId===assetId);
    if(!sibling||!assetIds.includes(assetId))fail(404,'资料不属于当前项目或已不可读取。');
    const detail={...sibling,id,document};details.set(id,detail);return detail;
  }
  function checkVersion(detail,version){
    if(version!==undefined&&version!==null&&String(version)!==detail.version)fail(409,'资料版本与引用记录不一致，请核对更新后重新选择。');
  }
  async function unchangedDocument(detail){
    if(detail.searchable===false)fail(409,'这份资料目前仅支持阅读，尚无可检索文字，不能加入问答上下文。');
    const fresh=await sources.readSource(detail.id);
    if(fresh.source?.quoted_text_hash!==detail.document.source?.quoted_text_hash||fresh.text!==detail.document.text)fail(409,'资料原文已变化，请核对更新；旧引用不会自动跳到新版本。');
    if(detail.importId){const record=importRecords.find(r=>r.id===detail.importId);if(record?.status!=='ready'||record.searchable!==true)fail(409,'这份资料的导入状态已失效，请移除固定资料或重新核对导入记录。');if(!record.indexedTextSha256||hash(fresh.text)!==record.indexedTextSha256)fail(409,'资料检索文字与保留原件不一致，请核对导入记录。');}
    return fresh;
  }
  function state(){const s=current();return clone({stateRevision:db.revision||0,projectId,catalogWarnings,imports:importRecords,importBusy,importError,projectTitle:'社区数字服务研究',documentTitle:'申请书.docx',sources:catalog.map(({assetId,pdf,...v})=>v),sessions:db.sessions.map(s=>({id:s.id,title:s.title})),currentSessionId:s.id,messages:s.messages,contextSources:s.context.map(id=>{const d=details.get(id)||catalog.find(x=>x.id===id);return {id,title:d?.title||'已固定资料',version:s.contextVersions?.[id]||d?.version||'1'}}),busy:s.busy,error:s.error,contextNote:s.excluded.length?'已移除资料不再加入后续上下文；旧聊天仍可能含有其内容。':'默认检索当前项目资料；不会读取正文。'});}
  async function expectation(version=0){const p=await req('');return {expected_project_version:p.project_version,expected_context_revision:p.current_context_revision,expected_version:version};}
  async function allowModel(detail,operation){
    const assetId=detail.assetId;if(!assetIds.includes(assetId))fail(403,'资料超出项目范围。');
    const policy=await req(`/assets/${enc(assetId)}/policy`);if(policy.model_use_allowed)return;
    const step=async(name,suffix,bodyFactory)=>{operation.steps||={};if(!operation.steps[name]){operation.steps[name]={key:randomUUID(),body:await bodyFactory()};await save();}const item=operation.steps[name];if(item.value)return item.value;item.value=await req(suffix,{method:'POST',body:item.body,key:item.key});await save();return item.value;};
    const proposal=await step('grant:'+assetId,`/assets/${enc(assetId)}/policy/proposals`,async()=>({...await expectation(policy.version),confirmed:true,reason:'用户在当前项目主动发起资料问答。',policy:{...policy,model_use_allowed:true,purpose:'当前项目资料问答',reason:'用户授权当前项目资料用于问答，不读取正文。'}}));
    await step('assess:'+assetId,`/change-sets/${enc(proposal.id)}/assess`,async()=>({}));
    await step('apply:'+assetId,`/change-sets/${enc(proposal.id)}/apply`,async()=>({}));
  }
  async function ask(session,text,operation){
    const allowedAssets=catalog.filter(c=>c.assetId&&c.searchable!==false&&!(session.excludedAssets||[]).includes(c.assetId)&&!session.excluded.some(id=>details.get(id)?.assetId===c.assetId)).map(c=>c.assetId);
    const retrieved=allowedAssets.length?rows(await req('/retrieval',{method:'POST',body:{query:text,asset_ids:allowedAssets}})):[];
    const selected=[];let count=0,overBudget=false;
    const previousSources=session.messages.findLast(m=>m.role==='assistant'&&m.usedSources?.length)?.usedSources||[];
    // Retrieval may narrow on a follow-up; keep prior permitted evidence before new hits
    // so Go can retain the compatible conversation history. Explicit exclusions still win.
    const continuity=previousSources.map(s=>s.sourceId);
    for(const id of [...session.context,...continuity,...retrieved.map(r=>r.id)]){
      if(!id||selected.some(s=>s.id===id)||session.excluded.includes(id))continue;
      const d=await resolve(id);if(!allowedAssets.includes(d.assetId))continue;
      if(session.context.includes(id))checkVersion(d,session.contextVersions?.[id]);
      const previous=continuity.includes(id)?previousSources.find(s=>s.sourceId===id):undefined;
      if(previous)checkVersion(d,previous.version);
      const fresh=await unchangedDocument(d);
      if(previous?.textHash&&previous.textHash!==hash(fresh.text))fail(409,'上轮使用的资料内容已变化，请核对资料后重新加入会话；旧回答的依据不会自动替换。');
      if(session.context.includes(id)&&session.contextHashes?.[id]&&session.contextHashes[id]!==hash(fresh.text))fail(409,'固定资料内容已变化，请核对版本并重新加入会话。');
      const length=Array.from(fresh.text).length;
      if(selected.length>=4)continue;
      if(count+length>12000){
        if(session.context.includes(id))fail(400,'固定资料超过本轮可读取长度（12000 字符），请移除部分资料或改用较短的资料后提问；本轮未调用模型。');
        overBudget=true;continue;
      }
      selected.push(d);count+=length;
    }
    if(!selected.length){session.messages.push({id:randomUUID(),role:'assistant',text:overBudget?'已找到资料，但全文超过本轮问答可读取长度（12000 字符），仍可打开原文；暂不能据此回答。':'当前项目资料中没有找到可核对的相关内容。可以换用资料里的关键词，或先将一份资料加入当前会话。',citations:[],kind:overBudget?'context_over_budget':'retrieval_empty'});return;}
    for(const d of selected)await allowModel(d,operation);
    if(!session.goId){operation.session={key:randomUUID(),body:await expectation()};await save();const remote=await req('/agents/workspace_assistant/sessions',{method:'POST',...operation.session});session.goId=remote.id;await save();}
    const source_excerpts=selected.map(d=>({source_id:d.id,selection_start:0,selection_end:Array.from(d.document.text).length,quoted_text:d.document.text,excerpt_text_hash:hash(d.document.text),source_quoted_text_hash:d.document.source.quoted_text_hash}));
    operation.model={key:randomUUID(),body:{...await expectation(),user_input:text,source_ids:[],source_excerpts,card_refs:[],sensitive_context_confirmed:true}};
    operation.modelSubmitted=true;await save();
    const turn=await req(`/agent-sessions/${enc(session.goId)}/turns`,{method:'POST',...operation.model});operation.turn=turn;await save();
    if(turn.status!=='completed'){if(turn.failure_code==='outcome_unknown')session.uncertain=true;fail(502,turnFailureMessage(turn));}
    if(turn.run_mode!=='real'||!turn.model_id||typeof turn.reply!=='string'||!turn.reply.trim())fail(502,'本轮未取得可核验的真实模型回答，问题和请求记录已保留；系统不会自动重发。');
    let suggestions=turn.suggestions||{};if(typeof suggestions==='string'){try{suggestions=JSON.parse(suggestions)}catch{fail(502,'回答来源记录无法读取。')}}
    const citations=[];
    for(const c of suggestions.citations||[]){
      const d=selected.find(s=>s.id===c.source_id);
      if(!d||typeof c.quote!=='string'||!c.quote.trim()||!d.document.text.includes(c.quote))fail(502,'回答中的出处未通过原文核验。');
      let page=d.pdf?pdfFixture?.manifest.pages.find(p=>p.text.includes(c.quote))?.page:undefined;
      if(d.importId&&imports){const original=await imports.resolve(d.importId);page=original.pageTexts?.find(p=>p.text.includes(c.quote))?.page;}
      citations.push({sourceId:d.id,title:d.title,version:d.version,quote:c.quote,...(page?{page}:{})});
    }
    // Used context is visible even when the model gives no explicit quotation.
    const usedSources=selected.map(d=>({sourceId:d.id,title:d.title,version:d.version,textHash:hash(d.document.text)}));
    session.messages.push({id:turn.id,role:'assistant',text:turn.reply,citations,usedSources,historyUsage:suggestions.history_usage||null,modelId:turn.model_id,runMode:turn.run_mode});
  }
  let serial=Promise.resolve(),actionPending=false;
  async function action(input){
    if(!input||typeof input.key!=='string'||!/^[a-zA-Z0-9_-]{8,128}$/.test(input.key))fail(400,'缺少有效操作编号。');
    if(input.expectedSessionId!==undefined&&input.expectedSessionId!==db.currentSessionId)fail(409,'会话已切换，请刷新当前会话后重新操作。');
    const fingerprint=hash(input),old=db.operations[input.key];
    if(old){if(old.fingerprint!==fingerprint)fail(409,'操作编号已被其他内容使用。');return state();}
    const session=current();if(db.sessions.some(s=>s.busy))fail(409,'当前回答尚未完成，请稍候。');
    if(Object.keys(db.operations).length>=2000)fail(409,'本地试用记录已达上限，请保留记录后维护。');
    const operation={fingerprint,kind:input.kind,sessionId:session.id,status:'running',startedAt:new Date().toISOString()};db.operations[input.key]=operation;
    try{
      if(input.kind==='newSession'){if(db.sessions.length>=100)fail(409,'会话数量达到试用上限。');newSession();}
      else if(input.kind==='selectSession'){if(!db.sessions.some(s=>s.id===input.sessionId))fail(404,'会话不存在。');db.currentSessionId=input.sessionId;}
      else if(input.kind==='addContext'){const d=await resolve(input.sourceId);checkVersion(d,input.version);await unchangedDocument(d);if(session.context.length>=4&&!session.context.includes(d.id))fail(400,'当前会话最多固定四份资料。');if(!session.context.includes(d.id))session.context.push(d.id);session.contextVersions||={};session.contextHashes||={};session.contextVersions[d.id]=d.version;session.contextHashes[d.id]=hash(d.document.text);session.contextAssets||={};session.contextAssets[d.id]=d.assetId;session.excluded=session.excluded.filter(id=>id!==d.id&&details.get(id)?.assetId!==d.assetId);session.excludedAssets=(session.excludedAssets||[]).filter(id=>id!==d.assetId);session.error='';}
      else if(input.kind==='removeContext'){
        // Removing a known pin must remain possible even when its original disappeared.
        const pinned=session.context.includes(input.sourceId);
        const d=details.get(input.sourceId)||(pinned?{id:input.sourceId,assetId:session.contextAssets?.[input.sourceId]}:await resolve(input.sourceId));
        session.context=session.context.filter(id=>id!==input.sourceId&&(!d.assetId||details.get(id)?.assetId!==d.assetId));
        if(!session.excluded.includes(input.sourceId))session.excluded.push(input.sourceId);
        session.excludedAssets||=[];if(d.assetId&&!session.excludedAssets.includes(d.assetId))session.excludedAssets.push(d.assetId);
        session.error='';
      }
      else if(input.kind==='send'){
        const text=typeof input.text==='string'?input.text.trim():'';if(!text||Array.from(text).length>4000)fail(400,'请输入1–4000字符的问题。');
        if(session.uncertain)fail(409,'上次请求结果尚未核对，请新建会话继续。');
        session.busy=true;session.error='';if(!session.messages.length)session.title=Array.from(text).slice(0,24).join('');
        session.messages.push({id:randomUUID(),role:'user',text});await save();
        await ask(session,text,operation);session.busy=false;
      }else fail(400,'未知工作区操作。');
      operation.status='completed';await save();return state();
    }catch(error){session.busy=false;if(operation.modelSubmitted&&!operation.turn){session.uncertain=true;error.message=turnFailureMessage({failure_code:'outcome_unknown'});}session.error=error.message;operation.status='failed';operation.error=error.message;await save();throw error;}
  }
  async function openSource(sourceId,page,version){
    const d=await resolve(sourceId);checkVersion(d,version);
    if(d.importId&&imports){
      const original=await imports.resolve(d.importId);
      if(!original?.payload||String(original.record.version)!==d.version)fail(409,'保留的资料版本不可用，请核对导入记录。');
      if(d.searchable!==false){const fresh=await unchangedDocument(d);if(!original.record.indexedTextSha256||hash(fresh.text)!==original.record.indexedTextSha256)fail(409,'资料检索文字与保留原件不一致，请核对导入记录。');}
      const p=page===undefined?1:Number(page),count=original.payload.op==='source.open'?original.payload.pages.length:1;
      if(!Number.isInteger(p)||p<1||p>count)fail(400,'资料页码无效。');
      return {payload:{...original.payload,sourceId:d.id,version:d.version},page:p};
    }
    const fresh=await unchangedDocument(d);
    if(d.pdf&&pdfFixture){if(hash(fresh.text)!==db.pdf.textHash)fail(409,'资料文字版本与保留的PDF不一致，请重新导入核对。');const p=page===undefined?1:Number(page);if(!Number.isInteger(p)||p<1||p>pdfFixture.payload.pages.length)fail(400,'资料页码无效。');return {payload:{...pdfFixture.payload,sourceId:d.id,title:d.title,version:d.version},page:p};}
    return {payload:{op:'source.openText',sourceId:d.id,title:d.title,version:d.version,text:fresh.text},page:1};
  }
  async function importFile(input){
    if(!imports)fail(409,'资料导入尚未接通。');
    if(importBusy||actionPending||db.sessions.some(s=>s.busy))fail(409,'当前操作尚未完成，请稍候再导入。');
    if(input.projectId!==undefined&&input.projectId!==projectId)fail(409,'项目已切换，请重新选择资料。');
    importBusy=true;importError='';
    const next=serial.then(async()=>{
      let failure;
      try{await save();const record=await imports.importFile(input);await refreshCatalog();return record;}
      catch(error){
        failure=error;importError=error.message;
        // Import validation may invalidate a formerly ready asset in this same process.
        // Quarantine synchronously from the local records, without relying on an upstream refresh.
        try{
          importRecords=await imports.list();
          const invalid=importRecords.filter(r=>r.assetId&&(r.status!=='ready'||r.searchable!==true));
          const ids=new Set(invalid.map(r=>r.assetId));
          catalog=catalog.filter(d=>!ids.has(d.assetId));
          details=new Map([...details].filter(([,d])=>!ids.has(d.assetId)));
          for(const r of invalid)if(!catalogWarnings.some(w=>w.assetId===r.assetId))catalogWarnings.push({assetId:r.assetId,title:r.displayTitle||r.name||'项目资料',message:'保留原件或导入状态尚未通过核验，未加入问答范围。'});
        }catch{/* Preserve the actual import failure when status refresh also fails. */}
        throw error;
      }
      finally{importBusy=false;try{await save()}catch(error){if(!failure)throw error}}
    });serial=next.catch(()=>{});return next;
  }
  return {state:async()=>state(),importFile,act:input=>{if(importBusy)return Promise.reject(Object.assign(new Error('资料正在导入，请稍候再操作会话。'),{status:409}));if(actionPending||db.sessions.some(s=>s.busy))return Promise.reject(Object.assign(new Error('当前回答或操作尚未完成，请稍候。'),{status:409}));actionPending=true;const sessionId=db.currentSessionId;const next=serial.then(()=>{if(db.currentSessionId!==sessionId)fail(409,'活动会话已经切换，请重新操作。');return action(input)}).finally(()=>{actionPending=false});serial=next.catch(()=>{});return next;},openSource};
}
