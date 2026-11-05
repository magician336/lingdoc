import assert from 'node:assert/strict';
import {mkdir,writeFile} from 'node:fs/promises';
import path from 'node:path';
import {browserClient} from '../browser-client.mjs';
const out=path.resolve(process.env.PHASE2_VISUAL_OUTPUT || ('prototypes/local-integration-20261103/native-reading/artifacts/phase2-visual-'+Date.now()));await mkdir(out,{recursive:true});
const result={passed:false,checks:[],screenshots:[],modelRequests:0};const b=await browserClient(path.join(out,'edge'));
try {
 const {send,evaluate:e,wait}=b;const engine=r=>e(`wordcraftHost.engine(${JSON.stringify(r)})`);const pause=()=>e('new Promise(r=>setTimeout(r,700))');
 let initScript;
 for(const theme of ['light','dark']) {
  if(initScript)await send('Page.removeScriptToEvaluateOnNewDocument',{identifier:initScript});
  initScript=(await send('Page.addScriptToEvaluateOnNewDocument',{source:`localStorage.clear();localStorage.setItem('wordcraft.ui',JSON.stringify({theme:'${theme}',animations:'off'}));`})).identifier;
  await send('Emulation.setEmulatedMedia',{features:[{name:'prefers-color-scheme',value:theme}]});
  for(const size of [[1366,768],[1920,1080]]) {
   await send('Emulation.setDeviceMetricsOverride',{width:size[0],height:size[1],deviceScaleFactor:1,mobile:false});
   await send('Page.navigate',{url:'http://localhost:7231/project'});await wait("window.nativeReadingReady?.phase==='ready'&&!!window.nativeWorkspace",120000);await pause();
   const before=await engine({op:'status'});
   const shot=async name=>{await pause();let file=path.join(out,`${theme}-${size[0]}-${name}.png`);await b.screenshot(file);result.screenshots.push(file)};
   await engine({op:'workspace.home',page:'home'});await shot('home');
   await engine({op:'workspace.home',page:'open'});await shot('file');
   await engine({op:'workspace.editor'});await engine({op:'workspace.layout',resourcesOpen:false,chatOpen:false});await shot('ribbon');
   await engine({op:'source.openText',sourceId:'phase2-visual-only',version:'1',title:'视觉验收资料（临时，不写入资料库）',text:'原生资料阅读验收\n核对深浅主题、留白与原文可读性。\n这不是模型回答。'});
   await engine({op:'source.mode',mode:'split'});await shot('reading');
   await engine({op:'workspace.layout',resourcesOpen:true,chatOpen:true});await shot('chat');
   const saved=await e('nativeWorkspace.state()');
   for(const mode of ['busy','error','full']) {
    const contextSources=mode==='full'?Array.from({length:4},(_,i)=>({id:'visual-'+i,title:'参考资料 '+(i+1),version:'1',searchable:true})):[];
    await engine({op:'workspace.hydrate',state:{...saved,busy:mode==='busy',error:mode==='error'?'视觉验收：模拟服务失败，资料与历史保留。':'',contextSources}});await shot('chat-'+mode);
   }
   await e('nativeWorkspace.refresh()');
   assert.deepEqual(await engine({op:'status'}),before,'visual switching must preserve body');result.checks.push(`${theme} ${size[0]} five native surfaces; body unchanged`);
  }
 }
 assert.equal(b.errors.length,0);result.passed=true;
}catch(error){result.error=error.stack;process.exitCode=1;await b.screenshot(path.join(out,'failure.png'))}finally{await b.close();await writeFile(path.join(out,'verification.json'),JSON.stringify(result,null,2));console.log(JSON.stringify({out,...result}))}
