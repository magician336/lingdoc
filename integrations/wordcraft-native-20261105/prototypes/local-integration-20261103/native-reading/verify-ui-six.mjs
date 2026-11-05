import assert from 'node:assert/strict';
import {mkdir,readFile,writeFile} from 'node:fs/promises';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {browserClient} from '../browser-client.mjs';

const here=path.dirname(fileURLToPath(import.meta.url));
const origin=process.env.UI_SIX_ORIGIN||'http://localhost:7224';
assert(['7224','7226'].includes(new URL(origin).port),'isolated acceptance port only');
const out=path.join(here,'artifacts','ui-six-'+new Date().toISOString().replaceAll(':','-')+'-'+process.pid);
const result={passed:false,origin,checks:[],screenshots:[],modelRequests:0,imports:0,scope:'native UI acceptance; citation fixture replay is not new model acceptance'};
await mkdir(out,{recursive:true});let b;
try {
 b=await browserClient(path.join(out,'edge'));const {send,evaluate:e,wait}=b;
 const engine=request=>e(`wordcraftHost.engine(${JSON.stringify(request)})`);
 const ws=()=>engine({op:'workspace.status'}),source=()=>engine({op:'source.status'});
 const settle=()=>e('new Promise(r=>setTimeout(r,450))');
 const pass=label=>{result.checks.push(label);console.log('PASS '+label)};
 const click=async(r,button='left')=>{assert(r,'click rectangle');const x=r.x+r.width/2,y=r.y+r.height/2;await send('Input.dispatchMouseEvent',{type:'mouseMoved',x,y});await send('Input.dispatchMouseEvent',{type:'mousePressed',x,y,button,clickCount:1});await send('Input.dispatchMouseEvent',{type:'mouseReleased',x,y,button,clickCount:1});await settle()};
 const rect=async name=>click((await ws())[name]);
 const shot=async name=>{await settle();const file=path.join(out,name+'.png');await b.screenshot(file);result.screenshots.push(file)};
 const ready=()=>wait("window.nativeReadingReady?.phase==='ready'&&!!window.nativeWorkspace",120000);
 await send('Page.navigate',{url:origin+'/'});await ready();
 assert.equal((await ws()).documentBound,false);await shot('01-native-home');
 await rect('projectNavRect');await wait("!!(await wordcraftHost.engine({op:'workspace.status'})).projectCardRect");await rect('projectCardRect');
 await wait("!!(await wordcraftHost.engine({op:'workspace.status'})).projectSearchRect");
 for(const width of [1600,1300,900]){
  await send('Emulation.setDeviceMetricsOverride',{width,height:1000,deviceScaleFactor:1,mobile:false});await settle();
  const ui=await ws();for(const key of ['projectSearchRect','projectDocumentRect','importButtonRect']){assert(ui[key],key);assert(ui[key].x+ui[key].width<=width+1,key+' fits '+width)}
  await shot('02-project-'+width);pass('project controls fit '+width+'px');
 }
 await rect('projectSearchRect');await send('Input.insertText',{text:'__ui_six_no_match__'});await wait("(await wordcraftHost.engine({op:'workspace.status'})).projectSearch==='__ui_six_no_match__'");
 assert.equal(Object.keys((await ws()).sourceRowRects).length,0);await shot('03-empty-search');
 await rect('projectSearchRect');await send('Input.dispatchKeyEvent',{type:'keyDown',key:'a',code:'KeyA',windowsVirtualKeyCode:65,modifiers:2});await send('Input.dispatchKeyEvent',{type:'keyUp',key:'a',code:'KeyA',windowsVirtualKeyCode:65,modifiers:2});await send('Input.dispatchKeyEvent',{type:'keyDown',key:'Backspace',code:'Backspace',windowsVirtualKeyCode:8});await send('Input.dispatchKeyEvent',{type:'keyUp',key:'Backspace',code:'Backspace',windowsVirtualKeyCode:8});
 await wait("(await wordcraftHost.engine({op:'workspace.status'})).projectSearch===''");pass('native typing filters and clears project search');
 await e("(window.nativeReadingReady={phase:'navigating'})");try{await rect('projectDocumentRect')}catch(error){if(!/Inspected target navigated or closed|Execution context was destroyed/.test(error.message))throw error;}await ready();assert.equal((await ws()).documentBound,true);pass('project manuscript opens native editor');
 const initial=await engine({op:'status'}),state=await e('nativeWorkspace.state()');result.initial=initial;assert.equal(state.busy,false);
 const pdf=state.sources.find(s=>s.title==='研究说明.pdf'),txt=state.sources.find(s=>s.title==='社区联系记录-v1.txt');assert(pdf&&txt,'generated fixtures required');
 const key=s=>JSON.stringify([s.id,s.version]);
 await e(`nativeWorkspace.openSource(${JSON.stringify(pdf.id)},undefined,${JSON.stringify(pdf.version)})`);
 await engine({op:'source.tabs',action:'newGroup',key:key(pdf)});
 await e(`nativeWorkspace.openSource(${JSON.stringify(txt.id)},undefined,${JSON.stringify(txt.version)})`);
 await engine({op:'source.tabs',action:'newGroup',key:key(txt)});
 assert.equal((await engine({op:'source.layout'})).groups.length,3);pass('body and two sources occupy three native groups');
 const bodyViewAction=async field=>{const layout=await engine({op:'source.layout'});const group=layout.groups.find(g=>g.tabs.includes('$body'));assert(group);await click((await source()).groupMenuRects[String(group.id)]);await click((await source())[field]);};
 await engine({op:'workspace.layout',resourcesOpen:false,chatOpen:false});await settle();
 await bodyViewAction('bodyActualSizeRect');await settle();
 const actualPageWidth=(await source()).bodyPageRects[0]?.width;assert(actualPageWidth>400,'100 percent body page geometry');result.actualPageWidth=actualPageWidth;
 for(const width of [1600,1300,900]){
  await send('Emulation.setDeviceMetricsOverride',{width,height:1000,deviceScaleFactor:1,mobile:false});
  await engine({op:'workspace.layout',resourcesOpen:true,chatOpen:true});await shot('04-three-groups-chat-'+width);
  let ui=await ws();assert(ui.resourcesOpen&&ui.chatOpen);assert(ui.composerRect&&ui.sendRect);assert(ui.sendRect.x+ui.sendRect.width<=width+1);
  if(width===900){
   const narrow=await source(),layout=await engine({op:'source.layout'});
   for(const g of layout.groups){const r=narrow.groupRects.find(item=>item.id===g.id)?.rect;assert(r);assert(r.width>=(g.tabs.includes('$body')?360:220)-1,'every explicit group retains minimum readable width');}
   const rightId=layout.groups.at(-1).id,beforeRight=narrow.groupRects.find(item=>item.id===rightId).rect;
   const viewport=narrow.workspaceRect;assert(viewport,'visible native workspace geometry');const x=viewport.x+viewport.width/2,y=viewport.y+12;
   await send('Input.dispatchMouseEvent',{type:'mouseMoved',x,y});await send('Input.dispatchMouseEvent',{type:'mouseWheel',x,y,deltaX:4000,deltaY:0});await settle();
   const afterRight=(await source()).groupRects.find(item=>item.id===rightId).rect;
   assert(afterRight.x<beforeRight.x-50,'horizontal wheel moves groups');assert(afterRight.x>=viewport.x-1&&afterRight.x+afterRight.width<=viewport.x+viewport.width+1,'right source group becomes fully visible');
   await shot('04-three-groups-900-scrolled-right');pass('900px native groups retain readable minimum widths and horizontal scrolling reveals right source');
   const beforeChat=await engine({op:'source.layout'});await click((await ws()).composerRect);await send('Input.insertText',{text:'焦点验收'});await settle();assert.equal((await ws()).draft,'焦点验收');const afterChat=await engine({op:'source.layout'});assert.equal(afterChat.focusGroup,beforeChat.focusGroup,'offscreen group cannot steal chat focus');assert.deepEqual(afterChat.sources,beforeChat.sources,'chat focus preserves source versions');
   await send('Input.dispatchKeyEvent',{type:'keyDown',key:'a',code:'KeyA',windowsVirtualKeyCode:65,modifiers:2});await send('Input.dispatchKeyEvent',{type:'keyUp',key:'a',code:'KeyA',windowsVirtualKeyCode:65,modifiers:2});await send('Input.dispatchKeyEvent',{type:'keyDown',key:'Backspace',code:'Backspace',windowsVirtualKeyCode:8});await send('Input.dispatchKeyEvent',{type:'keyUp',key:'Backspace',code:'Backspace',windowsVirtualKeyCode:8});await settle();assert.equal((await ws()).draft,'');pass('offscreen groups do not intercept chat input or change source focus');
   await send('Input.dispatchMouseEvent',{type:'mouseMoved',x,y});
   await send('Input.dispatchMouseEvent',{type:'mouseWheel',x,y,deltaX:-4000,deltaY:0});await settle();
  }
  await rect('chatToggleRect');assert.equal((await ws()).resourcesOpen,true);assert.equal((await ws()).chatOpen,false);
  await rect('chatToggleRect');assert.equal((await ws()).resourcesOpen,true);assert.equal((await ws()).chatOpen,true);
  await engine({op:'workspace.layout',resourcesOpen:false});assert.equal((await ws()).chatOpen,true);await shot('05-resources-hidden-'+width);
  assert(Math.abs((await source()).bodyPageRects[0].width-actualPageWidth)<1,'panel toggles preserve explicit 100 percent page width');
  pass('independent panels and usable composer at '+width+'px');
 }
 await bodyViewAction('bodyFitRect');await settle();
 const fitted=await source();assert(fitted.bodyPageRects.length);assert(fitted.bodyPageRects.every(p=>p.width<=fitted.bodyRect.width+1),'explicit fit matches body viewport');await shot('05-explicit-fit-900');pass('view menu explicitly fits body width after preserving user zoom');
 await bodyViewAction('bodyActualSizeRect');assert(Math.abs((await source()).bodyPageRects[0].width-actualPageWidth)<1);pass('view menu returns to explicit 100 percent');
 await send('Emulation.setDeviceMetricsOverride',{width:1600,height:1000,deviceScaleFactor:1,mobile:false});await engine({op:'workspace.layout',resourcesOpen:false,chatOpen:false});await settle();
 let s=await source();await click(s.tabRects[key(pdf)]);assert.equal((await source()).sourceId,pdf.id);
 await click((await source()).tabRects[key(pdf)],'middle');assert.equal((await engine({op:'source.layout'})).sources.length,1);await engine({op:'source.tabs',action:'reopen'});assert.equal((await engine({op:'source.layout'})).sources.length,2);pass('real tab activation middle-close and reopen preserve source identity');
 // Replay one existing synthetic answer, remapping only fixture identities to this isolated project.
 const prior=JSON.parse(await readFile(path.join(here,'artifacts/ai-conversation-2026-11-04T17-33-36.977Z-84544/verification.json'),'utf8'));
 const answer=structuredClone(prior.answers[0]);assert(answer.citations?.length);
 const mapped=c=>{const local=state.sources.find(s=>s.title===c.title);assert(local,'fixture title mapping');return {...c,sourceId:local.id,id:local.id,version:local.version}};
 answer.id='ui-six-replay-answer';answer.text='【界面回放：既有虚构资料回答，未发起模型请求】\n'+answer.text;answer.citations=answer.citations.map(mapped);answer.usedSources=(answer.usedSources||[]).map(mapped);
 await engine({op:'workspace.hydrate',state:{...state,documentBound:true,messages:[answer],busy:false}});await engine({op:'workspace.layout',resourcesOpen:false,chatOpen:true});await settle();
 const citation=answer.citations[0],citationKey=JSON.stringify([answer.id,citation.sourceId,citation.version,citation.page??null]);
 const r=(await ws()).citationOpenRects[citationKey];assert(r,'collapsed quote retains original action');await shot('06-citation-ui-replay');await click(r);
 await wait(`(await wordcraftHost.engine({op:'source.status'})).sourceId===${JSON.stringify(citation.sourceId)}`);assert.equal((await source()).version,citation.version);pass('collapsed citation UI replay opens exact-version original');
 await e('nativeWorkspace.refresh()');assert.deepEqual((await e('nativeWorkspace.state()')).messages,state.messages);pass('UI replay leaves persisted conversation untouched');
 // Stress the visible composer, using browser-local display state only and never pressing Send.
 const beforeLowHeight=await engine({op:'source.layout'});
 await send('Emulation.setDeviceMetricsOverride',{width:1300,height:700,deviceScaleFactor:1,mobile:false});
 await engine({op:'workspace.hydrate',state:{...state,documentBound:true,contextSources:[pdf,txt],error:'界面验收回放：请求失败，请检查资料后重试。'.repeat(30)}});
 await engine({op:'workspace.layout',resourcesOpen:true,chatOpen:true});await settle();
 await click((await ws()).composerRect);const draftLine='长问题'.repeat(100),longDraft=Array(10).fill(draftLine).join('\n');
 for(let i=0;i<10;i++){if(i){await send('Input.dispatchKeyEvent',{type:'keyDown',key:'Enter',code:'Enter',windowsVirtualKeyCode:13});await send('Input.dispatchKeyEvent',{type:'keyUp',key:'Enter',code:'Enter',windowsVirtualKeyCode:13});}await send('Input.insertText',{text:draftLine});await settle();}
 const compact=await ws();assert.equal(compact.draft,longDraft,'all 3009 multiline draft characters retained');
 for(const name of ['sendRect','composerRect']){const r=compact[name];assert(r&&r.height>0,name+' exists');assert(r.y>=0&&r.y+r.height<=700,name+' fully visible at 1300x700');assert(r.x>=0&&r.x+r.width<=1300,name+' fits horizontally');}
 result.lowHeight={composerRect:compact.composerRect,sendRect:compact.sendRect,draftLines:10,draftCharacters:longDraft.length,contextCount:compact.contextSources.length,errorLength:compact.error.length};await shot('07-low-height-long-draft-ui-replay');pass('1300x700 long error two context chips and 3009-character multiline draft keep composer and send visible');
 assert.deepEqual((await engine({op:'source.layout'})).sources,beforeLowHeight.sources,'long chat input preserves exact source versions');
 await e('nativeWorkspace.refresh()');assert.deepEqual((await e('nativeWorkspace.state()')).messages,state.messages);pass('low-height replay never sends or persists a message');
 assert.deepEqual(await engine({op:'status'}),initial);pass('all UI actions preserve document identity revision and dirty state');
 result.errors=b.errors;assert.equal(b.errors.length,0);result.passed=true;
}catch(error){result.error=error.stack;process.exitCode=1;if(b)try{result.failure=await b.evaluate("({ready:window.nativeReadingReady,ui:window.wordcraftHost?await wordcraftHost.engine({op:'workspace.status'}):null,source:window.wordcraftHost?await wordcraftHost.engine({op:'source.status'}):null})");await b.screenshot(path.join(out,'failure.png'))}catch(capture){result.captureError=capture.message}}
finally{if(b)try{await b.close()}catch(error){result.cleanupError=error.message;result.passed=false;process.exitCode=1}await writeFile(path.join(out,'verification.json'),JSON.stringify(result,null,2));console.log(JSON.stringify({out,passed:result.passed,checks:result.checks.length,error:result.error}))}

