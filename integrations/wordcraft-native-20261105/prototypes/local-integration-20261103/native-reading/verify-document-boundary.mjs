import assert from 'node:assert/strict';
import {mkdir,writeFile} from 'node:fs/promises';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {browserClient} from '../browser-client.mjs';
const trialOrigin=process.env.NATIVE_TRIAL_ORIGIN||'http://localhost:7214';
const trialAddress=new URL(trialOrigin);
if(!['localhost','127.0.0.1'].includes(trialAddress.hostname)||trialAddress.protocol!=='http:'||!['7214','7226','7227'].includes(trialAddress.port)||trialAddress.pathname!=='/')throw Error('Only dedicated native trials are allowed');
const out=path.join(path.dirname(fileURLToPath(import.meta.url)),'artifacts','document-boundary-'+new Date().toISOString().replaceAll(':','-'));
await mkdir(out,{recursive:true});const result={passed:false,checks:[]};let browser;
try {
 browser=await browserClient(path.join(out,'edge'));const {send,evaluate:e,wait}=browser;
 const ready=()=>wait(`window.nativeReadingReady?.phase==='ready'&&!!window.nativeWorkspace`,120000);
 const check=async(expr,label)=>{assert(await e(expr),label);result.checks.push(label);console.log('PASS '+label)};
 await send('Page.navigate',{url:trialOrigin+'/project'});await ready();
 const original=await e(`({disk:wordcraftHost.diskVersion,doc:await wordcraftHost.engine({op:'inspect'})})`);
 await check(`!(await wordcraftHost.engine({op:'status'})).dirty`,'project is clean before boundary test');
 await send('Input.dispatchKeyEvent',{type:'keyDown',key:'n',code:'KeyN',windowsVirtualKeyCode:78,modifiers:2});
 await send('Input.dispatchKeyEvent',{type:'keyUp',key:'n',code:'KeyN',windowsVirtualKeyCode:78,modifiers:2});
 await wait(`location.pathname==='/'&&location.search.includes('document=new')&&window.nativeReadingReady?.phase==='ready'`,120000);
 await check(`window.WORDCRAFT_STANDALONE&&!(await wordcraftHost.engine({op:'workspace.status'})).documentBound&&!wordcraftHost.saveIntent`,'native New in a project enters independent save mode');
 await check(`!JSON.stringify(await wordcraftHost.engine({op:'inspect'})).includes('社区老年人数字公共服务使用障碍研究')`,'ordinary New does not reuse project content');
 await send('Page.navigate',{url:trialOrigin+'/project'});await ready();
 await check(`wordcraftHost.diskVersion===${original.disk}&&JSON.stringify(await wordcraftHost.engine({op:'inspect'}))===${JSON.stringify(JSON.stringify(original.doc))}`,'returning to project reads the unchanged saved main document');
 await send('Input.dispatchKeyEvent',{type:'keyDown',key:'o',code:'KeyO',windowsVirtualKeyCode:79,modifiers:2});
 await send('Input.dispatchKeyEvent',{type:'keyUp',key:'o',code:'KeyO',windowsVirtualKeyCode:79,modifiers:2});
 await wait(`location.pathname==='/'&&location.search.includes('document=open')&&window.nativeReadingReady?.phase==='ready'`,120000);
 await check(`window.WORDCRAFT_STANDALONE&&!(await wordcraftHost.engine({op:'workspace.status'})).documentBound`,'native Open in a project enters ordinary Open without a project binding');
 await browser.screenshot(path.join(out,'ordinary-open.png'));
 result.passed=true;
}catch(error){result.error=error.stack;process.exitCode=1;if(browser)try{await browser.screenshot(path.join(out,'failure.png'))}catch{}}
finally{if(browser)await browser.close();await writeFile(path.join(out,'verification.json'),JSON.stringify(result,null,2));console.log(JSON.stringify({out,passed:result.passed,checks:result.checks.length,error:result.error}));}


