import assert from 'node:assert/strict';
import {mkdir,writeFile} from 'node:fs/promises';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {browserClient} from './browser-client.mjs';
const url=process.argv[2]||'http://127.0.0.1:7211',readonly=process.argv.includes('--readonly');
const out=path.join(path.dirname(fileURLToPath(import.meta.url)),'artifacts','final-'+new Date().toISOString().replaceAll(':','-'));await mkdir(out,{recursive:true});
const result={url,readonly,checks:[],passed:false};let b;
try{
  for(let i=0;;i++){try{if((await fetch(url+'/integration/health').then(r=>r.json())).ready)break}catch{}if(i>300)throw Error('Service not ready');await new Promise(r=>setTimeout(r,400))}
  b=await browserClient(path.join(out,'edge'));const {evaluate:e,send,wait}=b;
  await send('Page.navigate',{url});await wait(`location.origin===${JSON.stringify(url)} && !!document.querySelector('.top-nav')`);
  await e(`(async()=>{window.c=await fetch('/integration/config').then(r=>r.json());window.read=()=>fetch('/integration/state',{headers:{'X-Integration-Token':c.token}}).then(r=>r.json());return true})()`);
  const before=await e('await read()');
  await new Promise(r=>setTimeout(r,300));await b.screenshot(path.join(out,'overview.png'));
  await e(`document.querySelector('.top-nav button:nth-child(2)').click()`);await wait(`!!document.querySelector('iframe')`);
  await e(`(()=>{window.frame=document.querySelector('iframe');window.h=()=>frame.contentWindow.wordcraftHost;return true})()`);
  await wait(`h()&&(()=>{try{return !!JSON.parse(frame.contentDocument.querySelector('#host-log').textContent).opened}catch{return false}})()`);
  assert(await e(`JSON.stringify(await h().engine({op:'inspect'})).includes('社区老年人数字公共服务')`));result.checks.push('final bundle mounts actual native draft');
  assert(await e(`document.body.innerText.includes('AI / 方案 / 图源仍为演示')`));result.checks.push('remaining demonstrations visibly identified');
  if(!readonly){
    assert(await e(`document.querySelector('.comment-panel').innerText.includes('保存版本 v')`));result.checks.push('completion displays actual saved version and handling explanation');
    await e(`(()=>{const details=document.querySelector('.comment-edit');details.open=true;const form=details.querySelector('form');form.elements.author.value='人工录入（验收）';form.elements.text.value='补充社区选择标准，并说明结论只适用于本期进入的社区。';form.requestSubmit();return true})()`);
    await wait(`(async()=> (await read()).comment.author==='人工录入（验收）')()`);
    const after=await e('await read()');assert.equal(after.records.C01.progress,'awaiting_decision');assert.equal(after.records.C01.history.at(-1).previous.text,before.comment.text);
    result.checks.push('manual opinion entry persists and retains previous text in history');
    const beforeReload=await e('performance.timeOrigin');await send('Page.reload');await wait(`performance.timeOrigin!==${beforeReload} && !!document.querySelector('.top-nav')`);
    assert(await e(`document.body.innerText.includes('补充社区选择标准')`));result.checks.push('manual opinion text restores after browser refresh');
    await e(`document.querySelector('.top-nav button:nth-child(2)').click()`);await wait(`!!document.querySelector('iframe')`);
  }else{
    const after=await e('await read()');assert.equal(after.revision,before.revision);assert.equal(after.sourceLatest,1);assert.equal(after.records.C01.progress,'awaiting_decision');
    result.checks.push('user trial remains at untouched first-step state');
  }
  await new Promise(r=>setTimeout(r,500));await b.screenshot(path.join(out,'writing.png'));
  result.pageErrors=b.errors;assert.equal(b.errors.length,0);result.passed=true;
}catch(error){result.error=error.stack;result.pageErrors=b?.errors;process.exitCode=1;if(b)await b.screenshot(path.join(out,'failure.png')).catch(()=>{})}
finally{try{await b?.close();result.ownedBrowserStopped=true}catch(error){result.cleanupError=error.message;result.passed=false;process.exitCode=1}await writeFile(path.join(out,'verification.json'),JSON.stringify(result,null,2));console.log(JSON.stringify({out,...result},null,2))}
