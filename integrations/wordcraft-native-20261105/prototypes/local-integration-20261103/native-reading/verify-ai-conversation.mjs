// Three bounded real-provider turns on generated fixtures. No automatic send retry.
import assert from 'node:assert/strict';
import {mkdir,readFile,writeFile} from 'node:fs/promises';
import {fileURLToPath} from 'node:url';
import path from 'node:path';
import {browserClient} from '../browser-client.mjs';
const resumeUi=process.argv.includes('--resume-ui');
if(!resumeUi&&!process.argv.includes('--run-real-ai'))throw Error('Explicit --run-real-ai is required; this test sends up to three real model requests.');
const origin=process.env.AI_QA_ORIGIN||'http://localhost:7221';
const address=new URL(origin);
if(address.protocol!=='http:'||!['localhost','127.0.0.1'].includes(address.hostname)||!['7221','7222','7223','7225','7226'].includes(address.port)||!['/','/project'].includes(address.pathname))throw Error('Only the dedicated AI acceptance instances are allowed.');
const here=path.dirname(fileURLToPath(import.meta.url));
const out=path.join(here,'artifacts','ai-conversation-'+new Date().toISOString().replaceAll(':','-')+'-'+process.pid);
const result={passed:false,origin,scope:'three real generated-source turns; no document mutation',checks:[],resumeUi,attemptedSends:0,realModelReceipts:0,answers:[],screenshots:[]};
await mkdir(out,{recursive:true});let browser;
const record=label=>{result.checks.push(label);console.log('PASS '+label)};
try{
 browser=await browserClient(path.join(out,'edge'));
 const {evaluate:e,send,wait}=browser;
 const pause=()=>e('new Promise(r=>setTimeout(r,350))');
 const ws=()=>e("wordcraftHost.engine({op:'workspace.status'})");
 const state=()=>e('nativeWorkspace.state()');
 const body=()=>e("wordcraftHost.engine({op:'status'})");
 const screenshot=async name=>{await pause();const file=path.join(out,name+'.png');await browser.screenshot(file);result.screenshots.push(file)};
 const click=async r=>{assert(r&&r.width>0&&r.height>0,'required native control must exist');const x=r.x+r.width/2,y=r.y+r.height/2;assert(x>=0&&x<=1600&&y>=0&&y<=1000,'required native control must be visible');await send('Input.dispatchMouseEvent',{type:'mousePressed',x,y,button:'left',clickCount:1});await send('Input.dispatchMouseEvent',{type:'mouseReleased',x,y,button:'left',clickCount:1});await pause()};
 const assertBody=async initial=>assert.deepEqual(await body(),initial,'chat and reading must preserve native document identity revision dirty and state');
 await send('Page.navigate',{url:address.origin+'/project'});
 await wait("window.nativeReadingReady?.phase==='ready'&&!!window.nativeWorkspace",120000);
 const initial=await body();result.initialBody=initial;
 assert.equal(initial.dirty,false,'test refuses an already-dirty document');
 const catalog=(await state()).sources;assert.equal(catalog.length,2,'refuse non-fixture project materials');
 assert.deepEqual(catalog.map(s=>s.title).sort(),['社区联系记录-v1.txt','研究说明.pdf'].sort());
 const pdf=catalog.find(s=>s.title.endsWith('.pdf')),txt=catalog.find(s=>s.title.endsWith('.txt'));assert(pdf&&txt);
 const payloads=await e(`Promise.all(${JSON.stringify(catalog)}.map(async s=>{const r=await fetch('/native-reading/workspace/source',{method:'POST',headers:{'X-Workspace-Token':window.NATIVE_WORKSPACE_TOKEN,'Content-Type':'application/json'},body:JSON.stringify({sourceId:s.id,version:s.version})});if(!r.ok)throw Error('fixture verification failed');return (await r.json()).payload}))`);
 const txtText='四个社区表示愿意在本期协助研究团队开展联系与招募。';
 assert.equal(payloads.find(p=>p.op==='source.openText')?.text,txtText);
 const retained=JSON.parse(await readFile(path.join(here,'fixtures','source.json'),'utf8'));
 assert.deepEqual(payloads.find(p=>p.op==='source.open')?.pages,retained.pages);
 const manifest=JSON.parse(await readFile(path.join(here,'fixtures','manifest.json'),'utf8'));
 const originals=new Map([[pdf.id,manifest.pages.map(p=>p.text).join('\n')],[txt.id,txtText]]);
 record('real provider input restricted to unchanged two generated fictional fixtures');
 // An existing session means an earlier attempt already sent something: preserve it and do not resend.
 if(!resumeUi)assert.equal((await state()).messages.length,0,'fresh instance required; refusing to retry an existing conversation');
 if(resumeUi&&process.env.AI_QA_SESSION_ID){
   let controls=await ws();if(!controls.chatOpen)await click(controls.chatToggleRect);
   await pause();const widthBefore=(await ws()).chatRect.width;
   const sessionId=process.env.AI_QA_SESSION_ID;
   assert((await state()).sessions.some(s=>s.id===sessionId),'explicit existing acceptance session required');
   await e(`nativeWorkspace.act({kind:'selectSession',sessionId:${JSON.stringify(sessionId)}})`);
   await pause();const widthAfter=(await ws()).chatRect.width;
   result.chatWidths={before:widthBefore,after:widthAfter};
   assert(Math.abs(widthBefore-widthAfter)<=1,'long real session title must not expand chat panel');
   await screenshot('long-session-title-fixed');record('long real conversation title preserves chosen chat panel width');
 }
 const conversation=(await state()).currentSessionId;result.sessionId=conversation;
 let ui=await ws();if(!ui.chatOpen)await click(ui.chatToggleRect);
 await wait("(await wordcraftHost.engine({op:'workspace.status'})).chatOpen");
 assert.equal((await ws()).resourcesOpen,ui.resourcesOpen);record('opening chat preserves independent resource panel');
 const sendQuestion=async(question,expectedSource)=>{
   assert(result.attemptedSends<3,'live send budget exceeded');
   const before=await state();assert.equal(before.busy,false);assert.equal(before.error,'');
   let controls=await ws();await click(controls.composerRect);await send('Input.insertText',{text:question});
   await wait(`(await wordcraftHost.engine({op:'workspace.status'})).draft===${JSON.stringify(question)}`,5000);await assertBody(initial);
   await screenshot('before-send-'+(result.attemptedSends+1));controls=await ws();result.attemptedSends++;
   await click(controls.sendRect);
   await wait(`(await nativeWorkspace.state()).messages.some(m=>m.role==='user'&&m.text===${JSON.stringify(question)})||(await nativeWorkspace.state()).error`,10000);
   await wait(`!(await nativeWorkspace.state()).busy&&((await nativeWorkspace.state()).messages.length>${before.messages.length+1}||!!(await nativeWorkspace.state()).error)`,180000);
   const after=await state();result.lastState=after;assert.equal(after.error,'','failed model request is retained and never retried');
   const answer=after.messages.findLast(m=>m.role==='assistant');assert(answer&&answer.id!==before.messages.findLast(m=>m.role==='assistant')?.id,'new assistant turn required');
   result.answers.push(answer);assert.equal(answer.runMode,'real');assert(answer.modelId);result.realModelReceipts++;
   await wait("!(await wordcraftHost.engine({op:'workspace.status'})).busy",10000);
   assert(answer.usedSources?.some(s=>s.sourceId===expectedSource),'expected source must be disclosed in used context');
   assert(answer.citations?.length,'the request must produce at least one literal source quotation');
   for(const citation of answer.citations){assert(originals.get(citation.sourceId)?.includes(citation.quote),'every quoted citation must be a literal substring of a permitted fixture');assert.equal(citation.version,catalog.find(s=>s.id===citation.sourceId)?.version);}
   await assertBody(initial);await screenshot('answer-'+result.attemptedSends);record('turn '+result.attemptedSends+' has real receipt, visible used sources and literal version-bound quotations');return answer;
 };
 const addContext=async source=>{
   await click((await ws()).contextMenuRect);
   await wait(`!!(await wordcraftHost.engine({op:'workspace.status'})).contextAddRects[${JSON.stringify(source.id)}]`,5000);
   await click((await ws()).contextAddRects[source.id]);
   await wait(`(await nativeWorkspace.state()).contextSources.some(s=>s.id===${JSON.stringify(source.id)}&&s.version===${JSON.stringify(source.version)})`,10000);
   await wait(`(await wordcraftHost.engine({op:'workspace.status'})).contextSources.some(s=>s.id===${JSON.stringify(source.id)})`,5000);
 };
 let first,second,third;
 if(resumeUi){
   const answers=(await state()).messages.filter(m=>m.role==='assistant'&&m.runMode==='real');assert.equal(answers.length,3,'UI-only continuation requires existing three completed real turns');
   [first,second,third]=answers;result.answers=answers;record('UI-only continuation reuses three existing receipts; zero new model sends');
 }else{
 first=await sendQuestion('研究说明中，研究内容与研究目标需要什么关系？请引用一处资料原句并简短解释。',pdf.id);
 await addContext(pdf);record('native Add material menu pins PDF visibly in current conversation');
 second=await sendQuestion('刚才说的这个关系，请换成更容易理解的话解释，并引用相关原句；不要引入资料没有说的事实。',pdf.id);
 assert(second.historyUsage?.included_turns>=1,'Go must attest that the follow-up actually includes earlier turns');
 assert((await state()).contextSources.some(s=>s.id===pdf.id));record('follow-up retains pinned exact-version source and Go attests actual prior-turn inclusion');
 await click((await ws()).contextRemoveRects[pdf.id]);
 await wait(`!(await nativeWorkspace.state()).contextSources.some(s=>s.id===${JSON.stringify(pdf.id)})`,10000);
 await addContext(txt);await screenshot('context-switched');record('native remove and add controls visibly switch pinned context without deleting history');
 third=await sendQuestion('社区联系记录中，有几个社区愿意协助？具体协助什么？请引用原句。',txt.id);
 assert(third.usedSources.every(s=>s.sourceId!==pdf.id),'removed PDF must not re-enter later current context');
 assert(third.usedSources.some(s=>s.sourceId===txt.id));record('removed PDF excluded from third-turn context while original answers remain');
 }
 await e("(()=>{window.__qaWheelEvents=[];window.addEventListener('wheel',event=>window.__qaWheelEvents.push({x:event.clientX,y:event.clientY,dy:event.deltaY}),{capture:true,passive:true});return true})()");
 result.scrollTrace=[];
 const oldCitation=first.citations.find(c=>c.sourceId===pdf.id);assert(oldCitation);
 const citationKey=JSON.stringify([first.id,oldCitation.sourceId,oldCitation.version,oldCitation.page??null]);
 let citationRect;
 for(let i=0;i<20;i++){
   ui=await ws();const r=ui.citationOpenRects?.[citationKey];result.scrollTrace.push({i,rect:r,events:await e('window.__qaWheelEvents.splice(0)')});
   if(r&&r.y>=ui.chatRect.y+120&&r.y+r.height<ui.composerRect.y){citationRect=r;break;}
   const x=ui.chatRect.x+ui.chatRect.width/2,y=Math.min(ui.composerRect.y-60,ui.chatRect.y+ui.chatRect.height*0.55);
   await send('Input.dispatchMouseEvent',{type:'mouseMoved',x,y});await pause();await send('Input.dispatchMouseEvent',{type:'mouseWheel',x,y,deltaX:0,deltaY:-350});await pause();
 }
 assert(citationRect,'old answer citation must be reachable by ordinary chat scrolling');await click(citationRect);
 await wait(`(await wordcraftHost.engine({op:'source.status'})).sourceId===${JSON.stringify(pdf.id)}&&(await wordcraftHost.engine({op:'source.status'})).version===${JSON.stringify(pdf.version)}`,10000);
 const opened=await e("wordcraftHost.engine({op:'source.status'})");if(oldCitation.page)assert.equal(opened.page,oldCitation.page);
 assert(!(await state()).contextSources.some(s=>s.id===pdf.id),'opening an old citation must not re-add removed context');
 await assertBody(initial);await screenshot('old-citation-original');record('real click on an old answer opens exact-version original/page without re-adding context or modifying body');
 // Scroll down to the text citation and open it twice: the second hit uses the cached reader.
 const txtCitation=third.citations.find(c=>c.sourceId===txt.id);assert(txtCitation);
 const txtKey=JSON.stringify([third.id,txtCitation.sourceId,txtCitation.version,txtCitation.page??null]);
 for(let hit=0;hit<2;hit++){
   let found;
   for(let i=0;i<20;i++){
     ui=await ws();const r=ui.citationOpenRects?.[txtKey];
     if(r&&r.y>=ui.chatRect.y+120&&r.y+r.height<ui.composerRect.y){found=r;break;}
     const x=ui.chatRect.x+ui.chatRect.width/2,y=Math.min(ui.composerRect.y-60,ui.chatRect.y+ui.chatRect.height*0.55);
     await send('Input.dispatchMouseEvent',{type:'mouseMoved',x,y});await pause();await send('Input.dispatchMouseEvent',{type:'mouseWheel',x,y,deltaX:0,deltaY:350});await pause();
   }
   assert(found,'text citation must be reachable');await click(found);
   await wait(`(await wordcraftHost.engine({op:'source.status'})).sourceId===${JSON.stringify(txt.id)}&&(await wordcraftHost.engine({op:'source.status'})).version===${JSON.stringify(txt.version)}`,10000);
   assert.equal((await e("wordcraftHost.engine({op:'source.status'})")).page,null);await assertBody(initial);
 }
 await screenshot('text-citation-cached');record('text citation opens without a page number and opens again from cache, body unchanged');
 const beforeSwitch=await state();await e("nativeWorkspace.act({kind:'newSession'})");assert.equal((await state()).messages.length,0);assert.equal((await state()).contextSources.length,0);
 await e(`nativeWorkspace.act({kind:'selectSession',sessionId:${JSON.stringify(conversation)}})`);assert.deepEqual((await state()).messages,beforeSwitch.messages);assert.deepEqual((await state()).contextSources,beforeSwitch.contextSources);record('separate conversation starts empty and switching back preserves history and context');
 const marker='qa-'+Date.now();await e(`(()=>{window.__qaReloadMarker=${JSON.stringify(marker)};return true})()`);await send('Page.reload',{ignoreCache:true});
 await wait(`window.__qaReloadMarker!==${JSON.stringify(marker)}&&window.nativeReadingReady?.phase==='ready'&&!!window.nativeWorkspace`,120000);
 const restored=await state();assert.equal(restored.currentSessionId,conversation);assert.deepEqual(restored.messages,beforeSwitch.messages);assert.deepEqual(restored.contextSources,beforeSwitch.contextSources);await screenshot('reloaded-chat');record('refresh retains full conversation and only remaining pinned context');
 result.final=restored;
 if(process.argv.includes('--prepare-user')){await e("nativeWorkspace.act({kind:'newSession'})");await wait("(await wordcraftHost.engine({op:'workspace.status'})).messageCount===0",10000);await screenshot('ready-for-user');result.userSession=await state();assert.equal(result.userSession.messages.length,0);assert.equal(result.userSession.contextSources.length,0);record('fresh empty user conversation prepared while completed acceptance history remains');}
 result.errors=browser.errors;assert.equal(browser.errors.length,0);result.passed=true;
}catch(error){result.error=error.stack;process.exitCode=1;if(browser)try{result.failureState=await browser.evaluate("({ready:window.nativeReadingReady,workspace:window.nativeWorkspace?await nativeWorkspace.state():null,ui:window.wordcraftHost?await wordcraftHost.engine({op:'workspace.status'}):null})");await browser.screenshot(path.join(out,'failure.png'))}catch(e){result.captureError=e.message}}
finally{if(browser)try{await browser.close()}catch(error){result.cleanupError=error.message;result.passed=false;process.exitCode=1}await writeFile(path.join(out,'verification.json'),JSON.stringify(result,null,2));console.log(JSON.stringify({out,passed:result.passed,checks:result.checks.length,attemptedSends:result.attemptedSends,realModelReceipts:result.realModelReceipts,error:result.error}));}



