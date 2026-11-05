// Independent acceptance of the controlled-writing slice. Uses an isolated
// real Go/DS/WASM/browser runtime; never connects to the user's 7210 database.
import assert from 'node:assert/strict';
import {createServer} from 'node:net';
import {mkdir,writeFile} from 'node:fs/promises';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {startIntegration} from './server.mjs';
import {browserClient} from './browser-client.mjs';

const here=path.dirname(fileURLToPath(import.meta.url));
const runId=new Date().toISOString().replaceAll(':','-')+'-'+process.pid;
const out=path.join(here,'artifacts','controlled-'+runId);
const directory=path.join(out,'runtime');
const generationOnly=process.argv.includes('--generation-only');
const nativeOnly=process.argv.includes('--native-only');
const workflowOnly=process.argv.includes('--workflow-only');
if([generationOnly,nativeOnly,workflowOnly].filter(Boolean).length>1)throw Error('Choose at most one acceptance scope');
const scope=generationOnly?'generation-only':nativeOnly?'native-only':workflowOnly?'workflow-only':'full';
const result={runId,kind:`isolated real controlled-writing ${scope}`,scope,passed:false,scopePassed:false,checks:[],unverified:[],pageErrors:[]};
let app,browser;
async function freePort(){
  const s=createServer();await new Promise((resolve,reject)=>s.once('error',reject).listen(0,'127.0.0.1',resolve));
  const port=s.address().port;await new Promise(resolve=>s.close(resolve));return port;
}

try{runFlow:{
  await mkdir(out,{recursive:true});
  app=await startIntegration({directory,port:await freePort()});
  result.runtime={origin:app.origin,projectId:app.projectId,runtimeId:app.runtimeId};
  browser=await browserClient(path.join(out,'edge'));
  const {evaluate:e,wait,send}=browser;
  const check=async(expression,label)=>{assert(await e(expression),label);result.checks.push(label);console.log('PASS '+label)};
  await send('Page.navigate',{url:app.origin});
  await wait(`location.origin===${JSON.stringify(app.origin)} && !!document.querySelector('.top-nav')`);
  await e(`document.querySelector('.top-nav button:nth-child(2)').click()`);
  await wait(`!!document.querySelector('iframe')`);
  await e(`(()=>{window.frame=document.querySelector('iframe');window.h=()=>frame.contentWindow.wordcraftHost;return true})()`);
  await wait(`h()&&(()=>{try{return !!JSON.parse(frame.contentDocument.querySelector('#host-log').textContent).opened}catch{return false}})()`);
  await check(`(await h().engine({op:'status'})).documentId != null`,'real native editor is ready');
  result.initial=await e(`(async()=>({state:await h().engine({op:'status'}),document:await h().engine({op:'inspect'})}))()`);
  await browser.screenshot(path.join(out,'initial.png'));

  // Geometry locates the fixture's actual paragraph path; edits are not based
  // on hard-coded canvas coordinates or text occurrence guesses.
  await e(`(async()=>{
    window.paragraphs=async()=>{const g=await h().engine({op:'geometry'});return [...new Map(g.rows.filter(r=>r.area==='body'&&typeof r.text==='string').map(r=>[JSON.stringify(r.path),{path:r.path,text:r.text,styles:r.styles}])).values()]};
    window.initialParagraphs=await paragraphs();
    const found=initialParagraphs.filter(r=>r.text.startsWith('样本选择：'));
    if(found.length!==1)throw Error('Fixture target is absent or ambiguous');
    window.targetPara=found[0];
    window.selectTarget=async()=>{const s=await h().engine({op:'status'});const anchor={story:'body',path:targetPara.path,off:0},focus={...anchor,off:new TextEncoder().encode(targetPara.text).length};await h().engine({op:'select',range:{anchor,focus},expectedDocumentId:s.documentId,expectedRevision:s.revision});return h().engine({op:'selection'})};
    window.snapshot=async()=>({state:await h().engine({op:'status'}),paragraphs:await paragraphs()});
    window.attempt=async request=>{try{return {accepted:true,result:await h().engine(request)}}catch(error){return {accepted:false,error:error.message}}};
    const selected=await selectTarget();
    window.nativeRequest={op:'replaceRange',documentId:selected.state.documentId,revision:selected.state.revision,anchor:selected.selection.anchor,focus:selected.selection.focus,expectedText:selected.selection.text,text:'样本选择：本次仅研究已联系社区的受访者，不据此概括全市情况。🔬',operationId:'independent-native-'+crypto.randomUUID()};
    window.preNative=await snapshot();return true;
  })()`);
  if(!generationOnly&&!workflowOnly){
  await check(`!(await attempt({...nativeRequest,operationId:'invalid-text-'+crypto.randomUUID(),expectedText:'not the selected text'})).accepted`,'mismatched target text is rejected');
  await check(`!(await attempt({...nativeRequest,operationId:'invalid-range-'+crypto.randomUUID(),focus:{...nativeRequest.focus,off:nativeRequest.focus.off+100000}})).accepted`,'out-of-bounds range is rejected instead of clamped');
  await check(`!(await attempt({...nativeRequest,operationId:'invalid-utf8-'+crypto.randomUUID(),anchor:{...nativeRequest.anchor,off:1}})).accepted`,'non-UTF8 character boundary is rejected');
  await check(`!(await attempt({...nativeRequest,operationId:'invalid-lines-'+crypto.randomUUID(),text:'line one\\r\\nline two'})).accepted`,'unsupported paragraph-changing replacement is rejected');
  await check(`JSON.stringify(await snapshot())===JSON.stringify(preNative)`,'rejected operations preserve document content and state');
  await e(`window.nativeApplied=await h().engine(nativeRequest)`);
  await check(`nativeApplied.status==='applied' && (await paragraphs()).find(r=>JSON.stringify(r.path)===JSON.stringify(targetPara.path)).text===nativeRequest.text`,'real WASM applies exact Unicode replacement inside the fixture table');
  await check(`JSON.stringify((await paragraphs()).filter(r=>JSON.stringify(r.path)!==JSON.stringify(targetPara.path)))===JSON.stringify(initialParagraphs.filter(r=>JSON.stringify(r.path)!==JSON.stringify(targetPara.path)))`,'unrelated paragraph text and font styles remain unchanged');
  await e(`(async()=>{window.appliedSnapshot=await snapshot();window.repeatReceipt=await h().engine(nativeRequest);return true})()`);
  await check(`JSON.stringify(await snapshot())===JSON.stringify(appliedSnapshot) && repeatReceipt.operationId===nativeApplied.operationId`,'replayed apply returns its receipt without a second document edit');
  await check(`!(await attempt({...nativeRequest,operationId:'old-revision-'+crypto.randomUUID()})).accepted`,'a different operation with a stale revision is rejected');
  await check(`!(await attempt({...nativeRequest,text:'different payload'})).accepted`,'operation identity cannot be reused for another payload');
  await e(`window.undoReceipt=await h().engine({op:'undoOperation',operationId:nativeRequest.operationId,documentId:nativeRequest.documentId,expectedRevision:(await h().engine({op:'status'})).revision})`);
  await check(`JSON.stringify(await paragraphs())===JSON.stringify(initialParagraphs)`,'one task undo restores original text and styles');
  await e(`(async()=>{window.afterUndo=await snapshot();window.replayUndone=await h().engine(nativeRequest);return true})()`);
  await check(`replayUndone.status==='undone'&&JSON.stringify(await snapshot())===JSON.stringify(afterUndo)`,'retrying an undone operation does not silently reapply it');
  await e(`window.redoReceipt=await h().engine({op:'redoOperation',operationId:nativeRequest.operationId,documentId:nativeRequest.documentId,expectedRevision:(await h().engine({op:'status'})).revision})`);
  await check(`(await paragraphs()).find(r=>JSON.stringify(r.path)===JSON.stringify(targetPara.path)).text===nativeRequest.text`,'task redo restores precisely the accepted replacement');
  await e(`await h().engine({op:'undoOperation',operationId:nativeRequest.operationId,documentId:nativeRequest.documentId,expectedRevision:(await h().engine({op:'status'})).revision})`);
  await e(`await selectTarget()`);
  }
  if(nativeOnly){
    await browser.screenshot(path.join(out,'native-guards-complete.png'));
    await e(`(async()=>{
      const m=await import('/integration/native-adapter.js');window.testAdapter=m.createNativeAdapter({hostWindow:frame.contentWindow});
      const s=await h().engine({op:'status'});
      window.persistenceRequest={...nativeRequest,revision:s.revision,operationId:'native-persistence-'+crypto.randomUUID()};
      await h().engine(persistenceRequest);
      window.originalHostFetch=frame.contentWindow.fetch;
      frame.contentWindow.fetch=(input,options)=>{const url=new URL(input instanceof frame.contentWindow.Request?input.url:String(input),frame.contentWindow.location.href);return url.pathname==='/api/save'?Promise.reject(Error('independent native persistence outage')):originalHostFetch(input,options)};
      window.saveRejected=await testAdapter.save().then(()=>false,()=>true);return true;
    })()`);
    await check(`saveRejected&&testAdapter.getState().phase==='save_failed'&&(await h().engine({op:'status'})).dirty`,'controlled DS outage preserves the native edit and reports save failure');
    await e(`(async()=>{frame.contentWindow.fetch=originalHostFetch;await testAdapter.save();window.persistedNative=testAdapter.getState();return true})()`);
    await check(`persistedNative.phase==='saved'&&persistedNative.saved.verified&&!(await h().engine({op:'status'})).dirty`,'real native save retry completes DS/Go verification');
    await check(`(async()=>{const ds=await h().api('version',{diskVersion:persistedNative.saved.diskVersion}),go=await h().goApi('versions/latest');return ds.sha256===go.docSha256&&go.docSha256===persistedNative.saved.sha256})()`,'persisted DOCX digest agrees between DS, Go and native adapter');
    result.nativePersistence=await e(`({text:persistenceRequest.text,saved:persistedNative.saved})`);
    await browser.screenshot(path.join(out,'native-persisted.png'));
    const port=Number(new URL(app.origin).port);
    await send('Page.navigate',{url:'about:blank'});await app.stop();app=null;
    app=await startIntegration({directory,port});
    await send('Page.navigate',{url:app.origin});
    await wait(`location.origin===${JSON.stringify(app.origin)}&&!!document.querySelector('.top-nav')`);
    await e(`document.querySelector('.top-nav button:nth-child(2)').click()`);await wait(`!!document.querySelector('iframe')`);
    await e(`(()=>{window.frame=document.querySelector('iframe');window.h=()=>frame.contentWindow.wordcraftHost;return true})()`);
    await wait(`h()&&(()=>{try{return !!JSON.parse(frame.contentDocument.querySelector('#host-log').textContent).opened}catch{return false}})()`);
    await check(`(await h().engine({op:'geometry'})).rows.some(r=>r.text===${JSON.stringify(result.nativePersistence.text)})&&!(await h().engine({op:'status'})).dirty`,'full service restart reopens the exact controlled replacement from DOCX');
    await check(`(await h().goApi('versions/latest')).docSha256===${JSON.stringify(result.nativePersistence.saved.sha256)}`,'reopened Go version retains the verified DOCX digest');
    await browser.screenshot(path.join(out,'native-reopened.png'));
    result.unverified.push('real AI/UI candidate flow','source review');result.scopePassed=true;result.outcome='partial';break runFlow;
  }

  const click=async testId=>{
    await wait(`(()=>{const b=document.querySelector('[data-testid="${testId}"]');return b&&b.getClientRects().length&&!b.disabled})()`,15000);
    const p=await e(`(()=>{const b=document.querySelector('[data-testid="${testId}"]');if(!b||b.disabled)throw Error('Control unavailable: ${testId}');b.scrollIntoView({block:'center'});const r=b.getBoundingClientRect();return {x:r.x+r.width/2,y:r.y+r.height/2}})()`);
    await send('Input.dispatchMouseEvent',{type:'mousePressed',button:'left',clickCount:1,...p});
    await send('Input.dispatchMouseEvent',{type:'mouseReleased',button:'left',clickCount:1,...p});
  };
  await e(`(()=>{const b=[...document.querySelectorAll('button')].find(b=>b.getClientRects().length&&b.textContent.trim()==='助手');if(!b)throw Error('Assistant tab missing');b.click();return true})()`);
  await wait(`!!document.querySelector('[data-testid="cw-capture"]')`);
  await click('cw-capture');
  await e(`(()=>{const field=document.querySelector('[data-testid="cw-instruction"]');Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set.call(field,'保留四个社区与问卷访谈安排，将全市结论收窄为已联系社区的受访者，不新增事实或数字，只返回一段替换文字。');field.dispatchEvent(new Event('input',{bubbles:true}));return true})()`);
  await check(`document.querySelector('[data-testid="cw-generate"]').disabled`,'AI generation requires explicit context confirmation');
  await click('cw-source-v1');
  await click('cw-context-confirm');
  await e(`(async()=>{window.beforeGeneration=await snapshot();window.config=await fetch('/integration/config').then(r=>r.json());window.workflow=()=>fetch('/integration/workflow',{headers:{'X-Integration-Token':config.token}}).then(r=>r.json());window.chain=()=>fetch('/integration/state',{headers:{'X-Integration-Token':config.token}}).then(r=>r.json());window.act=async body=>{const state=await chain();const response=await fetch('/integration/actions',{method:'POST',headers:{'Content-Type':'application/json','X-Integration-Token':config.token},body:JSON.stringify({key:crypto.randomUUID(),expectedRevision:state.revision,...body})});return {status:response.status,body:await response.json()}};return true})()`);
  await click('cw-generate');
  await wait(`document.querySelector('[data-testid="cw-candidate"]')?.dataset.status==='proposed'||!!document.querySelector('.controlled-error')`,150000);
  await check(`document.querySelector('[data-testid="cw-candidate"]')?.dataset.status==='proposed'`,'real model returns a reviewable candidate');
  await check(`JSON.stringify(await snapshot())===JSON.stringify(beforeGeneration)`,'generating and displaying the candidate does not change native text or revision');
  await e(`window.candidate=(await workflow()).candidates.at(-1)`);
  await check(`candidate.modelReceipt?.runMode==='real'&&candidate.modelReceipt?.turnId&&candidate.context.sources.length===1&&candidate.context.sources[0].versionId==='v1'&&!candidate.context.comment`,'candidate records real model receipt and only the explicitly authorized material');
  result.modelReceipt=await e(`candidate.modelReceipt`);
  result.candidate={original:await e(`candidate.original`),replacement:await e(`candidate.replacement`)};
  await browser.screenshot(path.join(out,'candidate-before-accept.png'));
  if(generationOnly){result.unverified.push('native apply/undo','save/reopen','source review');result.scopePassed=true;result.outcome='partial';break runFlow;}
  await click('cw-accept');
  await wait(`['applied','saved'].includes(document.querySelector('[data-testid="cw-candidate"]')?.dataset.status)||!!document.querySelector('.controlled-error')`);
  await check(`document.querySelector('[data-testid="cw-candidate"]')?.dataset.status==='applied'`,'user accepts through UI and receives actual native application receipt');
  await check(`(await paragraphs()).find(r=>JSON.stringify(r.path)===JSON.stringify(targetPara.path)).text===candidate.replacement`,'native document equals the displayed candidate exactly');
  await check(`JSON.stringify((await paragraphs()).filter(r=>JSON.stringify(r.path)!==JSON.stringify(targetPara.path)))===JSON.stringify(initialParagraphs.filter(r=>JSON.stringify(r.path)!==JSON.stringify(targetPara.path)))`,'AI candidate application preserves neighboring paragraphs');
  await e(`(()=>{window.originalHostFetch=frame.contentWindow.fetch;frame.contentWindow.fetch=(input,options)=>{const url=new URL(input instanceof frame.contentWindow.Request?input.url:String(input),frame.contentWindow.location.href);return url.pathname==='/api/save'?Promise.reject(Error('independent controlled save outage')):originalHostFetch(input,options)};return true})()`);
  await click('cw-save');
  await wait(`!!document.querySelector('.controlled-error')`);
  await check(`(await h().engine({op:'status'})).dirty && document.querySelector('[data-testid="cw-candidate"]').dataset.status==='applied'`,'save failure preserves native dirty content and does not mark candidate saved');
  await e(`(()=>{frame.contentWindow.fetch=originalHostFetch;return true})()`);
  await click('cw-save');
  await wait(`document.querySelector('[data-testid="cw-candidate"]')?.dataset.status==='saved'||!!document.querySelector('.controlled-error')`);
  await check(`document.querySelector('[data-testid="cw-candidate"]')?.dataset.status==='saved'&&!(await h().engine({op:'status'})).dirty`,'save result is linked only after native coordinator verifies persistence');
  await check(`(await chain()).records.C01.progress!=='completed'`,'a candidate save does not automatically close a separate human opinion');
  result.savedCandidate=await e(`(await workflow()).candidates.at(-1)`);
  await browser.screenshot(path.join(out,'accepted-and-saved.png'));
  await e(`window.beforePublication=await snapshot()`);
  await check(`(await act({type:'publish_source'})).status===200`,'source version update persists through the real source service');
  await check(`JSON.stringify(await snapshot())===JSON.stringify(beforePublication)`,'source publication does not silently modify native document');
  await check(`(await chain()).records.R1.progress==='awaiting_decision' && (await chain()).sourceVersions.v1.sourceId!==(await chain()).sourceVersions.v2.sourceId`,'source update retains both source identities and creates an explicit review');
  await check(`(await act({type:'decide',id:'R1',decision:'defer',reason:'等待核实新的可进入社区范围'})).status===200`,'human defer decision is durably recorded');
  const persistedOrigin=app.origin;
  await send('Page.navigate',{url:'about:blank'});
  await app.stop();app=null;
  app=await startIntegration({directory,port:Number(new URL(persistedOrigin).port)});
  await send('Page.navigate',{url:app.origin});
  await wait(`location.origin===${JSON.stringify(app.origin)}&&!!document.querySelector('.top-nav')`);
  await e(`document.querySelector('.top-nav button:nth-child(2)').click()`);
  await wait(`!!document.querySelector('iframe')`);
  await e(`(()=>{window.frame=document.querySelector('iframe');window.h=()=>frame.contentWindow.wordcraftHost;return true})()`);
  await wait(`h()&&(()=>{try{return !!JSON.parse(frame.contentDocument.querySelector('#host-log').textContent).opened}catch{return false}})()`);
  await check(`(await h().engine({op:'geometry'})).rows.some(r=>r.text===${JSON.stringify(result.candidate.replacement)})&&!(await h().engine({op:'status'})).dirty`,'full runtime restart reopens the exact accepted candidate from DOCX');
  await e(`(async()=>{window.config=await fetch('/integration/config').then(r=>r.json());window.workflow=()=>fetch('/integration/workflow',{headers:{'X-Integration-Token':config.token}}).then(r=>r.json());window.chain=()=>fetch('/integration/state',{headers:{'X-Integration-Token':config.token}}).then(r=>r.json());return true})()`);
  await check(`(await workflow()).candidates.at(-1).status==='saved'&&(await chain()).records.R1.progress==='deferred'&&(await chain()).sourceLatest===2`,'candidate history, source versions and human decision survive full restart');
  await browser.screenshot(path.join(out,'reopened.png'));
  assert.equal(browser.errors.length,0,'uncaught browser exceptions');
  result.scopePassed=true;
  result.passed=!workflowOnly;
  if(workflowOnly){result.unverified.push('native guard/undo edge cases are a separate native-only run');result.outcome='partial';}
  }
}catch(error){
  result.error=error.stack;process.exitCode=1;
  if(browser)await browser.screenshot(path.join(out,'failure.png')).catch(()=>{});
}finally{
  if(browser)result.pageErrors=browser.errors;
  try{await browser?.close();result.ownedBrowserStopped=true}catch(error){result.cleanupError=error.message;result.passed=false;process.exitCode=1}
  try{await app?.stop();result.ownedRuntimeStopped=true}catch(error){result.runtimeCleanupError=error.message;result.passed=false;process.exitCode=1}
  if(result.pageErrors.length||result.cleanupError||result.runtimeCleanupError){result.scopePassed=false;result.passed=false;process.exitCode=1;}
  result.completedAt=new Date().toISOString();
  await mkdir(out,{recursive:true});await writeFile(path.join(out,'verification.json'),JSON.stringify(result,null,2));
  console.log(JSON.stringify({out,...result,initial:undefined},null,2));
}
