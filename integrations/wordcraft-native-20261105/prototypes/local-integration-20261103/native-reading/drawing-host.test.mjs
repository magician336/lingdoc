import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';

test('document channel filters requests and forwards only the addressed drawing operations', async () => {
  const sent = [], calls = [], events = {};
  let channel;
  class Channel {
    constructor(name) { assert.equal(name, 'lingdoc-drawing'); channel = this; }
    postMessage(value) { sent.push(value); }
    close() { this.closed = true; }
  }
  const window = { NATIVE_WORKSPACE_TOKEN: 'token', WORDCRAFT_STANDALONE: true,
    wordcraftHost: { engine: async request => { calls.push(request); return {status:'applied'}; } },
    addEventListener: (name, callback) => { events[name] = callback; } };
  vm.runInNewContext(await readFile(new URL('./drawing-host.js', import.meta.url), 'utf8'),
    { window, document: {title:'Document'}, BroadcastChannel: Channel, crypto: {randomUUID: () => 'host-1'} });
  const send = data => channel.onmessage({data});
  await send({type:'discover', token:'wrong'});
  assert.equal(sent.length, 0);
  await send({type:'discover', token:'token'});
  assert.equal(sent[0].hostId, 'host-1');
  assert.equal(sent[0].standalone, true);
  for (const data of [
    {token:'wrong',hostId:'host-1',request:{op:'diagram.insert'}},
    {token:'token',hostId:'another',request:{op:'diagram.insert'}},
    {token:'token',hostId:'host-1',request:{op:'file.open'}},
  ]) await send({type:'request', requestId:'x', ...data});
  assert.equal(calls.length, 0);
  await send({type:'request',token:'token',hostId:'host-1',requestId:'ok',request:{op:'diagram.insert'}});
  assert.equal(calls.length, 1);
  assert.equal(sent.at(-1).requestId, 'ok');
  assert.equal(sent.at(-1).result.status, 'applied');
  window.wordcraftHost.engine = async () => { throw Error('private failure'); };
  await send({type:'request',token:'token',hostId:'host-1',requestId:'bad',request:{op:'diagram.replace'}});
  assert.match(sent.at(-1).error, /重新选择/);
  assert.doesNotMatch(sent.at(-1).error, /private failure/);
  events.pagehide(); assert.equal(channel.closed, true);
});
