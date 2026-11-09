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
    { window, document: {title:'Document'}, BroadcastChannel: Channel, crypto: {randomUUID: () => 'host-1'}, URLSearchParams, setTimeout, clearTimeout });
  const send = data => channel.onmessage({data});
  await send({type:'discover', token:'wrong'});
  assert.equal(sent.length, 0);
  await send({type:'discover', token:'token'});
  assert.equal(sent[0].hostId, 'host-1');
  assert.equal(sent[0].standalone, true);
  assert.equal(sent[0].picker, true);
  assert.equal(window.LINGDOC_DRAWING_HOST_ID, 'host-1');
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

test('picker validates the live document and issues expiring single-use tickets', async () => {
  const sent = [], calls = [], events = {}, timers = new Map(), popups = [];
  let channel, now = 1000, serial = 0, activeSource = false, auxiliary = false, blocked = false, noDocument = false, missingStatus = false;
  let selection = {state:{documentId:1,revision:2},selection:{anchor:{off:0},focus:{off:1}},image:null};
  class FakeDate extends Date { static now() { return now; } }
  class Element {
    constructor(tag) { this.tag = tag; this.style = {}; this.children = []; this.listeners = {}; this.removed = false; }
    setAttribute() {}
    append(...children) { this.children.push(...children); }
    addEventListener(name, fn) { this.listeners[name] = fn; }
    remove() { this.removed = true; }
    click() { return this.listeners.click(); }
  }
  class Channel { constructor() { channel = this; } postMessage(value) { sent.push(value); } close() {} }
  const body = new Element('body');
  const window = { NATIVE_WORKSPACE_TOKEN:'token',
    wordcraftHost:{engine:async request => {
      calls.push(request.op);
      if (request.op === 'workspace.editor') { if (calls.length === 1) assert.equal(body.children.length, 0); return {}; }
      if (request.op === 'source.status') return missingStatus ? {mode:'split'} : {mode:'split',sourceActive:activeSource};
      if (request.op === 'workspace.status') return {auxiliaryActive:auxiliary};
      if (request.op === 'diagram.selection') { if (noDocument) throw Error('Document canvas unavailable'); return selection; }
      throw Error('unexpected operation');
    }},
    open:() => { if (blocked) return null; const popup={location:{href:''},closed:false,close(){this.closed=true;}};popups.push(popup);return popup; },
    addEventListener:(name, fn) => { events[name] = fn; },
  };
  const setTimer=(fn, delay) => { const id=++serial; timers.set(id,{fn,at:now+delay}); return id; };
  const advance=ms => { now+=ms; for (const [id,timer] of [...timers]) if (timer.at<=now) {timers.delete(id);timer.fn();} };
  vm.runInNewContext(await readFile(new URL('./drawing-host.js', import.meta.url), 'utf8'), {
    window, document:{title:'Doc',body,createElement:tag=>new Element(tag)}, BroadcastChannel:Channel,
    crypto:{randomUUID:()=>`uuid-${++serial}`}, URLSearchParams, Date:FakeDate, setTimeout:setTimer, clearTimeout:id=>timers.delete(id),
  });
  const hostId=window.LINGDOC_DRAWING_HOST_ID;
  const send=(request, token='token', target=hostId) => channel.onmessage({data:{type:'request',token,hostId:target,requestId:'request',request}});
  await send({op:'diagram.pick',pickId:'pick-1',draftId:'draft-1'},'wrong');
  assert.equal(body.children.length,0);
  await send({op:'diagram.pick',pickId:'pick-1',draftId:'draft-1'});
  assert.equal(sent.at(-1).result.picking,true);
  assert.deepEqual(calls,['workspace.editor'],'picker must show the editor before creating the banner');
  const banner=body.children.at(-1), [label,confirm,cancel]=banner.children;
  assert.match(banner.style.cssText,/bottom:40px/); assert.match(banner.style.cssText,/flex-wrap:wrap/);
  assert.match(banner.style.cssText,/max-width:calc\(100% - 32px\)/);
  blocked=true; await confirm.click(); assert.match(label.textContent,/阻止/); assert.deepEqual(calls,['workspace.editor']);
  blocked=false; await confirm.click();
  assert.equal(popups.at(-1).closed,true); assert.equal(banner.removed,false);
  assert.match(label.textContent,/单击/); assert.deepEqual(calls,['workspace.editor','source.status','workspace.status','diagram.selection']);
  activeSource=true; selection={...selection,selection:{anchor:{off:2},focus:{off:2}}};
  await confirm.click(); assert.equal(popups.at(-1).closed,true); assert.match(label.textContent,/正文/);
  activeSource=false; auxiliary=true; await confirm.click(); assert.equal(popups.at(-1).closed,true);
  auxiliary=false; noDocument=true; await confirm.click();
  assert.equal(popups.at(-1).closed,true); assert.equal(banner.removed,false);
  noDocument=false; missingStatus=true; await confirm.click();
  assert.equal(popups.at(-1).closed,true); assert.match(label.textContent,/正文/);
  missingStatus=false; await confirm.click();
  const url=new URL(popups.at(-1).location.href,'http://localhost');
  assert.equal(url.pathname,'/native-reading/drawing'); assert.equal(url.searchParams.get('host'),hostId);
  assert.equal(url.searchParams.get('draft'),'draft-1'); assert.equal(banner.removed,true);
  const ticket=url.searchParams.get('resume');
  await send({op:'diagram.resume',ticket},'wrong');
  await send({op:'diagram.resume',ticket},'token','other-host');
  await send({op:'diagram.resume',ticket}); assert.equal(sent.at(-1).result.state.documentId,1);
  await send({op:'diagram.resume',ticket}); assert.match(sent.at(-1).error,/失效/);
  selection={...selection,selection:{anchor:{off:0},focus:{off:3}},image:{sha256:'image-digest'}};
  await send({op:'diagram.pick',pickId:'pick-2',draftId:null});
  const second=body.children.at(-1); await second.children[1].click();
  assert.equal(second.removed,true,'an explicitly selected image may be the replacement target');
  const expired=new URL(popups.at(-1).location.href,'http://localhost').searchParams.get('resume');
  advance(300001); await send({op:'diagram.resume',ticket:expired}); assert.match(sent.at(-1).error,/失效/);
  await send({op:'diagram.pick',pickId:'pick-3'}); const third=body.children.at(-1);
  await send({op:'diagram.cancel-pick',pickId:'wrong'}); assert.equal(third.removed,false);
  await send({op:'diagram.cancel-pick',pickId:'pick-3'}); assert.equal(third.removed,true);
  await send({op:'diagram.pick',pickId:'pick-4'}); const fourth=body.children.at(-1);
  fourth.children[2].click(); assert.equal(fourth.removed,true);
  await send({op:'diagram.pick',pickId:'pick-5'}); const fifth=body.children.at(-1);
  await send({op:'diagram.pick',pickId:'pick-6'}); assert.equal(fifth.removed,true);
  const sixth=body.children.at(-1);
  advance(300001); assert.equal(sixth.removed,true);
  assert(!calls.some(op=>['diagram.insert','diagram.replace'].includes(op)), 'picker must never write to the manuscript');
  events.pagehide();
});
