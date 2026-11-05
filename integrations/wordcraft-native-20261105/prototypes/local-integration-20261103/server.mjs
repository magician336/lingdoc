// Local integration gateway. Exact loopback hosts, same-origin tokened writes,
// fixed proxy routes; upstream native save and Go checks remain enabled.
import http from 'node:http';
import { readFile, readdir, mkdir, writeFile } from 'node:fs/promises';
import { spawnSync } from 'node:child_process';
import { randomBytes, createHash } from 'node:crypto';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { startDeliveryRuntime } from '../../candidates/lingdoc-native-workspace-20261027/scripts/delivery-runtime.mjs';
import { fixture } from '../../candidates/lingdoc-native-workspace-20261027/scripts/local-environment.mjs';
import { createChainStore, atomicJSON, BASELINE, fail } from './chain-store.mjs';
import { createGoSources } from './go-sources.mjs';
import { createWorkflowStore } from './workflow-store.mjs';
import { createWorkflowModel } from './workflow-model.mjs';

const here = path.dirname(fileURLToPath(import.meta.url));
const sha = bytes => createHash('sha256').update(bytes).digest('hex');
const send = (res, status, value) => { res.writeHead(status, { 'Content-Type':'application/json; charset=utf-8', 'Cache-Control':'no-store', 'X-Content-Type-Options':'nosniff' }); res.end(JSON.stringify(value)); };
async function requestBody(req, limit=20*1024*1024) {
  let length=0; const pieces=[];
  for await (const part of req) { length+=part.length; if(length>limit) fail(413,'请求过大'); pieces.push(part); }
  return Buffer.concat(pieces);
}

export async function startIntegration({ directory=path.join(here,'runtime','trial'), port=7210 }={}) {
  directory=path.resolve(directory);
  if (!Number.isSafeInteger(port)||port<1024||port>65535) throw Error('Invalid integration port');
  const token=randomBytes(32).toString('hex');
  const hosts=new Set([`127.0.0.1:${port}`,`localhost:${port}`]);
  let runtime, server, stopping=false;
  // Reserve user port before starting owned services; no port-based termination.
  server=http.createServer();
  await new Promise((resolve,reject)=>{server.once('error',reject);server.listen(port,'127.0.0.1',resolve)});
  server.on('request', (req,res)=>send(res,503,{error:'集成服务正在启动'}));
  const stop=async()=>{
    if(stopping)return; stopping=true;
    await new Promise(resolve=>server.close(resolve));
    await runtime?.stop();
  };
  try {
    await mkdir(directory,{recursive:true});
    process.env.LINGDOC_RESEARCH_GO_BINARY ||= '/tmp/lingdoc-local-source-ai';
    // Provider credentials remain in the existing private runtime configuration.
    // Only an explicitly confirmed workflow request invokes the model.
    runtime=await startDeliveryRuntime({runtimeDir:directory,research:true});
    const rawConfig=await fetch(runtime.origin+'/host-config.js').then(r=>r.text());
    const hostConfig=JSON.parse(rawConfig.slice(rawConfig.indexOf('=')+1).replace(/;\s*$/,''));
    const sources=createGoSources({goOrigin:runtime.goOrigin,origin:runtime.origin,runtimeId:runtime.runtimeId});
    const bootstrap=await sources.bootstrap();
    const projectId=bootstrap.projectId;
    async function ds(endpoint,body) {
      const response=await fetch(runtime.origin+'/api/'+endpoint,{method:'POST',headers:{Origin:runtime.origin,'X-Host-Token':hostConfig.token,'Content-Type':'application/json'},body:JSON.stringify(body),signal:AbortSignal.timeout(30000)});
      const result=await response.json(); if(!response.ok)fail(response.status,result.error||'DS错误');return result;
    }
    async function go(endpoint,{method='GET',body,key}={}) {
      const response=await fetch(`${runtime.goOrigin}/api/docx/projects/${encodeURIComponent(projectId)}/${endpoint}`,{method,headers:{Origin:runtime.origin,'Content-Type':'application/json',...(key?{'Idempotency-Key':key}:{})},body:body?JSON.stringify(body):undefined,signal:AbortSignal.timeout(30000)});
      const result=await response.json();if(!response.ok)fail(response.status,result.message||result.error||'Go错误');return result;
    }
    const opened=await ds('open',{});
    if(opened.diskVersion===0) {
      const seed=path.join(directory,'initial-fictional-draft.docx');
      const python=spawnSync('python',['-X','utf8',path.join(here,'seed-document.py'),fixture,seed],{windowsHide:true,encoding:'utf8'});
      if(python.status!==0)throw Error('Create fictional DOCX failed: '+python.stderr);
      const bytes=await readFile(seed);
      await ds('save',{bytes:Array.from(bytes),sha256:sha(bytes),saveToken:'local-r1-initial-document',documentId:0,revision:0,expectedDiskVersion:0});
      await go('versions',{method:'POST',key:'local-r1-initial-document',body:{expectedDiskVersion:0,data:bytes.toString('base64')}});
    } else {
      // Recover a first-run DS save whose Go registration response was lost.
      try { await go('versions/latest'); }
      catch(error) {
        if(error.status!==404)throw error;
        if(opened.receipt?.saveToken!=='local-r1-initial-document')throw Error('Unregistered non-seed DOCX requires explicit recovery');
        await go('versions',{method:'POST',key:'local-r1-initial-document',body:{expectedDiskVersion:0,data:opened.data}});
      }
    }
    const store=await createChainStore({directory,projectId,sources,latestVersion:()=>go('versions/latest')});
    const workflow=await createWorkflowStore({directory,chain:store,model:createWorkflowModel({goOrigin:runtime.goOrigin,origin:runtime.origin,projectId,sources}),latestVersion:()=>go('versions/latest')});
    const visibleChain=value=>({...value,backend:{...value.backend,ai:bootstrap.ai?.configured?'real-explicit-context':'unavailable',figure:'out_of_scope',componentVersion:'controlled-writing-0.3.0'}});
    const staticFiles=new Map();
    const mime={'.html':'text/html; charset=utf-8','.js':'text/javascript; charset=utf-8','.css':'text/css; charset=utf-8','.png':'image/png','.svg':'image/svg+xml'};
    async function scan(dir,prefix='') {
      for(const e of await readdir(dir,{withFileTypes:true})) {
        if(e.isSymbolicLink())throw Error('Static bundle cannot include links');
        const key=prefix+'/'+e.name, full=path.join(dir,e.name);
        if(e.isDirectory())await scan(full,key);else staticFiles.set(key,full);
      }
    }
    await scan(path.join(here,'frontend','dist'));
    staticFiles.set('/',staticFiles.get('/index.html'));
    staticFiles.set('/integration/native-adapter.js',path.join(here,'native-adapter.js'));
    // Freeze one complete built bundle for this process, so a later build cannot
    // mix a new index with missing old assets in an already-open trial session.
    const staticBytes=new Map();
    for(const [route,file] of staticFiles) {
      if(!file)throw Error('Frontend dist is incomplete; build before starting');
      staticBytes.set(route,{bytes:await readFile(file),type:mime[path.extname(file)]||'application/octet-stream'});
    }
    for(const match of staticBytes.get('/').bytes.toString('utf8').matchAll(/(?:src|href)="(\.\/assets\/[^"?#]+)"/g)) {
      if(!staticBytes.has('/'+match[1].slice(2)))throw Error('Frontend dist references a missing asset');
    }
    const dsStatic=new Set(['/native.html','/boot.js','/host.js','/save-coordinator.js','/source-document-page.css','/source-document-page.js','/wordcraft.js','/wordcraft_bg.wasm']);
    async function proxy(req,res,url,body) {
      const response=await fetch(url,{method:req.method,headers:{Origin:runtime.origin,'Content-Type':req.headers['content-type']||'application/json','X-Host-Token':hostConfig.token,...(req.headers['idempotency-key']?{'Idempotency-Key':req.headers['idempotency-key']}:{})},body:['GET','HEAD'].includes(req.method)?undefined:body,redirect:'error',signal:AbortSignal.timeout(90000)});
      res.writeHead(response.status,{'Content-Type':response.headers.get('content-type')||'application/octet-stream','Cache-Control':'no-store','X-Content-Type-Options':'nosniff','Cross-Origin-Resource-Policy':'same-origin','Content-Security-Policy':"default-src 'self' blob: data:; script-src 'self' 'wasm-unsafe-eval'; connect-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' blob: data:; frame-ancestors 'self'"});
      res.end(Buffer.from(await response.arrayBuffer()));
    }
    server.removeAllListeners('request');
    server.on('request',async(req,res)=>{
      try {
        if(!hosts.has(req.headers.host))fail(403,'Host not allowed');
        const localOrigin=`http://${req.headers.host}`;
        const url=new URL(req.url,localOrigin);
        const isRead=req.method==='GET';
        if(req.headers.origin&&req.headers.origin!==localOrigin)fail(403,'Origin not allowed');
        if(req.headers['sec-fetch-site']==='cross-site')fail(403,'Cross-site request refused');
        const authorized=req.headers['x-integration-token']===token||req.headers['x-host-token']===token;
        if(!isRead && (req.headers.origin!==localOrigin||!authorized))fail(403,'Same-origin token required');
        if(url.pathname==='/integration/config'&&isRead)return send(res,200,{token,baselineId:BASELINE,projectId,componentVersion:'controlled-writing-0.3.0',aiConfigured:bootstrap.ai?.configured===true});
        if(url.pathname==='/host-config.js'&&isRead) {
          res.writeHead(200,{'Content-Type':'text/javascript','Cache-Control':'no-store','Cross-Origin-Resource-Policy':'same-origin'});
          return res.end(`window.WORDCRAFT_HOST_CONFIG=${JSON.stringify({...hostConfig,token,origin:localOrigin,goOrigin:localOrigin,researchEnabled:false,projectId})};
const hostFetch = window.fetch.bind(window);
window.fetch = (input, options = {}) => {
  const url = new URL(input instanceof Request ? input.url : String(input), location.href);
  if (url.origin === location.origin && url.pathname.startsWith(${JSON.stringify('/api/docx/projects/'+encodeURIComponent(projectId)+'/')})) {
    const headers = new Headers(options.headers || (input instanceof Request ? input.headers : undefined));
    headers.set('X-Integration-Token', window.WORDCRAFT_HOST_CONFIG.token);
    options = { ...options, headers };
  }
  return hostFetch(input, options);
};`);
        }
        if(url.pathname==='/integration/health'&&isRead)return send(res,200,{ready:true,baselineId:BASELINE,componentVersion:'controlled-writing-0.3.0',aiConfigured:bootstrap.ai?.configured===true,pid:process.pid,projectId});
        if(url.pathname==='/integration/state'&&isRead) {
          if(!authorized)fail(403,'Token required'); return send(res,200,visibleChain(store.read()));
        }
        if(url.pathname==='/integration/actions'&&req.method==='POST') {
          let input;try{input=JSON.parse((await requestBody(req,1024*1024)).toString('utf8'))}catch{fail(400,'Invalid JSON')}
          return send(res,200,visibleChain(await store.act(input)));
        }
        if(url.pathname==='/integration/workflow') {
          if(!authorized)fail(403,'Token required');
          if(isRead)return send(res,200,workflow.read());
          if(req.method==='POST') {
            let input;try{input=JSON.parse((await requestBody(req,256*1024)).toString('utf8'))}catch{fail(400,'Invalid JSON')}
            return send(res,200,await workflow.act(input));
          }
        }
        if(url.pathname==='/integration/source'&&isRead) {
          if(!authorized)fail(403,'Token required');
          const s=store.read().sourceVersions[url.searchParams.get('version')];
          if(!s?.sourceId)fail(404,'资料版本尚未登记');
          return send(res,200,await sources.readSource(s.sourceId));
        }
        if(dsStatic.has(url.pathname)&&isRead)return proxy(req,res,runtime.origin+(url.pathname==='/native.html'?'/':url.pathname));
        if(['/api/open','/api/save','/api/version','/api/font'].includes(url.pathname)&&req.method==='POST')return proxy(req,res,runtime.origin+url.pathname,await requestBody(req));
        const base=`/api/docx/projects/${encodeURIComponent(projectId)}/`;
        if(url.pathname.startsWith(base)&&/^(?:versions(?:\/latest|\/[a-zA-Z0-9_-]+\/delivery)?|confirmations|freezes|exports(?:\/[a-zA-Z0-9_-]+)?)$/.test(url.pathname.slice(base.length))) {
          if(!authorized)fail(403,'Token required');
          return proxy(req,res,runtime.goOrigin+url.pathname+url.search,isRead?undefined:await requestBody(req));
        }
        const file=staticBytes.get(url.pathname);
        if(isRead&&file) {
          res.writeHead(200,{'Content-Type':file.type,'Cache-Control':'no-store','X-Content-Type-Options':'nosniff','Content-Security-Policy':"default-src 'self'; script-src 'self' 'wasm-unsafe-eval'; connect-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' blob: data:; frame-src 'self'; frame-ancestors 'self'"});
          return res.end(file.bytes);
        }
        fail(404,'Not found');
      } catch(error) { if(!res.headersSent)send(res,error.status||500,{error:error.message});else res.end(); }
    });
    const info={origin:`http://127.0.0.1:${port}`,directory,projectId,baselineId:BASELINE,componentVersion:'controlled-writing-0.3.0',pid:process.pid,runtimeId:runtime.runtimeId,startedAt:new Date().toISOString()};
    await atomicJSON(path.join(directory,'integration.json'),info);
    runtime.finished.catch(error=>{console.error(error.message);stop()});
    return {...info,store,runtime,stop};
  }catch(error){await stop();throw error}
}

if(process.argv[1]&&path.resolve(process.argv[1])===fileURLToPath(import.meta.url)) {
  const args=process.argv.slice(2);const options={};
  for(let i=0;i<args.length;i+=2){if(args[i]==='--port')options.port=Number(args[i+1]);else if(args[i]==='--directory')options.directory=args[i+1];else throw Error('Unknown argument')}
  const app=await startIntegration(options);
  console.log(JSON.stringify({ready:true,origin:app.origin,pid:process.pid,baselineId:BASELINE,directory:app.directory}));
  process.on('SIGINT',()=>app.stop());process.on('SIGTERM',()=>app.stop());
  // Shutdown is an owned control file, never a PID/port kill operation.
  const timer=setInterval(async()=>{try{if((await readFile(path.join(app.directory,'stop.request'),'utf8')).trim()===app.runtimeId){clearInterval(timer);await app.stop()}}catch{}},1000);
  timer.unref();
}
