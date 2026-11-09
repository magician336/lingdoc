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
  const window = {LINGDOC_DRAWING:{token:'secret',origin:'http://127.0.0.1:4000'}, location:{search:''}, addEventListener:(name, callback) => { events[name] = callback; }};
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
    setTimeout, clearTimeout, URLSearchParams, location:window.location, confirm:() => true,
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


async function createWorkflowHarness({search='', picker=true, origin='http://127.0.0.1:4000'}={}) {
  const elements=new Map(), events={}, frameMessages=[], hostMessages=[], calls=[], order=[];
  const state={empty:false,exportError:false,saveError:false,allowClose:true,closed:false,focus:0,navigations:[]};
  const draft={id:'draft-1',revision:1,title:'Saved draft',xml:'<mxfile/>',messages:[],png:'png',embeddedPng:'encoded-png'};
  let channel,serial=0;
  class Option {constructor(text,value){this.text=text;this.value=value;}}
  class Element {
    constructor(){this.options=[];this.value='';this.disabled=false;this.textContent='';}
    replaceChildren(...children){this.options=children;this.value=children[0]?.value||'';}
    add(option){this.options.push(option);}
  }
  const element=id=>{if(!elements.has(id))elements.set(id,new Element());return elements.get(id);};
  const frame=element('editor');
  const result={state:{documentId:9,revision:4},selection:{anchor:{off:3},focus:{off:3},text:''},image:null};
  frame.contentWindow={postMessage(message){
    frameMessages.push(message);order.push('frame:'+message.type);
    queueMicrotask(()=>events.message({origin,source:frame.contentWindow,data:{channel:'lingdoc-drawing',requestId:message.requestId,
      ...(message.type==='export'&&state.exportError?{error:'export failed'}:{result:message.type==='export'
        ? state.empty?{empty:true}:{xml:'<mxfile/>',messages:[],png:'data:image/png;base64,png'}:{loaded:true}})}}));
  }};
  class Channel {
    constructor(){channel=this;}
    postMessage(message){
      hostMessages.push(message);
      if(message.type!=='request')return;
      order.push('host:'+message.request.op);
      queueMicrotask(()=>channel.onmessage({data:{type:'result',token:'secret',hostId:message.hostId,requestId:message.requestId,
        result:message.request.op==='diagram.pick'?{picking:true}:message.request.op==='diagram.resume'?result:{status:'applied'}}}));
    }
    close(){}
  }
  const location={search,assign:value=>state.navigations.push(value)};
  const window={LINGDOC_DRAWING:{token:'secret',origin},location,opener:{focus(){state.focus++;}},
    get closed(){return state.closed;},close(){if(state.allowClose)state.closed=true;},
    addEventListener:(name,callback)=>{events[name]=callback;}};
  vm.runInNewContext(await readFile(new URL('./drawing.js',import.meta.url),'utf8'),{
    window,location,URLSearchParams,document:{getElementById:element},BroadcastChannel:Channel,Option,
    crypto:{randomUUID:()=>`workflow-${++serial}`},setTimeout,clearTimeout,confirm:()=>true,
    fetch:async(url,options)=>{
      const action=url.split('/').at(-1);const body=options?.body?JSON.parse(options.body):null;calls.push({action,body});order.push('api:'+action);
      if(action==='save')return state.saveError?{ok:false,json:async()=>({error:'save failed'})}:{ok:true,json:async()=>draft};
      if(action==='get')return {ok:true,json:async()=>({record:draft})};
      return {ok:true,json:async()=>[draft]};
    },
  });
  channel.onmessage({data:{type:'host',token:'secret',hostId:'host-1',title:'Doc',...(picker?{picker:true}:{})}});
  element('hosts').value='host-1';
  const ready=()=>events.message({origin,source:frame.contentWindow,data:{channel:'lingdoc-drawing',type:'ready'}});
  return {element,events,frameMessages,hostMessages,calls,order,state,draft,ready,channel,window};
}
const tick=()=>new Promise(resolve=>setImmediate(resolve));

test('capture saves then starts host picking and keeps insertion disabled',async()=>{
  const h=await createWorkflowHarness();
  try {
    h.ready();await tick();h.order.length=0;
    await h.element('capture').onclick();
    assert.equal(h.frameMessages.at(-1).type,'export');
    assert.equal(h.frameMessages.at(-1).skipEmpty,true);
    const request=h.hostMessages.find(m=>m.request?.op==='diagram.pick');
    assert.equal(request.hostId,'host-1');
    assert.equal(request.request.draftId,'draft-1');
    assert.ok(request.request.pickId);
    assert.ok(h.order.indexOf('api:save')<h.order.indexOf('host:diagram.pick'));
    assert.equal(h.element('insert').disabled,true);
    assert.equal(h.state.closed,true);
    assert.deepEqual(h.state.navigations,[]);
  } finally {h.events.pagehide();}
});

test('capture save failure and unsupported old host never start picking',async()=>{
  const h=await createWorkflowHarness();
  try {
    h.ready();await tick();h.state.saveError=true;
    await h.element('capture').onclick();
    assert.equal(h.element('status').textContent,'save failed');
    assert.equal(h.state.closed,false);
    assert.equal(h.element('insert').disabled,true);
    assert.equal(h.hostMessages.some(m=>m.request?.op==='diagram.pick'),false);
  } finally {h.events.pagehide();}
  const old=await createWorkflowHarness({picker:false});
  try {
    old.ready();await tick();await old.element('capture').onclick();
    assert.equal(old.hostMessages.some(m=>m.type==='request'),false);
    assert.equal(old.state.closed,false);
    assert.match(old.element('status').textContent,/刷新|重新打开/);
    assert.equal(old.element('insert').disabled,true);
  } finally {old.events.pagehide();}
});

test('resume restores saved draft before consuming confirmed target and permits insertion',async()=>{
  const h=await createWorkflowHarness({search:'?resume=ticket-1&host=host-1&draft=draft-1'});
  try {
    h.ready();
    for(let i=0;i<15&&h.element('insert').disabled;i++)await tick();
    assert.equal(h.calls.find(c=>c.action==='get')?.body.id,'draft-1');
    assert.ok(h.order.indexOf('frame:load')<h.order.indexOf('host:diagram.resume'));
    const resume=h.hostMessages.find(m=>m.request?.op==='diagram.resume');
    assert.equal(resume.request.ticket,'ticket-1');
    assert.equal(h.element('insert').disabled,false);
    await h.element('insert').onclick();
    const insert=h.hostMessages.find(m=>m.request?.op==='diagram.insert');
    assert.equal(insert.request.expectedDocumentId,9);
    assert.equal(insert.request.expectedRevision,4);
    assert.equal(insert.request.data,'encoded-png');
  } finally {h.events.pagehide();}
});

test('exit saves nonempty drawing before closing and preserves page on failure',async()=>{
  const html=await readFile(new URL('./drawing.html',import.meta.url),'utf8');
  assert.ok(/id=["']exit["']/.test(html),'exit button exists');
  const h=await createWorkflowHarness();
  try {
    h.ready();await tick();h.order.length=0;await h.element('exit').onclick();
    assert.ok(h.order.indexOf('frame:export')<h.order.indexOf('api:save'));
    assert.equal(h.state.focus,1);assert.equal(h.state.closed,true);
  } finally {h.events.pagehide();}
  for(const failure of ['exportError','saveError']){
    const failed=await createWorkflowHarness();
    try {
      failed.ready();await tick();failed.state[failure]=true;await failed.element('exit').onclick();
      assert.equal(failed.state.closed,false);assert.deepEqual(failed.state.navigations,[]);
      assert.match(failed.element('status').textContent,/failed/);
    } finally {failed.events.pagehide();}
  }
});

test('exit skips empty draft and falls back to home if close is refused',async()=>{
  const h=await createWorkflowHarness();
  try {
    h.ready();await tick();h.state.empty=true;h.state.allowClose=false;
    await h.element('exit').onclick();
    await new Promise(resolve=>setTimeout(resolve,650));
    assert.equal(h.calls.some(c=>c.action==='save'),false);
    assert.deepEqual(h.state.navigations,['/']);
  } finally {h.events.pagehide();}
});


test('capture close refusal gives manual return guidance without navigation',async()=>{
  const h=await createWorkflowHarness();
  try {
    h.ready();await tick();h.state.allowClose=false;
    await h.element('capture').onclick();
    await new Promise(resolve=>setTimeout(resolve,650));
    assert.equal(h.state.closed,false);
    assert.deepEqual(h.state.navigations,[]);
    assert.match(h.element('status').textContent,/手动|切回|返回文稿/);
    assert.equal(h.element('insert').disabled,true);
  } finally {h.events.pagehide();}
});

test('exit works without a ready drawing service',async()=>{
  const h=await createWorkflowHarness({origin:''});
  try {
    await h.element('exit').onclick();
    assert.equal(h.frameMessages.length,0);
    assert.equal(h.calls.some(c=>c.action==='save'),false);
    assert.equal(h.state.closed,true);
  } finally {h.events.pagehide();}
});

test('exit preserves a configured canvas until its connection is ready',async()=>{
  const h=await createWorkflowHarness();
  try {
    await h.element('exit').onclick();
    assert.equal(h.state.closed,false);
    assert.deepEqual(h.state.navigations,[]);
    assert.match(h.element('notice').textContent,/尚未连接/);
  } finally {h.events.pagehide();}
});
