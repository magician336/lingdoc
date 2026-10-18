// Historical #109 plaintext-editor fixture. NOT the current continuous-editor acceptance runner.
// Use conditions-safety.browser.mjs for current navigation/conditions checks; keep this source for history.
// Optional local component integration test. Uses real Vue SFCs with synthetic
// API providers; no product service, credentials, external API or model calls.
import fs from 'node:fs'
import path from 'node:path'
import http from 'node:http'
import { spawn } from 'node:child_process'
import { fileURLToPath } from 'node:url'
import { setTimeout as delay } from 'node:timers/promises'
import { createHash } from 'node:crypto'
import { build } from 'esbuild'
import { parse, compileScript, compileStyle } from '@vue/compiler-sfc'

const frontend = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const output = process.env.UI_SAFETY_EVIDENCE
if (!output) throw new Error('Set UI_SAFETY_EVIDENCE to an isolated local evidence directory')
fs.mkdirSync(output, { recursive: true })
const profile = fs.mkdtempSync(path.join(output, 'edge-profile-'))
const edge = process.env.UI_SAFETY_BROWSER || 'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe'
const sourceHashes = {'src/views/lingdoc/uiSafety.ts':createHash('sha256').update(fs.readFileSync(path.join(frontend,'src/views/lingdoc/uiSafety.ts'))).digest('hex')}
const bundle = await build({
  absWorkingDir: frontend, stdin: { contents: `
    import {createApp, h, ref} from 'vue';
    import {createRouter, createMemoryHistory, RouterView} from 'vue-router';
    import Workspace from '@/views/lingdoc/Workspace.vue';
    import Dialog from '@/components/LingDocCandidateAdoptionDialog.vue';
    const project = {id:'p1', name:'合成 UI 项目', status:'active', project_version:1,
      spec_revision:1, current_context_revision:1, spec:{research_subject:'合成主题', research_goal:'合成目标'},
      template_id:'template',template_version:'1',members:[]};
    const chapter = {id:'c1',project_id:'p1',section_id:'s1',title:'合成章节',current_version_id:'v1',
      body_markdown:'合成正式正文',source_ids:[],citation_usages:[],confirmation_valid:false,
      review_items:[{id:'r1',statement:'合成待核一'},{id:'r2',statement:'合成待核二'}]};
    const secondProject={...project,id:'p2',name:'另一个合成项目',spec:{research_subject:'第二主题',research_goal:'第二目标'}};
    const secondChapter={...chapter,id:'c2',project_id:'p2',title:'第二项目章节',review_items:[]};
    const conditions=[];
    const controls = window.controls = {saveMode:'pending',adoptMode:'success',readMode:'success',projectMode:'success',
      previewMode:'success',applyMode:'success',lastConditionsInput:null,confirmCalls:0,
      calls:{},emitted:null,resolveSave:null,resolveProject:null};
    const envelope = data => ({data:structuredClone(data),meta:{replayed:false,refresh_required:false}});
    let saved = {...chapter,chapter_id:'c1',base_chapter_version_id:'v1',spec_revision:1,working_copy_revision:1};
    window.resetConditionsBody=()=>{saved={...saved,body_markdown:chapter.body_markdown,citation_usages:[],spec_revision:project.spec_revision}};
    window.__uiApi = async (name,args) => {
      controls.calls[name] = (controls.calls[name]||0)+1;
      if(name.startsWith('new')) return crypto.randomUUID();
      switch(name){
        case 'listProjects': return envelope({items:[project,secondProject],truncated:false});
        case 'getProject': {
          const current=args[0]==='p2'?secondProject:project;
          if(controls.projectMode==='fail') throw new Error('合成项目读取失败');
          if(controls.projectMode==='pending') return new Promise((resolve,reject)=>controls.resolveProject=ok=>{
            controls.resolveProject=null;
            if(ok) { current.project_version++; resolve(envelope(current)); } else reject(new Error('合成刷新失败')); });
          return envelope(current);
        }
        case 'listChapters':
          if(controls.readMode==='fail') throw new Error('合成读回失败');
          return envelope([args[0]==='p2'?secondChapter:chapter]);
        case 'getWorkingCopy': return envelope(args[0]==='p2'?{...secondChapter,chapter_id:'c2',base_chapter_version_id:'v1',spec_revision:1,working_copy_revision:1}:saved);
        case 'listChapterVersions': return envelope({items:[]});
        case 'listAssets': return envelope([]);
        case 'listChangeSets': return envelope(conditions.filter(item=>item.project_id===args[0]));
        case 'createChangeSet': {
          const input=args[1],current=args[0]==='p2'?secondProject:project;
          controls.lastConditionsInput=structuredClone(input);
          if(controls.previewMode==='fail') throw new Error('合成条件预览失败');
          const item={id:'conditions-'+(conditions.length+1),project_id:args[0],created_by:'fixture',status:'assessed',
            reason:input.reason,base_spec_revision:current.spec_revision,base_context_revision:current.current_context_revision,
            fields:Object.entries(input.fields).map(([key,value])=>({key,...value})),
            impacts:input.affected_chapter_ids.map(id=>({chapter_id:id,title:id==='c2'?'第二项目章节':'合成章节',reason:input.reason})),
            created_at:new Date().toISOString()};
          conditions.push(item);return envelope(item);
        }
        case 'applyChangeSet': {
          if(controls.applyMode==='fail') throw new Error('合成条件应用失败');
          const item=conditions.find(item=>item.id===args[1]),current=args[0]==='p2'?secondProject:project;
          for(const field of item.fields)current.spec[field.key]=field.new_value;
          current.spec_revision++;current.current_context_revision++;current.project_version++;
          (args[0]==='p2'?secondChapter:chapter).confirmation_valid=false;
          item.status='applied';return envelope(item);
        }
        case 'rejectChangeSet': {
          const item=conditions.find(item=>item.id===args[1]);item.status='rejected';return envelope(item);
        }
        case 'getChangeSet': return envelope(conditions.find(item=>item.id===args[1]));
        case 'listGenerationCandidates': return envelope([]);
        case 'getAccessStatus': return envelope({project_id:'p1',status:'available',recovery_actions:[]});
        case 'listReleases': case 'listExports': return envelope({items:[],truncated:false});
        case 'checkDelivery': throw {status:409,error:{code:'version_conflict'}};
        case 'saveWorkingCopy': return new Promise((resolve,reject)=>controls.resolveSave=ok=>{
          controls.resolveSave=null;
          if(!ok) return reject(new Error('合成保存失败'));
          saved={...saved,...args[2],working_copy_revision:saved.working_copy_revision+1};resolve(envelope(saved));});
        case 'confirmChapter': chapter.confirmation_valid=true; return envelope({});
        case 'acceptCandidate':
          if(controls.adoptMode==='fail') throw new Error('合成写入失败');
          chapter.body_markdown='采纳后的服务器最新正文';chapter.current_version_id='v2';return envelope(chapter);
        default: throw new Error('Unexpected synthetic API call: '+name);
      }
    };
    const candidate={id:'candidate1',body_markdown:'合成候选正文',source_ids:[],review_items:[],
      basis:{spec_revision:1,template_id:'template',template_version:'1',asset_versions:[]}};
    const mode=ref('workspace');
    const router=window.fixtureRouter=createRouter({history:createMemoryHistory(),routes:[
      {path:'/',component:Workspace},{path:'/else',component:{render:()=>h('p','已离开工作台')}}]});
    window.showDialog=()=>mode.value='dialog';
    const app=createApp({render:()=>mode.value==='workspace'?h(RouterView):h(Dialog,{projectId:'p1',chapter,
      candidate,expectedSpecRevision:1,onAdopted:value=>controls.emitted=value})});
    app.use(router); router.push('/').then(()=>router.isReady()).then(()=>{app.mount('#app');window.ready=true});
  `, resolveDir: frontend, loader:'ts' }, bundle:true, write:false, format:'iife', target:'es2022',
  define: {'process.env.NODE_ENV':'"development"',__VUE_OPTIONS_API__:'true',__VUE_PROD_DEVTOOLS__:'false',__VUE_PROD_HYDRATION_MISMATCH_DETAILS__:'false'},
  plugins:[{name:'actual-sfc-synthetic-api',setup(b){
    b.onResolve({filter:/^@\/api\/lingdoc\//},args=>({path:path.join(frontend,'src',args.path.slice(2))+'.ts',namespace:'fixture-api'}));
    b.onLoad({filter:/.*/,namespace:'fixture-api'},args=>{
      const names=[...fs.readFileSync(args.path,'utf8').matchAll(/^export\s+(?:(?:async\s+)?function|const)\s+(\w+)/gm)].map(m=>m[1]);
      return {contents:names.map(name=>`export const ${name}=(...args)=>window.__uiApi('${name}',args);`).join('\n'),loader:'js'};
    });
    b.onResolve({filter:/^@\//},args=>{
      const base=path.join(frontend,'src',args.path.slice(2));
      return {path:[base,base+'.ts',base+'.vue'].find(p=>fs.existsSync(p)&&fs.statSync(p).isFile())||base};
    });
    b.onLoad({filter:/\.vue$/},args=>{
      const raw=fs.readFileSync(args.path,'utf8');
      sourceHashes[path.relative(frontend,args.path).replaceAll('\\','/')]=createHash('sha256').update(raw).digest('hex');
      const {descriptor}=parse(raw,{filename:args.path});
      const id='fixture-'+createHash('sha256').update(args.path).digest('hex').slice(0,8);
      const script=compileScript(descriptor,{id,inlineTemplate:true,fs:{fileExists:fs.existsSync,readFile:p=>fs.readFileSync(p,'utf8')}});
      const css=descriptor.styles.map(style=>compileStyle({id:'data-v-'+id,source:style.content,scoped:style.scoped}).code).join('\n');
      return {contents:script.content.replace('export default','const __component =')+`\n__component.__scopeId='data-v-${id}';\nconst style=document.createElement('style');style.textContent=${JSON.stringify(css)};document.head.append(style);export default __component;`,loader:'ts',resolveDir:path.dirname(args.path)};
    });
  }}],
})
const js=bundle.outputFiles[0].text
let ws, child, server
const results=[], runtimeErrors=[], dialogs=[]
let nextId=0
const pending=new Map()
const send=(method,params={})=>new Promise((resolve,reject)=>{
  const id=++nextId, timer=setTimeout(()=>{pending.delete(id);reject(new Error('CDP timeout '+method))},10000);
  pending.set(id,{resolve,reject,timer});ws.send(JSON.stringify({id,method,params}));
})
const evaluate=async expression=>{
  const r=await send('Runtime.evaluate',{expression,awaitPromise:true,returnByValue:true});
  if(r.exceptionDetails) throw new Error(r.exceptionDetails.exception?.description||r.exceptionDetails.text);
  return r.result.value;
}
const waitFor=async expression=>{for(let i=0;i<70;i++){if(await evaluate(expression))return;await delay(100)}throw new Error('Fixture wait timed out: '+expression)}
const check=async(name,expression)=>{const pass=!!await evaluate(expression);results.push({name,pass});if(!pass)throw new Error('FAIL '+name)}
const clickText=async(text,selector='button')=>evaluate(`(()=>{const el=[...document.querySelectorAll(${JSON.stringify(selector)})].find(x=>x.textContent.trim().startsWith(${JSON.stringify(text)}));if(!el||el.disabled)throw Error('Button unavailable: '+${JSON.stringify(text)});el.click()})()`)
const input=async(selector,value)=>evaluate(`(()=>{const el=document.querySelector(${JSON.stringify(selector)});if(!el)throw Error('Input missing');el.value=${JSON.stringify(value)};el.dispatchEvent(new Event('input',{bubbles:true}))})()`)
const dirtyEvent=`(()=>{const e=new Event('beforeunload',{cancelable:true});window.dispatchEvent(e);return e.defaultPrevented})()`
try {
  server=http.createServer((req,res)=>{if(req.url==='/fixture.js'){res.setHeader('content-type','text/javascript; charset=utf-8');res.end(js)}else{res.setHeader('content-type','text/html; charset=utf-8');res.end('<!doctype html><meta charset="utf-8"><title>LingDoc 合成 UI 验证</title><div id="app"></div><script src="/fixture.js"></script>')}});
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
  const url=`http://127.0.0.1:${server.address().port}/`;
  child=spawn(edge,['--headless=new','--remote-debugging-port=0',`--user-data-dir=${profile}`,'--no-first-run','--window-size=1440,1000',url],{windowsHide:true,stdio:'ignore'});
  let spawnError;child.on('error',e=>spawnError=e);
  let port;for(let i=0;i<70;i++){if(spawnError)throw spawnError;const active=path.join(profile,'DevToolsActivePort');if(fs.existsSync(active)){port=Number(fs.readFileSync(active,'utf8').split(/\r?\n/)[0]);break}await delay(150)}
  if(!port)throw Error('Edge DevTools port missing');
  const targets=await(await fetch(`http://127.0.0.1:${port}/json/list`)).json();
  ws=new WebSocket(targets.find(x=>x.type==='page').webSocketDebuggerUrl);
  await new Promise((resolve,reject)=>{ws.addEventListener('open',resolve,{once:true});ws.addEventListener('error',reject,{once:true})});
  ws.addEventListener('message',event=>{const message=JSON.parse(event.data);if(message.method==='Runtime.exceptionThrown')runtimeErrors.push(message.params.exceptionDetails.text);if(message.method==='Page.javascriptDialogOpening')dialogs.push(message.params.type);const job=pending.get(message.id);if(job){clearTimeout(job.timer);pending.delete(message.id);message.error?job.reject(Error(JSON.stringify(message.error))):job.resolve(message.result)}});
  await send('Runtime.enable');await send('Page.enable');await waitFor("window.ready&&document.body.textContent.includes('合成 UI 项目')");
  await clickText('合成 UI 项目');await waitFor("!!document.querySelector('#chapter-body')&&!document.querySelector('#chapter-body').disabled");
  await check('clean workspace does not prevent refresh',`!${dirtyEvent}`);
  await check('confirmation explains both missing review reasons',"document.querySelector('#chapter-confirm-help')?.textContent.includes('2 条')&&[...document.querySelectorAll('button')].find(x=>x.textContent.trim()==='确认当前章节版本').disabled");
  await check('conditions auxiliary form is absent while saved spec is unchanged',"!document.querySelector('#change-reason')");
  const conditionsBox=await evaluate("(()=>{const el=document.querySelector('#subject');el.scrollIntoView({block:'center'});const r=el.getBoundingClientRect();return {x:r.x+20,y:r.y+10}})()");
  await send('Input.dispatchMouseEvent',{type:'mousePressed',...conditionsBox,button:'left',clickCount:1});
  await send('Input.dispatchMouseEvent',{type:'mouseReleased',...conditionsBox,button:'left',clickCount:1});
  await input('#subject','合成修改主题');await waitFor("!!document.querySelector('#change-reason')");
  await input('#change-reason','仅理由保留');await input('#subject','合成主题');
  await waitFor("!document.querySelector('#change-reason')");
  await check('reason entered through visible form remains protected after restoring spec',dirtyEvent);
  const conditionDialogCount=dialogs.filter(type=>type==='beforeunload').length;
  const conditionsReload=send('Page.reload');
  for(let i=0;i<50&&dialogs.filter(type=>type==='beforeunload').length===conditionDialogCount;i++)await delay(50);
  if(dialogs.filter(type=>type==='beforeunload').length===conditionDialogCount)throw Error('Conditions draft reload did not show beforeunload dialog');
  await send('Page.handleJavaScriptDialog',{accept:false});await conditionsReload;
  await evaluate("window.confirm=()=>{controls.confirmCalls++;return false};fixtureRouter.push('/else')");
  const readsBeforeCancel=await evaluate('controls.calls.getProject');
  const confirmsBeforeCancel=await evaluate('controls.confirmCalls');
  await clickText('合成 UI 项目');await clickText('重新读取');await clickText('另一个合成项目');
  await check('same-project click, explicit reload and project switch all confirm hidden conditions inputs',
    `controls.confirmCalls===${confirmsBeforeCancel+3}&&controls.calls.getProject===${readsBeforeCancel}&&fixtureRouter.currentRoute.value.path==='/'`);
  await input('#subject','合成修改主题');await waitFor("!!document.querySelector('#change-reason')");
  await check('cancelled actual refresh and navigation retain the exact reason',"document.querySelector('#change-reason').value==='仅理由保留'");
  await evaluate("window.confirm=()=>true;controls.projectMode='fail'");await clickText('另一个合成项目');
  await waitFor("document.body.textContent.includes('合成项目读取失败')");
  await check('failed target project read retains current project and auxiliary draft',
    "document.querySelector('.work-area h2').textContent==='合成 UI 项目'&&document.querySelector('#change-reason').value==='仅理由保留'");
  await evaluate("controls.projectMode='success';controls.readMode='fail'");await clickText('另一个合成项目');
  await waitFor("document.body.textContent.includes('合成读回失败')");
  await check('failed target chapter read also keeps the original project and auxiliary draft',
    "document.querySelector('.work-area h2').textContent==='合成 UI 项目'&&document.querySelector('#subject').value==='合成修改主题'&&document.querySelector('#change-reason').value==='仅理由保留'");
  await evaluate("controls.readMode='success'");await clickText('重新读取');
  await waitFor("document.querySelector('#subject').value==='合成主题'&&!document.querySelector('#change-reason')");
  await check('confirmed successful same-project reload resets auxiliary input baseline',`!${dirtyEvent}`);
  await input('#subject','仅范围变动');await waitFor("!!document.querySelector('#change-reason')");
  await evaluate("document.querySelector('.chapter-impact-choice input[value=\"c1\"]').click()");
  await input('#subject','合成主题');await waitFor("!document.querySelector('#change-reason')");
  await check('chapter selection alone remains protected after restoring spec',dirtyEvent);
  await clickText('另一个合成项目');await waitFor("document.querySelector('.work-area h2').textContent==='另一个合成项目'&&!!document.querySelector('#chapter-body')&&!document.querySelector('#chapter-body').disabled");
  await input('#subject','第二修改主题');await waitFor("!!document.querySelector('#change-reason')");
  await check('successful project switch cannot carry the previous reason or chapter selection',
    "document.querySelector('#change-reason').value===''&&!document.querySelector('.chapter-impact-choice input[value=\"c2\"]').checked");
  await input('#change-reason','项目二理由');await evaluate("document.querySelector('.chapter-impact-choice input[value=\"c2\"]').click()");
  await clickText('生成变更预览');await waitFor("!!document.querySelector('.change-set-preview')");
  await check('new project preview submits only its own chapter IDs',"controls.lastConditionsInput.reason==='项目二理由'&&JSON.stringify(controls.lastConditionsInput.affected_chapter_ids)==='[\"c2\"]'");
  await check('successful preview still protects unapplied spec edits',dirtyEvent);
  await input('#subject','第二主题');await waitFor("!document.querySelector('#change-reason')");
  await check('acknowledged auxiliary inputs alone no longer cause stale warnings',`!${dirtyEvent}`);
  await clickText('合成 UI 项目');await waitFor("document.querySelector('.work-area h2').textContent==='合成 UI 项目'&&!!document.querySelector('#chapter-body')&&!document.querySelector('#chapter-body').disabled");
  await input('#subject','待应用主题');await waitFor("!!document.querySelector('#change-reason')");
  await input('#change-reason','应用理由');await evaluate("document.querySelector('.chapter-impact-choice input[value=\"c1\"]').click()");
  await evaluate("controls.previewMode='fail'");await clickText('生成变更预览');await waitFor("document.body.textContent.includes('合成条件预览失败')");
  await input('#subject','合成主题');await waitFor("!document.querySelector('#change-reason')");
  await check('failed preview leaves auxiliary input dirty even when spec is restored',dirtyEvent);
  await input('#subject','待应用主题');await waitFor("!!document.querySelector('#change-reason')");
  await check('failed preview preserves exact reason and affected chapter selection',
    "document.querySelector('#change-reason').value==='应用理由'&&document.querySelector('.chapter-impact-choice input[value=\"c1\"]').checked");
  await evaluate("controls.previewMode='success'");await clickText('生成变更预览');await waitFor("!!document.querySelector('.change-set-preview')");
  await evaluate("controls.applyMode='fail'");await clickText('应用预览并开始复核');await waitFor("document.body.textContent.includes('合成条件应用失败')");
  await check('failed apply retains the reason, affected chapters and retryable preview',
    "document.querySelector('#change-reason').value==='应用理由'&&document.querySelector('.chapter-impact-choice input[value=\"c1\"]').checked&&!!document.querySelector('.change-set-preview')");
  const confirmsBeforeApply=await evaluate('controls.confirmCalls');
  await evaluate("controls.applyMode='success';window.confirm=()=>{controls.confirmCalls++;return false}");
  await clickText('应用预览并开始复核');await waitFor("document.body.textContent.includes('已应用')&&!document.querySelector('#change-reason')");
  await check('clean successful apply refresh adds no discard confirmation and clears auxiliary dirty',`controls.confirmCalls===${confirmsBeforeApply}&&!${dirtyEvent}`);
  await input('#subject','第二次已应用主题');await waitFor("!!document.querySelector('#change-reason')");
  await check('applied conditions form starts empty for a subsequent edit',
    "document.querySelector('#change-reason').value===''&&!document.querySelector('.chapter-impact-choice input[value=\"c1\"]').checked");
  await input('#change-reason','与正文并行变更');await evaluate("document.querySelector('.chapter-impact-choice input[value=\"c1\"]').click()");
  await clickText('生成变更预览');await waitFor("!!document.querySelector('.change-set-preview')");
  await input('#chapter-body','保留的未保存正文');
  const readsBeforeAppliedCancel=await evaluate('controls.calls.getProject');
  const confirmsBeforeAppliedCancel=await evaluate('controls.confirmCalls');
  await clickText('应用预览并开始复核');await waitFor("document.body.textContent.includes('已应用')&&!document.querySelector('.change-set-preview')");
  await check('apply refresh still confirms unrelated unsaved body and cancellation preserves it',
    `controls.confirmCalls===${confirmsBeforeAppliedCancel+1}&&controls.calls.getProject===${readsBeforeAppliedCancel}&&document.querySelector('#chapter-body').value==='保留的未保存正文'`);
  await waitFor('!!controls.resolveSave');await evaluate('controls.resolveSave(true)');await waitFor("document.body.textContent.includes('· 已保存')");
  await evaluate('resetConditionsBody();window.confirm=()=>true');await clickText('重新读取');
  await waitFor("document.querySelector('#subject').value==='第二次已应用主题'&&document.querySelector('#chapter-body').value==='合成正式正文'");
  await input('#subject','将驳回主题');await waitFor("!!document.querySelector('#change-reason')");
  await input('#change-reason','驳回理由');await evaluate("document.querySelector('.chapter-impact-choice input[value=\"c1\"]').click()");
  await clickText('生成变更预览');await waitFor("!!document.querySelector('.change-set-preview')");
  const confirmsBeforeReject=await evaluate('controls.confirmCalls');
  await evaluate("window.confirm=()=>{controls.confirmCalls++;return false}");await clickText('驳回预览');
  await waitFor("document.body.textContent.includes('已驳回')&&!document.querySelector('#change-reason')");
  await check('successful reject handles only its conditions inputs without an extra confirmation',`controls.confirmCalls===${confirmsBeforeReject}&&!${dirtyEvent}`);

  await input('#reason-r1','合成核查依据一');await check('unconfirmed review reason protects refresh',dirtyEvent);
  await input('#reason-r2','合成核查依据二');
  await evaluate('window.confirm=()=>true');await clickText('确认当前章节版本');await waitFor("document.body.textContent.includes('当前章节版本已确认')");
  await check('acknowledged review reasons no longer cause leave warning',`!${dirtyEvent}`);
  const box=await evaluate("(()=>{const el=document.querySelector('#chapter-body');el.scrollIntoView({block:'center'});const r=el.getBoundingClientRect();return {x:r.x+20,y:r.y+20}})()");
  await send('Input.dispatchMouseEvent',{type:'mousePressed',...box,button:'left',clickCount:1});
  await send('Input.dispatchMouseEvent',{type:'mouseReleased',...box,button:'left',clickCount:1});
  await input('#chapter-body','未保存合成草稿');await check('900ms debounce interval protects refresh',dirtyEvent);
  const bodyDialogCount=dialogs.filter(type=>type==='beforeunload').length;
  const reload=send('Page.reload');
  for(let i=0;i<50&&dialogs.filter(type=>type==='beforeunload').length===bodyDialogCount;i++)await delay(50);
  if(dialogs.filter(type=>type==='beforeunload').length===bodyDialogCount)throw Error('Real reload did not show browser beforeunload dialog');
  await send('Page.handleJavaScriptDialog',{accept:false});await reload;
  await check('real browser reload cancellation retains draft',"document.querySelector('#chapter-body').value==='未保存合成草稿'");
  await evaluate("window.confirm=()=>false;fixtureRouter.push('/else')");
  await check('cancelled route leave preserves exact draft',"fixtureRouter.currentRoute.value.path==='/'&&document.querySelector('#chapter-body').value==='未保存合成草稿'");
  await waitFor('!!controls.resolveSave');await check('in-flight save protects refresh',dirtyEvent);
  await evaluate('controls.resolveSave(false)');await waitFor("document.body.textContent.includes('保存失败，输入仍保留')");
  await check('failed save keeps input protected',dirtyEvent);
  await clickText('保存工作副本');await evaluate('controls.resolveSave(true)');await waitFor("document.body.textContent.includes('· 已保存')");
  await check('saved working copy is safe to leave even before formal commit',`!${dirtyEvent}`);
  await evaluate("window.confirm=()=>true;fixtureRouter.push('/else')");await waitFor("fixtureRouter.currentRoute.value.path==='/else'");
  await check('unmount removes refresh handler',`!${dirtyEvent}`);
  await evaluate("fixtureRouter.push('/')");await waitFor("document.body.textContent.includes('合成 UI 项目')");await clickText('合成 UI 项目');await waitFor("!!document.querySelector('#chapter-body')&&!document.querySelector('#chapter-body').disabled");
  await evaluate("controls.projectMode='pending'");await clickText('交付检查');await waitFor('!!controls.resolveProject');
  await check('409 shows refresh pending rather than premature success',"document.body.textContent.includes('正在重新读取当前版本')&&!document.body.textContent.includes('已重新读取当前版本')");
  await evaluate('controls.resolveProject(false)');await waitFor("document.body.textContent.includes('项目版本重新读取失败')");
  await check('failed refresh never claims success and never retries the write',"controls.calls.checkDelivery===1&&!document.body.textContent.includes('已重新读取当前版本')");
  await clickText('交付检查');await waitFor('!!controls.resolveProject');await evaluate('controls.resolveProject(true)');await waitFor("document.body.textContent.includes('已重新读取当前版本')");
  await check('success appears only after acknowledged parent refresh',"controls.calls.checkDelivery===2");
  await evaluate("controls.projectMode='success';showDialog();controls.adoptMode='fail'");await waitFor("!!document.querySelector('.candidate-adoption__primary')");
  await clickText('确认整章替换');await waitFor("document.body.textContent.includes('合成写入失败')");await check('failed write is not described as accepted',"!document.body.textContent.includes('候选已采纳。')");
  await evaluate("controls.adoptMode='success';controls.readMode='fail'");await clickText('确认整章替换');await waitFor("document.body.textContent.includes('章节状态读取失败')");
  await check('accepted write remains acknowledged when read fails',"document.body.textContent.includes('候选已采纳。')&&document.querySelector('.candidate-adoption__primary').disabled");
  const writeCount=await evaluate('controls.calls.acceptCandidate');
  await clickText('重新读取章节状态');await waitFor("document.body.textContent.includes('章节状态读取失败')");
  await check('failed read retry never repeats acceptance',`controls.calls.acceptCandidate===${writeCount}`);
  await evaluate("controls.readMode='success'");await clickText('重新读取章节状态');await waitFor('!!controls.emitted');
  await check('successful read retry emits current server chapter without another write',`controls.calls.acceptCandidate===${writeCount}&&controls.emitted.body_markdown==='采纳后的服务器最新正文'`);
  const screenshot=await send('Page.captureScreenshot',{format:'png'});fs.writeFileSync(path.join(output,'adoption-read-recovery.png'),Buffer.from(screenshot.data,'base64'));
  results.push({name:'no browser runtime exceptions',pass:runtimeErrors.length===0});
} catch(error) {
  let page;try{page=await evaluate('document.body.innerText')}catch{}
  results.push({name:'runner completion',pass:false,error:error.stack,page});
}
finally {
  fs.writeFileSync(path.join(output,'browser-result.json'),JSON.stringify({pass:results.length>0&&results.every(x=>x.pass),mode:'actual Vue components; synthetic API',sourceHashes,runtimeErrors,dialogs,results},null,2));
  if(ws?.readyState===WebSocket.OPEN){try{await send('Browser.close')}catch{}ws.close()}
  if(child?.pid&&child.exitCode===null){await delay(300);if(child.exitCode===null)await new Promise(resolve=>{const stop=spawn('taskkill.exe',['/PID',String(child.pid),'/T','/F'],{windowsHide:true,stdio:'ignore'});stop.once('exit',resolve);stop.once('error',resolve)})}
  server?.close();for(const job of pending.values())clearTimeout(job.timer);
}
console.log(JSON.stringify({pass:results.every(x=>x.pass),checks:results.length,evidence:output,failed:results.filter(x=>!x.pass)}));
if(results.some(x=>!x.pass))process.exitCode=1;
