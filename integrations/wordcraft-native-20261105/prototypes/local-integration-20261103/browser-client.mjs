import {spawn,spawnSync} from 'node:child_process';
import {createServer} from 'node:net';
import {writeFile} from 'node:fs/promises';
import {edge} from '../../candidates/lingdoc-native-workspace-20261027/scripts/local-environment.mjs';
const sleep=ms=>new Promise(resolve=>setTimeout(resolve,ms));
export async function browserClient(profile) {
  const server=createServer();await new Promise(r=>server.listen(0,'127.0.0.1',r));const port=server.address().port;await new Promise(r=>server.close(r));
  const child=spawn(edge,['--headless=new','--disable-extensions','--disable-gpu','--no-first-run','--no-default-browser-check','--disable-background-timer-throttling','--disable-backgrounding-occluded-windows','--disable-renderer-backgrounding',`--remote-debugging-port=${port}`,`--user-data-dir=${profile}`,'about:blank'],{windowsHide:true,stdio:'ignore'});
  let page;
  for(let i=0;i<100&&!page;i++){try{page=(await fetch(`http://127.0.0.1:${port}/json/list`).then(r=>r.json())).find(p=>p.type==='page')}catch{}if(!page)await sleep(150)}
  if(!page)throw Error('Browser not ready');
  const socket=new WebSocket(page.webSocketDebuggerUrl);await new Promise((r,j)=>{socket.onopen=r;socket.onerror=j});
  let sequence=0;const pending=new Map(),errors=[];
  socket.onmessage=event=>{const m=JSON.parse(event.data);if(m.method==='Runtime.exceptionThrown')errors.push(m.params.exceptionDetails);const p=pending.get(m.id);if(!p)return;pending.delete(m.id);clearTimeout(p.timer);m.error?p.reject(Error(m.error.message)):p.resolve(m.result)};
  const send=(method,params={})=>new Promise((resolve,reject)=>{const id=++sequence;const timer=setTimeout(()=>{pending.delete(id);reject(Error(method+' timeout'))},45000);pending.set(id,{resolve,reject,timer});socket.send(JSON.stringify({id,method,params}))});
  const evaluate=async expression=>{const r=await send('Runtime.evaluate',{expression:`(async()=>(${expression}))()`,awaitPromise:true,returnByValue:true});if(r.exceptionDetails)throw Error(r.exceptionDetails.exception?.description||r.exceptionDetails.text);return r.result.value};
  const wait=async(expression,timeout=60000)=>{const deadline=Date.now()+timeout;let last;while(Date.now()<deadline){try{if(await evaluate(`(async()=>Boolean(await (${expression})))()`))return}catch(e){last=e}await sleep(200)}throw Error('Wait failed: '+expression+(last?' '+last.message:''))};
  const screenshot=async file=>{const {data}=await send('Page.captureScreenshot',{format:'png'});await writeFile(file,Buffer.from(data,'base64'))};
  await send('Runtime.enable');await send('Page.enable');await send('Emulation.setDeviceMetricsOverride',{width:1600,height:1000,deviceScaleFactor:1,mobile:false});
  return {send,evaluate,wait,screenshot,errors,async close(){
    // Edge can relaunch itself: the original spawn PID is not the browser PID.
    await Promise.race([send('Browser.close').catch(()=>{}),sleep(1200)]);
    socket.close();
    const literal=profile.replaceAll('/','\\').replaceAll("'","''");
    const cleanup=`$ownedBrowserProfile='${literal}'; $rows=@(Get-CimInstance Win32_Process -Filter "Name='msedge.exe'" | Where-Object { $_.CommandLine -and $_.CommandLine.Replace('/','\\').Contains($ownedBrowserProfile) -and $_.CommandLine -match '--user-data-dir=' }); foreach($p in $rows){ $now=Get-CimInstance Win32_Process -Filter "ProcessId=$($p.ProcessId)"; if($now -and $now.CommandLine -eq $p.CommandLine -and $now.CreationDate -eq $p.CreationDate){Stop-Process -Id $p.ProcessId -Force -ErrorAction SilentlyContinue} }; $left=@(); for($attempt=0;$attempt -lt 20;$attempt++){ $left=@(Get-CimInstance Win32_Process -Filter "Name='msedge.exe'" | Where-Object { $_.CommandLine -and $_.CommandLine.Replace('/','\\').Contains($ownedBrowserProfile) }); if(-not $left.Count){break}; Start-Sleep -Milliseconds 200 }; if($left.Count){$left | Select-Object ProcessId,CreationDate | ConvertTo-Json -Compress; exit 1}`;
    const cleaned=spawnSync('powershell.exe',['-NoProfile','-NonInteractive','-EncodedCommand',Buffer.from(cleanup,'utf16le').toString('base64')],{windowsHide:true,encoding:'utf8',timeout:45000});
    await writeFile(profile+'-cleanup.json',JSON.stringify({status:cleaned.status,error:cleaned.error?.message,stderr:cleaned.stderr,stdout:cleaned.stdout}));
    if(cleaned.status!==0)throw Error('Owned browser cleanup could not be verified');
  }};
}
