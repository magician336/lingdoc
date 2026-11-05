// One-time clean user trial preparation. No model request or body mutation.
import assert from 'node:assert/strict';
import {mkdir,writeFile,readFile} from 'node:fs/promises';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {browserClient} from '../browser-client.mjs';
const here=path.dirname(fileURLToPath(import.meta.url)),out=path.join(here,'artifacts','r4-user-'+Date.now());
const runtime=JSON.parse(await readFile(path.join(here,'runtime','multi-r4-user','trial.json'),'utf8'));assert.equal(runtime.port,7219);
await mkdir(out,{recursive:true});let browser;const result={passed:false,origin:'http://localhost:7219',checks:[],modelRequests:0};
const text=['【虚构演示资料：社区数字服务研究访谈提纲】','本提纲仅用于产品演示，不代表真实受访者或研究结论。','研究目的：了解居民使用社区数字服务时的实际困难。','问题一：最近一次办理社区事务时，使用了哪些线上或线下渠道？','问题二：注册、身份验证、查询进度分别遇到什么障碍？','问题三：遇到困难时，会向社区人员、家属还是邻居求助？','问题四：哪些事项希望保留线下窗口，为什么？','记录原则：区分受访者原话、访谈者观察和待核实推断。','伦理说明：正式研究需另行取得知情同意，不记录无关身份信息。'].join('\n');
try{
 browser=await browserClient(path.join(out,'edge'));const {evaluate:e,send,wait}=browser;
 const engine=r=>e(`await wordcraftHost.engine(${JSON.stringify(r)})`);
 const settle=()=>e('new Promise(r=>setTimeout(r,400))');
 await send('Page.navigate',{url:result.origin+'/project'});await wait(`window.nativeReadingReady?.phase==='ready'`,120000);
 const before=await engine({op:'status'});let state=await e('await nativeWorkspace.state()');
 let material=state.sources.find(s=>s.title==='访谈提纲.txt');
 if(!material){
  const record=state.imports?.find(i=>i.name==='访谈提纲.txt');assert(!record,'An earlier import exists: inspect its state explicitly, do not repeat');
  const input={projectId:state.projectId,name:'访谈提纲.txt',base64:Buffer.from(text).toString('base64'),key:'r4-user-interview-outline-v1',folder:'前期研究材料'};
  const response=await e(`(async()=>{const r=await fetch('/native-reading/workspace/import',{method:'POST',headers:{'Content-Type':'application/json','X-Workspace-Token':NATIVE_WORKSPACE_TOKEN},body:JSON.stringify(${JSON.stringify(input)})});return {status:r.status,value:await r.json()}})()`);
  assert.equal(response.status,200,JSON.stringify(response.value));assert.equal(response.value.record.status,'ready');material={id:response.value.record.sourceId,version:response.value.record.version};
  await e('await nativeWorkspace.refresh()');state=await e('await nativeWorkspace.state()');result.checks.push('one fictional interview outline imported');
 }else result.checks.push('existing outline reused without duplicate import');
 const pdf=state.sources.find(s=>s.title==='研究说明.pdf'),txt=state.sources.find(s=>s.title.startsWith('社区联系记录'));assert(pdf&&txt);
 for(const s of [pdf,txt,material])await e(`await nativeWorkspace.openSource(${JSON.stringify(s.id)},undefined,${JSON.stringify(s.version)})`);
 const key=s=>JSON.stringify([s.id,s.version]);
 const loaded=await engine({op:'source.layout'});
 const positions=loaded.positions;
 const layout={...loaded,groups:[{id:1,tabs:['$body'],active:'$body',weight:1.35},{id:2,tabs:[key(pdf),key(material)],active:key(pdf),weight:1},{id:3,tabs:[key(txt)],active:key(txt),weight:1}],focusGroup:1,maximized:null,positions};
 const ticket=await engine({op:'source.begin'});await engine({op:'source.restore',state:layout,expectedDocumentId:ticket.document.documentId,expectedViewEpoch:ticket.source.viewEpoch});
 await engine({op:'workspace.layout',resourcesOpen:false,chatOpen:false});await settle();
 assert.equal((await engine({op:'source.layout'})).sources.length,3);assert.deepEqual(await engine({op:'status'}),before);
 await browser.screenshot(path.join(out,'body-three-groups-1600.png'));
 await send('Emulation.setDeviceMetricsOverride',{width:1300,height:1000,deviceScaleFactor:1,mobile:false});await settle();await browser.screenshot(path.join(out,'body-three-groups-1300.png'));
 result.checks.push('body plus PDF plus text in three groups with fourth tab retained','body identity revision and dirty unchanged','1600 and 1300 screenshots');
 result.entry=result.origin+'/project?source='+encodeURIComponent(pdf.id)+'&version='+encodeURIComponent(pdf.version);
 result.steps=['打开入口先核对PDF原文；点击“并排阅读”显示正文与资料。','打开“项目资源”，依次打开社区联系记录和访谈提纲，保留四个标签。','在资料组的“组 / 标签”选择“当前标签移到新组”；按需拖分隔条、最大化或恢复布局。'];
 result.passed=true;
}catch(error){result.error=error.stack;process.exitCode=1;}finally{if(browser)await browser.close();await writeFile(path.join(out,'verification.json'),JSON.stringify(result,null,2));console.log(JSON.stringify({out,...result}));}
