import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';

const source = await readFile(new URL('./drawing.js', import.meta.url), 'utf8');

async function setup() {
  const elements = new Map(), events = {}, calls = [], frames = [];
  let serial = 0, confirmCalls = 0;
  const records = new Map(['a', 'b', 'c'].map(id => [id, {
    id, revision: 1, title: id.toUpperCase(),
    xml: `<mxfile id="${id}"/>`, messages: [], png: 'png',
  }]));
  const state = { empty: true, exportError: false, saveError: false, holdExport: false,
    featureError: false, holdFeature: false, deleteError: false, deleteStatus: 0, listError: false, loseDelete: false, confirm: true, held: null,
    xml: '<mxfile id="edited"/>', messages: [] };
  class Option {
    constructor(text, value) {
      this.text = text;
      this.value = value;
    }
  }
  class Element {
    constructor() {
      this.options = []; this.children = []; this.dataset = {}; this.attributes = {}; this.style = {};
      this.value = '';
      this.disabled = false;
      this.textContent = '';
      this.inert = false;
    }
    replaceChildren(...children) {
      this.children = children; this.options = children;
      this.value = children[0]?.value || '';
    }
    add(option) { this.options.push(option); }
    append(...children) { this.children.push(...children); }
    setAttribute(key, value) { this.attributes[key] = value; }
    querySelectorAll() { return this.children.flatMap(child => [...(child.type === 'button' ? [child] : []), ...(child.querySelectorAll?.() || [])]); }
  }
  const el = id => {
    if (!elements.has(id)) elements.set(id, new Element());
    return elements.get(id);
  };
  const origin = 'http://127.0.0.1:4000', frame = el('editor');
  const reply = (message, result, error) => events.message({
    origin, source: frame.contentWindow,
    data: { channel: 'lingdoc-drawing', requestId: message.requestId, ...(error ? { error } : { result }) },
  });
  frame.contentWindow = {
    postMessage(message) {
      frames.push(message);
      if ((message.type === 'export' && state.holdExport) ||
          (message.type === 'feature' && state.holdFeature)) {
        state.held = message;
        return;
      }
      queueMicrotask(() => {
        if (message.type === 'export') {
          const result = state.empty && message.skipEmpty
            ? { empty: true }
            : { xml: state.xml, messages: state.messages, png: 'data:image/png;base64,png' };
          reply(message, result, state.exportError ? 'export failed' : null);
        } else if (message.type === 'feature') {
          reply(message, { selected: true }, state.featureError ? 'feature failed' : null);
        } else {
          reply(message, { loaded: true });
        }
      });
    },
  };
  class Channel {
    postMessage() { }
    close() { }
  }
  const window = {
    LINGDOC_DRAWING: { token: 'token', origin },
    addEventListener: (name, fn) => { events[name] = fn; },
  };
  vm.runInNewContext(source, {
    window, document: { getElementById: el, createElement: () => new Element() }, BroadcastChannel: Channel, Option, URLSearchParams,
    crypto: { randomUUID: () => `key-${++serial}` }, setTimeout, clearTimeout,
    confirm: () => { confirmCalls++; return state.confirm; },
    fetch: async (url, options) => {
      const action = url.split('/').at(-1), body = options.body ? JSON.parse(options.body) : null;
      calls.push({ action, body });
      if (action === 'list' && state.listError) throw Error('list failed');
      if (action === 'list')
        return { ok: true, json: async () => [...records.values()] };
      if (action === 'get')
        return { ok: true, json: async () => ({ record: records.get(body.id) || null }) };
      if (action === 'delete') {
        if (state.deleteStatus) { const status = state.deleteStatus; state.deleteStatus = 0; return {ok:false,status,json:async()=>({error:'delete rejected'})}; }
        if (state.deleteError) return {ok:false,json:async()=>({error:'delete failed'})};
        records.delete(body.id);
        if (state.loseDelete) { state.loseDelete = false; throw Error('response lost'); }
        return {ok:true,json:async()=>({id:body.id,revision:body.expectedRevision,deleted:true})};
      }
      if (action === 'save') {
        if (state.saveError)
          return { ok: false, json: async () => ({ error: 'save failed' }) };
        const record = { ...body, id: body.id || 'new-id', revision: body.expectedRevision + 1, embeddedPng: 'image' };
        records.set(record.id, record);
        return { ok: true, json: async () => record };
      }
      throw Error(`unexpected ${action}`);
    },
  });
  events.message({ origin, source: frame.contentWindow, data: { channel: 'lingdoc-drawing', type: 'ready' } });
  await new Promise(resolve => setImmediate(resolve));
  async function select(id) {
    const row = el('library-list').children.find(item => item.dataset.id === id);
    await row.children[0].onclick();
  }
  async function remove(id) { const row = el('library-list').children.find(item => item.dataset.id === id); await row.children[1].onclick({stopPropagation(){}}); }
  return { el, state, calls, frames, records, select, remove, reply,
    confirmCalls: () => confirmCalls, close: () => events.pagehide() };
}
test('blank canvas switches without saving or confirmation; selecting current is a no-op', async () => {
  const h = await setup();
  try {
    await h.select('a');
    assert.deepEqual(h.calls.map(c => c.action), ['list', 'get']);
    assert.equal(h.frames[0].skipEmpty, true);
    assert.equal(h.frames[1].type, 'load');
    assert.equal(h.frames[1].xml, h.records.get('a').xml);
    assert.equal(h.confirmCalls(), 0);
    assert.equal(h.el('library-trigger').dataset.currentId, 'a');
    const before = h.calls.length;
    await h.select('a');
    assert.equal(h.calls.length, before);
  } finally {
    h.close();
  }
});

test('existing drawing is saved before loading originally selected destination', async () => {
  const h = await setup();
  try {
    await h.select('a');
    h.state.empty = false;
    h.calls.length = 0;
    h.frames.length = 0;
    await h.select('b');
    assert.deepEqual(h.calls.map(c => c.action), ['get', 'save', 'list']);
    assert.equal(h.frames[0].skipEmpty, false);
    assert.equal(h.frames[1].xml, h.records.get('b').xml);
    assert.equal(h.calls[1].body.id, 'a');
    assert.equal(h.calls[1].body.expectedRevision, 1);
    assert.equal(h.el('library-trigger').dataset.currentId, 'b');
    assert.equal(h.confirmCalls(), 0);
  } finally {
    h.close();
  }
});

test('failed save leaves current canvas selected, then retry succeeds with same key', async () => {
  const h = await setup();
  try {
    await h.select('a');
    h.state.empty = false;
    h.state.saveError = true;
    h.calls.length = 0;
    h.frames.length = 0;
    await h.select('b');
    assert.equal(h.el('library-trigger').dataset.currentId, 'a');
    assert.equal(h.el('status').textContent, 'save failed');
    assert.equal(h.frames.filter(m => m.type === 'load').length, 0);
    const key = h.calls.find(c => c.action === 'save').body.key;
    h.state.saveError = false;
    await h.select('b');
    assert.equal(h.calls.filter(c => c.action === 'save').at(-1).body.key, key);
    assert.equal(h.el('library-trigger').dataset.currentId, 'b');
    assert.equal(h.frames.filter(m => m.type === 'load').length, 1);
  } finally {
    h.close();
  }
});

test('export failure preserves current selection and does not save or load', async () => {
  const h = await setup();
  try {
    await h.select('a');
    h.state.exportError = true;
    h.calls.length = 0;
    h.frames.length = 0;
    await h.select('b');
    assert.equal(h.el('library-trigger').dataset.currentId, 'a');
    assert.equal(h.el('status').textContent, 'export failed');
    assert.deepEqual(h.calls.map(c => c.action), ['get']);
    assert.equal(h.frames.filter(m => m.type === 'load').length, 0);
  } finally {
    h.close();
  }
});

test('busy transition locks canvas and controls and rejects another selection', async () => {
  const h = await setup();
  try {
    await h.select('a');
    h.state.holdExport = true;
    const pending = h.select('b');
    await new Promise(resolve => setImmediate(resolve));
    assert.equal(h.el('editor').inert, true);
    assert.equal(h.el('title').disabled, true);
    assert.equal(h.el('library-trigger').disabled, true);
    await h.select('c');
    assert.equal(h.el('library-trigger').dataset.currentId, 'a');
    assert.equal(h.calls.filter(c => c.action === 'get').at(-1).body.id, 'b');
    h.state.holdExport = false;
    h.reply(h.state.held, { xml: '<mxfile/>', messages: [], png: 'data:image/png;base64,png' });
    await pending;
    assert.equal(h.el('library-trigger').dataset.currentId, 'b');
    assert.equal(h.el('editor').inert, false);
    assert.equal(h.el('title').disabled, false);
    assert.equal(h.el('library-trigger').disabled, false);
  } finally {
    h.close();
  }
});

test('cleared existing drawing still exports without skipEmpty and saves', async () => {
  const h = await setup();
  try {
    await h.select('a');
    h.state.empty = true;
    h.frames.length = 0;
    h.calls.length = 0;
    await h.select('b');
    assert.equal(h.frames[0].skipEmpty, false);
    assert.ok(h.calls.some(c => c.action === 'save'));
    assert.equal(h.el('library-trigger').dataset.currentId, 'b');
  } finally {
    h.close();
  }
});

test('missing destination preserves current drawing without exporting or saving', async () => {
  const h = await setup();
  try {
    await h.select('a');
    h.records.delete('b');
    h.calls.length = 0;
    h.frames.length = 0;
    await h.select('b');
    assert.deepEqual(h.calls.map(c => c.action), ['get']);
    assert.equal(h.frames.length, 0);
    assert.equal(h.el('library-trigger').dataset.currentId, 'a');
    assert.match(h.el('status').textContent, /图稿不存在/);
  } finally {
    h.close();
  }
});


test('feature buttons select each feature without saving or loading', async () => {
  const h = await setup();
  const ids = ['paper', 'animated', 'aws', 'flowchart', 'creative'];
  try {
    h.calls.length = 0;
    h.frames.length = 0;
    for (const id of ids) {
      await h.el('feature-' + id).onclick();
      const request = h.frames.at(-1);
      assert.equal(request.type, 'feature');
      assert.equal(request.feature, id);
      for (const other of ids) {
        assert.equal(h.el('feature-' + other).ariaPressed, String(other === id));
      }
      assert.match(h.el('status').textContent, /已选择/);
    }
    assert.equal(h.frames.length, ids.length);
    assert.deepEqual(h.calls, []);
  } finally {
    h.close();
  }
});

test('feature failure preserves selection and pending request locks controls', async () => {
  const h = await setup();
  const ids = ['paper', 'animated', 'aws', 'flowchart', 'creative'];
  try {
    await h.el('feature-paper').onclick();
    h.calls.length = 0;
    h.frames.length = 0;
    h.state.holdFeature = true;
    const pending = h.el('feature-aws').onclick();
    assert.equal(h.el('editor').inert, true);
    for (const id of ids) assert.equal(h.el('feature-' + id).disabled, true);
    await h.el('feature-creative').onclick();
    assert.equal(h.frames.length, 1);
    assert.equal(h.frames[0].feature, 'aws');
    h.state.holdFeature = false;
    h.reply(h.state.held, null, 'feature failed');
    await pending;
    assert.equal(h.el('status').textContent, 'feature failed');
    assert.equal(h.el('feature-paper').ariaPressed, 'true');
    for (const id of ids.slice(1)) assert.equal(h.el('feature-' + id).ariaPressed, 'false');
    for (const id of ids) assert.equal(h.el('feature-' + id).disabled, false);
    assert.equal(h.el('editor').inert, false);
    assert.deepEqual(h.calls, []);
    assert.deepEqual(h.frames.map(message => message.type), ['feature']);
  } finally {
    h.close();
  }
});


test('delete current drawing preserves canvas and next save creates a new identity', async () => {
  const h=await setup();
  try {
    await h.select('a');h.calls.length=0;h.frames.length=0;
    await h.remove('a');
    const request=h.calls.find(c=>c.action==='delete');
    assert.equal(request.body.id,'a');assert.equal(request.body.expectedRevision,1);assert.ok(request.body.key);
    assert.equal(h.records.has('a'),false);
    assert.equal(h.frames.some(m=>m.type==='load'),false);
    assert.equal(h.el('library-trigger').dataset.currentId,'');
    assert.match(h.el('version').textContent,/未保存/);
    h.state.empty=false;await h.el('save').onclick();
    const save=h.calls.find(c=>c.action==='save');assert.equal(save.body.id,undefined);assert.equal(save.body.expectedRevision,0);
  } finally {h.close();}
});

test('delete failure preserves current drawing and retries with the same key', async () => {
  const h=await setup();
  try {
    await h.select('a');h.calls.length=0;h.frames.length=0;
    h.state.deleteError=true;await h.remove('a');
    assert.equal(h.el('library-trigger').dataset.currentId,'a');assert.equal(h.records.has('a'),true);
    assert.equal(h.frames.some(m=>m.type==='load'),false);
    const first=h.calls.find(c=>c.action==='delete').body;
    h.state.deleteError=false;await h.remove('a');
    const second=h.calls.filter(c=>c.action==='delete').at(-1).body;
    assert.deepEqual(second,first);assert.equal(h.records.has('a'),false);
  } finally {h.close();}
});

test('cancelled deletion performs no write', async () => {
  const h=await setup();
  try {
    await h.select('a');h.calls.length=0;h.state.confirm=false;
    await h.remove('a');
    assert.equal(h.calls.some(c=>c.action==='delete'),false);assert.equal(h.el('library-trigger').dataset.currentId,'a');
  } finally {h.close();}
});


test('deleting a drawing then switching unchanged does not recreate it', async () => {
  const h=await setup();
  try {
    await h.select('a');h.state.empty=false;
    await h.remove('a');
    h.calls.length=0;h.frames.length=0;
    await h.select('b');
    assert.equal(h.calls.some(c=>c.action==='save'),false);
    assert.equal(h.records.has('a'),false);
    assert.equal(h.records.has('new-id'),false);
    assert.equal(h.el('library-trigger').dataset.currentId,'b');
    assert.equal(h.frames.filter(m=>m.type==='load').length,1);
  } finally {h.close();}
});

test('edits after deletion are saved as a new drawing on switch', async () => {
  for(const change of ['xml','messages','title']){
    const h=await setup();
    try {
      await h.select('a');h.state.empty=false;
      await h.remove('a');
      if(change==='xml')h.state.xml='<mxfile id="new-change"/>';
      if(change==='messages')h.state.messages=[{role:'user',content:'changed'}];
      if(change==='title')h.el('title').value='Changed title';
      h.calls.length=0;await h.select('b');
      const saves=h.calls.filter(c=>c.action==='save');assert.equal(saves.length,1,change);
      assert.equal(saves[0].body.id,undefined,change);assert.equal(saves[0].body.expectedRevision,0,change);
      assert.equal(h.records.has('new-id'),true,change);assert.equal(h.el('library-trigger').dataset.currentId,'b');
    } finally {h.close();}
  }
});

test('explicit save after deletion recreates the drawing even if unchanged',async()=>{
  const h=await setup();
  try {
    await h.select('a');h.state.empty=false;await h.remove('a');
    h.calls.length=0;await h.el('save').onclick();
    const save=h.calls.find(c=>c.action==='save');assert.ok(save);
    assert.equal(save.body.id,undefined);assert.equal(save.body.expectedRevision,0);
    assert.equal(h.records.has('new-id'),true);
  } finally {h.close();}
});

test('delete snapshot export failure keeps saved drawing and canvas',async()=>{
  const h=await setup();
  try {
    await h.select('a');h.state.empty=false;h.state.exportError=true;
    h.calls.length=0;h.frames.length=0;await h.remove('a');
    assert.equal(h.calls.some(c=>c.action==='delete'),false);
    assert.equal(h.records.has('a'),true);assert.equal(h.el('library-trigger').dataset.currentId,'a');
    assert.equal(h.frames.some(m=>m.type==='load'),false);
    assert.match(h.el('status').textContent,/export failed/);
  } finally {h.close();}
});


test('library search filters titles and deleting another row leaves canvas and selection untouched', async () => {
  const h = await setup();
  try {
    await h.select('a');
    h.el('library-search').value = 'B'; h.el('library-search').oninput();
    assert.deepEqual(h.el('library-list').children.map(row => row.dataset.id), ['b']);
    h.calls.length = 0; h.frames.length = 0;
    await h.remove('b');
    assert.deepEqual(h.calls.map(call => call.action), ['delete', 'list']);
    assert.equal(h.calls[0].body.id, 'b');
    assert.equal(h.calls[0].body.expectedRevision, 1);
    assert.equal(h.frames.length, 0);
    assert.equal(h.el('library-trigger').dataset.currentId, 'a');
    assert.equal(h.records.has('a'), true);
    assert.equal(h.records.has('b'), false);
  } finally { h.close(); }
});

test('cancel and lost response for another row retain canvas and freeze delete identity across refresh', async () => {
  const h = await setup();
  try {
    await h.select('a'); h.calls.length = 0; h.frames.length = 0;
    h.state.confirm = false; await h.remove('b');
    assert.equal(h.calls.length, 0); assert.equal(h.frames.length, 0);
    h.state.confirm = true; h.state.loseDelete = true; await h.remove('b');
    const first = h.calls.find(call => call.action === 'delete').body;
    assert.equal(h.records.has('b'), false);
    await h.remove('c');
    assert.equal(h.calls.filter(call => call.action === 'delete').length, 1);
    assert.match(h.el('status').textContent, /重试/);
    // Refresh the list via a normal save; the missing row remains available for retry.
    await h.el('save').onclick();
    assert.ok(h.el('library-list').children.some(row => row.dataset.id === 'b'));
    h.frames.length = 0; await h.remove('b');
    const requests = h.calls.filter(call => call.action === 'delete');
    assert.deepEqual(requests[1].body, first);
    assert.equal(h.frames.length, 0);
    assert.equal(h.el('library-trigger').dataset.currentId, 'a');
    assert.equal(h.records.has('c'), true);
  } finally { h.close(); }
});


test('delete conflict refreshes latest version and permits a newly confirmed delete', async () => {
  for (const id of ['a', 'b']) {
    const h = await setup();
    try {
      await h.select('a'); h.calls.length = 0; h.frames.length = 0;
      h.records.set(id, {...h.records.get(id), revision: 2});
      h.state.deleteStatus = 409;
      await h.remove(id);
      const first = h.calls.find(call => call.action === 'delete').body;
      assert.equal(first.expectedRevision, 1);
      assert.equal(h.records.has(id), true);
      assert.equal(h.el('library-trigger').dataset.currentId, 'a');
      assert.equal(h.frames.filter(frame => frame.type === 'load').length, 0);
      h.state.confirm = false; await h.remove(id);
      assert.equal(h.calls.filter(call => call.action === 'delete').length, 1);
      h.state.confirm = true; await h.remove(id);
      const second = h.calls.filter(call => call.action === 'delete').at(-1).body;
      assert.equal(second.expectedRevision, 2);
      assert.notEqual(second.key, first.key);
      assert.equal(h.records.has(id), false);
      assert.equal(h.el('library-trigger').dataset.currentId, id === 'a' ? '' : 'a');
    } finally { h.close(); }
  }
});

test('an HTTP 500 preserves the exact deletion request until retry', async () => {
  const h = await setup();
  try {
    await h.select('a'); h.state.deleteStatus = 500;
    await h.remove('b');
    const first = h.calls.find(call => call.action === 'delete').body;
    await h.remove('c');
    assert.equal(h.calls.filter(call => call.action === 'delete').length, 1);
    await h.remove('b');
    assert.deepEqual(h.calls.filter(call => call.action === 'delete').at(-1).body, first);
    assert.equal(h.el('library-trigger').dataset.currentId, 'a');
  } finally { h.close(); }
});


test('delete rejection with failed list refresh preserves the current canvas', async () => {
  const h = await setup();
  try {
    await h.select('a'); h.calls.length = 0; h.frames.length = 0;
    h.state.deleteStatus = 409; h.state.listError = true;
    await h.remove('a');
    assert.equal(h.records.has('a'), true);
    assert.equal(h.el('library-trigger').dataset.currentId, 'a');
    assert.equal(h.frames.filter(frame => frame.type === 'load').length, 0);
    assert.equal(h.el('status').textContent, 'delete rejected');
    h.state.listError = false;
    await h.remove('b');
    assert.equal(h.records.has('b'), false);
    assert.equal(h.el('library-trigger').dataset.currentId, 'a');
  } finally { h.close(); }
});
