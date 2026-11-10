import {test} from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp,rm,writeFile} from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import {createDrawingStore} from './drawing-store.mjs';
const png='iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGP4z8DwHwAFAAH/iZk9HQAAAABJRU5ErkJggg==';
const request={expectedRevision:0,key:'one',title:'first',xml:'<mxfile/>',messages:[],png};
test('save survives restart and retries are idempotent',async()=>{const dir=await mkdtemp(path.join(process.cwd(),'draw-test-'));try {let store=createDrawingStore({directory:dir}); const one=await store.save(request); store=createDrawingStore({directory:dir}); assert.deepEqual(await store.save(request),one); assert.deepEqual(await store.get(one.id),Object.fromEntries(Object.entries(one).filter(([key])=>key!=='embeddedPng'))); await assert.rejects(store.save({...request,title:'different'}),{code:'CONFLICT'}); const two=await store.save({...request,id:one.id,key:'two',expectedRevision:1}); assert.equal(two.revision,2); assert.equal((await store.list()).length,1); assert.deepEqual(await store.save(request),one); }finally{await rm(dir,{recursive:true,force:true})}});
test('concurrent writes have one winner',async()=>{const dir=await mkdtemp(path.join(process.cwd(),'draw-test-'));try {const store=createDrawingStore({directory:dir});const one=await store.save(request);const results=await Promise.allSettled(['a','b'].map(key=>store.save({...request,id:one.id,expectedRevision:1,key}))); assert.equal(results.filter(r=>r.status==='fulfilled').length,1); assert.equal(results.filter(r=>r.status==='rejected'&&r.reason.code==='CONFLICT').length,1); }finally{await rm(dir,{recursive:true,force:true})}});
test('failed write leaves prior revision intact and retry succeeds',async()=>{const dir=await mkdtemp(path.join(process.cwd(),'draw-test-'));try {let fail=false;const store=createDrawingStore({directory:dir,write:async (...args)=>{if(fail) throw new Error('disk failure');return writeFile(...args)}});const one=await store.save(request);fail=true;await assert.rejects(store.save({...request,id:one.id,expectedRevision:1,key:'two'}),/disk failure/);assert.deepEqual(await store.get(one.id),Object.fromEntries(Object.entries(one).filter(([key])=>key!=='embeddedPng')));fail=false;assert.equal((await store.save({...request,id:one.id,expectedRevision:1,key:'two'})).revision,2);}finally{await rm(dir,{recursive:true,force:true})}});
test('reject traversal and unsafe XML',async()=>{const dir=await mkdtemp(path.join(process.cwd(),'draw-test-'));try {const store=createDrawingStore({directory:dir});await assert.rejects(store.save({...request,id:'../x'}),{code:'VALIDATION'});await assert.rejects(store.save({...request,xml:'<!DOCTYPE x><x/>'}),{code:'VALIDATION'});}finally{await rm(dir,{recursive:true,force:true})}});



test('metadata size exceeding PNG limit fails before commit',async()=>{const dir=await mkdtemp(path.join(process.cwd(),'draw-test-'));try {const original=Buffer.from(png,'base64');const data=Buffer.alloc(4*1024*1024-original.length-12,65);const type=Buffer.from('tEXt');let crc=0xffffffff;for(const b of Buffer.concat([type,data])){crc^=b;for(let i=0;i<8;i++)crc=crc&1?0xedb88320^(crc>>>1):crc>>>1;}const chunk=Buffer.alloc(data.length+12);chunk.writeUInt32BE(data.length);type.copy(chunk,4);data.copy(chunk,8);chunk.writeUInt32BE((crc^0xffffffff)>>>0,chunk.length-4);const large=Buffer.concat([original.subarray(0,original.length-12),chunk,original.subarray(original.length-12)]).toString('base64');const store=createDrawingStore({directory:dir});await assert.rejects(store.save({...request,png:large}),{code:'VALIDATION'});assert.deepEqual(await store.list(),[]);}finally{await rm(dir,{recursive:true,force:true})}});

test('delete removes all revisions, survives restart, and prevents revival',async()=>{
  const dir=await mkdtemp(path.join(process.cwd(),'draw-test-'));
  try {
    let store=createDrawingStore({directory:dir});
    const first=await store.save(request);
    const second=await store.save({...request,id:first.id,key:'update',expectedRevision:1});
    const del={id:first.id,expectedRevision:2,key:'delete-one'};
    assert.deepEqual(await store.delete(del),{id:first.id,revision:2,deleted:true});
    assert.equal(await store.get(first.id),null);
    assert.deepEqual(await store.list(),[]);
    store=createDrawingStore({directory:dir});
    assert.deepEqual(await store.delete(del),{id:first.id,revision:2,deleted:true});
    await assert.rejects(store.delete({...del,expectedRevision:1}),{code:'CONFLICT'});
    await assert.rejects(store.save(request),{code:'CONFLICT'});
    await assert.rejects(store.save({...request,id:first.id,key:'revive',expectedRevision:0}),{code:'CONFLICT'});
    await assert.rejects(store.save({...request,id:first.id,key:'revive2',expectedRevision:2}),{code:'CONFLICT'});
    assert.equal((await import('./drawing-png.mjs')).readDrawing(second.embeddedPng).revision,2);
  } finally {await rm(dir,{recursive:true,force:true});}
});

test('delete guards revisions, key reuse, missing ids, and invalid input',async()=>{
  const dir=await mkdtemp(path.join(process.cwd(),'draw-test-'));
  try {
    const store=createDrawingStore({directory:dir});const first=await store.save(request);
    await assert.rejects(store.delete({id:first.id,expectedRevision:2,key:'d1'}),{code:'CONFLICT'});
    await assert.rejects(store.delete({id:first.id,expectedRevision:1,key:'one'}),{code:'CONFLICT'});
    await assert.rejects(store.delete({id:'missing',expectedRevision:1,key:'d2'}),{code:'NOT_FOUND'});
    await assert.rejects(store.delete({id:'../x',expectedRevision:1,key:'d3'}),{code:'VALIDATION'});
    assert.equal((await store.get(first.id)).revision,1);
    await store.delete({id:first.id,expectedRevision:1,key:'d4'});
    await assert.rejects(store.save({...request,key:'d4'}),{code:'CONFLICT'});
    await assert.rejects(store.delete({id:'other',expectedRevision:1,key:'d4'}),{code:'CONFLICT'});
  } finally {await rm(dir,{recursive:true,force:true});}
});

test('concurrent save and delete cannot both commit the same revision',async()=>{
  const dir=await mkdtemp(path.join(process.cwd(),'draw-test-'));
  try {
    const store=createDrawingStore({directory:dir});const first=await store.save(request);
    const results=await Promise.allSettled([
      store.delete({id:first.id,expectedRevision:1,key:'remove'}),
      store.save({...request,id:first.id,expectedRevision:1,key:'update'}),
    ]);
    assert.equal(results.filter(r=>r.status==='fulfilled').length,1);
    assert.equal(results.filter(r=>r.status==='rejected'&&r.reason.code==='CONFLICT').length,1);
    assert.equal(await store.get(first.id),null);
  } finally {await rm(dir,{recursive:true,force:true});}
});

test('delete write failure preserves the original record and can be retried',async()=>{
  const dir=await mkdtemp(path.join(process.cwd(),'draw-test-'));
  try {
    let failing=false;
    const store=createDrawingStore({directory:dir,write:async(...args)=>{if(failing)throw Error('disk failure');return writeFile(...args);}});
    const first=await store.save(request);const del={id:first.id,expectedRevision:1,key:'remove'};
    failing=true;await assert.rejects(store.delete(del),/disk failure/);
    assert.equal((await store.get(first.id)).revision,1);
    failing=false;assert.equal((await store.delete(del)).deleted,true);
  } finally {await rm(dir,{recursive:true,force:true});}
});

test('delete retry is serialized and a prior edit rejects stale deletion',async()=>{
  const dir=await mkdtemp(path.join(process.cwd(),'draw-test-'));
  try {
    const store=createDrawingStore({directory:dir});const first=await store.save(request);
    const [edit,stale]=await Promise.allSettled([
      store.save({...request,id:first.id,expectedRevision:1,key:'edit'}),
      store.delete({id:first.id,expectedRevision:1,key:'delete-stale'}),
    ]);
    assert.equal(edit.status,'fulfilled');assert.equal(stale.status,'rejected');assert.equal(stale.reason.code,'CONFLICT');
    const deletion={id:first.id,expectedRevision:2,key:'delete-current'};
    const results=await Promise.all([store.delete(deletion),store.delete(deletion)]);
    assert.deepEqual(results[0],results[1]);
  } finally {await rm(dir,{recursive:true,force:true});}
});
