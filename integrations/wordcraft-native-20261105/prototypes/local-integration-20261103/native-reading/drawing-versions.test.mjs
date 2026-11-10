import {test} from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp,rm,writeFile} from 'node:fs/promises';
import path from 'node:path';
import {createDrawingStore} from './drawing-store.mjs';
const png='iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGP4z8DwHwAFAAH/iZk9HQAAAABJRU5ErkJggg==';
const input={expectedRevision:0,key:'first',title:'drawing',xml:'<mxfile/>',messages:[],png};
async function fixture(run){const directory=await mkdtemp(path.join(process.cwd(),'drawing-version-test-'));try{await run(directory);}finally{await rm(directory,{recursive:true,force:true});}}
async function three(store){const one=await store.save(input);const two=await store.save({...input,id:one.id,expectedRevision:1,key:'second'});const three=await store.save({...input,id:one.id,expectedRevision:2,key:'third'});return [one,two,three];}
test('deleting a single saved version preserves the others and portable source across restart',()=>fixture(async directory=>{
  let store=createDrawingStore({directory});const [one,two,third]=await three(store);
  assert.deepEqual((await store.history(one.id)).map(item=>item.revision),[3,2,1]);
  const request={id:one.id,revision:2,key:'delete-second'};
  const receipt=await store.deleteVersion(request);
  assert.equal(receipt.deleted,true);assert.equal(receipt.headRevision,3);
  store=createDrawingStore({directory});assert.deepEqual(await store.deleteVersion(request),receipt);
  assert.deepEqual((await store.history(one.id)).map(item=>item.revision),[3,1]);
  assert.equal((await store.version(one.id,2)).record,null);
  assert.equal((await store.version(one.id,1)).record.revision,1);
  assert.equal((await store.get(one.id)).revision,3);
  await assert.rejects(store.save({...input,id:one.id,expectedRevision:1,key:'second'}),{code:'CONFLICT'});
  assert.equal((await import('./drawing-png.mjs')).readDrawing(two.embeddedPng).revision,2);
  assert.equal((await import('./drawing-png.mjs')).readDrawing(third.embeddedPng).id,one.id);
}));
test('deleting the newest version never reuses its revision and the last version tombstones its identity',()=>fixture(async directory=>{
  const store=createDrawingStore({directory});const [one]=await three(store);
  const removed=await store.deleteVersion({id:one.id,revision:3,key:'delete-third'});
  assert.equal(removed.previousHeadRevision,3);assert.equal(removed.headRevision,2);
  const next=await store.save({...input,id:one.id,expectedRevision:2,key:'fourth'});
  assert.equal(next.revision,4);
  for(const revision of [1,2,4])await store.deleteVersion({id:one.id,revision,key:'delete-'+revision});
  assert.equal(await store.get(one.id),null);assert.deepEqual(await store.history(one.id),[]);
  await assert.rejects(store.save({...input,id:one.id,expectedRevision:0,key:'revive'}),{code:'CONFLICT'});
}));
test('single-version deletion validates identity, has atomic failure and a fixed retry receipt',()=>fixture(async directory=>{
  let failing=false;const store=createDrawingStore({directory,write:async(...args)=>{if(failing)throw Error('write failed');return writeFile(...args);}});
  const [one]=await three(store);const request={id:one.id,revision:2,key:'delete-two'};
  await assert.rejects(store.deleteVersion({...request,revision:0}),{code:'VALIDATION'});
  await assert.rejects(store.deleteVersion({...request,id:'../outside'}),{code:'VALIDATION'});
  await assert.rejects(store.deleteVersion({...request,revision:99}),{code:'NOT_FOUND'});
  await assert.rejects(store.deleteVersion({...request,key:'first'}),{code:'CONFLICT'});
  failing=true;await assert.rejects(store.deleteVersion(request),/write failed/);
  assert.deepEqual((await store.history(one.id)).map(item=>item.revision),[3,2,1]);
  failing=false;const receipt=await store.deleteVersion(request);
  await store.save({...input,id:one.id,expectedRevision:3,key:'fourth'});
  assert.deepEqual(await store.deleteVersion(request),receipt);
  await assert.rejects(store.deleteVersion({...request,revision:1}),{code:'CONFLICT'});
  await assert.rejects(store.save({...input,key:'delete-two'}),{code:'CONFLICT'});
}));
test('saving from a deleted head cannot race it back into the library',()=>fixture(async directory=>{
  const store=createDrawingStore({directory});const [one]=await three(store);
  const outcomes=await Promise.allSettled([
    store.deleteVersion({id:one.id,revision:3,key:'delete-head'}),
    store.save({...input,id:one.id,expectedRevision:3,key:'racing-save'}),
  ]);
  assert.equal(outcomes[0].status,'fulfilled');assert.equal(outcomes[1].status,'rejected');
  assert.equal(outcomes[1].reason.code,'CONFLICT');
  assert.equal((await store.get(one.id)).revision,2);
}));
