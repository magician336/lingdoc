import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';

test('window trusts only the configured frame and sends captured text to that origin', async () => {
  const elements = new Map(), events = {}, frameMessages = [], channelMessages = [];
  let channel, serial = 0, lostResponse = false, committed, versions = 0; const retryRequests = [];
  class Option { constructor(text, value) { this.text = text; this.value = value; } }
  class Element {
    constructor() { this.options = []; this.value = ''; this.disabled = false; this.textContent = ''; }
    replaceChildren(...children) { this.options = children; this.value = children[0]?.value || ''; }
    add(option) { this.options.push(option); }
  }
  const element = id => { if (!elements.has(id)) elements.set(id, new Element()); return elements.get(id); };
  const frame = element('editor'); frame.contentWindow = {postMessage: (data, origin) => frameMessages.push({data, origin})};
  class Channel {
    constructor() { channel = this; }
    postMessage(data) { channelMessages.push(data); }
    close() { this.closed = true; }
  }
  const window = {LINGDOC_DRAWING:{token:'secret',origin:'http://127.0.0.1:4000'}, addEventListener:(name, callback) => { events[name] = callback; }};
  vm.runInNewContext(await readFile(new URL('./drawing.js', import.meta.url), 'utf8'), {
    window, document:{getElementById:element}, BroadcastChannel:Channel, Option,
    crypto:{randomUUID:() => `id-${++serial}`}, fetch:async (url, options) => {
      if (url.endsWith('/save') && lostResponse) {
        const request = JSON.parse(options.body); retryRequests.push(request);
        if (!committed) { committed = {id:'drawing-1',revision:2,title:request.title,embeddedPng:'encoded-png'}; versions++; throw Error('connection lost'); }
        assert.equal(request.key, retryRequests[0].key);
        return {ok:true,json:async () => committed};
      }
      return {ok:true,json:async () => url.endsWith('/save') ? {id:'drawing-1',revision:1,title:'Diagram',embeddedPng:'encoded-png'} : []};
    },
    setTimeout, clearTimeout, confirm:() => true,
  });
  assert.equal(frame.src, 'http://127.0.0.1:4000/zh');
  assert.equal(element('capture').disabled, true);
  const ready = {channel:'lingdoc-drawing',type:'ready'};
  events.message({origin:'http://evil',source:frame.contentWindow,data:ready});
  events.message({origin:window.LINGDOC_DRAWING.origin,source:{},data:ready});
  assert.equal(element('capture').disabled, true);
  events.message({origin:window.LINGDOC_DRAWING.origin,source:frame.contentWindow,data:ready});
  assert.equal(element('capture').disabled, false);
  assert.equal(element('insert').disabled, true);
  channel.onmessage({data:{type:'host',token:'wrong',hostId:'host-1',title:'Doc'}});
  assert.equal(element('hosts').options.length, 1);
  channel.onmessage({data:{type:'host',token:'secret',hostId:'host-1',title:'Doc'}});
  element('hosts').value = 'host-1';
  const running = element('context').onclick();
  const request = channelMessages.at(-1);
  assert.equal(request.hostId, 'host-1'); assert.equal(request.request.op, 'diagram.selection');
  channel.onmessage({data:{type:'result',token:'secret',hostId:'host-1',requestId:request.requestId,
    result:{state:{documentId:1,revision:2},selection:{anchor:0,focus:1,text:'研究内容'},image:null}}});
  await new Promise(resolve => setImmediate(resolve));
  const prompt = frameMessages.at(-1);
  assert.equal(prompt.origin, window.LINGDOC_DRAWING.origin);
  assert.equal(prompt.data.type, 'prompt'); assert.match(prompt.data.prompt, /研究内容/);
  // Wrong window cannot resolve a request even with a matching requestId.
  events.message({origin:window.LINGDOC_DRAWING.origin,source:{},data:{channel:'lingdoc-drawing',requestId:prompt.data.requestId,result:{}}});
  assert.equal(element('context').disabled, true);
  events.message({origin:window.LINGDOC_DRAWING.origin,source:frame.contentWindow,data:{channel:'lingdoc-drawing',requestId:prompt.data.requestId,result:{}}});
  await running;
  assert.equal(element('context').disabled, false);
  assert.equal(element('insert').disabled, true);

  const capture = element('capture').onclick();
  const captureRequest = channelMessages.at(-1);
  const selection = {anchor:{off:3},focus:{off:3},text:''};
  channel.onmessage({data:{type:'result',token:'secret',hostId:'host-1',requestId:captureRequest.requestId,
    result:{state:{documentId:9,revision:4},selection,image:null}}});
  await capture;
  assert.equal(element('insert').disabled, false);
  assert.equal(element('replace').disabled, true);
  const insertion = element('insert').onclick();
  const exportRequest = frameMessages.at(-1);
  assert.equal(exportRequest.data.type, 'export');
  events.message({origin:window.LINGDOC_DRAWING.origin,source:frame.contentWindow,
    data:{channel:'lingdoc-drawing',requestId:exportRequest.data.requestId,
      result:{xml:'<mxfile/>',messages:[],png:'data:image/png;base64,encoded'}}});
  await new Promise(resolve => setImmediate(resolve));
  const write = channelMessages.at(-1);
  assert.equal(write.hostId, 'host-1'); assert.equal(write.request.op, 'diagram.insert');
  assert.equal(write.request.expectedDocumentId, 9); assert.equal(write.request.expectedRevision, 4);
  assert.equal(write.request.bookmark, selection); assert.equal(write.request.data, 'encoded-png');
  channel.onmessage({data:{type:'result',token:'secret',hostId:'host-1',requestId:write.requestId,result:{status:'applied'}}});
  await insertion;
  assert.equal(element('insert').disabled, true);
  // The server commits but the first response is lost. A second click with the
  // same export must send the same key and receive the original revision.
  lostResponse = true;
  const exportResult = {xml:'<mxfile/>',messages:[],png:'data:image/png;base64,encoded'};
  const firstSave = element('save').onclick();
  let pendingExport = frameMessages.at(-1);
  events.message({origin:window.LINGDOC_DRAWING.origin,source:frame.contentWindow,
    data:{channel:'lingdoc-drawing',requestId:pendingExport.data.requestId,result:exportResult}});
  await firstSave;
  assert.equal(element('status').textContent, 'connection lost');
  const secondSave = element('save').onclick();
  pendingExport = frameMessages.at(-1);
  events.message({origin:window.LINGDOC_DRAWING.origin,source:frame.contentWindow,
    data:{channel:'lingdoc-drawing',requestId:pendingExport.data.requestId,result:exportResult}});
  await secondSave;
  assert.equal(retryRequests.length, 2);
  assert.equal(retryRequests[0].key, retryRequests[1].key);
  assert.deepEqual(retryRequests[0], retryRequests[1]);
  assert.equal(versions, 1);
  assert.match(element('status').textContent, /第 2 版/);
  events.pagehide(); assert.equal(channel.closed, true);
});
