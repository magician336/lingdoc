import test from 'node:test';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import vm from 'node:vm';

async function setup() {
  const events={}, sent=[], calls=[], opened=[], timers=new Map();let channel,serial=0,now=1000;
  const state={documentId:7,revision:3,dirty:false,imageSha:'a'.repeat(64),lost:false,receipt:null,operationId:null,forcedError:null};
  const selection={anchor:{off:0},focus:{off:3}};
  const image={data:'base64-png',sha256:'a'.repeat(64),alt:'original'};
  const snapshot={state:{...state},selection:structuredClone(selection),image:{...image}};
  class FakeDate extends Date {static now(){return now;}}
  class Channel {constructor(){channel=this;}postMessage(value){sent.push(value);}close(){}}
  const window={NATIVE_WORKSPACE_TOKEN:'token',WORDCRAFT_STANDALONE:true,
    addEventListener:(name,fn)=>{events[name]=fn;},
    open(url,target){const popup={url,target,closed:false,location:{href:url},close(){this.closed=true;}};opened.push(popup);return popup;},
    wordcraftHost:{async engine(request){
      calls.push(request);
      if(request.op==='status')return {...state};
      if(request.op==='diagram.replace'){
        if(state.forcedError)throw Error(state.forcedError);
        if(state.receipt&&request.operationId===state.operationId)return state.receipt;
        if(request.expectedDocumentId!==state.documentId||request.expectedRevision!==state.revision)throw Error('stale document or revision');
        if(request.expectedImageSha256!==state.imageSha)throw Error('selected picture changed');
        state.revision++;state.dirty=true;state.operationId=request.operationId;state.receipt={status:'applied',state:{documentId:state.documentId,revision:state.revision,dirty:true}};
        if(state.lost){state.lost=false;throw Error('response lost');}return state.receipt;
      }
      throw Error('unexpected '+request.op);
    },async saveIntent(){throw Error('unexpected auto-save');}},
  };
  vm.runInNewContext(await readFile(new URL('./drawing-host.js',import.meta.url),'utf8'),{
    window,document:{title:'Doc',body:{append(){}},createElement:()=>({style:{},append(){},setAttribute(){},addEventListener(){},remove(){}})},
    BroadcastChannel:Channel,crypto:{randomUUID:()=>`id-${++serial}`},URLSearchParams,Date:FakeDate,
    setTimeout:(fn,ms)=>{const id=++serial;timers.set(id,{fn,ms});return id;},clearTimeout:id=>timers.delete(id),
  });
  const send=(request,token='token',hostId=window.LINGDOC_DRAWING_HOST_ID)=>channel.onmessage({data:{type:'request',token,hostId,requestId:`request-${++serial}`,request}});
  function start(){const count=opened.length;window.lingdocEditDiagram(JSON.stringify(snapshot));if(opened.length===count)return null;const url=new URL(opened.at(-1).url,'http://localhost');return url.searchParams.get('edit');}
  return {window,sent,calls,opened,state,snapshot,send,start,events,advance:ms=>{now+=ms;}};
}

test('edit entry opens one drawing window with an expiring frozen snapshot',async()=>{
  const h=await setup();
  try {
    const ticket=h.start();assert.ok(ticket);assert.equal(h.opened.length,1);
    const url=new URL(h.opened[0].url,'http://localhost');assert.equal(url.pathname,'/native-reading/drawing');
    assert.equal(url.searchParams.get('host'),h.window.LINGDOC_DRAWING_HOST_ID);
    const original=structuredClone(h.snapshot);h.snapshot.selection.anchor.off=99;h.snapshot.image.data='changed';
    await h.send({op:'diagram.edit',ticket});assert.deepEqual(JSON.parse(JSON.stringify(h.sent.at(-1).result)),original);
    await h.send({op:'diagram.edit',ticket:'invalid'});assert.ok(h.sent.at(-1).error);
  } finally {h.events.pagehide();}
});

test('edit apply replaces frozen image once and supports exact idempotent retry',async()=>{
  const h=await setup();
  try {
    const ticket=h.start();await h.send({op:'diagram.edit',ticket});
    const request={op:'diagram.edit-apply',ticket,operationId:'edit-operation',data:'new-png',alt:'new title'};
    await h.send(request);assert.equal(h.sent.at(-1).result.status,'applied');
    const call=h.calls.find(c=>c.op==='diagram.replace');
    assert.equal(call.expectedDocumentId,7);assert.equal(call.expectedRevision,3);
    assert.equal(call.expectedImageSha256,'a'.repeat(64));assert.equal(call.restoreBookmark,true);
    assert.deepEqual(JSON.parse(JSON.stringify(call.bookmark)),h.snapshot.selection);
    await h.send(request);assert.equal(h.sent.at(-1).result.status,'applied');
    assert.equal(h.calls.filter(c=>c.op==='diagram.replace').length,1);
    await h.send({...request,data:'different'});assert.ok(h.sent.at(-1).error);
    assert.equal(h.calls.filter(c=>c.op==='diagram.replace').length,1);
    assert.equal(h.state.dirty,true);
  } finally {h.events.pagehide();}
});

test('edit rejects expired or invalid tickets and changed document revision',async()=>{
  const h=await setup();
  try {
    const expired=h.start();h.advance(8*60*60*1000);
    await h.send({op:'diagram.edit',ticket:expired});assert.ok(h.sent.at(-1).error);
    await h.send({op:'diagram.edit-apply',ticket:'wrong',operationId:'one',data:'new',alt:''});assert.ok(h.sent.at(-1).error);
    const stale=h.start();await h.send({op:'diagram.edit',ticket:stale});h.state.revision++;
    await h.send({op:'diagram.edit-apply',ticket:stale,operationId:'two',data:'new',alt:''});assert.ok(h.sent.at(-1).error);
    assert.equal(h.state.dirty,false);
  } finally {h.events.pagehide();}
});



test('wrong token or host cannot read or apply an edit ticket',async()=>{
  const h=await setup();
  try {
    const ticket=h.start();const count=h.sent.length;
    await h.send({op:'diagram.edit',ticket},'wrong');
    await h.send({op:'diagram.edit',ticket},'token','another-host');
    await h.send({op:'diagram.edit-apply',ticket,operationId:'x',data:'new',alt:''},'wrong');
    assert.equal(h.sent.length,count);assert.equal(h.calls.length,0);
  } finally {h.events.pagehide();}
});

test('changed image digest is refused by the native replace guard',async()=>{
  const h=await setup();
  try {
    const ticket=h.start();await h.send({op:'diagram.edit',ticket});h.state.imageSha='b'.repeat(64);
    await h.send({op:'diagram.edit-apply',ticket,operationId:'x',data:'new',alt:''});
    assert.ok(h.sent.at(-1).error);assert.equal(h.state.dirty,false);
  } finally {h.events.pagehide();}
});


test('uncertain edit survives expiry and retries the identical native operation',async()=>{
  const h=await setup();
  try {
    const ticket=h.start();await h.send({op:'diagram.edit',ticket});h.state.lost=true;
    const request={op:'diagram.edit-apply',ticket,operationId:'edit-lost',data:'new-png',alt:'title'};
    await h.send(request);assert.ok(h.sent.at(-1).error);assert.equal(h.state.revision,4);
    h.advance(8*60*60*1000);
    await h.send(request);assert.equal(h.sent.at(-1).result.status,'applied');assert.equal(h.state.revision,4);
    const calls=h.calls.filter(c=>c.op==='diagram.replace');assert.equal(calls.length,2);
    assert.deepEqual(JSON.parse(JSON.stringify(calls[0])),JSON.parse(JSON.stringify(calls[1])));
  } finally {h.events.pagehide();}
});


test('confirmed no-write rejections expire and do not exhaust the edit limit',async()=>{
  const h=await setup();
  try {
    for(let i=0;i<16;i++) {
      const ticket=h.start();h.state.revision=4;
      const request={op:'diagram.edit-apply',ticket,operationId:`rejected-${i}`,data:'new-png',alt:'title'};
      await h.send(request);assert.ok(h.sent.at(-1).error);
      const count=h.calls.filter(c=>c.op==='diagram.replace').length;
      await h.send(request);assert.ok(h.sent.at(-1).error);
      assert.equal(h.calls.filter(c=>c.op==='diagram.replace').length,count,'confirmed rejection is cached');
      await h.send({...request,data:'different'});assert.ok(h.sent.at(-1).error);
      h.state.revision=3;
    }
    const opened=h.opened.length;h.start();assert.equal(h.opened.length,opened,'live rejected sessions count until expiry');
    h.advance(5*60*1000+1);const ticket=h.start();assert.ok(ticket);assert.equal(h.opened.length,opened+1);
  } finally {h.events.pagehide();}
});

test('closing an unsubmitted edit window releases its slot without waiting for expiry',async()=>{
  const h=await setup();
  try {
    for(let i=0;i<16;i++)h.start();
    const opened=h.opened.length;h.start();assert.equal(h.opened.length,opened);
    h.opened[0].close();const next=h.start();assert.ok(next);assert.equal(h.opened.length,opened+1);
  } finally {h.events.pagehide();}
});


test('only exact native pre-write rejection messages permit expiry',async()=>{
  for(const message of ['stale document or revision','selected picture changed','select the original picture first','selection changed']) {
    const h=await setup();
    try {
      const ticket=h.start();h.state.forcedError=message;
      const request={op:'diagram.edit-apply',ticket,operationId:'guarded',data:'new',alt:''};
      await h.send(request);assert.ok(h.sent.at(-1).error);
      h.advance(5*60*1000+1);await h.send({op:'diagram.edit',ticket});
      assert.ok(h.sent.at(-1).error,message);
    } finally {h.events.pagehide();}
  }
  const h=await setup();
  try {
    const ticket=h.start();h.state.forcedError='stale document or revision (transport)';
    const request={op:'diagram.edit-apply',ticket,operationId:'guarded',data:'new',alt:''};
    await h.send(request);h.advance(8*60*60*1000);h.state.forcedError=null;
    await h.send(request);assert.equal(h.sent.at(-1).result.status,'applied');
  } finally {h.events.pagehide();}
});
