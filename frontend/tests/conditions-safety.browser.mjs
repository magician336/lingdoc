// Real Vue/Tiptap components with synthetic APIs. No product services or model calls.
// UI_SAFETY_EVIDENCE must be private. UI_SAFETY_BUILD_ONLY=1 bundles without launching Edge or HTTP.
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
if (!output) throw Error('Set UI_SAFETY_EVIDENCE to an isolated evidence directory')
fs.mkdirSync(output, { recursive: true })
const edge = process.env.UI_SAFETY_BROWSER || 'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe'
const sourceHashes = {}
for (const relative of ['src/views/lingdoc/uiSafety.ts', 'src/views/lingdoc/editor/saveQueue.ts']) {
  sourceHashes[relative] = createHash('sha256').update(fs.readFileSync(path.join(frontend, relative))).digest('hex')
}
const bundle = await build({
  absWorkingDir: frontend, stdin: { contents: `
    import {createApp, h} from 'vue';
    import {createRouter, createMemoryHistory, RouterView} from 'vue-router';
    import Workspace from '@/views/lingdoc/Workspace.vue';
    const projects = {
      p1:{id:'p1',name:'合成条件项目一',status:'active',project_version:1,spec_revision:1,current_context_revision:1,
        spec:{research_subject:'合成主题一',research_goal:'合成目标一'},template_id:'template',template_version:'1',members:[{id:'u1'}]},
      p2:{id:'p2',name:'合成条件项目二',status:'active',project_version:1,spec_revision:1,current_context_revision:1,
        spec:{research_subject:'合成主题二',research_goal:'合成目标二'},template_id:'template',template_version:'1',members:[{id:'u1'}]},
    };
    const chapters = {
      p1:[{id:'c1',project_id:'p1',section_id:'s1',title:'研究方案',current_version_id:'v1',body_markdown:'第一章合成正式正文。',source_ids:[],citation_usages:[],confirmation_valid:false,review_items:[{id:'r1',statement:'合成待核项'}]},
          {id:'c2',project_id:'p1',section_id:'s2',title:'研究问题',current_version_id:'v2',body_markdown:'第二章合成正式正文。',source_ids:[],citation_usages:[],confirmation_valid:false,review_items:[]}],
      p2:[{id:'d1',project_id:'p2',section_id:'s1',title:'第二项目章节',current_version_id:'w1',body_markdown:'第二项目合成正文。',source_ids:[],citation_usages:[],confirmation_valid:false,review_items:[]}],
    };
    // Synthetic authenticated Owner and exact template fields: this harness
    // verifies navigation, not product authentication or permission grants.
    for (const [id, project] of Object.entries(projects)) {
      project.members=[{user_id:'u1',role:'owner',governance_role:'owner',status:'active'}];
      project.template_copy={id:'fixture-copy-'+id,version:1,status:'bound',definition:{
        id:'template',version:'1',fields:[{field_id:'research_subject',label:'研究主题',type:'string'},
          {field_id:'research_goal',label:'研究目标',type:'string'}],
        sections:chapters[id].map((ch,order)=>({section_id:ch.section_id,title:ch.title,order,required:true})),terms:[],rules:[]}};
    }
    const copies = Object.fromEntries(Object.values(chapters).flat().map(ch=>[ch.id,{...structuredClone(ch),chapter_id:ch.id,base_chapter_version_id:ch.current_version_id,spec_revision:1,working_copy_revision:1}]));
    const conditions=[];
    const controls=window.controls={calls:{},requests:[],confirmCalls:0,readFailure:'',pendingProject:false,pendingPreview:false,pendingSave:false,
      previewFailure:false,applyFailure:false,rejectFailure:false,resolveProjects:[],resolvePreview:null,resolveSaves:[],lastConditionsInput:null};
    const envelope=data=>({data:structuredClone(data),meta:{replayed:false,refresh_required:false}});
    window.__uiApi=async(name,args)=>{
      controls.calls[name]=(controls.calls[name]||0)+1;controls.requests.push({name,args:structuredClone(args)});
      if(name.startsWith('new')) return crypto.randomUUID();
      const pid=args[0], project=projects[pid];
      if(controls.readFailure===name) throw Error('合成初读失败：'+name);
      switch(name){
        case 'listKnowledgeBases':case 'listKnowledgeFiles':return envelope({items:[]});
        case 'listProjects':return envelope({items:Object.values(projects),truncated:false});
        case 'listMyProjectInvitations':case 'listTemplateProfiles':case 'listTemplateVersions':return envelope([]);
        case 'getEffectivePermission':return envelope({allowed:true,capability:args[1],target:pid,reason_code:'fixture_owner',project_version:project.project_version,context_revision:project.current_context_revision});
        case 'getProject':
          if(controls.pendingProject)return new Promise((resolve,reject)=>controls.resolveProjects.push({id:pid,release:ok=>ok?resolve(envelope(project)):reject(Error('合成项目读取失败'))}));
          return envelope(project);
        case 'listChapters':return envelope(chapters[pid]);
        case 'getWorkingCopy':return envelope(copies[args[1]]);
        case 'listChapterVersions':case 'listReleases':case 'listExports':return envelope({items:[],truncated:false});
        case 'listAssets':case 'listGenerationCandidates':return envelope([]);
        case 'listChangeSets':return envelope(conditions.filter(item=>item.project_id===pid));
        case 'getAccessStatus':return envelope({project_id:pid,status:'available',recovery_actions:[]});
        case 'getCurrentConfirmation':return envelope(null);
        case 'createChangeSet':{
          const input=structuredClone(args[1]);controls.lastConditionsInput=input;
          const finish=()=>{
            if(controls.previewFailure)throw Error('合成条件预览失败');
            const item={id:'conditions-'+(conditions.length+1),project_id:pid,created_by:'fixture',status:'assessed',reason:input.reason,
              base_spec_revision:project.spec_revision,base_context_revision:project.current_context_revision,
              fields:Object.entries(input.fields).map(([key,value])=>({key,...value})),
              impacts:input.affected_chapter_ids.map(id=>({chapter_id:id,title:chapters[pid].find(ch=>ch.id===id)?.title||id,reason:input.reason})),created_at:new Date().toISOString()};
            conditions.push(item);return envelope(item);
          };
          if(controls.pendingPreview)return new Promise((resolve,reject)=>controls.resolvePreview=()=>{controls.resolvePreview=null;try{resolve(finish())}catch(e){reject(e)}});
          return finish();
        }
        case 'applyChangeSet':case 'rejectChangeSet':{
          if(name==='applyChangeSet'&&controls.applyFailure)throw Error('合成条件应用失败');
          if(name==='rejectChangeSet'&&controls.rejectFailure)throw Error('合成条件驳回失败');
          const item=conditions.find(item=>item.id===args[1]);
          item.status=name==='applyChangeSet'?'applied':'rejected';
          if(item.status==='applied'){
            for(const field of item.fields)project.spec[field.key]=field.new_value;
            project.spec_revision++;project.current_context_revision++;project.project_version++;
            for(const ch of chapters[pid])copies[ch.id].spec_revision=project.spec_revision;
          }
          return envelope(item);
        }
        case 'getChangeSet':return envelope(conditions.find(item=>item.id===args[1]));
        case 'saveWorkingCopy':{
          const cid=args[1],input=structuredClone(args[2]);
          const finish=()=>{
            const copy=copies[cid];
            if(input.expected_working_copy_revision!==copy.working_copy_revision||input.expected_spec_revision!==project.spec_revision||input.base_chapter_version_id!==copy.base_chapter_version_id)throw {status:409,message:'合成 CAS 冲突'};
            Object.assign(copy,input,{working_copy_revision:copy.working_copy_revision+1});
            return envelope(copy);
          };
          if(controls.pendingSave)return new Promise((resolve,reject)=>controls.resolveSaves.push({chapterId:cid,release:ok=>{try{ok?resolve(finish()):reject(Error('合成保存失败'))}catch(e){reject(e)}}}));
          return finish();
        }
        default:throw Error('Unexpected synthetic API call: '+name);
      }
    };
    const router=window.fixtureRouter=createRouter({history:createMemoryHistory(),routes:[
      {path:'/',component:Workspace},{path:'/else',component:{render:()=>h('p','已离开合成工作台')}}]});
    const app=createApp({render:()=>h(RouterView)});
    // Traverse actual mounted Vue instances; only access the editor's public defineExpose API.
    window.editorHandle=()=>{
      const visit=vnode=>{
        if(!vnode)return null;
        if(vnode.component){if(vnode.component.type.__name==='LingDocEditor')return vnode.component.exposed;const found=visit(vnode.component.subTree);if(found)return found;}
        if(Array.isArray(vnode.children))for(const child of vnode.children){const found=visit(child);if(found)return found;}
        return null;
      };
      return visit(app._instance?.subTree);
    };
    window.workspaceDebug=()=>{
      const visit=vnode=>{
        if(!vnode)return null;
        if(vnode.component){if(vnode.component.type.__name==='Workspace')return vnode.component.exposed?.conditionsSafetySnapshot?.();const found=visit(vnode.component.subTree);if(found)return found;}
        if(Array.isArray(vnode.children))for(const child of vnode.children){const found=visit(child);if(found)return found;}
        return null;
      };
      return visit(app._instance?.subTree);
    };
    window.tiptap=()=>window.editorHandle()?.editor?.value;
    window.chapterPos=id=>{
      let position=null;tiptap().state.doc.forEach((node,offset)=>{if(node.type.name==='chapterTitle'&&node.attrs.chapterId===id)position=offset+node.nodeSize+1;});return position;
    };
    app.use(router);router.push('/').then(()=>router.isReady()).then(()=>{app.mount('#app');window.ready=true});
  `, resolveDir: frontend, loader: 'ts' }, bundle: true, write: false, format: 'iife', target: 'es2022',
  define: { 'process.env.NODE_ENV': '"development"', __VUE_OPTIONS_API__: 'true', __VUE_PROD_DEVTOOLS__: 'false', __VUE_PROD_HYDRATION_MISMATCH_DETAILS__: 'false' },
  plugins: [{ name: 'actual-sfc-synthetic-api', setup(b) {
    b.onResolve({filter:/^@\/stores\/auth$/},()=>({path:'synthetic-owner',namespace:'fixture-auth'}))
    b.onLoad({filter:/.*/,namespace:'fixture-auth'},()=>({contents:"export const useAuthStore=()=>({currentUserId:'u1',effectiveTenantId:7})",loader:'js'}))
    b.onResolve({ filter: /^@\/api\/(?:lingdoc\/|knowledge-base$)/ }, args => {
      const base = path.join(frontend, 'src', args.path.slice(2))
      return { path: [base + '.ts', path.join(base, 'index.ts')].find(p => fs.existsSync(p)) || base + '.ts', namespace: 'fixture-api' }
    })
    b.onLoad({ filter: /.*/, namespace: 'fixture-api' }, args => {
      const names = [...fs.readFileSync(args.path, 'utf8').matchAll(/^export\s+(?:(?:async\s+)?function|const)\s+(\w+)/gm)].map(m => m[1])
      return { contents: names.map(name => `export const ${name}=(...args)=>window.__uiApi('${name}',args);`).join('\n'), loader: 'js' }
    })
    // Fail closed if a newly imported helper reaches the real HTTP transport.
    b.onResolve({filter:/^@\/utils\/request$/},args=>{
      console.log('Blocked real HTTP transport in fixture: '+path.relative(frontend,args.importer))
      return {path:'blocked-transport',namespace:'fixture-denied-http'}
    })
    b.onLoad({filter:/.*/,namespace:'fixture-denied-http'},()=>({contents:
      "const denied=()=>{throw Error('Unexpected real HTTP transport in synthetic fixture')};"+
      ['get','getDown','postUpload','postChat','post','put','patch','del','forceReloginRedirect','refreshAccessTokenShared','getCurrentLanguage']
        .map(name=>'export const '+name+'=denied;').join(''),loader:'js'}))
    b.onResolve({ filter: /^@\// }, args => {
      const base = path.join(frontend, 'src', args.path.slice(2))
      return { path: [base, base + '.ts', base + '.vue'].find(p => fs.existsSync(p) && fs.statSync(p).isFile()) || base }
    })
    b.onLoad({ filter: /\.vue$/ }, args => {
      const raw = fs.readFileSync(args.path, 'utf8')
      sourceHashes[path.relative(frontend, args.path).replaceAll('\\', '/')] = createHash('sha256').update(raw).digest('hex')
      const { descriptor } = parse(raw, { filename: args.path })
      const id = 'fixture-' + createHash('sha256').update(args.path).digest('hex').slice(0, 8)
      const script = compileScript(descriptor, { id, inlineTemplate: true, fs: { fileExists: fs.existsSync, readFile: p => fs.readFileSync(p, 'utf8') } })
      // Test-only, read-only primitive snapshot. The on-disk product SFC stays unchanged.
      if (path.basename(args.path) === 'Workspace.vue') {
        if (!script.content.includes('setup(__props) {')) throw Error('Workspace fixture diagnostic setup marker missing')
        script.content = script.content.replace('setup(__props) {', 'setup(__props, {expose: __fixtureExpose}) { __fixtureExpose({conditionsSafetySnapshot:()=>({editorDirtyAny:editorDirtyAny.value,editorRefHasUnsaved:editorRef.value?.hasUnsaved??false,busy:busy.value,projectId:project.value?.id??null,chapterId:chapter.value?.id??null,editorSessionEpoch,navigationLocked:projectSwitchCommitting.value})});')
      }
      const css = descriptor.styles.map(style => compileStyle({ id: 'data-v-' + id, source: style.content, scoped: style.scoped }).code).join('\n')
      return { contents: script.content.replace('export default', 'const __component =') + `\n__component.__scopeId='data-v-${id}';\nconst style=document.createElement('style');style.textContent=${JSON.stringify(css)};document.head.append(style);export default __component;`, loader: 'ts', resolveDir: path.dirname(args.path) }
    })
  } }],
})
const js = bundle.outputFiles[0].text
if (process.env.UI_SAFETY_BUILD_ONLY === '1') {
  fs.writeFileSync(path.join(output, 'fixture.js'), js)
  fs.writeFileSync(path.join(output, 'fixture-build.json'), JSON.stringify({ bundled: true, sourceHashes, browserRun: false }, null, 2))
  console.log(JSON.stringify({ bundled: true, browserRun: false, evidence: output }))
  process.exit(0)
}
const profile = fs.mkdtempSync(path.join(output, 'edge-profile-'))
let ws, child, server, nextId = 0
const results = [], runtimeErrors = [], dialogs = [], diagnostics = [], pending = new Map()
const send = (method, params = {}) => new Promise((resolve, reject) => {
  const id = ++nextId, timer = setTimeout(() => { pending.delete(id); reject(Error('CDP timeout ' + method)) }, 12000)
  pending.set(id, { resolve, reject, timer }); ws.send(JSON.stringify({ id, method, params }))
})
const evaluate = async expression => {
  const r = await send('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true, userGesture: true })
  if (r.exceptionDetails) throw Error(r.exceptionDetails.exception?.description || r.exceptionDetails.text)
  return r.result.value
}
const waitFor = async expression => { for (let i = 0; i < 90; i++) { if (await evaluate(expression)) return; await delay(100) } throw Error('Fixture wait timed out: ' + expression) }
const check = async (name, expression) => { const pass = !!await evaluate(expression); results.push({ name, pass }); console.log((pass ? 'PASS ' : 'FAIL ') + name); if (!pass) throw Error('FAIL ' + name) }
const input = (selector, value) => evaluate(`(()=>{const el=document.querySelector(${JSON.stringify(selector)});if(!el)throw Error('Input missing');el.value=${JSON.stringify(value)};el.dispatchEvent(new Event('input',{bubbles:true}))})()`)
const clickText = (text, selector = 'button') => evaluate(`(()=>{const el=[...document.querySelectorAll(${JSON.stringify(selector)})].find(x=>x.textContent.trim().startsWith(${JSON.stringify(text)}));if(!el||el.disabled)throw Error('Button unavailable: '+${JSON.stringify(text)});el.click()})()`)
const dirtyEvent = `(()=>{const e=new Event('beforeunload',{cancelable:true});window.dispatchEvent(e);return e.defaultPrevented})()`
const pick = async id => {
  await clickText('切换课题', 'aside.panel button')
  await evaluate(`document.querySelector('.wb-picker-list button').closest('ul').querySelectorAll('button')[${id === 'p1' ? 0 : 1}].click()`)
}
const ready = () => waitFor("!!window.tiptap()&&tiptap().isEditable")
const diagnose = async (name, targetId = null) => {
  const serialized = await evaluate(`JSON.stringify((()=>{
    const ed=tiptap(),handle=editorHandle(),active=document.activeElement,domSelection=window.getSelection();
    let domPosition=null;try{if(ed&&domSelection?.anchorNode&&ed.view.dom.contains(domSelection.anchorNode))domPosition=ed.view.posAtDOM(domSelection.anchorNode,domSelection.anchorOffset)}catch{}
    return {workspace:workspaceDebug(),confirmCalls:controls.confirmCalls,getProject:controls.calls.getProject??0,saves:controls.calls.saveWorkingCopy??0,
      hasUnsaved:handle?.hasUnsaved?.value??false,viewSameCancel:!!ed&&ed.view===window.cancelView,
      editable:ed?.isEditable??false,hasFocus:ed?.view.hasFocus()??false,activeElement:{tag:active?.tagName??null,id:active?.id??null,contentEditable:active?.isContentEditable??false,insideEditor:!!ed&&!!active&&ed.view.dom.contains(active)},
      selection:ed?.state.selection.toJSON()??null,domPosition,targetPosition:ed&&${JSON.stringify(targetId)}?chapterPos(${JSON.stringify(targetId)}):null,
      chapters:['c1','c2','d1'].map(id=>({id,markdown:handle?.currentMarkdown(id)??null,queue:handle?.queue.state(id)??null}))};
  })())`)
  diagnostics.push({name,...JSON.parse(serialized)})
}
const typeChapter = async (id, text) => {
  await diagnose('native-input before focus', id)
  await evaluate(`tiptap().chain().focus().setTextSelection(chapterPos(${JSON.stringify(id)})).run()`)
  try {
    await waitFor(`(()=>{const ed=tiptap(),sel=window.getSelection(),pos=chapterPos(${JSON.stringify(id)});return ed.view.hasFocus()&&ed.view.dom.contains(document.activeElement)&&!!sel?.anchorNode&&ed.view.dom.contains(sel.anchorNode)&&ed.view.posAtDOM(sel.anchorNode,sel.anchorOffset)===pos&&ed.state.selection.from===pos})()`)
  } catch (error) {
    await diagnose('driver focus precondition failed', id)
    throw Error('Driver precondition failed: native focus/DOM selection did not reach target chapter '+id+'; '+error.message)
  }
  await diagnose('native-input focused target', id)
  await send('Input.insertText', { text })
  try { await waitFor(`editorHandle().currentMarkdown(${JSON.stringify(id)})?.includes(${JSON.stringify(text)})&&tiptap().getText().includes(${JSON.stringify(text)})`) }
  catch (error) { await diagnose('driver native insertion precondition failed', id); throw Error('Driver precondition failed: native text did not reach serialized chapter '+id+'; '+error.message) }
  await diagnose('native-input landed in target', id)
}
try {
  server = http.createServer((req, res) => { res.setHeader('content-type', req.url === '/fixture.js' ? 'text/javascript; charset=utf-8' : 'text/html; charset=utf-8'); res.end(req.url === '/fixture.js' ? js : '<!doctype html><meta charset="utf-8"><title>条件与连续编辑合成验证</title><div id="app"></div><script src="/fixture.js"></script>') })
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  child = spawn(edge, ['--headless=new', '--remote-debugging-port=0', `--user-data-dir=${profile}`, '--no-first-run', '--window-size=1440,1000', `http://127.0.0.1:${server.address().port}/`], { windowsHide: true, stdio: 'ignore' })
  let spawnError, port; child.on('error', error => { spawnError = error })
  for (let i = 0; i < 80; i++) { if (spawnError) throw spawnError; try { port = Number(fs.readFileSync(path.join(profile, 'DevToolsActivePort'), 'utf8').split(/\r?\n/)[0]); if (port) break } catch {} await delay(150) }
  if (!port) throw Error('Edge DevTools port missing')
  const targets = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json()
  ws = new WebSocket(targets.find(x => x.type === 'page').webSocketDebuggerUrl)
  await new Promise((resolve, reject) => { ws.addEventListener('open', resolve, { once: true }); ws.addEventListener('error', reject, { once: true }) })
  ws.addEventListener('message', event => { const m = JSON.parse(event.data); if (m.method === 'Runtime.exceptionThrown') runtimeErrors.push(m.params.exceptionDetails.exception?.description || m.params.exceptionDetails.text); if (m.method === 'Page.javascriptDialogOpening') dialogs.push(m.params.type); const job = pending.get(m.id); if (job) { clearTimeout(job.timer); pending.delete(m.id); m.error ? job.reject(Error(JSON.stringify(m.error))) : job.resolve(m.result) } })
  await send('Runtime.enable'); await send('Page.enable'); await waitFor('window.ready'); await pick('p1'); await ready(); await clickText('研究条件')
  await check('C01 real continuous editor starts with both chapters and clean unload state', "tiptap().getText().includes('第一章')&&tiptap().getText().includes('第二章')&&!" + dirtyEvent)
  await input('#subject', '改动主题一'); await input('#change-reason', '隐藏但保留的理由'); await input('#subject', '合成主题一')
  await check('C02 restoring spec hides auxiliary form but reason still guards unload', "!document.querySelector('#change-reason')&&" + dirtyEvent)
  await evaluate('window.confirm=()=>{controls.confirmCalls++;return false};fixtureRouter.push("/else")')
  const reads = await evaluate('controls.calls.getProject'), confirms = await evaluate('controls.confirmCalls')
  await pick('p1'); await clickText('重新读取'); await pick('p2')
  await check('C03 route, same project, reread and switch cancel without reads', `fixtureRouter.currentRoute.value.path==='/'&&controls.calls.getProject===${reads}&&controls.confirmCalls===${confirms + 3}`)
  await input('#subject', '改动主题一'); await check('C04 cancelled navigation retains exact auxiliary reason', "document.querySelector('#change-reason').value==='隐藏但保留的理由'")
  // Native reload requires a real user gesture and a real beforeunload dialog.
  await evaluate("document.querySelectorAll('.wb-rail-entries button')[0].click();document.querySelector('#subject').focus()")
  await send('Input.insertText', { text: '真实激活' })
  const nativeDialogs = dialogs.filter(x => x === 'beforeunload').length, reload = send('Page.reload')
  for (let i = 0; i < 70 && dialogs.filter(x => x === 'beforeunload').length === nativeDialogs; i++) await delay(50)
  if (dialogs.filter(x => x === 'beforeunload').length === nativeDialogs) throw Error('Native reload did not produce beforeunload dialog')
  await send('Page.handleJavaScriptDialog', { accept: false }); await reload
  await check('C05 native refresh cancellation retains exact auxiliary reason', "document.querySelector('#change-reason').value==='隐藏但保留的理由'")
  await evaluate('window.confirm=()=>true;window.originalView=tiptap().view;window.originalSelection=JSON.stringify(tiptap().state.selection.toJSON());void 0')
  for (const api of ['getProject', 'listChapters', 'listAssets', 'listChangeSets', 'getWorkingCopy', 'listChapterVersions']) {
    await evaluate(`controls.readFailure=${JSON.stringify(api)}`); await pick('p2'); await waitFor(`document.body.textContent.includes(${JSON.stringify('合成初读失败：' + api)})`)
    await check('C06 failed initial ' + api + ' preserves same editable EditorView and selection', "tiptap().view===originalView&&tiptap().isEditable&&JSON.stringify(tiptap().state.selection.toJSON())===originalSelection&&document.querySelector('#change-reason').value==='隐藏但保留的理由'")
  }
  await evaluate("controls.readFailure=''"); await clickText('重新读取'); await ready(); await waitFor("document.querySelector('#subject').value==='合成主题一'")
  await input('#subject', '只变范围'); await evaluate("document.querySelector('.chapter-impact-choice input[value=\"c2\"]').click()"); await input('#subject', '合成主题一')
  await check('C07 chapter selection alone guards hidden form', dirtyEvent)
  await pick('p2'); await ready(); await input('#subject', '项目二新主题')
  await check('C08 successful switch clears prior reason and all foreign chapter IDs', "document.querySelector('#change-reason').value===''&&!document.querySelector('.chapter-impact-choice input[value=\"d1\"]').checked")
  await input('#change-reason', '第二项目理由'); await evaluate("document.querySelector('.chapter-impact-choice input[value=\"d1\"]').click()"); await clickText('生成变更预览'); await waitFor("!!document.querySelector('.change-set-preview')")
  await check('C09 preview submits only target project chapter IDs', "controls.lastConditionsInput.reason==='第二项目理由'&&JSON.stringify(controls.lastConditionsInput.affected_chapter_ids)==='[\"d1\"]'")
  await input('#subject', '合成主题二'); await check('C10 acknowledged auxiliary snapshot is clean after restoring spec', '!' + dirtyEvent)
  await pick('p1'); await ready(); await input('#subject', '提交中的主题'); await input('#change-reason', '提交中的理由'); await evaluate("document.querySelector('.chapter-impact-choice input[value=\"c1\"]').click();controls.pendingPreview=true")
  await clickText('生成变更预览'); await waitFor('!!controls.resolvePreview'); await input('#subject', '请求后又改的主题'); await input('#change-reason', '请求后又改的理由')
  await evaluate('controls.resolvePreview();controls.pendingPreview=false'); await waitFor("!!document.querySelector('.change-set-preview')")
  await check('C11 pending preview acknowledges submitted snapshot only', "[...document.querySelectorAll('button')].find(b=>b.textContent.includes('应用预览并开始复核')).disabled&&document.querySelector('#change-reason').value==='请求后又改的理由'&&" + dirtyEvent)
  await evaluate('window.confirm=()=>true'); await clickText('重新读取'); await ready()
  await input('#subject', '预览失败主题'); await input('#change-reason', '失败理由'); await evaluate("document.querySelector('.chapter-impact-choice input[value=\"c1\"]').click();controls.previewFailure=true")
  await clickText('生成变更预览'); await waitFor("document.body.textContent.includes('合成条件预览失败')"); await input('#subject', '合成主题一')
  await check('C12 failed preview keeps auxiliary inputs dirty', dirtyEvent)
  await input('#subject', '要应用的主题'); await evaluate('controls.previewFailure=false'); await clickText('生成变更预览'); await waitFor("!!document.querySelector('.change-set-preview')")
  await evaluate('controls.applyFailure=true'); await clickText('应用预览并开始复核'); await waitFor("document.body.textContent.includes('合成条件应用失败')")
  await check('C13 failed apply retains retryable preview and reason', "!!document.querySelector('.change-set-preview')&&document.querySelector('#change-reason').value==='失败理由'")
  await evaluate('controls.applyFailure=false;window.confirm=()=>{controls.confirmCalls++;return false}')
  const cleanApplyConfirms = await evaluate('controls.confirmCalls')
  await clickText('应用预览并开始复核'); await waitFor("document.body.textContent.includes('已应用')&&!document.querySelector('#change-reason')"); await ready()
  await check('C14 clean successful apply avoids stale discard warning', `controls.confirmCalls===${cleanApplyConfirms}&&!` + dirtyEvent)
  await input('#subject', '保留非当前章的主题'); await input('#change-reason', '并行正文理由'); await evaluate("document.querySelector('.chapter-impact-choice input[value=\"c1\"]').click()"); await clickText('生成变更预览'); await waitFor("!!document.querySelector('.change-set-preview')")
  await evaluate('controls.pendingSave=true;window.cancelView=tiptap().view;void 0'); await typeChapter('c2', '非当前章未保存输入。'); await diagnose('C15 before switching back', 'c2'); await clickText('研究方案', '.wb-chapters button'); await diagnose('C15 after switching back', 'c2')
  const cancelReads = await evaluate('controls.calls.getProject'), cancelConfirms = await evaluate('controls.confirmCalls')
  await diagnose('C15 before apply', 'c2')
  await clickText('应用预览并开始复核'); await waitFor("document.body.textContent.includes('已应用')&&!document.querySelector('.change-set-preview')")
  await diagnose('C15 after apply', 'c2')
  await check('C15 handled conditions still guard non-current chapter and preserve EditorView on cancel', `controls.confirmCalls===${cancelConfirms + 1}&&controls.calls.getProject===${cancelReads}&&tiptap().view===cancelView&&tiptap().getText().includes('非当前章未保存输入')&&` + dirtyEvent)
  await waitFor('controls.resolveSaves.length>0'); await check('C16 real non-current chapter save in flight guards unload', dirtyEvent)
  await evaluate('controls.resolveSaves.shift().release(false)'); await waitFor("document.body.textContent.includes('保存失败')")
  await check('C17 failed continuous-editor save retains all chapter input', "tiptap().getText().includes('非当前章未保存输入')&&tiptap().isEditable&&" + dirtyEvent)
  await evaluate('controls.pendingSave=false;window.confirm=()=>true'); await clickText('重新读取'); await ready()
  // GET pending: live editor remains writable and cancellation after new input never pauses it.
  await evaluate('controls.pendingProject=true;window.pendingView=tiptap().view;void 0'); await pick('p2'); await waitFor('controls.resolveProjects.length>0')
  await typeChapter('c1', '读取中新增输入。'); await evaluate('window.confirm=()=>false;controls.resolveProjects.shift().release(true);controls.pendingProject=false')
  await delay(250)
  await check('C18 new input during target GET prompts and cancellation retains same usable EditorView', "tiptap().view===pendingView&&tiptap().isEditable&&tiptap().getText().includes('读取中新增输入')&&document.querySelector('#subject').value==='保留非当前章的主题'")
  await evaluate('window.confirm=()=>true'); await clickText('重新读取'); await ready()
  // Staged GET sees the old revision, then navigation drains save A before installing it.
  await evaluate('controls.pendingSave=true;window.casView=tiptap().view;void 0'); await typeChapter('c1', 'CAS排空回执正文。'); await waitFor('controls.resolveSaves.length>0')
  await input('#subject', '临界段待预览主题'); await input('#change-reason', '临界段理由'); await evaluate("document.querySelector('.chapter-impact-choice input[value=\"c1\"]').click();controls.pendingPreview=true")
  await clickText('生成变更预览'); await waitFor('!!controls.resolvePreview')
  const stagedRevision = await evaluate("controls.requests.filter(r=>r.name==='saveWorkingCopy').at(-1).args[2].expected_working_copy_revision")
  await clickText('重新读取'); await waitFor('!tiptap().isEditable')
  await evaluate('controls.resolvePreview();controls.pendingPreview=false'); await delay(100)
  await check('C18a late preview cannot unlock input while navigation drains save', "document.querySelector('main').inert===true&&document.querySelector('#subject').disabled&&tiptap().isEditable===false")
  await evaluate('controls.resolveSaves.shift().release(true)'); await ready()
  await check('C18b drained save receipt overrides older staged working-copy body', "tiptap().view!==casView&&tiptap().getText().includes('CAS排空回执正文')")
  await evaluate('controls.pendingSave=false'); await typeChapter('c1', 'CAS下一次写入。'); await evaluate('editorHandle().flushSave()')
  await check('C18c remounted queue uses the acknowledged monotonic CAS revision', `controls.requests.filter(r=>r.name==='saveWorkingCopy').at(-1).args[2].expected_working_copy_revision===${stagedRevision + 1}&&tiptap().getText().includes('CAS排空回执正文')&&tiptap().getText().includes('CAS下一次写入')&&!` + dirtyEvent)
  // A→B→A preview response cannot acknowledge or busy-unlock the new session.
  await input('#subject', '迟预览主题'); await input('#change-reason', '迟预览理由'); await evaluate("document.querySelector('.chapter-impact-choice input[value=\"c1\"]').click();controls.pendingPreview=true")
  await clickText('生成变更预览'); await waitFor('!!controls.resolvePreview'); await pick('p2'); await ready(); await pick('p1'); await ready()
  await evaluate('controls.resolvePreview();controls.pendingPreview=false'); await delay(200)
  await check('C19 A to B to A late preview does not restore old draft or acknowledgement', "!document.querySelector('.change-set-preview')&&!document.querySelector('#change-reason')&&!" + dirtyEvent)
  await input('#subject', '要驳回的主题'); await input('#change-reason', '驳回理由'); await evaluate("document.querySelector('.chapter-impact-choice input[value=\"c1\"]').click()"); await clickText('生成变更预览'); await waitFor("!!document.querySelector('.change-set-preview')")
  await evaluate('window.confirm=()=>{controls.confirmCalls++;return false}')
  const rejectConfirms = await evaluate('controls.confirmCalls')
  await clickText('驳回预览'); await waitFor("document.body.textContent.includes('已驳回')&&!document.querySelector('#change-reason')"); await ready()
  await check('C20 successful reject releases only handled conditions', `controls.confirmCalls===${rejectConfirms}&&!` + dirtyEvent)
  await evaluate('window.confirm=()=>true'); await pick('p2'); await ready()
  await check('C21 project switch clears prior applied/rejected receipt', "!document.body.textContent.includes('已驳回')&&!document.body.textContent.includes('已应用')")
  results.push({ name: 'C22 no browser runtime exceptions', pass: runtimeErrors.length === 0 })
  if (runtimeErrors.length) throw Error('Browser runtime errors: ' + runtimeErrors.join('\n'))
} catch (error) {
  let page; try { page = await evaluate('document.body.innerText') } catch {}
  results.push({ name: 'runner completion', pass: false, error: error.stack, page })
} finally {
  fs.writeFileSync(path.join(output, 'conditions-browser-result.json'), JSON.stringify({ pass: results.length > 0 && results.every(x => x.pass), mode: 'actual Vue SFC and Tiptap; synthetic APIs; no product/backend/model', sourceHashes, runtimeErrors, dialogs, diagnostics, results }, null, 2))
  if (ws?.readyState === WebSocket.OPEN) { try { await send('Browser.close') } catch {} ws.close() }
  if (child?.pid && child.exitCode === null) { await delay(300); if (child.exitCode === null) await new Promise(resolve => { const stop = spawn('taskkill.exe', ['/PID', String(child.pid), '/T', '/F'], { windowsHide: true, stdio: 'ignore' }); stop.once('exit', resolve); stop.once('error', resolve) }) }
  server?.close(); for (const job of pending.values()) clearTimeout(job.timer)
}
console.log(JSON.stringify({ pass: results.length > 0 && results.every(x => x.pass), checks: results.length, evidence: output, failed: results.filter(x => !x.pass).map(x => x.name) }))
if (!results.length || results.some(x => !x.pass)) process.exitCode = 1
