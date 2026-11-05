import assert from 'node:assert/strict';
import {mkdir,writeFile} from 'node:fs/promises';
import {fileURLToPath} from 'node:url';
import path from 'node:path';
import {browserClient} from '../browser-client.mjs';
const here=path.dirname(fileURLToPath(import.meta.url));
const base=process.env.LAYOUT_BASE_URL||'http://localhost:7215';
const out=path.join(here,'artifacts','layout-foundation-'+new Date().toISOString().replaceAll(':','-')+'-'+process.pid);
const result={passed:false,base,checks:[],screenshots:[],modelRequests:0,imports:0};
await mkdir(out,{recursive:true});let browser;
try {
 browser=await browserClient(path.join(out,'edge'));
 const {send,evaluate:e,wait}=browser;
 const check=async(expression,label)=>{assert(await e(expression),label);result.checks.push(label);console.log('PASS '+label)};
 const click=async(x,y)=>{await send('Input.dispatchMouseEvent',{type:'mousePressed',x,y,button:'left',clickCount:1});await send('Input.dispatchMouseEvent',{type:'mouseReleased',x,y,button:'left',clickCount:1});};
 const rectClick=async key=>{const r=await e(`(await wordcraftHost.engine({op:'workspace.status'}))[${JSON.stringify(key)}]`);assert(r,key);await click(r.x+r.width/2,r.y+r.height/2)};
 const ready=()=>wait(`window.nativeReadingReady?.phase==='ready'&&!!window.nativeWorkspace`,120000);
 const screenshot=async name=>{await e(`new Promise(r=>setTimeout(r,350))`);const file=path.join(out,name+'.png');await browser.screenshot(file);result.screenshots.push(file)};
 await send('Page.navigate',{url:base+'/'});await ready();
 await check(`window.WORDCRAFT_STANDALONE===true&&!(await wordcraftHost.engine({op:'workspace.status'})).documentBound`,'root is native standalone start page');
 await screenshot('01-start-home');await rectClick('projectNavRect');
 await wait(`!!(await wordcraftHost.engine({op:'workspace.status'})).projectCardRect`);await screenshot('02-project-list');
 await rectClick('projectCardRect');await wait(`!!(await wordcraftHost.engine({op:'workspace.status'})).projectDocumentRect`);await screenshot('03-project-details');
 await e(`(()=>{window.nativeReadingReady={phase:'navigating'};return true})()`);await rectClick('projectDocumentRect');await ready();
 await check(`location.pathname==='/project'&&document.querySelectorAll('iframe').length===0&&(await wordcraftHost.engine({op:'workspace.status'})).documentBound`,'project uses native editor without an outer iframe');
 result.initial=await e(`({body:await wordcraftHost.engine({op:'status'}),workspace:await nativeWorkspace.state()})`);
 assert(!result.initial.workspace.busy,'in-flight model call must not be disturbed');
 const sources=result.initial.workspace.sources;assert(sources.length>=2,'two actual project sources are required');
 const docIdentity=body=>JSON.stringify({documentId:body.documentId,revision:body.revision,rev:body.rev,dirty:body.dirty});
 result.initialIdentity=docIdentity(result.initial.body);
 const immutable=async label=>{assert.equal(docIdentity(await e(`await wordcraftHost.engine({op:'status'})`)),result.initialIdentity,label);result.checks.push(label)};
 await e(`await nativeWorkspace.openSource(${JSON.stringify(sources[0].id)},undefined,${JSON.stringify(sources[0].version)})`);
 for(const [width,height] of [[1600,1000],[1300,900]]) {
  await send('Emulation.setDeviceMetricsOverride',{width,height,deviceScaleFactor:1,mobile:false});
  await e(`await wordcraftHost.engine({op:'workspace.layout',resourcesOpen:true,chatOpen:false})`);
  await e(`await wordcraftHost.engine({op:'source.mode',mode:'body'})`);await screenshot(`${width}-body`);
  await e(`await wordcraftHost.engine({op:'source.mode',mode:'split'})`);
  await wait(`(await wordcraftHost.engine({op:'source.status'})).bodyFitWidth`);
  await check(`(await wordcraftHost.engine({op:'source.status'})).bodyFitWidth`,'split fit enabled at '+width);
  let source=await e(`await wordcraftHost.engine({op:'source.status'})`);assert(source.bodyRect?.width>0&&source.sourceRect?.width>0,'split rectangles exist');
  result['geometry'+width]=source;
  if(source.sourcePageRect&&source.zoom===1){assert(source.sourcePageRect.width<=source.sourceRect.width+1,'100% PDF must fit reader');result.checks.push('PDF image fits its reader at '+width);}
  if(source.bodyPageRects){assert(source.bodyPageRects.length>0&&source.bodyPageRects.every(p=>p.width<=source.bodyRect.width+1),'rendered body pages fit viewport');result.checks.push('rendered page widths fit at '+width)}else{result.pageGeometryLimitation='bodyFitWidth and viewport rectangles verified; rendered page rectangles not exposed';}
  await screenshot(`${width}-split`);
  let ui=await e(`await wordcraftHost.engine({op:'workspace.status'})`);assert(Object.values(ui.sourceRowRects).length>0,'resource rows exported');assert(Object.values(ui.sourceRowRects).every(r=>r.height<=32),'resource rows are single-height');result.checks.push('resource rows single-line at '+width);
  await click(ui.topRect.x+90,ui.topRect.y+14);await wait(`(await wordcraftHost.engine({op:'workspace.status'})).chatOpen`);
  await check(`(await wordcraftHost.engine({op:'workspace.status'})).resourcesOpen&&(await wordcraftHost.engine({op:'source.status'})).mode==='split'`,'chat opens independently at '+width);
  await screenshot(`${width}-split-chat`);
  source=await e(`await wordcraftHost.engine({op:'source.status'})`);
  const x=source.bodyRect.x+source.bodyRect.width+3,y=source.bodyRect.y+Math.min(120,source.bodyRect.height/2);
  const dx=source.bodyRect.width>source.sourceRect.width? -40:40;
  await send('Input.dispatchMouseEvent',{type:'mousePressed',x,y,button:'left',clickCount:1});
  await send('Input.dispatchMouseEvent',{type:'mouseMoved',x:x+dx,y,button:'left',buttons:1});
  await send('Input.dispatchMouseEvent',{type:'mouseReleased',x:x+dx,y,button:'left',clickCount:1});
  await wait(`Math.abs((await wordcraftHost.engine({op:'source.status'})).bodyRect.width-${source.bodyRect.width})>10`);
  result.checks.push('native divider responds to drag at '+width);
  ui=await e(`await wordcraftHost.engine({op:'workspace.status'})`);const row=ui.sourceRowRects[sources[1].id];assert(row,'second source row');await click(row.x+row.width/2,row.y+row.height/2);
  await wait(`(await wordcraftHost.engine({op:'workspace.status'})).activeSourceId===${JSON.stringify(sources[1].id)}`);
  await check(`(await wordcraftHost.engine({op:'source.status'})).sourceId===${JSON.stringify(sources[1].id)}`,'resource selection matches actual reader at '+width);
  await e(`await wordcraftHost.engine({op:'source.mode',mode:'body'})`);await immutable('view operations preserve document identity/revision/dirty at '+width);
 }
 await check(`JSON.stringify((await nativeWorkspace.state()).messages)===${JSON.stringify(JSON.stringify(result.initial.workspace.messages))}`,'layout acceptance leaves model history untouched');
 result.errors=browser.errors;assert.equal(browser.errors.length,0);result.passed=true;
}catch(error){result.error=error.stack;process.exitCode=1;if(browser)try{result.failure=await browser.evaluate(`({url:location.href,ready:window.nativeReadingReady,source:window.wordcraftHost?await wordcraftHost.engine({op:'source.status'}):null,workspace:window.wordcraftHost?await wordcraftHost.engine({op:'workspace.status'}):null})`);await browser.screenshot(path.join(out,'failure.png'))}catch{}}
finally{if(browser)try{await browser.close()}catch(error){result.cleanupError=error.message;result.passed=false;process.exitCode=1}await writeFile(path.join(out,'verification.json'),JSON.stringify(result,null,2));console.log(JSON.stringify({out,passed:result.passed,checks:result.checks.length,error:result.error}));}
