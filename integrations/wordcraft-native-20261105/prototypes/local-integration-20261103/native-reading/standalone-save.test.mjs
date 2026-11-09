import test from 'node:test';
import assert from 'node:assert/strict';
import vm from 'node:vm';
import { readFile } from 'node:fs/promises';

async function host(replies) {
  const source = await readFile(new URL('./drawing-host.js', import.meta.url), 'utf8');
  const start = source.indexOf('function installStandaloneSave(');
  assert.notEqual(start, -1, 'drawing-host must provide the standalone save helper');
  const calls = [];
  const host = {standalone:true,engine:async request => {
    calls.push(request);
    const answer = replies.shift();
    if (!answer) throw Error('unexpected bridge call');
    if (!answer.ok) throw Error(answer.error);
    return answer.result;
  }};
  const install = vm.runInNewContext(source.slice(start) + '\ninstallStandaloneSave', {setTimeout, Date, Error});
  install(host);
  return {host,calls};
}
const ok = result => ({ok:true,result});
const state = {documentId:4,revision:8,dirty:true};

test('ordinary save reports actual saved and downloaded outcomes separately', async () => {
  for (const status of ['saved','downloaded']) {
    const live={documentId:4,revision:9,dirty:false};
    const h=await host([ok(state),ok({ticket:3,state:live}),ok({status,state:live})]);
    const result=await h.host.saveIntent('drawing',state);
    assert.equal(result.status,status); assert.equal(result.live.revision,9);
    assert.equal(h.calls[1].op,'save.begin'); assert.equal(h.calls[1].expectedDocumentId,4);
    assert.equal(h.calls[2].ticket,3);
  }
});

test('ordinary save rejects changed identity and actual cancellation or failure', async () => {
  const changed=await host([ok(state)]);
  await assert.rejects(changed.host.saveIntent('drawing',{documentId:5,revision:8}), e=>e.code==='stale');
  assert.equal(changed.calls.length,1);
  for (const status of ['cancelled','failed','stale']) {
    const h=await host([ok(state),ok({ticket:3}),ok({status,state})]);
    await assert.rejects(h.host.saveIntent('drawing',state), e=>e.code===status);
  }
});
