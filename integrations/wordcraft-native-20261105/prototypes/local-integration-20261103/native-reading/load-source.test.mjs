import test from 'node:test';
import assert from 'node:assert/strict';
import vm from 'node:vm';
import {readFile} from 'node:fs/promises';
const script=await readFile(new URL('./load-source.js',import.meta.url),'utf8');
const state={stateRevision:1,projectId:'p',documentTitle:'Draft.docx',sources:[],currentSessionId:'s',messages:[]};
const key='lingdoc-native-reading:'+JSON.stringify(['p','Draft.docx']);
async function boot({saved,tabs=[],sourceFetch,query='',storageReadError=false,storageWriteError=false,afterEngine,files=[],stateFetch,standalone=false,importFetch,panelLayout}={}){
 const calls=[],requests=[],storage=new Map(saved?[[key,JSON.stringify(saved)]]:[]),listeners=new Map();
 if(panelLayout)storage.set('lingdoc-native-layout:'+JSON.stringify(['p','Draft.docx']),JSON.stringify(panelLayout));
 let epoch=1;const timers=[];
 const status=()=>({viewEpoch:epoch,mode:'body',tabs});
 const handleEngine=async r=>{
  calls.push(r);
  if(r.op==='source.begin'){epoch++;return {document:{documentId:42},source:status()};}
  if(r.op==='source.commit'){assert.equal(r.expectedViewEpoch,epoch);epoch++;tabs.push({key:JSON.stringify([r.payload.sourceId,r.payload.version]),id:r.payload.sourceId,version:r.payload.version});return status();}
  if(r.op==='source.tabs'){assert.equal(r.expectedViewEpoch,epoch);epoch++;return status();}
  if(r.op==='source.restore'){assert.equal(r.expectedDocumentId,42);assert.equal(r.expectedViewEpoch,epoch);epoch++;return status();}
  if(r.op==='source.status')return status();
  if(r.op==='workspace.actions')return [];
  return {};
 };
 const engine=async r=>{const value=await handleEngine(r);return afterEngine?afterEngine(r,value):value;};
 const picker={style:{},files,removed:false,click(){this.onchange();},remove(){this.removed=true;}};
 const ctx={console,URLSearchParams,AbortSignal,JSON,Number,Array,Error,crypto:{randomUUID:()=> 'operation-test'},location:{search:query,assign:url=>calls.push({navigation:url})},sessionStorage:{getItem:()=>null,setItem(){},removeItem(){}},FileReader:class{readAsDataURL(){this.result='data:text/plain;base64,dGVzdA==';this.onload();}},localStorage:{getItem:k=>{if(storageReadError)throw Error('SecurityError');return storage.get(k)||null;},setItem:(k,v)=>{if(storageWriteError)throw Error('QuotaExceededError');storage.set(k,v);}},document:{createElement:()=>picker,body:{append(){}}},setTimeout:fn=>{if(ctx.window.nativeReadingReady?.phase==='loading')setImmediate(fn);else timers.push(fn);},fetch:async(url,opts)=>{
  const body=opts.body&&JSON.parse(opts.body);requests.push({url,body});
  if(url.endsWith('/state'))return stateFetch?stateFetch():{ok:true,json:async()=>state};
  if(url.endsWith('/source'))return sourceFetch?sourceFetch(body,listeners):{ok:true,json:async()=>({payload:{op:'source.openText',sourceId:body.sourceId,version:body.version,title:'Source',text:'text'},page:1})};
  if(url.endsWith('/import'))return importFetch?importFetch(body):{ok:true,json:async()=>({state,record:{status:'ready'}})};
  throw Error('Unexpected API');
 }};
 ctx.window={WORDCRAFT_STANDALONE:standalone,STANDALONE_READY:standalone,wordcraftHost:{engine,diskVersion:3},WORDCRAFT_FONT_READY:{ok:true},addEventListener:(n,f)=>listeners.set(n,f),removeEventListener:n=>listeners.delete(n)};
 vm.runInNewContext('(async()=>{'+script+'})()',ctx);
 for(let i=0;i<100&&!ctx.window.nativeWorkspace;i++)await new Promise(r=>setTimeout(r,2));
 assert.equal(ctx.window.nativeReadingReady.phase,'ready');
 return {ctx,calls,requests,storage,picker,runTicks:async(n=6)=>{for(let i=0;i<n;i++){timers.shift()?.();await new Promise(r=>setTimeout(r,2));}}};
}
const saved=()=>({projectId:'p',documentTitle:'Draft.docx',diskVersion:3,layout:{sources:[{id:'old-source',version:'v1'}],groups:[{id:1,tabs:['$body','["old-source","v1"]'],active:'$body',weight:1}],focusGroup:1,positions:[{key:'["old-source","v1"]',page:null,zoom:1,scroll:[0,0]}]}});
test('cached exact-version source activates without HTTP and forwards citation page',async()=>{
 const f=await boot({tabs:[{key:JSON.stringify(['cached','v2']),id:'cached',version:'v2'}]});
 await f.ctx.window.nativeWorkspace.openSource('cached',2,'v2');
 assert.equal(f.requests.filter(r=>r.url.endsWith('/source')).length,0);
 assert.deepEqual(JSON.parse(JSON.stringify(f.calls.find(r=>r.op==='source.tabs'))),{op:'source.tabs',action:'activate',key:'["cached","v2"]',page:2,expectedDocumentId:42,expectedViewEpoch:2});
});
test('reload loads saved versions then restores with current document and epoch, never saved runtime ID',async()=>{
 const f=await boot({saved:saved()});
 assert.deepEqual(JSON.parse(JSON.stringify(f.requests.find(r=>r.url.endsWith('/source')).body)),{sourceId:'old-source',version:'v1'});
 const restore=f.calls.find(r=>r.op==='source.restore');assert.equal(restore.expectedDocumentId,42);assert.equal(restore.expectedViewEpoch,3);
 assert.equal(f.calls.find(r=>r.op==='workspace.hydrate').state.resumeAvailable,true);
});
test('unavailable old version is reported and never silently replaced',async()=>{
 const original=saved();const f=await boot({saved:original,sourceFetch:async()=>({ok:false,json:async()=>({error:'版本不可用'})})});
 assert.equal(f.calls.some(r=>r.op==='source.restore'),false);assert.equal(f.requests.filter(r=>r.url.endsWith('/source')).length,1);
 assert.match(f.calls.filter(r=>r.op==='workspace.hydrate').at(-1).state.error,/没有自动切换/);
 assert.equal(f.storage.get(key),JSON.stringify(original));
});
test('user interaction during restore cancels late commit and preserves saved layout',async()=>{
 const f=await boot({saved:saved(),sourceFetch:async(body,listeners)=>{listeners.get('pointerdown')();return {ok:true,json:async()=>({payload:{op:'source.openText',sourceId:body.sourceId,version:body.version,text:'text'}})};}});
 assert.equal(f.calls.some(r=>r.op==='source.commit'||r.op==='source.restore'),false);
});


test('new resources keep existing groups unless caller explicitly requests a view mode',async()=>{
 const f=await boot();
 await f.ctx.window.nativeWorkspace.openSource('new-source',undefined,'v1');
 assert.equal(f.calls.find(r=>r.op==='source.commit').mode,'keep');
 await f.ctx.window.nativeWorkspace.openSource('new-source-2',undefined,'v2','body');
 assert.equal(f.calls.filter(r=>r.op==='source.commit').at(-1).mode,'body');
});

test('blocked browser storage does not prevent the workspace becoming ready',async()=>{
 const f=await boot({storageReadError:true});
 assert.equal(f.ctx.window.nativeReadingReady.phase,'ready');
 assert.match(f.calls.filter(r=>r.op==='workspace.hydrate').at(-1).state.error,/无法读取阅读布局/);
 assert.equal(f.calls.some(r=>r.op==='source.restore'),false);
});

test('failed layout persistence is visible once and is not falsely acknowledged as saved',async()=>{
 const f=await boot({storageWriteError:true});
 await f.runTicks(18);
 const notices=f.calls.filter(r=>r.op==='workspace.hydrate'&&r.state.error?.includes('未能保存布局'));
 assert.equal(notices.length,1);
 assert.equal(f.storage.size,0);
 await f.ctx.window.nativeWorkspace.refresh();
 assert.match(f.calls.filter(r=>r.op==='workspace.hydrate').at(-1).state.error,/未能保存布局/);
});

test('native text source card with null page reopens cached original without a page command',async()=>{
 const f=await boot({tabs:[{key:JSON.stringify(['text','1']),id:'text',version:'1'}]});
 await f.ctx.window.nativeWorkspace.openSource('text',null,'1');
 const action=f.calls.find(r=>r.op==='source.tabs');
 assert.equal(Object.hasOwn(action,'page'),false);
 assert.equal(f.requests.filter(r=>r.url.endsWith('/source')).length,0);
});

test('invalid citation pages are rejected equally before cached or uncached navigation',async()=>{
 const f=await boot({tabs:[{key:JSON.stringify(['cached','1']),id:'cached',version:'1'}]});
 const before=f.calls.length;
 for(const id of ['cached','new'])for(const page of [0,-1,1.5,Number.NaN,Number.MAX_SAFE_INTEGER+1]){
  await assert.rejects(f.ctx.window.nativeWorkspace.openSource(id,page,'1'),/页码无效/);
 }
 assert.equal(f.calls.length,before,'invalid pages must not invalidate an existing native reading ticket');
 assert.equal(f.requests.filter(r=>r.url.endsWith('/source')).length,0);
});

test('late source commit completion cannot re-enter editor after a newer reading action',async()=>{
 let release,reached;
 const paused=new Promise(resolve=>{release=resolve;});
 const committed=new Promise(resolve=>{reached=resolve;});
 const f=await boot({afterEngine:async(request,result)=>{
  if(request.op==='source.commit'&&request.payload.sourceId==='first'){reached();await paused;}
  return result;
 }});
 const first=f.ctx.window.nativeWorkspace.openSource('first',undefined,'1');
 await committed;
 await f.ctx.window.nativeWorkspace.openSource('second',undefined,'1');
 assert.equal(f.calls.filter(r=>r.op==='workspace.editor').length,1);
 release();await first;
 assert.equal(f.calls.filter(r=>r.op==='workspace.editor').length,1,'superseded completion must not emit an extra editor navigation');
});

test('import failure remains actionable when the cleanup refresh also fails',async()=>{
 let stateReads=0;
 const f=await boot({files:[{name:'large.pdf',size:11*1024*1024}],stateFetch:async()=>++stateReads===1?{ok:true,json:async()=>state}:{ok:false,json:async()=>({error:'刷新网络失败'})}});
 await f.ctx.window.nativeWorkspace.act({kind:'importSources'});
 const shown=f.calls.filter(r=>r.op==='workspace.hydrate').at(-1).state;
 assert.match(shown.importError,/每份资料最大10 MiB/);
 assert.equal(shown.error,shown.importError);
 assert.equal(f.picker.removed,true);
 assert.equal(f.requests.filter(r=>r.url.endsWith('/import')).length,0);
});

test('ordinary new document is ready before a delayed project connection, with no empty-project writes',async()=>{
 let release;const delayed=new Promise(resolve=>{release=resolve;});
 const f=await boot({standalone:true,query:'?document=new',stateFetch:()=>delayed});
 assert(f.calls.some(r=>r.op==='standalone.new'));
 assert.equal(f.ctx.window.nativeReadingReady.phase,'ready');
 assert.equal(f.ctx.window.nativeReadingReady.project,'loading');
 await f.runTicks(12);assert.equal(f.storage.size,0);
 await f.ctx.window.nativeWorkspace.act({kind:'importSources'});
 assert.equal(f.requests.filter(r=>r.url.endsWith('/import')).length,0);
 assert.match(f.calls.filter(r=>r.op==='workspace.hydrate').at(-1).state.error,/项目尚未连接/);
 release({ok:false,json:async()=>({error:'项目服务不可用'})});await new Promise(r=>setTimeout(r,5));
 assert.equal(f.ctx.window.nativeReadingReady.phase,'ready');
 assert.equal(f.ctx.window.nativeReadingReady.project,'failed');
 const failed=f.calls.filter(r=>r.op==='workspace.hydrate').at(-1).state;
 assert.equal(failed.projectId,'');assert.equal(failed.documentBound,false);assert.match(failed.error,/仍可新建或打开普通文稿/);
});

test('ordinary Home and Open remain usable when project state cannot be fetched',async()=>{
 for(const [query,page] of [['','home'],['?document=open','open']]){
  const f=await boot({standalone:true,query,stateFetch:async()=>({ok:false,json:async()=>({error:'项目离线'})})});
  await new Promise(r=>setTimeout(r,5));
  assert(f.calls.some(r=>r.op==='workspace.home'&&r.page===page));
  assert.equal(f.ctx.window.nativeReadingReady.phase,'ready');
  assert.equal(f.ctx.window.nativeReadingReady.project,'failed');
 }
});

test('invalid saved layouts are neither advertised nor fetched or restored',async()=>{
 const mutations=[
  s=>s.layout.sources[0].version='',
  s=>s.layout.groups=[],
  s=>s.layout.groups[0].tabs.push('$body'),
  s=>s.layout.focusGroup=99,
  s=>s.layout.positions[0].zoom=-1,
  s=>s.layout.positions[0].scroll=[0,Infinity],
  s=>s.layout.positions=[],
  s=>s.layout.groups[0].weight=1e-300,
  s=>s.layout.sources.push({...s.layout.sources[0]}),
  s=>s.layout.sources[0]=null,
  s=>s.layout.groups[0]=null,
  s=>s.layout.positions[0]=null,
 ];
 for(const mutate of mutations){
  const invalid=saved();mutate(invalid);const f=await boot({saved:invalid});
  assert.equal(f.calls.find(r=>r.op==='workspace.hydrate').state.resumeAvailable,false);
  assert.equal(f.requests.filter(r=>r.url.endsWith('/source')).length,0);
  assert.equal(f.calls.some(r=>r.op==='source.restore'),false);
  assert.equal(f.storage.get(key),JSON.stringify(invalid));
 }
});

test('valid body-only layout still offers continue work and restores without source requests',async()=>{
 const bodyOnly=saved();bodyOnly.layout.sources=[];bodyOnly.layout.positions=[];bodyOnly.layout.groups[0].tabs=['$body'];
 const f=await boot({saved:bodyOnly});
 assert.equal(f.calls.find(r=>r.op==='workspace.hydrate').state.resumeAvailable,true);
 assert.equal(f.requests.filter(r=>r.url.endsWith('/source')).length,0);
 assert(f.calls.some(r=>r.op==='source.restore'));
});

test('batch size validation names the invalid file before uploading any earlier valid file',async()=>{
 const f=await boot({files:[{name:'valid.txt',size:4},{name:'oversize.pdf',size:11*1024*1024}]});
 await f.ctx.window.nativeWorkspace.act({kind:'importSources'});
 assert.equal(f.requests.filter(r=>r.url.endsWith('/import')).length,0);
 const error=f.calls.filter(r=>r.op==='workspace.hydrate').at(-1).state.importError;
 assert.match(error,/oversize.pdf/);assert.match(error,/本批尚未导入/);
});

test('mid-batch import failure states completed count, failed filename and remaining unprocessed files',async()=>{
 const f=await boot({files:['first.txt','bad.pdf','later.txt'].map(name=>({name,size:4})),importFetch:async body=>body.name==='bad.pdf'?{ok:false,json:async()=>({error:'PDF无法解析'})}:{ok:true,json:async()=>({state,record:{status:'ready'}})}});
 await f.ctx.window.nativeWorkspace.act({kind:'importSources'});
 assert.deepEqual(f.requests.filter(r=>r.url.endsWith('/import')).map(r=>r.body.name),['first.txt','bad.pdf']);
 const error=f.calls.filter(r=>r.op==='workspace.hydrate').at(-1).state.importError;
 assert.match(error,/已导入 1 份/);assert.match(error,/bad.pdf/);assert.match(error,/其余文件尚未处理/);
 assert.equal(f.picker.removed,true);
});

test('background project hydration cannot overwrite panel layout before it is restored',async()=>{
 let release;const paused=new Promise(resolve=>{release=resolve;});
 const layout={resourcesOpen:false,chatOpen:true};
 const f=await boot({standalone:true,panelLayout:layout,afterEngine:async(request,result)=>{
  if(request.op==='workspace.hydrate'&&request.state.projectId==='p')await paused;
  return result;
 }});
 await f.runTicks(6);
 const layoutKey='lingdoc-native-layout:'+JSON.stringify(['p','Draft.docx']);
 assert.equal(f.storage.get(layoutKey),JSON.stringify(layout),'persisting defaults must wait until saved panel layout has been applied');
 release();await new Promise(r=>setTimeout(r,5));
 assert(f.calls.some(r=>r.op==='workspace.layout'&&r.resourcesOpen===false&&r.chatOpen===true));
 assert.equal(f.ctx.window.nativeReadingReady.project,'ready');
});

test('manual reconnect restores panel layout before persistence and later refresh does not reapply it',async()=>{
 let reads=0,release;const paused=new Promise(resolve=>{release=resolve;});const layout={resourcesOpen:false,chatOpen:true};
 const f=await boot({standalone:true,panelLayout:layout,stateFetch:async()=>++reads===1?{ok:false,json:async()=>({error:'离线'})}:{ok:true,json:async()=>state},afterEngine:async(request,result)=>{
  if(request.op==='workspace.hydrate'&&request.state.projectId==='p')await paused;
  return result;
 }});
 await new Promise(r=>setTimeout(r,5));assert.equal(f.ctx.window.nativeReadingReady.project,'failed');
 const reconnect=f.ctx.window.nativeWorkspace.refresh();await new Promise(r=>setTimeout(r,5));await f.runTicks(6);
 assert.equal(f.storage.get('lingdoc-native-layout:'+JSON.stringify(['p','Draft.docx'])),JSON.stringify(layout));
 release();await reconnect;assert.equal(f.ctx.window.nativeReadingReady.project,'ready');
 assert.equal(f.calls.filter(r=>r.op==='workspace.layout').length,1);
 await f.ctx.window.nativeWorkspace.refresh();
 assert.equal(f.calls.filter(r=>r.op==='workspace.layout').length,1,'data refresh must not overwrite a layout the user subsequently changed');
});
