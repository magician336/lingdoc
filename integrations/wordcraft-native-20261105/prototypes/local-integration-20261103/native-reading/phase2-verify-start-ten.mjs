// Isolated native start-page acceptance. No model calls or document edits.
// Extreme display states are UI-only; real import/navigation use existing services.
import assert from 'node:assert/strict';
import {mkdir,writeFile,rename} from 'node:fs/promises';
import {fileURLToPath} from 'node:url';
import path from 'node:path';
import {browserClient} from '../browser-client.mjs';

const here=path.dirname(fileURLToPath(import.meta.url));
const origin=process.env.START_TEN_ORIGIN||'http://localhost:7231';
const url=new URL(origin);
assert(url.protocol==='http:'&&['localhost','127.0.0.1'].includes(url.hostname)&&url.port==='7231'&&url.pathname==='/'&&!url.search&&!url.hash,'only dedicated local 7231 root is allowed');
const stamp=new Date().toISOString().replaceAll(':','-')+'-'+process.pid;
const out=path.join(here,'artifacts','start-ten-'+stamp);
const fixture=path.join(out,'开始页导入验收-'+stamp+'.txt'),moved=fixture+'.moved';
const content='开始页导入验收 '+stamp+'\n这份资料保留在项目中，移动桌面原件后仍可以阅读。\n';
const result={passed:false,origin:url.origin,scope:'native start page; generated import only; extreme states are UI-only',checks:[],screenshots:[],modelRequests:0,fixture,moved};
await mkdir(out,{recursive:true});await writeFile(fixture,content);let browser;
try{
 browser=await browserClient(path.join(out,'edge'));
 const {send,evaluate:e,wait}=browser;
 let viewport={width:1600,height:1000};
 const engine=request=>e(`wordcraftHost.engine(${JSON.stringify(request)})`);
 const ws=()=>engine({op:'workspace.status'}),state=()=>e('nativeWorkspace.state()'),body=()=>engine({op:'status'});
 const settle=()=>e('new Promise(r=>setTimeout(r,400))');
 const ordinaryReady=()=>wait("window.nativeReadingReady?.phase==='ready'&&!!window.nativeWorkspace",120000);
 const ready=async()=>{await ordinaryReady();await wait("!window.WORDCRAFT_STANDALONE||window.nativeReadingReady?.project==='ready'",120000);};
 const pass=label=>{result.checks.push(label);console.log('PASS '+label)};
 const visible=(r,label)=>{assert(r&&r.width>0&&r.height>0,label+' exists');assert(r.x>=0&&r.y>=0&&r.x+r.width<=viewport.width+1&&r.y+r.height<=viewport.height+1,label+' fits visible viewport');};
 const click=async r=>{
  visible(r,'native click target');const x=r.x+r.width/2,y=r.y+r.height/2;
  await send('Input.dispatchMouseEvent',{type:'mouseMoved',x,y,buttons:0});
  await send('Input.dispatchMouseEvent',{type:'mousePressed',x,y,button:'left',buttons:1,clickCount:1});
  await send('Input.dispatchMouseEvent',{type:'mouseReleased',x,y,button:'left',buttons:0,clickCount:1});
  try{await settle()}catch(error){if(!/Execution context was destroyed|Inspected target navigated or closed/.test(error.message))throw error;}
 };
 const rect=async key=>click((await ws())[key]);
 const shot=async name=>{await settle();const file=path.join(out,name+'.png');await browser.screenshot(file);result.screenshots.push(file);};
 const resize=async(width,height)=>{viewport={width,height};await send('Emulation.setDeviceMetricsOverride',{...viewport,deviceScaleFactor:1,mobile:false});await settle();};
 const navigate=async pathname=>{await send('Page.navigate',{url:url.origin+pathname});await ready();await settle();};
 const projectDetails=async()=>{
  await rect('projectNavRect');await wait("!!(await wordcraftHost.engine({op:'workspace.status'})).projectCardRect");
  await rect('projectCardRect');await wait("!!(await wordcraftHost.engine({op:'workspace.status'})).projectSearchRect");await settle();
 };
 const navigateByRect=async key=>{const target=(await ws())[key];await e("(window.nativeReadingReady={phase:'navigating'})");await click(target);await ready();await settle();};
 const sameHistory=async()=>assert.deepEqual((await state()).messages,result.initial.messages,'no conversation mutation');
 const restoreDisplay=async()=>{await e('nativeWorkspace.refresh()');await settle();};

 // Browser-local failure injection only: hold the project's state response, then
 // release it as HTTP 503. The native standalone document must be ready first.
 const interception=await send('Page.addScriptToEvaluateOnNewDocument',{source:`
  (()=>{
   const originalFetch=window.fetch.bind(window);
   window.__startProjectFault={requests:0,released:false};
   window.fetch=(input,init)=>{
    const requestUrl=new URL(typeof input==='string'||input instanceof URL?String(input):input.url,location.href);
    if(requestUrl.origin===location.origin&&requestUrl.pathname==='/native-reading/workspace/state'){
     window.__startProjectFault.requests++;
     return new Promise(resolve=>{window.__releaseStartProject=()=>{window.__startProjectFault.released=true;resolve(new Response(JSON.stringify({error:'验收注入：项目暂不可用'}),{status:503,headers:{'Content-Type':'application/json'}}));};});
    }
    return originalFetch(input,init);
   };
  })();
 `});
 await send('Page.navigate',{url:url.origin+'/?document=new'});await ordinaryReady();
 await wait("window.__startProjectFault?.requests===1");
 assert.equal(await e('window.nativeReadingReady.project'),'loading');
 assert.equal(await e('window.STANDALONE_READY&&window.WORDCRAFT_STANDALONE&&wordcraftHost.standalone'),true);
 assert.equal((await ws()).documentBound,false);assert((await body()).documentId>0,'native new document exists before project response');
 await shot('00-project-held-native-new');pass('held project-state response does not block the real ordinary new-document entry');
 await e('window.__releaseStartProject()');await wait("window.nativeReadingReady.project==='failed'");
 assert.equal(await e('window.nativeReadingReady.phase'),'ready');assert.equal((await ws()).documentBound,false);
 // Open the existing native home page for inspection; no replacement UI is injected.
 await engine({op:'workspace.home',page:'home'});await settle();await rect('projectNavRect');await settle();
 const disconnected=await ws();assert.equal(disconnected.projectCardRect,null);assert.equal(disconnected.projectDocumentRect,null);assert.equal(disconnected.importButtonRect,null);
 assert.match(disconnected.error,/项目暂时无法读取/);result.projectFailureInjection=await e('window.__startProjectFault');
 await shot('00-project-503-native-start');pass('HTTP 503 project-state response leaves standalone ready and native project page shows failure without stale cards');
 await send('Page.removeScriptToEvaluateOnNewDocument',{identifier:interception.identifier});
 await navigate('/');result.initial=await state();result.initialBody=await body();
 assert.equal(result.initial.busy,false,'refuse an in-flight conversation');
 assert.equal(await e('window.WORDCRAFT_STANDALONE===true&&wordcraftHost.standalone===true'),true);
 assert.equal((await ws()).documentBound,false);assert.equal(await e("document.querySelectorAll('iframe').length"),0);
 await shot('01-native-home');pass('native root remains standalone with no forced project or outer workbench');
 // These are the retained native sidebar slots, not synthetic page commands.
 await click({x:80,y:112,width:20,height:20});await shot('02-native-new');
 await click({x:80,y:154,width:20,height:20});await shot('03-native-open');
 assert.deepEqual(await body(),result.initialBody);assert.equal((await ws()).documentBound,false);
 pass('native New/Open navigation preserves the unbound document');
 await projectDetails();await shot('04-project-details');
 assert.equal((await ws()).projectSelected,true);assert.equal((await ws()).documentBound,false);
 pass('project list opens project details before binding any manuscript');

 for(const [width,height] of [[1600,1000],[1300,900],[900,700]]){
  await resize(width,height);const ui=await ws();
  for(const key of ['projectBackRect','projectSearchRect','projectSearchClearRect','projectDocumentRect','importButtonRect'])visible(ui[key],key);
  await shot('05-project-'+width+'x'+height);pass('start-page core controls fit '+width+'x'+height);
 }
 await rect('projectBackRect');await wait("!!(await wordcraftHost.engine({op:'workspace.status'})).projectCardRect");
 assert.equal((await ws()).projectSelected,false);assert.equal((await ws()).projectDocumentRect,null);
 await rect('projectCardRect');await wait("!!(await wordcraftHost.engine({op:'workspace.status'})).projectSearchRect");
 pass('back to project list clears detail hit targets and reopens the same project');
 await rect('projectSearchRect');const query='__start_ten_no_match__';await send('Input.insertText',{text:query});
 await wait(`(await wordcraftHost.engine({op:'workspace.status'})).projectSearch===${JSON.stringify(query)}`);await settle();
 let ui=await ws();assert.equal(Object.keys(ui.sourceRowRects).length,0);assert.equal(ui.projectDocumentRect,null);
 await shot('06-search-empty');pass('actual typing filters both manuscript and sources without stale clickable results');
 await rect('projectSearchClearRect');await wait("(await wordcraftHost.engine({op:'workspace.status'})).projectSearch===''");
 await wait("Object.keys((await wordcraftHost.engine({op:'workspace.status'})).sourceRowRects).length>0");
 pass('clear search restores real manuscript and source entries');

 // No backend mutation: display stress fixtures are restored before any business action.
 const base=await state();
 const longTitle='开始页长名称资料'.repeat(30);
 await engine({op:'workspace.hydrate',state:{...base,documentBound:false,projectTitle:longTitle,documentTitle:longTitle+'.docx',sources:base.sources.map(s=>({...s,title:longTitle+(s.title.endsWith('.pdf')?'.pdf':'.txt')})),resumeAvailable:true,resumeTabCount:2,resumeGroupCount:2}});
 await settle();ui=await ws();
 for(const key of ['projectBackRect','resumeRect','projectSearchRect','projectDocumentRect','importButtonRect'])visible(ui[key],key);
 for(const r of Object.values(ui.sourceRowRects))assert(r.x+r.width<=viewport.width+1,'long source title does not enlarge page');
 await shot('07-long-names-ui-only');pass('UI-only long names and resume summary retain usable 900x700 controls');
 await engine({op:'workspace.hydrate',state:{...base,documentBound:false,documentTitle:'',sources:[],imports:[],resumeAvailable:false}});await settle();ui=await ws();
 assert.equal(ui.projectDocumentRect,null);assert.equal(Object.keys(ui.sourceRowRects).length,0);visible(ui.importButtonRect,'empty project import');
 await shot('08-empty-project-ui-only');pass('UI-only empty project keeps import available and has no fake manuscript target');
 await engine({op:'workspace.hydrate',state:{...base,documentBound:false,importError:'UI-only import failure details. '.repeat(80),imports:[{name:longTitle+'.pdf',status:'failed',error:'UI-only failure details. '.repeat(80)}],resumeAvailable:false}});await settle();ui=await ws();
 visible(ui.importButtonRect,'failed import retry entry');visible(ui.projectSearchRect,'search after failure');
 await shot('09-import-failure-ui-only');pass('UI-only long import failure remains collapsed and does not hide basic controls');
 await engine({op:'workspace.hydrate',state:{...base,documentBound:false,projectId:'',error:'UI-only project connection unavailable'}});await settle();ui=await ws();
 for(const key of ['projectCardRect','projectDocumentRect','projectSearchRect','importButtonRect'])assert.equal(ui[key],null,'disconnected '+key+' cleared');
 assert.equal(Object.keys(ui.sourceRowRects).length,0);visible(ui.projectNavRect,'native project sidebar remains available');
 await shot('09-disconnected-ui-only');pass('UI-only disconnected project clears stale actions while native sidebar remains available');
 await restoreDisplay();await sameHistory();assert.deepEqual(await body(),result.initialBody);
 pass('UI-only stress states leave persisted project conversation and ordinary document unchanged');

 await resize(1600,1000);await send('Page.setInterceptFileChooserDialog',{enabled:true});
 const beforeCancel=await state();await rect('importButtonRect');await wait("!!document.querySelector('#native-project-import')");
 await e("document.querySelector('#native-project-import').dispatchEvent(new Event('cancel'))");
 await wait("!document.querySelector('#native-project-import')");await settle();
 assert.deepEqual((await state()).sources,beforeCancel.sources);assert.equal((await ws()).importBusy,false);
 pass('file chooser cancel event releases import state without adding a source');
 await rect('importButtonRect');await wait("!!document.querySelector('#native-project-import')");
 const {root}=await send('DOM.getDocument');const {nodeId}=await send('DOM.querySelector',{nodeId:root.nodeId,selector:'#native-project-import'});
 await send('DOM.setFileInputFiles',{nodeId,files:[fixture]});
 await wait(`(await nativeWorkspace.state()).sources.some(s=>s.title===${JSON.stringify(path.basename(fixture))})`,120000);
 await wait("!document.querySelector('#native-project-import')");await settle();
 result.imported=(await state()).sources.find(s=>s.title===path.basename(fixture));
 assert.equal((await state()).sources.length,result.initial.sources.length+1);assert.equal((await ws()).importBusy,false);await sameHistory();
 await shot('10-real-imported');pass('native import selects a real generated file and adds one source with no model or history mutation');
 // Move only the uniquely generated fixture under this run's artifact directory.
 assert.equal(path.dirname(fixture),out);assert.equal(path.dirname(moved),out);await rename(fixture,moved);
 const openedId=result.imported.id,openedVersion=result.imported.version;
 await navigateByRect('projectDocumentRect');assert.equal((await ws()).documentBound,true);assert.equal(await e('wordcraftHost.diskVersion>0'),true);
 result.projectBody=await body();pass('project manuscript entry opens the bound native document');
 ui=await ws();assert(ui.sourceRowRects[openedId],'imported source has a native resource row');await click(ui.sourceRowRects[openedId]);
 await wait(`(await wordcraftHost.engine({op:'source.status'})).sourceId===${JSON.stringify(openedId)}`,30000);
 assert.equal((await engine({op:'source.status'})).version,openedVersion);
 const copy=await e(`(async()=>{const r=await fetch('/native-reading/workspace/source',{method:'POST',headers:{'Content-Type':'application/json','X-Workspace-Token':window.NATIVE_WORKSPACE_TOKEN},body:JSON.stringify({sourceId:${JSON.stringify(openedId)},version:${JSON.stringify(openedVersion)}})});if(!r.ok)throw Error('source copy failed '+r.status);return await r.json()})()`);
 assert.equal(copy.payload.text,content);assert.deepEqual(await body(),result.projectBody);
 await shot('11-retained-source-copy');pass('actual source row opens retained exact-version original after generated desktop file moved; body unchanged');
 await e('new Promise(r=>setTimeout(r,1800))');
 await navigate('/');await projectDetails();await wait("!!(await wordcraftHost.engine({op:'workspace.status'})).resumeRect");
 await shot('12-continue-available');await navigateByRect('resumeRect');
 await wait(`(await wordcraftHost.engine({op:'source.status'})).sourceId===${JSON.stringify(openedId)}`,30000);
 assert.equal((await engine({op:'source.status'})).version,openedVersion);await sameHistory();
 await shot('13-continued-reading');pass('continue previous work restores the real imported source and exact version');
 await e("(window.nativeReadingReady={phase:'reloading'})");await send('Page.reload',{ignoreCache:true});await ready();
 assert((await state()).sources.some(s=>s.id===openedId&&s.version===openedVersion));await sameHistory();
 await shot('14-reloaded');pass('refresh retains imported source and unchanged conversation');
 result.final={workspace:await ws(),source:await engine({op:'source.status'}),body:await body()};
 result.errors=browser.errors;assert.equal(browser.errors.length,0);result.passed=true;
}catch(error){
 result.error=error.stack;process.exitCode=1;
 if(browser)try{result.failure=await browser.evaluate("({url:location.href,ready:window.nativeReadingReady,ui:window.wordcraftHost?await wordcraftHost.engine({op:'workspace.status'}):null})");await browser.screenshot(path.join(out,'failure.png'));}catch(capture){result.captureError=capture.message;}
}finally{
 if(browser)try{await browser.close()}catch(error){result.cleanupError=error.message;result.passed=false;process.exitCode=1;}
 await writeFile(path.join(out,'verification.json'),JSON.stringify(result,null,2));
 console.log(JSON.stringify({out,passed:result.passed,checks:result.checks.length,error:result.error}));
}
