import assert from 'node:assert/strict';
import {mkdir,writeFile} from 'node:fs/promises';
import path from 'node:path';
import {browserClient} from '../browser-client.mjs';
const out=path.resolve('prototypes/local-integration-20261103/native-reading/artifacts/r4-project-'+new Date().toISOString().replaceAll(':','-'));
await mkdir(out,{recursive:true});let b;const result={passed:false,checks:[]};
try{
 b=await browserClient(path.join(out,'edge'));const {send,evaluate:e,wait}=b;
 const click=async(x,y)=>{await send('Input.dispatchMouseEvent',{type:'mousePressed',x,y,button:'left',clickCount:1});await send('Input.dispatchMouseEvent',{type:'mouseReleased',x,y,button:'left',clickCount:1})};
 const rect=async key=>{const r=await e(`(await wordcraftHost.engine({op:'workspace.status'}))[${JSON.stringify(key)}]`);assert(r,key);await click(r.x+r.width/2,r.y+r.height/2)};
 const ready=()=>wait(`window.nativeReadingReady?.phase==='ready'&&!!window.nativeWorkspace`,120000);
 await send('Page.navigate',{url:'http://localhost:7218/'});await ready();
 result.initial=await e(`({body:await wordcraftHost.engine({op:'status'}),workspace:await nativeWorkspace.state()})`);
 await b.screenshot(path.join(out,'01-home.png'));
 await click(90,122);await e(`new Promise(r=>setTimeout(r,300))`);await b.screenshot(path.join(out,'02-native-new.png'));
 await click(90,164);await e(`new Promise(r=>setTimeout(r,300))`);await b.screenshot(path.join(out,'03-native-open.png'));
 assert.deepEqual(await e(`await wordcraftHost.engine({op:'status'})`),result.initial.body);result.checks.push('native Home/New/Open navigation preserves document');
 await rect('projectNavRect');await wait(`!!(await wordcraftHost.engine({op:'workspace.status'})).projectCardRect`);await rect('projectCardRect');
 await wait(`!!(await wordcraftHost.engine({op:'workspace.status'})).projectSearchRect`);await b.screenshot(path.join(out,'04-project-details.png'));
 await rect('projectSearchRect');await send('Input.insertText',{text:'__no_matching_material__'});
 await wait(`(await wordcraftHost.engine({op:'workspace.status'})).projectSearch==='__no_matching_material__'`);
 await e(`new Promise(r=>setTimeout(r,250))`);
 const filtered=await e(`await wordcraftHost.engine({op:'workspace.status'})`);assert.equal(Object.keys(filtered.sourceRowRects).length,0);assert.equal(filtered.projectDocumentRect,null);result.checks.push('actual search typing filters document and all source rows');
 await b.screenshot(path.join(out,'05-no-results.png'));
 await send('Input.dispatchKeyEvent',{type:'keyDown',key:'a',code:'KeyA',windowsVirtualKeyCode:65,modifiers:2});await send('Input.dispatchKeyEvent',{type:'keyUp',key:'a',code:'KeyA',windowsVirtualKeyCode:65,modifiers:2});await send('Input.dispatchKeyEvent',{type:'keyDown',key:'Backspace',code:'Backspace',windowsVirtualKeyCode:8});await send('Input.dispatchKeyEvent',{type:'keyUp',key:'Backspace',code:'Backspace',windowsVirtualKeyCode:8});
 await wait(`(await wordcraftHost.engine({op:'workspace.status'})).projectSearch===''`);await wait(`Object.keys((await wordcraftHost.engine({op:'workspace.status'})).sourceRowRects).length>0`);result.checks.push('clearing search restores real sources');
 await e(`(()=>{window.nativeReadingReady={phase:'navigating'};return true})()`);await rect('projectDocumentRect');await ready();
 result.projectBody=await e(`await wordcraftHost.engine({op:'status'})`);
 const state=await e(`await nativeWorkspace.state()`);assert(state.sources.length);await e(`await nativeWorkspace.openSource(${JSON.stringify(state.sources[0].id)},undefined,${JSON.stringify(state.sources[0].version)})`);
 await e(`new Promise(r=>setTimeout(r,1600))`);
 await e(`(()=>{window.nativeReadingReady={phase:'navigating'};return true})()`);await send('Page.navigate',{url:'http://localhost:7218/'});await ready();
 await rect('projectNavRect');await wait(`!!(await wordcraftHost.engine({op:'workspace.status'})).projectCardRect`);await rect('projectCardRect');
 await wait(`!!(await wordcraftHost.engine({op:'workspace.status'})).resumeRect`);result.resume=await e(`await wordcraftHost.engine({op:'workspace.status'})`);await b.screenshot(path.join(out,'06-resume-available.png'));
 await e(`(()=>{window.nativeReadingReady={phase:'navigating'};return true})()`);await rect('resumeRect');await ready();
 result.restored=await e(`await wordcraftHost.engine({op:'source.status'})`);assert.equal(result.restored.sourceId,state.sources[0].id);result.checks.push('continue previous work restores actual saved source');await b.screenshot(path.join(out,'07-resumed.png'));
 assert.deepEqual((await e(`await nativeWorkspace.state()`)).messages,state.messages);result.checks.push('no model or conversation mutations');result.errors=b.errors;assert.equal(b.errors.length,0);result.passed=true;
}catch(error){result.error=error.stack;process.exitCode=1;if(b)try{result.failure=await b.evaluate(`({ready:window.nativeReadingReady,workspace:window.wordcraftHost?await wordcraftHost.engine({op:'workspace.status'}):null})`);await b.screenshot(path.join(out,'failure.png'))}catch{}}
finally{if(b)await b.close();await writeFile(path.join(out,'verification.json'),JSON.stringify(result,null,2));console.log(JSON.stringify({out,passed:result.passed,checks:result.checks,error:result.error}))}
