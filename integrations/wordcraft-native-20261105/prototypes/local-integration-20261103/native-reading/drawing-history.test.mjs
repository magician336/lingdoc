import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFile,mkdtemp,rm} from 'node:fs/promises';
import path from 'node:path';
import vm from 'node:vm';
import {createDrawingStore} from './drawing-store.mjs';
const source=await readFile(new URL('./drawing.js',import.meta.url),'utf8');
const png='iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGP4z8DwHwAFAAH/iZk9HQAAAABJRU5ErkJggg==';
async function setup(){
  const directory=await mkdtemp(path.join(process.cwd(),'drawing-history-ui-'));
  const store=createDrawingStore({directory});let latest;
  for(let revision=1;revision<=3;revision++)latest=await store.save({id:latest?.id,expectedRevision:revision-1,key:'seed-'+revision,title:'版本图稿',xml:`<mxfile version="${revision}"/>`,messages:[{role:'user',content:String(revision)}],png});
  class Element {
    constructor(){this.value='';this.textContent='';this.options=[];this.children=[];this.dataset={};this.attributes={};this.style={};this.open=false;}
    replaceChildren(...children){this.children=children;this.options=children;this.value=children[0]?.value||'';}
    append(...children){this.children.push(...children);}
    add(option){this.options.push(option);}
    setAttribute(key,value){this.attributes[key]=value;}
    querySelectorAll(){return this.children.flatMap(child=>[...(child.type==='button'?[child]:[]),...child.querySelectorAll?.()||[]]);}
  }
  class Option{constructor(text,value){this.text=text;this.value=value;}}
  const elements=new Map();const el=id=>{if(!elements.has(id))elements.set(id,new Element());return elements.get(id);};
  const events={},calls=[],canvas={xml:'<mxfile/>',messages:[],empty:true};let serial=0;
  const state={confirm:true,deleteError:false,loseDelete:false,exportError:false};
  const origin='http://127.0.0.1:4000';const frame=el('editor');
  frame.contentWindow={postMessage(message){queueMicrotask(()=>{
    let result;
    if(message.type==='load'){canvas.xml=message.xml;canvas.messages=message.messages;canvas.empty=false;result={loaded:true};}
    else if(message.type==='export'){result=canvas.empty&&message.skipEmpty?{empty:true}:{xml:canvas.xml,messages:canvas.messages,png:'data:image/png;base64,'+png};}
    const error=message.type==='export'&&state.exportError?'export failed':null;
    events.message({origin,source:frame.contentWindow,data:{channel:'lingdoc-drawing',requestId:message.requestId,...(error?{error}:{result})}});
  });}};
  const window={LINGDOC_DRAWING:{origin,token:'test'},location:{search:'',assign(){}},addEventListener(name,fn){events[name]=fn;},close(){this.closed=true;}};
  class Channel{postMessage(){}close(){}}
  vm.runInNewContext(source,{window,document:{getElementById:el,createElement:()=>new Element()},BroadcastChannel:Channel,Option,URLSearchParams,setTimeout,clearTimeout,
    crypto:{randomUUID:()=>`operation-${++serial}`},confirm:()=>state.confirm,
    fetch:async(url,options)=>{
      const action=url.split('/').at(-1),body=options.body?JSON.parse(options.body):undefined;calls.push({action,body});
      try {
        if(action==='delete-version'&&state.deleteError)throw Error('delete failed');
        const result=action==='list'?await store.list():action==='get'?{record:await store.get(body.id)}:action==='history'?await store.history(body.id):action==='version'?await store.version(body.id,body.revision):action==='delete-version'?await store.deleteVersion(body):action==='save'?await store.save(body):action==='delete'?await store.delete(body):undefined;
        if(action==='delete-version'&&state.loseDelete){state.loseDelete=false;throw Error('response lost');}
        return {ok:true,json:async()=>result};
      }catch(error){return {ok:false,json:async()=>({error:error.message})};}
    },
  });
  events.message({origin,source:frame.contentWindow,data:{channel:'lingdoc-drawing',type:'ready'}});
  for(let i=0;i<100&&!el('library-list').children.some(row=>row.dataset.id===latest.id);i++)await new Promise(resolve=>setTimeout(resolve,5));
  await el('library-list').children.find(row=>row.dataset.id===latest.id).children[0].onclick();
  el('version-history').open=true;await el('version-history').ontoggle();
  function button(revision,action){const row=el('history-versions').children.find(item=>item.dataset.revision===String(revision));return row?.children.find(item=>item.dataset.action===action);}
  return {el,store,latest,canvas,calls,state,window,button,close:async()=>{events.pagehide();await rm(directory,{recursive:true,force:true});}};
}
test('saved-history UI deletes only the selected revision and keeps the current canvas',async()=>{
  const h=await setup();try{
    const before=JSON.stringify(h.canvas);await h.button(2,'delete-version').onclick();
    assert.deepEqual((await h.store.history(h.latest.id)).map(item=>item.revision),[3,1]);
    assert.equal(JSON.stringify(h.canvas),before);assert.ok(h.button(1,'open-version'));assert.equal(h.button(2,'delete-version'),undefined);
    const saves=h.calls.filter(item=>item.action==='save').length;await h.el('exit').onclick();
    assert.equal(h.calls.filter(item=>item.action==='save').length,saves);assert.equal(h.window.closed,true);
  }finally{await h.close();}
});
test('deleting the displayed saved version retains the canvas without recreating it on exit',async()=>{
  const h=await setup();try{
    const before=JSON.stringify(h.canvas);await h.button(3,'delete-version').onclick();
    assert.equal(JSON.stringify(h.canvas),before);assert.match(h.el('version').textContent,/已删除/);
    assert.equal((await h.store.get(h.latest.id)).revision,2);
    await h.el('exit').onclick();assert.equal(h.calls.some(item=>item.action==='save'),false);
    assert.deepEqual((await h.store.history(h.latest.id)).map(item=>item.revision),[2,1]);
  }finally{await h.close();}
});
test('opening a saved version does not duplicate unchanged history and editing it creates a new revision',async()=>{
  const h=await setup();try{
    await h.button(1,'open-version').onclick();assert.equal(h.canvas.xml,'<mxfile version="1"/>');
    assert.deepEqual((await h.store.history(h.latest.id)).map(item=>item.revision),[3,2,1]);
    h.canvas.xml='<mxfile version="edited"/>';await h.el('save').onclick();
    assert.equal((await h.store.get(h.latest.id)).revision,4);
    assert.equal((await h.store.version(h.latest.id,1)).record.xml,'<mxfile version="1"/>');
  }finally{await h.close();}
});
test('explicit saving after current-version deletion allocates a new version without reviving the deleted one',async()=>{
  const h=await setup();try{
    await h.button(3,'delete-version').onclick();await h.el('save').onclick();
    assert.deepEqual((await h.store.history(h.latest.id)).map(item=>item.revision),[4,2,1]);
  }finally{await h.close();}
});
test('last-version deletion keeps the canvas and subsequent explicit save starts a new drawing',async()=>{
  const h=await setup();try{
    const before=JSON.stringify(h.canvas);
    for(const revision of [2,1,3])await h.button(revision,'delete-version').onclick();
    assert.equal(JSON.stringify(h.canvas),before);assert.equal(await h.store.get(h.latest.id),null);
    await h.el('save').onclick();const records=await h.store.list();assert.equal(records.length,1);assert.notEqual(records[0].id,h.latest.id);assert.equal(records[0].revision,1);
  }finally{await h.close();}
});
test('version deletion cancellation, failure, and a lost response preserve canvas and retry identity',async()=>{
  const h=await setup();try{
    h.state.confirm=false;await h.button(2,'delete-version').onclick();assert.equal(h.calls.some(item=>item.action==='delete-version'),false);
    h.state.confirm=true;h.state.exportError=true;await h.button(2,'delete-version').onclick();assert.equal(h.calls.some(item=>item.action==='delete-version'),false);
    h.state.exportError=false;h.state.loseDelete=true;const before=JSON.stringify(h.canvas);
    await h.button(2,'delete-version').onclick();assert.equal(JSON.stringify(h.canvas),before);assert.match(h.el('status').textContent,/response lost/);
    await h.button(2,'delete-version').onclick();assert.equal(JSON.stringify(h.canvas),before);
    const requests=h.calls.filter(item=>item.action==='delete-version');assert.equal(requests.length,2);assert.deepEqual(requests[0].body,requests[1].body);
    assert.deepEqual((await h.store.history(h.latest.id)).map(item=>item.revision),[3,1]);
  }finally{await h.close();}
});
test('viewing old history never adopts an unobserved concurrent save as its write base',async()=>{
  const h=await setup();try{
    await h.button(1,'open-version').onclick();
    await h.store.save({id:h.latest.id,expectedRevision:3,key:'other-window',title:'other',xml:'<mxfile other="true"/>',messages:[],png});
    await h.button(2,'delete-version').onclick();h.canvas.xml='<mxfile my="edits"/>';await h.el('save').onclick();
    assert.equal((await h.store.get(h.latest.id)).xml,'<mxfile other="true"/>');assert.equal(h.canvas.xml,'<mxfile my="edits"/>');assert.match(h.el('status').textContent,/revision changed/);
  }finally{await h.close();}
});
