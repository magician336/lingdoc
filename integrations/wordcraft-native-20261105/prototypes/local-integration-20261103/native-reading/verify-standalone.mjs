import assert from 'node:assert/strict';
import {mkdir,readdir,readFile,writeFile} from 'node:fs/promises';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {browserClient} from '../browser-client.mjs';
const trialOrigin=process.env.NATIVE_TRIAL_ORIGIN||'http://localhost:7214';
const trialAddress=new URL(trialOrigin);
if(!['localhost','127.0.0.1'].includes(trialAddress.hostname)||trialAddress.protocol!=='http:'||!['7214','7226','7227'].includes(trialAddress.port)||trialAddress.pathname!=='/')throw Error('Only dedicated native trials are allowed');
const here=path.dirname(fileURLToPath(import.meta.url));
const out=path.join(here,'artifacts','standalone-'+new Date().toISOString().replaceAll(':','-'));
await mkdir(path.join(out,'downloads'),{recursive:true});
const result={passed:false,checks:[],scope:'ordinary native new/open/save without project save calls'};let browser;
try {
 browser=await browserClient(path.join(out,'edge'));const {send,evaluate:e,wait}=browser;
 const check=async(expression,label)=>{assert(await e(expression),label);result.checks.push(label);console.log('PASS '+label)};
 await send('Browser.setDownloadBehavior',{behavior:'allow',downloadPath:path.join(out,'downloads')});
 await send('Page.navigate',{url:trialOrigin+'/?document=new'});
 await wait(`window.nativeReadingReady?.phase==='ready'&&!!window.nativeWorkspace`,120000);
 await check(`window.WORDCRAFT_STANDALONE&&!(await wordcraftHost.engine({op:'workspace.status'})).documentBound&&!wordcraftHost.saveIntent&&!document.querySelector('script[src="/host.js"]')`,'ordinary New has no project save binding');
 await e(`(()=>{window.writeRequests=[];const f=window.fetch;window.fetch=(...a)=>{if(a[1]?.method==='POST')writeRequests.push(String(a[0]));return f(...a)};window.showSaveFilePicker=undefined;return true})()`);
 const text='Independent ordinary document verification. No project binding.';
 await e(`(async()=>{const b=await wordcraftHost.engine({op:'selection'});return await wordcraftHost.engine({op:'insert',bookmark:b.selection,expectedDocumentId:b.state.documentId,expectedRevision:b.state.revision,text:${JSON.stringify(text)}})})()`);
 await check(`(await wordcraftHost.engine({op:'status'})).dirty`,'ordinary document accepts native editing');
 await send('Input.dispatchKeyEvent',{type:'keyDown',key:'s',code:'KeyS',windowsVirtualKeyCode:83,modifiers:2});
 await send('Input.dispatchKeyEvent',{type:'keyUp',key:'s',code:'KeyS',windowsVirtualKeyCode:83,modifiers:2});
 let downloaded;
 for(let i=0;i<100;i++){downloaded=(await readdir(path.join(out,'downloads'))).find(n=>n.endsWith('.docx'));if(downloaded)break;await new Promise(r=>setTimeout(r,200));}
 assert(downloaded,'native Ctrl+S produces a standalone DOCX');result.checks.push('native Ctrl+S downloads an ordinary DOCX');
 const bytes=await readFile(path.join(out,'downloads',downloaded));assert(bytes.length>100);
 await check(`window.writeRequests.every(p=>!p.includes('/api/save')&&!p.includes('/go/')&&!p.includes('/integration/'))`,'ordinary save does not call project DS or Go');
 await wait(`!(await wordcraftHost.engine({op:'status'})).dirty`);
 await e(`await wordcraftHost.engine({op:'open',data:${JSON.stringify(bytes.toString('base64'))}})`);
 await check(`JSON.stringify(await wordcraftHost.engine({op:'inspect'})).includes(${JSON.stringify(text)})`,'downloaded ordinary DOCX reopens with original text');
 await check(`!(await wordcraftHost.engine({op:'workspace.status'})).documentBound`,'opening an ordinary DOCX does not force project membership');
 await browser.screenshot(path.join(out,'ordinary-document.png'));
 result.errors=browser.errors;assert.equal(browser.errors.length,0);result.passed=true;
}catch(error){result.error=error.stack;process.exitCode=1;if(browser)try{await browser.screenshot(path.join(out,'failure.png'))}catch{}}
finally{if(browser)await browser.close();await writeFile(path.join(out,'verification.json'),JSON.stringify(result,null,2));console.log(JSON.stringify({out,passed:result.passed,checks:result.checks.length,error:result.error}));}


