import assert from 'node:assert/strict';
import {mkdir,readFile,readdir,writeFile} from 'node:fs/promises';
import {createHash} from 'node:crypto';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {browserClient} from './browser-client.mjs';
const root=path.dirname(fileURLToPath(import.meta.url)),runId=new Date().toISOString().replaceAll(':','-');
const out=path.join(root,'artifacts','reopen-'+runId),downloads=path.join(out,'downloads');await mkdir(downloads,{recursive:true});
const url=process.argv[2]||'http://127.0.0.1:7211';const result={runId,checks:[],passed:false};let b;
try{
  const deadline=Date.now()+120000;while(true){try{if((await fetch(url+'/integration/health').then(r=>r.json())).ready)break}catch{}if(Date.now()>deadline)throw Error('Service not ready');await new Promise(r=>setTimeout(r,400))}
  b=await browserClient(path.join(out,'edge'));const {evaluate:e,wait,send}=b;
  const check=async(expression,label)=>{assert(await e(expression),label);result.checks.push(label);console.log('PASS '+label)};
  const click=async label=>e(`(()=>{const b=[...document.querySelectorAll('button')].find(x=>x.getClientRects().length&&x.textContent.trim()===${JSON.stringify(label)});if(!b)throw Error('Button absent');b.click();return true})()`);
  await send('Page.navigate',{url});await wait(`location.origin===${JSON.stringify(url)} && !!document.querySelector('.top-nav')`);
  await e(`(async()=>{window.c=await fetch('/integration/config').then(r=>r.json());window.read=()=>fetch('/integration/state',{headers:{'X-Integration-Token':c.token}}).then(r=>r.json());return true})()`);
  await check(`(await read()).records.C01.progress==='completed' && (await read()).records.R2.progress==='deferred' && (await read()).records.METHOD.progress==='completed'`,'full service restart restores human decisions');
  await check(`(await read()).sourceLatest===2 && (await read()).sourceVersions.v1.sourceId !== (await read()).sourceVersions.v2.sourceId`,'both Go material identities persist after restart');
  await e(`document.querySelector('.top-nav button:nth-child(2)').click()`);await wait(`!!document.querySelector('iframe')`);
  await e(`(()=>{window.frame=document.querySelector('iframe');window.h=()=>frame.contentWindow.wordcraftHost;return true})()`);
  await wait(`h() && (()=>{try{return !!JSON.parse(frame.contentDocument.querySelector('#host-log').textContent).opened}catch{return false}})()`);
  await check(`JSON.stringify(await h().engine({op:'inspect'})).includes('人工处理说明') && !(await h().engine({op:'status'})).dirty`,'saved native edits reopen from real DOCX after Go and DS restart');
  await e(`(async()=>{const s=await h().engine({op:'status'});await h().engine({op:'focusSection',sectionId:'research-content',expectedDocumentId:s.documentId,expectedRevision:s.revision});const a=await h().engine({op:'selection'});const focus={...a.selection.focus,off:a.selection.focus.off+21};await h().engine({op:'select',range:{anchor:a.selection.anchor,focus},expectedDocumentId:a.state.documentId,expectedRevision:a.state.revision});return true})()`);
  await check(`(await h().engine({op:'selection'})).selection.text.length>0`,'native range selection yields actual source text');
  await click('绑定当前原生选区');
  await wait(`(async()=>!!(await read()).records.C01.anchor?.hostSessionId)()`);
  await check(`(await read()).records.C01.anchor.excerpt === (await h().engine({op:'selection'})).selection.text`,'human opinion binds exact current native selection and persists excerpt');
  await e(`window.nativeBefore=await h().engine({op:'status'})`);
  await click('资料');
  await check(`document.querySelector('.source-panel').hidden && !document.querySelector('.right-panel').hidden`,'source panel closes independently');
  await click('资料');
  await click('意见与助手');
  await check(`!document.querySelector('.source-panel').hidden && document.querySelector('.right-panel').hidden`,'right panel closes independently');
  await click('意见与助手');
  await e(`window.oldWidth=frame.getBoundingClientRect().width`);await click('专注写作');
  await check(`frame.getBoundingClientRect().width>oldWidth && !document.querySelector('.writing-grid').className.includes('with-source')`,'focus mode gives freed space to native writing');
  await click('退出专注');
  await check(`(await h().engine({op:'status'})).documentId===nativeBefore.documentId && (await h().engine({op:'status'})).revision===nativeBefore.revision`,'layout controls preserve native identity and revision');
  await b.screenshot(path.join(out,'writing.png'));
  await send('Page.setDownloadBehavior',{behavior:'allow',downloadPath:downloads});await click('导出副本');
  let files=[];for(let i=0;i<80;i++){files=(await readdir(downloads)).filter(x=>x.endsWith('.docx'));if(files.length)break;await new Promise(r=>setTimeout(r,250))}
  assert.equal(files.length,1);const bytes=await readFile(path.join(downloads,files[0]));const version=await e(`await h().goApi('versions/latest')`);
  assert.equal(createHash('sha256').update(bytes).digest('hex'),version.docSha256);result.checks.push('downloaded DOCX equals verified Go saved version');
  const before=await e('performance.timeOrigin');await send('Page.reload');await wait(`performance.timeOrigin!==${before} && !!document.querySelector('.top-nav')`);
  await e(`document.querySelector('.top-nav button:nth-child(2)').click()`);await wait(`!!document.querySelector('.anchor-section')`);
  await check(`document.querySelector('.anchor-section').innerText.includes('需人工重新定位')`,'reopen never treats a previous native session anchor as verified');
  await e(`document.querySelector('.top-nav button:nth-child(3)').click()`);await b.screenshot(path.join(out,'review.png'));
  result.pageErrors=b.errors;assert.equal(b.errors.length,0);result.passed=true;
}catch(error){result.error=error.stack;result.pageErrors=b?.errors;process.exitCode=1;if(b){result.pageText=await b.evaluate('document.body.innerText').catch(()=>null);await b.screenshot(path.join(out,'failure.png')).catch(()=>{})}}
finally{try{await b?.close();result.ownedBrowserStopped=true}catch(error){result.cleanupError=error.message;result.passed=false;process.exitCode=1}await writeFile(path.join(out,'verification.json'),JSON.stringify(result,null,2));console.log(JSON.stringify({out,...result},null,2))}
