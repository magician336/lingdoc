// Background bridge only. Resources, chat, tabs and reading controls are native egui.
const delay=ms=>new Promise(r=>setTimeout(r,ms));
window.nativeReadingReady={phase:'loading'};
let host, snapshot, revision=-1, readGeneration=0, pending=0, lastLayout='', lastReadingLayout='', restoring=false, restoreBlocked=false, storageNotice='', panelLayoutReady=false, panelLayoutRestore;
const standalone=window.WORDCRAFT_STANDALONE===true;
async function api(route,body) {
  const response=await fetch('/native-reading/workspace/'+route,{method:body===undefined?'GET':'POST',headers:{'X-Workspace-Token':window.NATIVE_WORKSPACE_TOKEN,'Content-Type':'application/json'},body:body===undefined?undefined:JSON.stringify(body),signal:AbortSignal.timeout(100000)});
  let result;try{result=await response.json()}catch{throw Error('工作区响应无法读取，请稍后刷新。')}
  if(!response.ok)throw Error(result.error||'工作区请求失败');return result;
}
async function hydrate(state) {
  if((state.stateRevision??0)<revision)return;
  revision=state.stateRevision??0;snapshot=state;
  await host.engine({op:'workspace.hydrate',state:{...state,...resumeMetadata(state),documentBound:!standalone,...(storageNotice&&!state.error?{error:storageNotice}:{})}});
}
async function refresh(){await hydrate(await api('state'));await ensurePanelLayout();if(standalone&&window.nativeReadingReady.phase==='ready')window.nativeReadingReady.project='ready';}
function validReadingSnapshot(saved,state){
  const layout=saved?.layout;
  if(saved?.projectId!==state.projectId||saved.documentTitle!==state.documentTitle||!Array.isArray(layout?.sources)||layout.sources.length>16||!Array.isArray(layout.groups)||!layout.groups.length||layout.groups.length>3||!Array.isArray(layout.positions))return false;
  const sourceKeys=new Set(),tabKeys=new Set(),groupIds=new Set(),positionKeys=new Set();
  for(const source of layout.sources){
    if(!source||typeof source.id!=='string'||!source.id||source.id.length>512||typeof source.version!=='string'||!source.version||source.version.length>512)return false;
    const key=JSON.stringify([source.id,source.version]);if(sourceKeys.has(key)||(source.key!==undefined&&source.key!==key))return false;sourceKeys.add(key);
  }
  for(const group of layout.groups){
    if(!group||!Number.isSafeInteger(group.id)||group.id<=0||group.id>=Number.MAX_SAFE_INTEGER||groupIds.has(group.id)||!Array.isArray(group.tabs)||!group.tabs.length||group.tabs.length>17||!group.tabs.includes(group.active)||!Number.isFinite(group.weight)||group.weight<=0||group.weight>100||Math.fround(group.weight)<=0)return false;
    groupIds.add(group.id);
    for(const key of group.tabs){if(typeof key!=='string'||(key!=='$body'&&!sourceKeys.has(key))||tabKeys.has(key))return false;tabKeys.add(key);}
  }
  if(!groupIds.has(layout.focusGroup)||(layout.maximized!=null&&layout.maximized!==layout.focusGroup))return false;
  tabKeys.delete('$body');if(tabKeys.size!==sourceKeys.size)return false;
  for(const position of layout.positions){
    if(!position||!sourceKeys.has(position.key)||positionKeys.has(position.key)||!(position.page===null||(Number.isSafeInteger(position.page)&&position.page>0))||!Number.isFinite(position.zoom)||position.zoom<0.25||position.zoom>3||!Array.isArray(position.scroll)||position.scroll.length!==2||position.scroll.some(n=>!Number.isFinite(n)||n<0||n>1e8)||(position.markdownRaw!==undefined&&typeof position.markdownRaw!=='boolean'))return false;
    positionKeys.add(position.key);
  }
  return positionKeys.size===sourceKeys.size;
}
function resumeMetadata(state){
  try{const saved=JSON.parse(localStorage.getItem('lingdoc-native-reading:'+JSON.stringify([state.projectId,state.documentTitle]))||'null');if(validReadingSnapshot(saved,state))return {resumeAvailable:true,resumeTabCount:saved.layout.sources.length,resumeGroupCount:saved.layout.groups.length};}catch{}
  return {resumeAvailable:false,resumeTabCount:0,resumeGroupCount:0};
}
const layoutKey=()=> 'lingdoc-native-layout:'+JSON.stringify([snapshot.projectId,snapshot.documentTitle]);
const readingKey=()=> 'lingdoc-native-reading:'+JSON.stringify([snapshot.projectId,snapshot.documentTitle]);
const superseded=error=>/source request superseded|source document changed/.test(error.message);
async function showReadingNotice(message){if(snapshot)await host.engine({op:'workspace.hydrate',state:{...snapshot,...resumeMetadata(snapshot),documentBound:!standalone,error:message}});}
async function showStorageNotice(message){
  if(storageNotice===message)return;
  storageNotice=message;
  await showReadingNotice(message||snapshot?.error||'');
}
async function restoreReading(){
  let raw;try{raw=localStorage.getItem(readingKey());}catch{restoreBlocked=true;await showStorageNotice('浏览器无法读取阅读布局；本次仍可阅读，历史布局未被覆盖。');return true;}
  if(!raw)return false;
  let saved;try{saved=JSON.parse(raw)}catch{restoreBlocked=true;await showReadingNotice('阅读布局记录无法读取，未自动恢复。');return true;}
  if(!validReadingSnapshot(saved,snapshot)){restoreBlocked=true;await showReadingNotice('阅读布局身份或格式不匹配，未自动恢复。');return true;}
  const generation=++readGeneration;
  restoring=true;
  const cancel=()=>{readGeneration++;restoreBlocked=true;};
  window.addEventListener('pointerdown',cancel,true);window.addEventListener('keydown',cancel,true);
  try{
    const ticket=await host.engine({op:'source.begin'});let status=ticket.source;
    for(const source of saved.layout.sources){
      if(generation!==readGeneration)return true;
      if(typeof source.id!=='string'||typeof source.version!=='string'||!source.version)throw Error('资料版本记录不完整');
      const result=await api('source',{sourceId:source.id,version:source.version});
      if(generation!==readGeneration)return true;
      status=await host.engine({op:'source.commit',expectedDocumentId:ticket.document.documentId,expectedViewEpoch:status.viewEpoch,payload:result.payload,mode:'body'});
    }
    if(generation!==readGeneration)return true;
    restoreBlocked=false;
    await host.engine({op:'source.restore',expectedDocumentId:ticket.document.documentId,expectedViewEpoch:status.viewEpoch,state:saved.layout});
    if(saved.diskVersion!==host.diskVersion)await showReadingNotice('已恢复资料阅读布局；正文磁盘版本已变化，正文内容和位置以本次打开为准。');
  }catch(error){restoreBlocked=true;if(!superseded(error)&&generation===readGeneration)await showReadingNotice('阅读布局未完整恢复：'+error.message+'；没有自动切换资料版本。');}
  finally{restoring=false;window.removeEventListener('pointerdown',cancel,true);window.removeEventListener('keydown',cancel,true);}
  return true;
}
async function importSources() {
  const input=document.createElement('input');input.type='file';input.accept='.txt,.md,.pdf,.docx';input.multiple=true;
  input.id='native-project-import';input.style.display='none';document.body.append(input);
  let failed=false,completed=0,currentName='';
  try {
    const files=await new Promise(resolve=>{input.onchange=()=>resolve([...input.files]);input.oncancel=()=>resolve([]);input.click();});
    if(files.length>10)throw Error('每次最多导入10份资料。');
    for(const file of files)if(file.size>10*1024*1024)throw Error(`“${file.name}”超过限制：每份资料最大10 MiB；本批尚未导入。`);
    for(const file of files) {
      currentName=file.name;
      const base64=await new Promise((resolve,reject)=>{const r=new FileReader();r.onload=()=>resolve(String(r.result).split(',')[1]);r.onerror=()=>reject(Error('文件读取失败'));r.readAsDataURL(file);});
      const result=await api('import',{projectId:snapshot.projectId,name:file.name,base64,key:crypto.randomUUID(),folder:'前期研究材料'});
      completed++;
      await hydrate(result.state);
    }
  } catch(error){failed=true;if(currentName)throw Error(`已导入 ${completed} 份；处理“${currentName}”时未完成：${error.message}。其余文件尚未处理，请核对资料列表后再导入。`);throw error;}
  finally {input.remove();try{await refresh()}catch(error){if(!failed)throw error;}}
}
async function openSource(sourceId,page,version,preferredMode) {
  // Native cards serialize an absent page as null; text sources have no page.
  if(page===null)page=undefined;
  if(page!==undefined&&(!Number.isSafeInteger(page)||page<1))throw Error('资料页码无效，请重新打开出处。');
  restoreBlocked=false;
  if(standalone){const q=new URLSearchParams({source:sourceId});if(version)q.set('version',version);if(page!==undefined)q.set('page',String(page));location.assign('/project?'+q);return;}
  const generation=++readGeneration;
  const ticket=await host.engine({op:'source.begin'});
  if(generation!==readGeneration)return;
  try {
    const effectiveVersion=version??snapshot?.sources.find(s=>s.id===sourceId)?.version;
    const key=JSON.stringify([sourceId,effectiveVersion]);
    const cached=effectiveVersion!==undefined&&ticket.source.tabs?.some(tab=>tab.key===key);
    if(cached){
      await host.engine({op:'source.tabs',action:'activate',key,...(page!==undefined?{page}:{}),expectedDocumentId:ticket.document.documentId,expectedViewEpoch:ticket.source.viewEpoch});
      if(generation===readGeneration)await host.engine({op:'workspace.editor'});
      return;
    }
    const result=await api('source',{sourceId,...(page?{page}:{}),...(effectiveVersion!==undefined?{version:effectiveVersion}:{})});
    if(generation!==readGeneration)return;
    // Payload, page and mode commit together. Native tab changes invalidate the ticket,
    // including changes made after this call is queued but before Rust handles it.
    await host.engine({op:'source.commit',expectedDocumentId:ticket.document.documentId,
      expectedViewEpoch:ticket.source.viewEpoch,payload:result.payload,
      ...(result.payload.op==='source.open'&&page!==undefined&&result.page?{page:result.page}:{}),
      mode:preferredMode||'keep'});
    if(generation===readGeneration)await host.engine({op:'workspace.editor'});
  } catch(error) {
    if(generation!==readGeneration||superseded(error))return;
    throw error;
  }
}
async function act(action) {
  pending++;
  try {
    if(!snapshot&&action.kind!=='standaloneCommand')throw Error('项目尚未连接；仍可新建或打开普通文稿，刷新页面可重试项目连接。');
    if(action.kind==='enterProject') {
      if(standalone)location.assign('/project');
      else {if(action.resume)await restoreReading();await host.engine({op:'workspace.editor'});}
      return;
    }
    if(action.kind==='importSources'){await importSources();return;}
    if(action.kind==='standaloneCommand') {
      // Keep the project's Session untouched. Native beforeunload protects any edits.
      const params=action.command==='file.new'?action.params||{}:{};
      sessionStorage.setItem('lingdoc-standalone-command',JSON.stringify({command:action.command,params}));
      location.assign('/?document='+ (action.command==='file.new'?'new':'open'));return;
    }
    if(action.kind==='body'){readGeneration++;await host.engine({op:'source.mode',mode:'body'});return;}
    if(action.kind==='openSource'){await openSource(action.sourceId,action.page,action.version);return;}
    await hydrate(await api('action',{...action,expectedSessionId:snapshot.currentSessionId,key:crypto.randomUUID()}));
  } catch(error) {
    if(snapshot)try {await refresh()}catch{}
    if(snapshot)await host.engine({op:'workspace.hydrate',state:{...snapshot,...resumeMetadata(snapshot),documentBound:!standalone,error:error.message,importError:action.kind==='importSources'?error.message:snapshot.importError}});
    else await host.engine({op:'workspace.hydrate',state:{documentBound:false,projectId:'',error:error.message}});
  } finally {pending--;}
}
async function restorePanelLayout(){
  try {const layout=JSON.parse(localStorage.getItem(layoutKey())||'null');if(layout)await host.engine({op:'workspace.layout',...layout});}catch{await showStorageNotice('浏览器布局记录无法读取；已使用默认面板布局。');}
}
function ensurePanelLayout(){
  if(!panelLayoutRestore)panelLayoutRestore=(async()=>{await restorePanelLayout();panelLayoutReady=true;})().catch(error=>{panelLayoutRestore=undefined;throw error;});
  return panelLayoutRestore;
}
async function connectStandaloneProject(){
  try{await refresh();window.nativeReadingReady.project='ready';}
  catch(error){
    window.nativeReadingReady.project='failed';
    try{await host.engine({op:'workspace.hydrate',state:{documentBound:false,projectId:'',error:'项目暂时无法读取：'+error.message+'；仍可新建或打开普通文稿。刷新页面可重试。'}});}catch{}
  }
}
try {
  for(let i=0;i<450;i++){host=window.wordcraftHost;if((standalone?window.STANDALONE_READY:host?.diskVersion>0)&&window.WORDCRAFT_FONT_READY?.ok)break;await delay(200)}
  if(!host||(standalone?!window.STANDALONE_READY:!host.diskVersion))throw Error('原生编辑器尚未打开。');
  const query=new URLSearchParams(location.search);
  if(standalone) {
    const intent=query.get('document');
    if(intent==='new') {
      let command;try{command=JSON.parse(sessionStorage.getItem('lingdoc-standalone-command')||'null')}catch{}
      try{sessionStorage.removeItem('lingdoc-standalone-command');}catch{}
      await host.engine({op:'standalone.new',params:command?.command==='file.new'?command.params:{}});
    } else await host.engine({op:'workspace.home',page:intent==='open'?'open':'home'});
    await host.engine({op:'workspace.hydrate',state:{documentBound:false,projectId:'',error:'正在连接项目…'}});
  } else {
    await refresh();
    if(query.has('source'))await openSource(query.get('source'),query.has('page')?Number(query.get('page')):undefined,query.get('version')||undefined,'source');
    else if(!await restoreReading()){
      const source=snapshot.sources.find(s=>s.title.endsWith('.pdf'));
      if(source)await openSource(source.id,undefined,source.version,'body');
    }
  }
  window.nativeReadingReady={phase:'ready',source:await host.engine({op:'source.status'})};
  window.nativeWorkspace={state:()=>api('state'),act,openSource,refresh};
  if(standalone){window.nativeReadingReady.project='loading';void connectStandaloneProject();}
  let tick=0;
  while(true){
    const actions=await host.engine({op:'workspace.actions'});
    for(const action of actions)void act(action);
    if(++tick%6===0&&snapshot&&panelLayoutReady){
      if(pending)try{await refresh()}catch{}
      const s=await host.engine({op:'workspace.status'});const value=JSON.stringify({resourcesOpen:s.resourcesOpen,chatOpen:s.chatOpen});
      let storageFailed=false;
      if(value!==lastLayout){try{localStorage.setItem(layoutKey(),value);lastLayout=value;}catch{storageFailed=true;await showStorageNotice('浏览器未能保存布局；本次操作仍有效，关闭页面后可能无法恢复。');}}
      if(!standalone&&!restoring&&!restoreBlocked&&!storageFailed){
        try{const layout=await host.engine({op:'source.layout'});const reading=JSON.stringify({projectId:snapshot.projectId,documentTitle:snapshot.documentTitle,diskVersion:host.diskVersion,layout});if(reading!==lastReadingLayout){localStorage.setItem(readingKey(),reading);lastReadingLayout=reading;}}
        catch(error){storageFailed=true;await showStorageNotice('浏览器未能保存阅读位置；本次操作仍有效，关闭页面后可能无法恢复。');}
      }
      if(!storageFailed&&storageNotice&&!restoreBlocked)await showStorageNotice('');
    }
    await delay(150);
  }
}catch(error){
  window.nativeReadingReady={phase:'failed',error:error.message};
  if(host&&snapshot){try{await host.engine({op:'workspace.hydrate',state:{...snapshot,...resumeMetadata(snapshot),error:error.message}})}catch{}}
  else {const message=document.createElement('div');message.setAttribute('role','alert');message.style.cssText='position:fixed;bottom:28px;left:16px;padding:10px 16px;background:#fff2e7;color:#793b13;z-index:99;font:14px system-ui';message.textContent='工作区读取未完成：'+error.message;document.body.append(message);}
}
