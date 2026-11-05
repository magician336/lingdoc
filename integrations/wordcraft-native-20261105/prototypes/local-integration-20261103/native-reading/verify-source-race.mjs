// Real native tab cancellation, no model calls and no document edits.
import assert from 'node:assert/strict';
import {mkdir,writeFile} from 'node:fs/promises';
import {fileURLToPath} from 'node:url';
import path from 'node:path';
import {browserClient} from '../browser-client.mjs';
const origin=process.env.NATIVE_READING_ORIGIN||'http://127.0.0.1:7213';
const url=new URL(origin);
if(!['localhost','127.0.0.1'].includes(url.hostname)||url.protocol!=='http:'||!['7212','7213','7226'].includes(url.port)||!['/','/project'].includes(url.pathname))throw Error('Only isolated native trials are supported');
const out=path.join(path.dirname(fileURLToPath(import.meta.url)),'artifacts','source-race-'+new Date().toISOString().replaceAll(':','-'));
await mkdir(out,{recursive:true});
const result={passed:false,checks:[],origin,scope:'native source request cancellation; no model calls or document edits'};
let browser;
try{
 browser=await browserClient(path.join(out,'edge'));
 const {evaluate:e,wait,send}=browser;
 await send('Page.navigate',{url:origin});
 await wait(`window.nativeReadingReady?.phase==='ready'&&!!window.nativeWorkspace`,120000);
 await e(`(async()=>{
   window.raceSnap=async()=>({state:await wordcraftHost.engine({op:'status'}),selection:await wordcraftHost.engine({op:'selection'}),document:await wordcraftHost.engine({op:'inspect'})});
   window.raceBefore=await raceSnap();
   await wordcraftHost.engine({op:'source.mode',mode:'source'});
   window.originalRaceFetch=window.fetch;window.holdNextSource=true;window.raceHeld=false;window.raceDone=false;window.raceFailure=null;
   window.fetch=async(...args)=>{const response=await originalRaceFetch(...args);if(holdNextSource&&String(args[0]).endsWith('/native-reading/workspace/source')){holdNextSource=false;raceHeld=true;await new Promise(resolve=>window.releaseRace=resolve)}return response};
   const state=await nativeWorkspace.state();
   window.raceTarget=state.sources.find(s=>s.id!==nativeReadingReady.source.sourceId)||state.sources[0];
   if(!raceTarget)throw Error('No source fixture');
   window.racePromise=nativeWorkspace.openSource(raceTarget.id,undefined,raceTarget.version).then(()=>raceDone=true).catch(error=>{raceFailure=error.message;raceDone=true});
   return true;
 })()`);
 await wait('window.raceHeld===true',30000);
 const rect=await e(`(await wordcraftHost.engine({op:'source.status'})).bodyTabRect`);
 assert(rect&&rect.width>0,'native body tab rectangle available');
 const x=rect.x+rect.width/2,y=rect.y+rect.height/2;
 await send('Input.dispatchMouseEvent',{type:'mousePressed',x,y,button:'left',clickCount:1});
 await send('Input.dispatchMouseEvent',{type:'mouseReleased',x,y,button:'left',clickCount:1});
 await wait(`(await wordcraftHost.engine({op:'source.status'})).mode==='body'`);
 result.checks.push('actual native body tab selects body while source response is delayed');
 await e('(()=>{window.releaseRace();return true})()');
 await wait('window.raceDone===true');
 assert.equal(await e('window.raceFailure'),null);
 assert.equal(await e(`(await wordcraftHost.engine({op:'source.status'})).mode`),'body');
 result.checks.push('late response cannot reopen source after native body tab');
 assert(await e('JSON.stringify(await raceSnap())===JSON.stringify(raceBefore)'));
 result.checks.push('identity, revision, dirty, selection and inspected body remain unchanged');
 await browser.screenshot(path.join(out,'cancelled-source-stays-body.png'));
 assert.equal(browser.errors.length,0);
 result.passed=true;
}catch(error){result.failure=error.stack||error.message;process.exitCode=1}
finally{
 if(browser){try{await browser.evaluate(`(()=>{if(window.releaseRace)releaseRace();if(window.originalRaceFetch)window.fetch=originalRaceFetch;return true})()`);await browser.close()}catch(error){result.cleanupFailure=error.message;result.passed=false;process.exitCode=1}}
 await writeFile(path.join(out,'verification.json'),JSON.stringify(result,null,2));
 console.log(JSON.stringify({passed:result.passed,checks:result.checks,output:out,failure:result.failure}));
}

