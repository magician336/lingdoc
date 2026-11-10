import {test} from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import {mkdtemp,rm,writeFile} from 'node:fs/promises';
import path from 'node:path';
import {createDrawingHTTP} from './drawing-http.mjs';

const png='iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGP4z8DwHwAFAAH/iZk9HQAAAABJRU5ErkJggg==';
const request={expectedRevision:0,key:'one',title:'图稿',xml:'<mxfile><diagram/></mxfile>',messages:[{role:'user',content:'hello'}],png};
const base='/native-reading/drawing';
async function launch(directory) {
 const handler=await createDrawingHTTP({directory,token:'test-token',drawingOrigin:'http://127.0.0.1:3000'});
 const server=http.createServer(async(req,res)=>{
   const host=`127.0.0.1:${server.address().port}`;
   if(req.headers.host!==host || req.headers['sec-fetch-site']==='cross-site'){res.writeHead(403);return res.end('Host refused');}
   const origin=`http://${host}`;
   if(req.headers.origin && req.headers.origin!==origin){res.writeHead(403);return res.end('Origin refused');}
   try { if (!await handler(req,res,new URL(req.url,origin))) {res.writeHead(404);res.end();} } catch {res.writeHead(500);res.end();}
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const origin=`http://127.0.0.1:${server.address().port}`;
 const call=async(route,{body,token='test-token',requestOrigin=origin,method='POST',headers={}}={})=>{
  const response=await fetch(origin+base+route,{method,headers:{...(token===null?{}:{'x-workspace-token':token}),...(requestOrigin===null?{}:{origin:requestOrigin}),...headers},...(body===undefined?{}:{body:typeof body==='string'?body:JSON.stringify(body)})});
  const text=await response.text(); let data;try{data=JSON.parse(text)}catch{data=text}return {status:response.status,data};
 };
 return {origin,call,close:()=>new Promise((resolve,reject)=>server.close(err=>err?reject(err):resolve()))};
}
test('HTTP authorization, persistence, portable images and retries',async()=>{
 const dir=await mkdtemp(path.join(process.cwd(),'draw-http-test-'));let server;
 try {
  server=await launch(dir);
  assert.equal((await server.call('/save',{body:request,token:null})).status,403);
  assert.equal((await server.call('/save',{body:request,requestOrigin:'http://evil.example'})).status,403);
  assert.equal((await server.call('/save',{body:request,requestOrigin:null})).status,403);
  assert.equal((await server.call('/list',{method:'GET',headers:{'sec-fetch-site':'cross-site'}})).status,403);
  const saved=await server.call('/save',{body:request});assert.equal(saved.status,200);assert.equal(saved.data.revision,1);
  const embedded=saved.data.embeddedPng; const record={...saved.data};delete record.embeddedPng;
  assert.deepEqual((await server.call('/save',{body:request})).data,saved.data);
  assert.equal((await server.call('/save',{body:{...request,title:'changed'}})).status,409);
  const portable=await server.call('/read-image',{body:{png:embedded}});assert.equal(portable.status,200);assert.deepEqual(portable.data.record,{id:record.id,revision:1,title:request.title,xml:request.xml,messages:request.messages});
  await server.close();server=await launch(dir);
  assert.deepEqual((await server.call('/get',{body:{id:record.id}})).data.record,record);
  assert.deepEqual((await server.call('/list',{method:'GET'})).data,[record]);
  assert.deepEqual((await server.call('/save',{body:request})).data,saved.data);
  assert.equal((await server.call('/save',{body:{...request,id:record.id,key:'new',expectedRevision:0}})).status,409);
 } finally {if(server)await server.close();await rm(dir,{recursive:true,force:true});}
});
test('HTTP input errors and internal errors do not expose paths',async()=>{
 const dir=await mkdtemp(path.join(process.cwd(),'draw-http-test-'));let server;
 try {
  server=await launch(dir);
  assert.equal((await server.call('/save',{body:'{' } )).status,400);
  assert.equal((await server.call('/read-image',{body:{png:'invalid'}})).status,400);
  assert.equal((await server.call('/save',{body:{...request,xml:'<!DOCTYPE x><x/>'}})).status,400);
  assert.equal((await server.call('/save',{body:' '.repeat(10*1024*1024+1)})).status,413);
  await writeFile(path.join(dir,'drawings.json'),'broken database');
  const failure=await server.call('/list',{method:'GET'});assert.equal(failure.status,400);assert.ok(!JSON.stringify(failure.data).includes(dir));
 } finally {if(server)await server.close();await rm(dir,{recursive:true,force:true});}
});

test('HTTP delete enforces authorization, revision, restart-safe retry and portable images',async()=>{
  const dir=await mkdtemp(path.join(process.cwd(),'draw-http-test-'));let server;
  try {
    server=await launch(dir);
    const saved=await server.call('/save',{body:request});
    assert.equal(saved.status,200);
    const deletion={id:saved.data.id,expectedRevision:saved.data.revision,key:'delete-key'};
    assert.equal((await server.call('/delete',{body:deletion,token:null})).status,403);
    assert.equal((await server.call('/delete',{body:deletion,requestOrigin:'http://evil.example'})).status,403);
    assert.equal((await server.call('/delete',{body:deletion,requestOrigin:null})).status,403);
    assert.equal((await server.call('/delete',{body:{...deletion,expectedRevision:2}})).status,409);
    assert.equal((await server.call('/get',{body:{id:deletion.id}})).data.record.id,deletion.id);
    const removed=await server.call('/delete',{body:deletion});
    assert.equal(removed.status,200);
    assert.deepEqual(removed.data,{id:deletion.id,revision:1,deleted:true});
    assert.equal((await server.call('/get',{body:{id:deletion.id}})).data.record,null);
    assert.deepEqual((await server.call('/list',{method:'GET'})).data,[]);
    const source=await server.call('/read-image',{body:{png:saved.data.embeddedPng}});
    assert.equal(source.status,200);
    assert.deepEqual(source.data.record,{id:deletion.id,revision:1,title:request.title,xml:request.xml,messages:request.messages});
    await server.close();server=await launch(dir);
    assert.deepEqual((await server.call('/delete',{body:deletion})).data,removed.data);
    assert.equal((await server.call('/delete',{body:{...deletion,expectedRevision:2}})).status,409);
    assert.equal((await server.call('/delete',{body:{...deletion,key:'new-delete'}})).status,404);
    assert.equal((await server.call('/save',{body:request})).status,409);
    assert.equal((await server.call('/save',{body:{...request,id:deletion.id,key:'revive'}})).status,409);
    assert.equal((await server.call('/delete',{body:{...deletion,id:'../x',key:'bad'}})).status,400);
  } finally {if(server)await server.close();await rm(dir,{recursive:true,force:true});}
});

test('HTTP saved-version deletion preserves other versions and survives restart without reusing ids',async()=>{
  const dir=await mkdtemp(path.join(process.cwd(),'draw-http-test-'));let server;
  try {
    server=await launch(dir);
    const first=(await server.call('/save',{body:request})).data;
    const second=(await server.call('/save',{body:{...request,id:first.id,expectedRevision:1,key:'v2'}})).data;
    await server.call('/save',{body:{...request,id:first.id,expectedRevision:2,key:'v3'}});
    const deletion={id:first.id,revision:2,key:'delete-version-2'};
    for(const route of ['/history','/version','/delete-version']) {
      assert.equal((await server.call(route,{body:deletion,token:null})).status,403);
      assert.equal((await server.call(route,{body:deletion,requestOrigin:'http://evil.example'})).status,403);
    }
    const history=await server.call('/history',{body:{id:first.id}});
    assert.equal(history.status,200);assert.deepEqual(history.data.map(row=>row.revision),[3,2,1]);
    const deleted=await server.call('/delete-version',{body:deletion});assert.equal(deleted.status,200);
    assert.deepEqual((await server.call('/history',{body:{id:first.id}})).data.map(row=>row.revision),[3,1]);
    assert.equal((await server.call('/version',{body:{id:first.id,revision:2}})).data.record,null);
    assert.equal((await server.call('/version',{body:{id:first.id,revision:1}})).data.headRevision,3);
    assert.equal((await server.call('/read-image',{body:{png:second.embeddedPng}})).data.record.revision,2);
    assert.equal((await server.call('/delete-version',{body:{...deletion,revision:0,key:'invalid'}})).status,400);
    await server.close();server=await launch(dir);
    assert.deepEqual((await server.call('/delete-version',{body:deletion})).data,deleted.data);
    await server.call('/delete-version',{body:{id:first.id,revision:3,key:'delete-v3'}});
    const fourth=await server.call('/save',{body:{...request,id:first.id,expectedRevision:1,key:'v4'}});
    assert.equal(fourth.status,200);assert.equal(fourth.data.revision,4);
  } finally {if(server)await server.close();await rm(dir,{recursive:true,force:true});}
});
