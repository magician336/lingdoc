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
    featureError: false, holdFeature: false, held: null };
  class Option {
    constructor(text, value) {
      this.text = text;
      this.value = value;
    }
  }
  class Element {
    constructor() {
      this.options = [];
      this.value = '';
      this.disabled = false;
      this.textContent = '';
      this.inert = false;
    }
    replaceChildren(...children) {
      this.options = children;
      this.value = children[0]?.value || '';
    }
    add(option) { this.options.push(option); }
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
            : { xml: '<mxfile id="edited"/>', messages: [], png: 'data:image/png;base64,png' };
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
    window, document: { getElementById: el }, BroadcastChannel: Channel, Option,
    crypto: { randomUUID: () => `key-${++serial}` }, setTimeout, clearTimeout,
    confirm: () => { confirmCalls++; return true; },
    fetch: async (url, options) => {
      const action = url.split('/').at(-1), body = options.body ? JSON.parse(options.body) : null;
      calls.push({ action, body });
      if (action === 'list')
        return { ok: true, json: async () => [...records.values()] };
      if (action === 'get')
        return { ok: true, json: async () => ({ record: records.get(body.id) || null }) };
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
    el('saved').value = id;
    await el('saved').onchange();
  }
  return { el, state, calls, frames, records, select, reply,
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
    assert.equal(h.el('saved').value, 'a');
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
    assert.equal(h.el('saved').value, 'b');
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
    assert.equal(h.el('saved').value, 'a');
    assert.equal(h.el('status').textContent, 'save failed');
    assert.equal(h.frames.filter(m => m.type === 'load').length, 0);
    const key = h.calls.find(c => c.action === 'save').body.key;
    h.state.saveError = false;
    await h.select('b');
    assert.equal(h.calls.filter(c => c.action === 'save').at(-1).body.key, key);
    assert.equal(h.el('saved').value, 'b');
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
    assert.equal(h.el('saved').value, 'a');
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
    h.el('saved').value = 'b';
    const pending = h.el('saved').onchange();
    await new Promise(resolve => setImmediate(resolve));
    assert.equal(h.el('editor').inert, true);
    assert.equal(h.el('title').disabled, true);
    assert.equal(h.el('saved').disabled, true);
    h.el('saved').value = 'c';
    await h.el('saved').onchange();
    assert.equal(h.el('saved').value, 'a');
    assert.equal(h.calls.filter(c => c.action === 'get').at(-1).body.id, 'b');
    h.state.holdExport = false;
    h.reply(h.state.held, { xml: '<mxfile/>', messages: [], png: 'data:image/png;base64,png' });
    await pending;
    assert.equal(h.el('saved').value, 'b');
    assert.equal(h.el('editor').inert, false);
    assert.equal(h.el('title').disabled, false);
    assert.equal(h.el('saved').disabled, false);
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
    assert.equal(h.el('saved').value, 'b');
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
    assert.equal(h.el('saved').value, 'a');
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
