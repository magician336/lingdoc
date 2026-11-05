// Diagnostic only: reuses existing three real answers, never calls the model or edits a document.
import {browserClient} from '../browser-client.mjs';
import {mkdir,readFile,writeFile} from 'node:fs/promises';
const out=new URL('./artifacts/scroll-route-'+Date.now()+'/',import.meta.url);await mkdir(out,{recursive:true});
const receipt=JSON.parse(await readFile(new URL('./artifacts/ai-conversation-2026-11-05T02-31-46.297Z-87924/verification.json',import.meta.url),'utf8'));
const b=await browserClient(new URL('edge',out).pathname.replace(/^\//,''));const trace=[];let original;
try{
const e=b.evaluate,pause=()=>e('new Promise(r=>setTimeout(r,350))'),ws=()=>e("wordcraftHost.engine({op:'workspace.status'})");
await b.send('Page.navigate',{url:'http://localhost:7226/project'});await b.wait("window.nativeReadingReady?.phase==='ready'&&!!window.nativeWorkspace",120000);original=await e('(await nativeWorkspace.state()).currentSessionId');await e(`nativeWorkspace.act({kind:'selectSession',sessionId:${JSON.stringify(receipt.sessionId)}})`);await e("wordcraftHost.engine({op:'workspace.layout',chatOpen:true})");await pause();
const key=Object.keys((await ws()).citationOpenRects).find(k=>k.includes(receipt.answers[0].id)&&k.includes(receipt.answers[0].citations[0].sourceId));
const wheel=async dy=>{const u=await ws(),x=u.chatRect.x+u.chatRect.width/2,y=Math.min(u.composerRect.y-60,u.chatRect.y+u.chatRect.height*.55);await b.send('Input.dispatchMouseEvent',{type:'mouseMoved',x,y});await pause();await b.send('Input.dispatchMouseEvent',{type:'mouseWheel',x,y,deltaX:0,deltaY:dy});await pause();return (await ws()).citationOpenRects[key].y};
let observer=false;
for(let cycle=0;cycle<8;cycle++){
 const u=await ws();const x=u.composerRect.x+20,y=cycle%2?u.chatRect.y+410:u.composerRect.y+12;
 await b.send('Input.dispatchMouseEvent',{type:'mouseMoved',x,y});await b.send('Input.dispatchMouseEvent',{type:'mousePressed',x,y,button:'left',clickCount:1});await b.send('Input.dispatchMouseEvent',{type:'mouseReleased',x,y,button:'left',clickCount:1});await pause();
 let previous=await wheel(1500),stalled=0;trace.push({cycle,bottom:previous,observer});
 for(let i=0;i<5;i++){
  const next=await wheel(-200);trace.push({cycle,i,previous,next,observer,events:observer?await e('window.__routeEvents.splice(0)'):null});
  stalled=next===previous?stalled+1:0;previous=next;
  if(stalled>=2&&!observer){await e("(()=>{window.__routeEvents=[];window.addEventListener('wheel',e=>window.__routeEvents.push({dy:e.deltaY,x:e.clientX,y:e.clientY}),{capture:true,passive:true});return true})()");observer=true;trace.push({activatedObserver:true,cycle,i});}
 }
 if(observer&&cycle>=3)break;
}
await b.screenshot(new URL('final.png',out).pathname.replace(/^\//,''));
}finally{if(original)await b.evaluate(`nativeWorkspace.act({kind:'selectSession',sessionId:${JSON.stringify(original)}})`);await writeFile(new URL('trace.json',out),JSON.stringify(trace,null,2));await b.close();console.log(out.href);console.log(JSON.stringify(trace))}
