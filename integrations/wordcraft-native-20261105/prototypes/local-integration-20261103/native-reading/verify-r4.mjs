import assert from 'node:assert/strict';
import {mkdir,writeFile} from 'node:fs/promises';
import {randomUUID} from 'node:crypto';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {browserClient} from '../browser-client.mjs';
const here=path.dirname(fileURLToPath(import.meta.url)),out=path.join(here,'artifacts','r4-'+Date.now());await mkdir(out,{recursive:true});
const result={passed:false,origin:'http://localhost:7218',modelRequests:0,checks:[]};let browser;
try{
 browser=await browserClient(path.join(out,'edge'));const {evaluate:e,send,wait}=browser;
 const ready=()=>wait(`window.nativeReadingReady?.phase==='ready'`,120000);
 const engine=r=>e(`await wordcraftHost.engine(${JSON.stringify(r)})`);
 const pause=()=>e('new Promise(r=>setTimeout(r,350))');
 const pass=s=>{result.checks.push(s);console.log('PASS '+s)};
 const click=async(x,y)=>{await send('Input.dispatchMouseEvent',{type:'mouseMoved',x,y});await send('Input.dispatchMouseEvent',{type:'mousePressed',x,y,button:'left',clickCount:1});await send('Input.dispatchMouseEvent',{type:'mouseReleased',x,y,button:'left',clickCount:1});};
 await send('Page.navigate',{url:result.origin+'/project'});await ready();
 const before=await engine({op:'status'}),initial=await e('await nativeWorkspace.state()');
 const chosen=initial.sources.filter(s=>s.searchable!==false).slice(0,2);assert.equal(chosen.length,2);
 for(let n=1;n<=3;n++){
  const input={projectId:initial.projectId,name:`R4合成资料${n}.txt`,base64:Buffer.from(`R4独立布局测试${n} ${randomUUID()}\n`+'用于阅读分组验证，不请求模型。\n'.repeat(50)).toString('base64'),key:randomUUID(),folder:'前期研究材料'};
  const response=await e(`(async()=>{const r=await fetch('/native-reading/workspace/import',{method:'POST',headers:{'Content-Type':'application/json','X-Workspace-Token':NATIVE_WORKSPACE_TOKEN},body:JSON.stringify(${JSON.stringify(input)})});return {status:r.status,value:await r.json()}})()`);
  assert.equal(response.status,200,JSON.stringify(response.value));assert.equal(response.value.record.status,'ready');chosen.push({id:response.value.record.sourceId,version:response.value.record.version});
 }
 await e('await nativeWorkspace.refresh()');
 for(const s of chosen)await e(`await nativeWorkspace.openSource(${JSON.stringify(s.id)},undefined,${JSON.stringify(s.version)})`);
 let status=await engine({op:'source.status'});assert.equal(status.tabs.length,6);pass('five real materials plus original body retain six tabs');
 const keys=chosen.map(s=>JSON.stringify([s.id,s.version]));
 await engine({op:'source.tabs',action:'newGroup',key:keys[1]});await engine({op:'source.tabs',action:'newGroup',key:keys[2]});
 status=await engine({op:'source.status'});if(status.layout.maximized!==null)await engine({op:'source.tabs',action:'maximize',group:status.layout.maximized});status=await engine({op:'source.status'});assert.equal(status.layout.groups.length,3);pass('three native groups created');
 await engine({op:'source.tabs',action:'move',key:keys[3],group:status.layout.groups[1].id});
 await pause();status=await engine({op:'source.status'});
 const rect=status.tabRects[keys[3]];assert(rect);await click(rect.x+rect.width/2,rect.y+rect.height/2);await pause();status=await engine({op:'source.status'});assert(status.layout.groups.some(g=>g.active===keys[3]));pass('real native tab click activates exact source');
 const groups=status.groupRects;assert.equal(groups.length,3);const g=groups[0].rect,x=g.x+g.width+3,y=g.y+Math.min(200,g.height/2);const weights=status.layout.groups.map(g=>g.weight);
 await send('Input.dispatchMouseEvent',{type:'mouseMoved',x,y});await send('Input.dispatchMouseEvent',{type:'mousePressed',x,y,button:'left',clickCount:1});
 for(let step=1;step<=5;step++)await send('Input.dispatchMouseEvent',{type:'mouseMoved',x:x+step*12,y,button:'left',buttons:1});
 await send('Input.dispatchMouseEvent',{type:'mouseReleased',x:x+60,y,button:'left',clickCount:1});await pause();status=await engine({op:'source.status'});assert.notDeepEqual(status.layout.groups.map(g=>g.weight),weights);pass('real splitter drag changes group widths');
 await engine({op:'source.tabs',action:'close',key:keys[4]});assert.equal((await engine({op:'source.layout'})).sources.length,4);await engine({op:'source.tabs',action:'reopen'});assert.equal((await engine({op:'source.layout'})).sources.length,5);pass('close and reopen preserve cached material');
 let layout=await engine({op:'source.layout'});const max=layout.groups[1].id;await engine({op:'source.tabs',action:'maximize',group:max});assert.equal((await engine({op:'source.layout'})).maximized,max);
 await browser.screenshot(path.join(out,'maximized-1600.png'));await engine({op:'source.tabs',action:'maximize',group:max});
 await engine({op:'source.tabs',action:'merge',group:layout.groups[2].id});assert.equal((await engine({op:'source.layout'})).groups.length,1);assert.equal((await engine({op:'source.layout'})).sources.length,5);pass('maximize and merge preserve documents');
 const t=await engine({op:'source.begin'});await engine({op:'source.restore',state:layout,expectedDocumentId:t.document.documentId,expectedViewEpoch:t.source.viewEpoch});assert.equal((await engine({op:'source.layout'})).groups.length,3);
 const pdf=chosen.find(s=>initial.sources.find(x=>x.id===s.id)?.title.endsWith('.pdf'));assert(pdf);
 await e(`await nativeWorkspace.openSource(${JSON.stringify(pdf.id)},2,${JSON.stringify(pdf.version)})`);await pause();
 layout=await engine({op:'source.layout'});await e('new Promise(r=>setTimeout(r,1600))');
 await browser.screenshot(path.join(out,'groups-1600.png'));
 assert.deepEqual(await engine({op:'status'}),before);pass('all reading operations leave document identity revision and dirty unchanged');
 await send('Page.reload',{});await ready();await pause();const restored=await engine({op:'source.layout'});
 assert.deepEqual(restored.sources,layout.sources);assert.deepEqual(restored.groups,layout.groups);assert.deepEqual(restored.positions,layout.positions);pass('reload restores exact versions groups and per-source positions');
 await send('Emulation.setDeviceMetricsOverride',{width:1300,height:1000,deviceScaleFactor:1,mobile:false});await pause();await browser.screenshot(path.join(out,'groups-1300.png'));
 result.layout=restored;result.bodyBefore=before;result.passed=true;
}catch(error){result.error=error.stack;process.exitCode=1;if(browser)await browser.screenshot(path.join(out,'failure.png')).catch(()=>{});}finally{if(browser)await browser.close();await writeFile(path.join(out,'verification.json'),JSON.stringify(result,null,2));console.log(JSON.stringify({out,...result}));}

