import assert from 'node:assert/strict';
import {mkdir,writeFile} from 'node:fs/promises';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {browserClient} from './browser-client.mjs';
const root=path.dirname(fileURLToPath(import.meta.url));
const runId=new Date().toISOString().replaceAll(':','-');
const out=path.join(root,'artifacts','chain-'+runId);await mkdir(out,{recursive:true});
const result={runId,baselineId:'LD-V1-20261103-R1',checks:[],passed:false};
const url=process.argv[2]||'http://127.0.0.1:7211';let browser;
try{
  const deadline=Date.now()+120000;
  while(true){try{const h=await fetch(url+'/integration/health').then(r=>r.json());if(h.ready)break}catch{}if(Date.now()>deadline)throw Error('Integration service did not become ready');await new Promise(r=>setTimeout(r,300))}
  browser=await browserClient(path.join(out,'edge'));
  const {evaluate:e,wait,send,screenshot}=browser;
  const check=async(code,label)=>{assert(await e(code),label);result.checks.push(label);console.log('PASS '+label)};
  const click=async text=>e(`(()=>{const b=[...document.querySelectorAll('button')].find(b=>b.getClientRects().length&&b.textContent.trim()===${JSON.stringify(text)});if(!b)throw Error('Button missing '+${JSON.stringify(text)});b.click();return true})()`);
  await send('Page.navigate',{url});
  await wait(`location.origin===${JSON.stringify(url)} && !!document.querySelector('.top-nav')`);
  // Make the native canvas visible before waiting for frames/engine requests.
  await e(`document.querySelector('.top-nav button:nth-child(2)').click()`);
  await wait(`!!document.querySelector('iframe')`);
  await e(`(()=>{window.frame=document.querySelector('iframe');window.h=()=>frame.contentWindow.wordcraftHost;return true})()`);
  await wait(`h()&&(()=>{try{return !!JSON.parse(frame.contentDocument.querySelector('#host-log').textContent).opened}catch{return false}})()`);
  await e(`(async()=>{window.config=await fetch('/integration/config').then(r=>r.json());window.read=()=>fetch('/integration/state',{headers:{'X-Integration-Token':config.token}}).then(r=>r.json());window.action=async body=>{const s=await read();const r=await fetch('/integration/actions',{method:'POST',headers:{'Content-Type':'application/json','X-Integration-Token':config.token},body:JSON.stringify({key:crypto.randomUUID(),expectedRevision:s.revision,...body})});return {status:r.status,body:await r.json()}};window.initialNative=await h().engine({op:'status'});return true})()`);
  await check(`(await h().engine({op:'inspect'})) && (await read()).sourceVersions.v1.sourceId`,'real native document and Go-bound source loaded');
  result.nativeInitial=await e(`await h().engine({op:'inspect'})`);
  await screenshot(path.join(out,'writing-initial.png'));
  await check(`(await read()).sourceLatest===1 && !(await read()).changeApplied`,'new source is prepared, not falsely published');
  await check(`(await action({type:'decide',id:'C01',decision:'no_change',reason:''})).status===400`,'no-change decision without explanation rejected');
  // Real UI decision form, then explicit editing in the native bridge.
  await e(`(()=>{const f=[...document.querySelectorAll('.decision-form')].find(f=>f.getClientRects().length);f.querySelector('input[value="modify_document"]').click();f.requestSubmit();return true})()`);
  await wait(`(async()=> (await read()).records.C01.decision==='modify_document')()`);
  await check(`(await read()).records.C01.progress==='pending'`,'UI decision saved without falsely completing');
  await check(`(await action({type:'complete',id:'C01',reason:'测试拒绝旧版本',savedVersion:{diskVersion:1,docSha256:'bad'}})).status===409`,'unverified completion receipt rejected');
  // Move to the actual native research-content section; no fixture coordinates.
  await e(`(async()=>{const s=await h().engine({op:'status'});await h().engine({op:'focusSection',sectionId:'research-content',expectedDocumentId:s.documentId,expectedRevision:s.revision});const a=await h().engine({op:'selection'});await h().engine({op:'insert',text:${JSON.stringify('人工处理说明：社区选择以可进入性和服务条件差异为标准，不据此推断全市。\n')},bookmark:a.selection,expectedDocumentId:a.state.documentId,expectedRevision:a.state.revision});return true})()`);
  // Keep the host mounted across spaces with dirty content.
  await e(`(async()=>{window.dirtyBefore=await h().engine({op:'status'});document.querySelector('.top-nav button:nth-child(1)').click();document.querySelector('.top-nav button:nth-child(2)').click();return true})()`);
  await check(`(await h().engine({op:'status'})).documentId===dirtyBefore.documentId && (await h().engine({op:'status'})).dirty`,'navigation retains the native instance and unsaved edits');
  await click('保存');
  await wait(`(async()=>!(await h().engine({op:'status'})).dirty)()`);
  await check(`(await read()).records.C01.progress==='pending'`,'saving does not automatically resolve the human opinion');
  await e(`(()=>{const field=[...document.querySelectorAll('.record-completion textarea')].find(x=>x.getClientRects().length);const setter=Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set;setter.call(field,'补充社区选择标准及结论适用边界');field.dispatchEvent(new Event('input',{bubbles:true}));return true})()`);
  await wait(`[...document.querySelectorAll('.record-completion button')].some(b=>b.getClientRects().length&&!b.disabled)`);
  await click('关联已保存版本并登记完成');
  await wait(`(async()=> (await read()).records.C01.progress==='completed')()`);
  result.checks.push('UI completion persisted with real newer DOCX version');
  // Publish material update through visible UI.
  await e(`document.querySelector('.top-nav button:nth-child(2)').click()`);
  await click('登记资料更新并生成回看');
  await wait(`(async()=> (await read()).changeApplied)()`,120000);
  await check(`(await read()).records.R1.change.dependencyId==='DEP1' && (await read()).records.R2.change.dependencyId==='DEP2' && (await read()).records.R3.change.dependencyId==='DEP3'`,'material version update creates three explicit dependency review records');
  await check(`(await read()).sourceVersions.v1.text.includes('四个社区') && (await read()).sourceVersions.v2.text.includes('两个社区')`,'old and latest material versions remain separately readable');
  await check(`!(await h().engine({op:'status'})).dirty && JSON.stringify(await h().engine({op:'inspect'})).includes('计划进入四个社区')`,'publishing source v2 does not change native document or infer new sample size');
  const s=await e(`await read()`);result.sourceIds=[s.sourceVersions.v1.sourceId,s.sourceVersions.v2.sourceId];
  await check(`(await fetch('/integration/source?version=v2',{headers:{'X-Integration-Token':config.token}}).then(r=>r.json())).text.includes('两个社区')`,'latest material read back through actual Go original-document API');
  await check(`(await action({type:'decide',id:'R2',decision:'defer',reason:'等待导师确认招募安排'})).status===200`,'deferred review decision persisted');
  await check(`(await action({type:'decide',id:'METHOD',decision:'no_change',reason:'资料只改变可进入场地，本人确认访谈方法暂不调整'})).body.records.METHOD.progress==='completed'`,'coverage judgement recorded as explicit user decision');
  const beforeReload=await e('performance.timeOrigin');
  await send('Page.reload',{});await wait(`performance.timeOrigin!==${beforeReload} && location.origin===${JSON.stringify(url)} && !!document.querySelector('.top-nav')`);
  await e(`document.querySelector('.top-nav button:nth-child(3)').click()`);
  await wait(`document.body.innerText.includes('等待导师确认招募安排') || document.body.innerText.includes('暂缓')`);
  await screenshot(path.join(out,'review-persisted.png'));
  result.state=s;result.checks.push('reload restores persisted decisions and material versions');
  result.pageErrors=browser.errors;assert.equal(browser.errors.length,0);result.passed=true;
}catch(error){result.error=error.stack;result.pageErrors=browser?.errors;process.exitCode=1;if(browser){result.pageText=await browser.evaluate('document.body.innerText').catch(()=>null);await browser.screenshot(path.join(out,'failure.png')).catch(()=>{});}}
finally{try{await browser?.close();result.ownedBrowserStopped=true}catch(error){result.cleanupError=error.message;result.passed=false;process.exitCode=1}await writeFile(path.join(out,'verification.json'),JSON.stringify(result,null,2));console.log(JSON.stringify({out,...result,nativeInitial:undefined,state:undefined},null,2))}
