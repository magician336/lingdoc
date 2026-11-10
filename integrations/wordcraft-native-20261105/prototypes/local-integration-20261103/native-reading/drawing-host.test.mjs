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
    wordcraftHost: { engine: async request => { calls.push(request); return {status:'applied'}; },
      saveIntent:Object.assign(async()=>({}),{supportsExpectedIdentity:true}) },
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
  assert.equal(sent[0].directInsert, true);
  assert.equal(sent[0].insertOnly, true);
  assert.equal(sent[0].editableDiagrams, true);
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


async function pickerHarness({standalone=false,identitySupport=true}={}) {
  const sent=[], calls=[], events=[], timers=new Map();
  let channel,serial=0;
  const state={documentId:1,revision:4,dirty:false,selection:{anchor:{off:3},focus:{off:3}},sourceActive:false,auxiliary:false,saveResult:standalone?{status:'saved'}:{ackPending:false},saveError:null,holdSave:null,insertLost:false,insertReceipt:null,insertOriginal:null,opens:0};
  class Element {
    constructor(){this.style={};this.children=[];this.listeners={};this.removed=false;this.disabled=false;this.textContent='';}
    setAttribute(){} append(...children){this.children.push(...children);} addEventListener(name,fn){this.listeners[name]=fn;}
    remove(){this.removed=true;} click(){return this.listeners.click();}
  }
  class Channel {constructor(){channel=this;}postMessage(value){sent.push(value);}close(){}}
  const body=new Element();
  const window={NATIVE_WORKSPACE_TOKEN:'token',WORDCRAFT_STANDALONE:standalone,
    open(){state.opens++;throw Error('unexpected window.open');},
    addEventListener:(name,fn)=>{events[name]=fn;},
    wordcraftHost:{
      async engine(request){
        calls.push({kind:'engine',...request});
        if(request.op==='workspace.editor')return {};
        if(request.op==='status')return {documentId:state.documentId,revision:state.revision,dirty:state.dirty};
        if(request.op==='source.status')return {mode:'body',sourceActive:state.sourceActive};
        if(request.op==='workspace.status')return {auxiliaryActive:state.auxiliary};
        if(request.op==='diagram.selection')return {state:{documentId:state.documentId,revision:state.revision,dirty:state.dirty},selection:state.selection,image:null};
        if(request.op==='diagram.insert'){
          if(state.insertReceipt && request.operationId===state.insertOriginal.operationId)return state.insertReceipt;
          state.revision++;state.dirty=true;
          state.insertOriginal={...request};
          state.insertReceipt={status:'applied',state:{documentId:state.documentId,revision:state.revision,dirty:true}};
          if(state.insertLost){state.insertLost=false;throw Error('lost insertion response');}
          return state.insertReceipt;
        }
        throw Error('unexpected operation '+request.op);
      },
      async saveIntent(reason,expected){
        calls.push({kind:'save',reason,expected});
        if(state.holdSave)await state.holdSave;
        if(state.saveError)throw Error(state.saveError);
        const success=state.saveResult.ackPending!==true && state.saveResult.live?.dirty!==true;
        if(success)state.dirty=false;
        return {...state.saveResult,live:state.saveResult.live||{documentId:state.documentId,revision:state.revision,dirty:state.dirty}};
      },
    }};
  if(identitySupport)window.wordcraftHost.saveIntent.supportsExpectedIdentity=true;
  vm.runInNewContext(await readFile(new URL('./drawing-host.js',import.meta.url),'utf8'),{
    window,document:{title:'Doc',body,createElement:()=>new Element()},BroadcastChannel:Channel,
    crypto:{randomUUID:()=>`uuid-${++serial}`},URLSearchParams,setTimeout:(fn,ms)=>{const id=++serial;timers.set(id,{fn,ms});return id;},clearTimeout:id=>timers.delete(id),
  });
  const hostId=window.LINGDOC_DRAWING_HOST_ID;
  const send=request=>channel.onmessage({data:{type:'request',token:'token',hostId,requestId:`request-${++serial}`,request}});
  const drawing={id:'draft-1',revision:1,title:'Draft',embeddedPng:'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGP4z8DwHwAFAAH/iZk9HQAAAABJRU5ErkJggg=='};
  const start=async(id='pick-1')=>{await send({op:'diagram.pick',pickId:id,draftId:drawing.id,drawing});return body.children.at(-1);};
  return {state,calls,sent,body,send,start,drawing,events,timers,channel};
}

test('picker confirms latest collapsed selection and inserts without saving',async()=>{
  const h=await pickerHarness();
  try {
    const banner=await h.start();assert.equal(h.sent.at(-1).result.picking,true);
    h.state.selection={anchor:{off:8},focus:{off:8}};h.state.revision=8;
    await banner.children[1].click();
    const insert=h.calls.filter(c=>c.op==='diagram.insert');assert.equal(insert.length,1);
    assert.equal(insert[0].expectedDocumentId,1);assert.equal(insert[0].expectedRevision,8);
    assert.deepEqual(JSON.parse(JSON.stringify(insert[0].bookmark)),h.state.selection);
    assert.equal(insert[0].data,h.drawing.embeddedPng);assert.equal(insert[0].alt,h.drawing.title);
    assert.equal(h.calls.filter(c=>c.kind==='save').length,0);
    assert.equal(h.state.dirty,true);
    assert.equal(banner.removed,true);assert.equal(h.state.opens,0);
  } finally {h.events.pagehide();}
});



test('picker refuses a changed document or noncollapsed selection',async()=>{
  for(const mode of ['changed','range']){
    const h=await pickerHarness();
    try {
      const banner=await h.start();
      if(mode==='changed')h.state.documentId=2;
      if(mode==='range')h.state.selection={anchor:{off:0},focus:{off:2}};
      if(mode==='pending')h.state.saveResult={ackPending:true,live:{documentId:1,dirty:false}};
      if(mode==='dirty')h.state.saveResult={ackPending:false,live:{documentId:1,dirty:true}};
      await banner.children[1].click();
      assert.equal(banner.removed,false);
      assert.equal(h.calls.filter(c=>c.op==='diagram.insert').length,0);
      assert.equal(h.state.opens,0);
    } finally {h.events.pagehide();}
  }
});


test('cancel before insertion and obsolete resume cannot mutate the document',async()=>{
  const h=await pickerHarness();
  try {
    const banner=await h.start();await banner.children[2].click();
    assert.equal(banner.removed,true);
    assert.equal(h.calls.some(c=>c.op==='diagram.insert'||c.kind==='save'),false);
    await h.send({op:'diagram.resume',ticket:'old-ticket'});
    assert.match(h.sent.at(-1).error,/失效|过期|版本/);
    assert.equal(h.state.opens,0);
  } finally {h.events.pagehide();}
});

test('source and auxiliary views cannot accept insertion',async()=>{
  for(const field of ['sourceActive','auxiliary']){
    const h=await pickerHarness();
    try {
      const banner=await h.start();h.state[field]=true;await banner.children[1].click();
      assert.equal(banner.removed,false);
      assert.equal(h.calls.some(c=>c.op==='diagram.insert'||c.kind==='save'),false);
      assert.equal(h.state.opens,0);
    } finally {h.events.pagehide();}
  }
});




test('unconfirmed picker may expire without writing',async()=>{
  const h=await pickerHarness();
  try {
    const banner=await h.start();for(const timer of [...h.timers.values()])timer.fn();
    assert.equal(banner.removed,true);
    assert.equal(h.calls.some(c=>c.op==='diagram.insert'||c.kind==='save'),false);
  } finally {h.events.pagehide();}
});



test('unknown insertion result retains operation through expiry and competing pick',async()=>{
  const h=await pickerHarness();
  try {
    const banner=await h.start();h.state.insertLost=true;await banner.children[1].click();
    assert.equal(h.calls.filter(c=>c.op==='diagram.insert').length,1);
    for(const timer of [...h.timers.values()])timer.fn();
    assert.equal(banner.removed,false,'unknown insertion outcome must remain recoverable');
    await h.send({op:'diagram.pick',pickId:'pick-2',draftId:h.drawing.id,drawing:h.drawing});
    assert.equal(banner.removed,false,'unknown insertion cannot be overwritten by a new request');
    const original=h.calls.find(c=>c.op==='diagram.insert');
    h.state.selection={anchor:{off:0},focus:{off:1}};
    await banner.children[1].click();
    const retries=h.calls.filter(c=>c.op==='diagram.insert');
    assert.equal(retries.length,2);
    assert.deepEqual(retries[1],original,'retry must reuse the exact insertion request');
    assert.equal(h.calls.filter(c=>c.kind==='save').length,0);
    assert.equal(banner.removed,true);
  } finally {h.events.pagehide();}
});


test('insertion requires no automatic save capability',async()=>{
  const h=await pickerHarness({identitySupport:false});
  try {
    h.channel.onmessage({data:{type:'discover',token:'token'}});
    assert.equal(h.sent.at(-1).directInsert,true);
    assert.equal(h.sent.at(-1).insertOnly,true);
    const banner=await h.start();await banner.children[1].click();
    assert.equal(h.calls.filter(c=>c.op==='diagram.insert').length,1);
    assert.equal(h.calls.filter(c=>c.kind==='save').length,0);
    assert.equal(h.state.dirty,true);
  } finally {h.events.pagehide();}
});
