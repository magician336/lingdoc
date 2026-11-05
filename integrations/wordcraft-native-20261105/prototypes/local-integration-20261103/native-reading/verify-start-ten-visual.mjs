import assert from 'node:assert/strict';
import {mkdir,writeFile} from 'node:fs/promises';
import path from 'node:path';
import {browserClient} from '../browser-client.mjs';
const out=path.resolve('prototypes/local-integration-20261103/native-reading/artifacts/start-ten-final-'+Date.now());await mkdir(out,{recursive:true});const b=await browserClient(path.join(out,'edge'));const result={passed:false,checks:[],modelRequests:0};
try{
 const {evaluate:e,send,wait}=b;const ws=()=>e("wordcraftHost.engine({op:'workspace.status'})"),pause=()=>e('new Promise(r=>setTimeout(r,350))');
 const click=async r=>{const x=r.x+r.width/2,y=r.y+r.height/2;await send('Input.dispatchMouseEvent',{type:'mouseMoved',x,y});await send('Input.dispatchMouseEvent',{type:'mousePressed',x,y,button:'left',clickCount:1});await send('Input.dispatchMouseEvent',{type:'mouseReleased',x,y,button:'left',clickCount:1});await pause()};
 await send('Page.navigate',{url:'http://localhost:7227/'});await wait("window.nativeReadingReady?.phase==='ready'&&window.nativeReadingReady.project==='ready'",120000);await b.screenshot(path.join(out,'01-home.png'));
 const original=await e('nativeWorkspace.state()'),body=await e("wordcraftHost.engine({op:'status'})");
 await click((await ws()).projectNavRect);await wait("!!(await wordcraftHost.engine({op:'workspace.status'})).projectCardRect");await b.screenshot(path.join(out,'02-project-list.png'));await click((await ws()).projectCardRect);await pause();await b.screenshot(path.join(out,'03-project.png'));result.checks.push('final native Home and project navigation render successfully');
 await send('Emulation.setDeviceMetricsOverride',{width:900,height:700,deviceScaleFactor:1,mobile:false});await pause();await b.screenshot(path.join(out,'04-project-900.png'));
 const name='非常长的失败资料文件名称'.repeat(20)+'.pdf';await e(`wordcraftHost.engine({op:'workspace.hydrate',state:${JSON.stringify({...original,documentBound:false,imports:[{id:'visual-only',name,status:'failed',error:'仅前端布局验证，不是实际导入失败。'}],importError:'仅前端布局验证',resumeAvailable:false})}})`);await pause();
 const ui=await ws();assert(ui.importButtonRect.y+ui.importButtonRect.height<700);const firstSource=Object.values(ui.sourceRowRects)[0];assert(firstSource&&firstSource.y<540,'long failed name must not consume multiple list rows');await b.screenshot(path.join(out,'05-compact-failed-name.png'));result.checks.push('UI-only long failed import is compact with fixed visible status and source list');
 await e('nativeWorkspace.refresh()');await pause();assert.deepEqual(await e("wordcraftHost.engine({op:'status'})"),body);assert.deepEqual((await e('nativeWorkspace.state()')).imports,original.imports);result.checks.push('visual stress does not mutate document or persisted imports');
 await click((await ws()).projectBackRect);await pause();assert((await ws()).projectCardRect.y>0);await click((await ws()).projectCardRect);await pause();assert((await ws()).projectSearchRect.y>0);result.checks.push('project back and re-entry retain visible start controls');
 await b.screenshot(path.join(out,'06-ready.png'));assert.equal(b.errors.length,0);result.checks.push('no browser exceptions');result.passed=true;
}catch(error){result.error=error.stack;process.exitCode=1;await b.screenshot(path.join(out,'failure.png'))}finally{await b.close();await writeFile(path.join(out,'verification.json'),JSON.stringify(result,null,2));console.log(JSON.stringify({out,...result}))}
