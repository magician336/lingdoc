import test from 'node:test';
import assert from 'node:assert/strict';
import vm from 'node:vm';
import {readFile} from 'node:fs/promises';

async function harness(states, pendingSave = null) {
  const source = await readFile(new URL('./host.js', import.meta.url), 'utf8');
  const helper = source.slice(source.indexOf('function matchesSaveTarget('), source.indexOf('\nconst coordinator ='));
  const start = source.indexOf('  async persistSavedFile() {');
  const end = source.indexOf('  async registerSavedVersion(', start);
  const persist = source.slice(start, end).trim().replace(/,\s*$/, '');
  const save = source.slice(source.indexOf('async function saveIntent('), source.indexOf('\nlet pendingSaveAs'));
  assert(start >= 0 && end > start && save.includes('guardedSaveTarget'));
  const calls=[];
  const ctx = {
    guardedSaveTarget:null,pendingSave,pendingGoRegistration:null,diskVersion:0,
    saveSources:[],lastSave:null,deliveryError:null,researchPanel:null,
    report:value=>value,notify:()=>{},hash:async()=> 'digest',
    engine:async request => {
      calls.push(request);
      if (request.op === 'status') {const state=states.shift();if(!state)throw Error('unexpected status');return state;}
      if (request.op === 'export') return {bytes:[1,2,3],sha256:'digest',saveToken:'token'};
      throw Error('unexpected operation');
    },
    api:async (_op,payload)=>({sha256:payload.sha256,bytes:payload.bytes.length,saveToken:payload.saveToken,
      documentId:payload.documentId,revision:payload.revision,baseDiskVersion:payload.expectedDiskVersion,diskVersion:1,verified:true}),
  };
  vm.createContext(ctx);
  vm.runInContext(helper+'\nglobalThis.adapter={'+persist+'};\n'+save+'\nglobalThis.saveIntent=saveIntent;',ctx);
  ctx.coordinator={transaction:null,nextTransaction:null,receipt:null,request:async()=>{
    const {receipt}=await ctx.adapter.persistSavedFile();
    return {receipt,version:{id:'v'},delivery:{},live:{documentId:receipt.documentId,revision:receipt.revision,dirty:false},ackPending:false};
  }};
  return {ctx,calls};
}

test('guarded project save rejects document change before actual export', async () => {
  const original={documentId:4,revision:8}, changed={documentId:5,revision:8};
  const {ctx,calls}=await harness([original,changed]);
  await assert.rejects(ctx.saveIntent('drawing',original),/文稿已变化/);
  assert.deepEqual(calls.map(call=>call.op),['status','status']);
  assert.equal(ctx.pendingSave,null); assert.equal(ctx.guardedSaveTarget,null);
});

test('guarded project save rejects mismatched retry and persists matching revision', async () => {
  const expected={documentId:4,revision:8};
  const rejected=await harness([expected],{state:{documentId:5,revision:8}});
  await assert.rejects(rejected.ctx.saveIntent('drawing',expected),/另一个文档/);
  assert.deepEqual(rejected.calls.map(call=>call.op),['status']);
  const good=await harness([expected,expected]);
  const result=await good.ctx.saveIntent('drawing',expected);
  assert.equal(result.receipt.documentId,4);assert.equal(result.receipt.revision,8);
  assert.deepEqual(good.calls.map(call=>call.op),['status','status','export']);
  assert.equal(good.calls[2].expectedDocumentId,4);assert.equal(good.calls[2].expectedRevision,8);
  assert.equal(good.ctx.guardedSaveTarget,null);
});

test('guarded project save rejects concurrent save and keeps old unguarded calls compatible', async () => {
  const expected={documentId:4,revision:8};
  const busy=await harness([expected]);busy.ctx.coordinator.transaction=Promise.resolve();
  await assert.rejects(busy.ctx.saveIntent('drawing',expected),/另一项保存/);
  const normal=await harness([expected,expected]);
  assert.equal((await normal.ctx.saveIntent('host-api')).receipt.documentId,4);
});
