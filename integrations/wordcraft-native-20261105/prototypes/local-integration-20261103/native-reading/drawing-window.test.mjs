import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';

test('window trusts only the configured frame and sends captured text to that origin', async () => {
  const elements = new Map(), events = {}, frameMessages = [], channelMessages = [];
  let channel, serial = 0, lostResponse = false, committed, versions = 0; const retryRequests = [];
  class Option { constructor(text, value) { this.text = text; this.value = value; } }
  class Element {
    constructor() { this.options = []; this.children = []; this.dataset = {}; this.style = {}; this.value = ''; this.disabled = false; this.textContent = ''; }
    replaceChildren(...children) { this.children = children; this.options = children; this.value = children[0]?.value || ''; }
    add(option) { this.options.push(option); }
    append(...children) { this.children.push(...children); }
    setAttribute() {}
    querySelectorAll() { return this.children.flatMap(child => [...(child.type === 'button' ? [child] : []), ...(child.querySelectorAll?.() || [])]); }
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
    window, document:{getElementById:element,createElement:()=>new Element()}, BroadcastChannel:Channel, Option,
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


async function createWorkflowHarness({search='', picker=true, directInsert=true, libraryMode='same', origin='http://127.0.0.1:4000'}={}) {
  const elements=new Map(), events={}, frameMessages=[], hostMessages=[], calls=[], order=[];
  const state={empty:false,exportError:false,saveError:false,applyError:false,xml:'<mxfile/>',messages:[],allowClose:true,closed:false,focus:0,navigations:[]};
  const draft={id:'draft-1',revision:1,title:'Saved draft',xml:'<mxfile/>',messages:[],png:'png',embeddedPng:'encoded-png'};
  let channel,serial=0;
  class Option {constructor(text,value){this.text=text;this.value=value;}}
  class Element {
    constructor(){this.options=[];this.children=[];this.dataset={};this.style={};this.value='';this.disabled=false;this.textContent='';}
    replaceChildren(...children){this.children=children;this.options=children;this.value=children[0]?.value||'';}
    add(option){this.options.push(option);}
    append(...children){this.children.push(...children);}
    setAttribute(){}
    querySelectorAll(){return this.children.flatMap(child=>[...(child.type==='button'?[child]:[]),...(child.querySelectorAll?.()||[])]);}
  }
  const element=id=>{if(!elements.has(id))elements.set(id,new Element());return elements.get(id);};
  const frame=element('editor');
  const result={state:{documentId:9,revision:4},selection:{anchor:{off:3},focus:{off:3},text:''},image:null};
  frame.contentWindow={postMessage(message){
    frameMessages.push(message);order.push('frame:'+message.type);
    queueMicrotask(()=>events.message({origin,source:frame.contentWindow,data:{channel:'lingdoc-drawing',requestId:message.requestId,
      ...(message.type==='export'&&state.exportError?{error:'export failed'}:{result:message.type==='export'
        ? state.empty?{empty:true}:{xml:state.xml,messages:state.messages,png:'data:image/png;base64,png'}:{loaded:true}})}}));
  }};
  class Channel {
    constructor(){channel=this;}
    postMessage(message){
      hostMessages.push(message);
      if(message.type!=='request')return;
      order.push('host:'+message.request.op);
      queueMicrotask(()=>channel.onmessage({data:{type:'result',token:'secret',hostId:message.hostId,requestId:message.requestId,
        ...(message.request.op==='diagram.edit-apply'&&state.applyError?{error:'apply failed'}:{result:message.request.op==='diagram.pick'?{picking:true}:message.request.op==='diagram.resume'?result:message.request.op==='diagram.edit'?{...result,image:{data:'embedded',sha256:'a'.repeat(64),alt:'Saved draft'}}:{status:'applied'}})}}));
    }
    close(){}
  }
  const location={search,assign:value=>state.navigations.push(value)};
  const window={LINGDOC_DRAWING:{token:'secret',origin},location,opener:{focus(){state.focus++;}},
    get closed(){return state.closed;},close(){if(state.allowClose)state.closed=true;},
    addEventListener:(name,callback)=>{events[name]=callback;}};
  vm.runInNewContext(await readFile(new URL('./drawing.js',import.meta.url),'utf8'),{
    window,location,URLSearchParams,document:{getElementById:element,createElement:()=>new Element()},BroadcastChannel:Channel,Option,
    crypto:{randomUUID:()=>`workflow-${++serial}`},setTimeout,clearTimeout,confirm:()=>true,
    fetch:async(url,options)=>{
      const action=url.split('/').at(-1);const body=options?.body?JSON.parse(options.body):null;calls.push({action,body});order.push('api:'+action);
      if(action==='save')return state.saveError?{ok:false,json:async()=>({error:'save failed'})}:{ok:true,json:async()=>draft};
      if(action==='delete')return {ok:true,json:async()=>({id:body.id,revision:body.expectedRevision,deleted:true})};
      if(action==='get')return {ok:true,json:async()=>({record:libraryMode==='missing'?null:libraryMode==='newer'?{...draft,revision:2}:libraryMode==='changed'?{...draft,xml:'<mxfile id="changed"/>'}:draft})};
      if(action==='read-image')return {ok:true,json:async()=>({record:draft})};
      return {ok:true,json:async()=>[draft]};
    },
  });
  channel.onmessage({data:{type:'host',token:'secret',hostId:'host-1',title:'Doc',...(picker?{picker:true}:{}),...(directInsert?{directInsert:true,insertOnly:true,editableDiagrams:true}:{})}});
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
    assert.deepEqual(JSON.parse(JSON.stringify(request.request.drawing)),{id:'draft-1',revision:1,title:'Saved draft',embeddedPng:'encoded-png'});
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
  const old=await createWorkflowHarness({picker:true,directInsert:false});
  try {
    old.ready();await tick();await old.element('capture').onclick();
    assert.equal(old.hostMessages.some(m=>m.type==='request'),false);
    assert.equal(old.state.closed,false);
    assert.match(old.element('status').textContent,/刷新|重新打开/);
    assert.equal(old.element('insert').disabled,true);
  } finally {old.events.pagehide();}
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


test('empty drawing cannot start document insertion',async()=>{
  const h=await createWorkflowHarness();
  try {
    h.ready();await tick();h.state.empty=true;
    await h.element('capture').onclick();
    assert.equal(h.hostMessages.some(m=>m.request?.op==='diagram.pick'),false);
    assert.equal(h.state.closed,false);
    assert.equal(h.element('insert').disabled,true);
    assert.match(h.element('status').textContent,/空|绘|图稿/);
  } finally {h.events.pagehide();}
});


test('editing an embedded image restores its source and applies to the original without document save',async()=>{
  const h=await createWorkflowHarness({search:'?edit=ticket-1&host=host-1'});
  try {
    h.ready();
    for(let i=0;i<30&&!h.frameMessages.some(m=>m.type==='load');i++)await tick();
    assert.ok(h.hostMessages.some(m=>m.request?.op==='diagram.edit'&&m.request.ticket==='ticket-1'));
    assert.equal(h.calls.find(c=>c.action==='read-image')?.body.png,'embedded');
    assert.equal(h.frameMessages.find(m=>m.type==='load')?.xml,'<mxfile/>');
    await h.element('apply-edit').onclick();
    const apply=h.hostMessages.find(m=>m.request?.op==='diagram.edit-apply');
    assert.equal(apply.request.ticket,'ticket-1');assert.ok(apply.request.operationId);
    assert.equal(apply.request.data,'encoded-png');
    assert.equal(h.hostMessages.some(m=>m.request?.op==='save'),false);
    assert.equal(h.state.closed,true);
  } finally {h.events.pagehide();}
});

test('failed edit apply retains page and reuses operation identity on retry',async()=>{
  const h=await createWorkflowHarness({search:'?edit=ticket-1&host=host-1'});
  try {
    h.ready();for(let i=0;i<30&&!h.frameMessages.some(m=>m.type==='load');i++)await tick();
    h.state.applyError=true;await h.element('apply-edit').onclick();
    assert.equal(h.state.closed,false);assert.deepEqual(h.state.navigations,[]);
    const first=h.hostMessages.find(m=>m.request?.op==='diagram.edit-apply')?.request;
    const saves=h.calls.filter(c=>c.action==='save').length;
    assert.equal(h.element('editor').inert,true);
    assert.equal(h.element('title').disabled,true);
    assert.equal(h.element('library-trigger').disabled,true);
    h.state.applyError=false;await h.element('apply-edit').onclick();
    const second=h.hostMessages.filter(m=>m.request?.op==='diagram.edit-apply').at(-1)?.request;
    assert.deepEqual(JSON.parse(JSON.stringify(second)),JSON.parse(JSON.stringify(first)));
    assert.equal(h.calls.filter(c=>c.action==='save').length,saves);
    assert.equal(h.state.closed,true);
  } finally {h.events.pagehide();}
});


test('editing image reuses exact library revision and forks stale or deleted originals',async()=>{
  for(const mode of ['same','newer','changed','missing']){
    const h=await createWorkflowHarness({search:'?edit=ticket-1&host=host-1',libraryMode:mode});
    try {
      h.ready();for(let i=0;i<30&&!h.frameMessages.some(m=>m.type==='load');i++)await tick();
      await h.element('apply-edit').onclick();
      const save=h.calls.find(c=>c.action==='save');assert.ok(save,mode);
      if(mode==='same'){assert.equal(save.body.id,'draft-1');assert.equal(save.body.expectedRevision,1);}
      else {assert.equal(save.body.id,undefined,mode);assert.equal(save.body.expectedRevision,0,mode);}
    } finally {h.events.pagehide();}
  }
});


test('exit after deletion skips unchanged canvas but saves later changes',async()=>{
  for(const changed of [false,true]){
    const h=await createWorkflowHarness();
    try {
      h.ready();await tick();await h.element('save').onclick();
      await h.element('library-list').children.find(row=>row.dataset.id==='draft-1').children[1].onclick({stopPropagation(){}});
      if(changed)h.state.xml='<mxfile id="later-edit"/>';
      h.calls.length=0;await h.element('exit').onclick();
      const saves=h.calls.filter(c=>c.action==='save');
      assert.equal(saves.length,changed?1:0);
      if(changed){assert.equal(saves[0].body.id,undefined);assert.equal(saves[0].body.expectedRevision,0);}
      assert.equal(h.state.closed,true);
    } finally {h.events.pagehide();}
  }
});

test('inserting the unchanged canvas after deletion explicitly creates a new drawing',async()=>{
  const h=await createWorkflowHarness();
  try {
    h.ready();await tick();await h.element('save').onclick();
    await h.element('library-list').children.find(row=>row.dataset.id==='draft-1').children[1].onclick({stopPropagation(){}});
    h.calls.length=0;h.hostMessages.length=0;
    await h.element('capture').onclick();
    const save=h.calls.find(c=>c.action==='save');assert.ok(save);
    assert.equal(save.body.id,undefined);assert.equal(save.body.expectedRevision,0);
    assert.ok(h.hostMessages.some(m=>m.request?.op==='diagram.pick'));
  } finally {h.events.pagehide();}
});
