// 方案一三栏工作台布局回归（合成 API；真实 SFC 编译挂载；不连产品后台）。
// 用法：cd frontend && $env:WB_LAYOUT_EVIDENCE='D:/Mon3tr/projects/lingdoc-dachuang/acceptance/real-workbench-20261012/browser' ; node tests/workbench-layout.browser.mjs
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
const output = process.env.WB_LAYOUT_EVIDENCE
if (!output) throw new Error('Set WB_LAYOUT_EVIDENCE to an isolated local evidence directory')
fs.mkdirSync(output, { recursive: true })
const profile = fs.mkdtempSync(path.join(output, 'edge-profile-'))
const edge = process.env.UI_SAFETY_BROWSER || 'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe'
const sourceHashes = {}
const bundle = await build({
  absWorkingDir: frontend, stdin: { contents: `
    import {createApp, h, ref} from 'vue';
    import {createRouter, createMemoryHistory, RouterView} from 'vue-router';
    import Workspace from '@/views/lingdoc/Workspace.vue';
    const project = {id:'p1', name:'合成工作台项目', status:'active', project_version:1,
      spec_revision:1, current_context_revision:1, spec:{research_subject:'合成主题', research_goal:'合成目标'},
      template_id:'template',template_version:'1',members:[{id:'u1'}]};
    const ch1 = {id:'c1',project_id:'p1',section_id:'s1',title:'研究方案',current_version_id:'v1',
      body_markdown:'第一章合成正式正文。',source_ids:['src-1'],citation_usages:[],confirmation_valid:false,
      review_items:[{id:'r1',statement:'合成待核一'},{id:'r2',statement:'合成待核二'}]};
    const ch2 = {id:'c2',project_id:'p1',section_id:'s2',title:'研究问题',current_version_id:'v2',
      body_markdown:'第二章合成正式正文。',source_ids:[],citation_usages:[],confirmation_valid:false,review_items:[]};
    const controls = window.controls = {calls:{},emitted:null,resolveSave:null,resolveProject:null};
    const envelope = data => ({data:structuredClone(data),meta:{replayed:false,refresh_required:false}});
    let saved = {c1:{...ch1,chapter_id:'c1',base_chapter_version_id:'v1',spec_revision:1,working_copy_revision:1,body_markdown:'第一章合成正式正文。'},
                 c2:{...ch2,chapter_id:'c2',base_chapter_version_id:'v2',spec_revision:1,working_copy_revision:1,body_markdown:'第二章合成正式正文。'}};
    let rewrite = null;
    window.__wbApi = async (name,args) => {
      controls.calls[name] = (controls.calls[name]||0)+1;
      if(name.startsWith('new')) return crypto.randomUUID();
      switch(name){
        case 'listProjects': return envelope({items:[project],truncated:false});
        case 'getProject': return envelope(project);
        case 'listChapters': return envelope([ch1,ch2]);
        case 'getWorkingCopy': return envelope(saved[args[1]]);
        case 'listChapterVersions': return envelope({items:[],truncated:false});
        case 'listAssets': return envelope([{id:'a1',knowledge_id:'k1',title:'合成资料',asset_revision:1,processing_state:'ready'}]);
        case 'listChangeSets': return envelope([]);
        case 'listGenerationCandidates': return envelope([]);
        case 'getAccessStatus': return envelope({project_id:'p1',status:'available',recovery_actions:[]});
        case 'listReleases': case 'listExports': return envelope({items:[],truncated:false});
        case 'saveWorkingCopy': { const s=saved[args[1]]; Object.assign(s,args[2]); s.working_copy_revision++; return envelope(s); }
        case 'retrieveSources': return envelope([{id:'src-1',locator:'合成来源',status:'available',quoted_text:'合成引文',context_available:true}]);
        case 'getSourceContext': return envelope({context_available:true,window:{before:1,after:1},rows:[],reason:''});
        case 'createSelectedRewrite':
          rewrite={candidate_id:'rw1',status:'ready',run_mode:'mock',spec_revision:1,working_copy_revision:saved[args[1]].working_copy_revision,
            base_chapter_version_id:saved[args[1]].base_chapter_version_id,
            selection:{selected_text:args[2].selection.selected_text},replacement_markdown:'合成改写候选正文。',
            source_ids:[],review_items:[],error_code:''};
          return envelope(rewrite);
        case 'getSelectedRewrite': return envelope(rewrite);
        case 'applySelectedRewrite': { const s=saved[args[1]]; s.body_markdown=rewrite.replacement_markdown; s.working_copy_revision++; return envelope(s); }
        case 'confirmChapter': return envelope({});
        default: throw new Error('Unexpected synthetic API call: '+name);
      }
    };
    const router=window.fixtureRouter=createRouter({history:createMemoryHistory(),routes:[{path:'/',component:Workspace}]});
    const app=createApp({render:()=>h(RouterView)});
    app.use(router); router.push('/').then(()=>router.isReady()).then(()=>{app.mount('#app');window.ready=true});
  `, resolveDir: frontend, loader:'ts' }, bundle:true, write:false, format:'iife', target:'es2022',
  define: {'process.env.NODE_ENV':'"development"',__VUE_OPTIONS_API__:'true',__VUE_PROD_DEVTOOLS__:'false',__VUE_PROD_HYDRATION_MISMATCH_DETAILS__:'false'},
  plugins:[{name:'sfc-synthetic-api',setup(b){
    b.onResolve({filter:/^@\/api\/lingdoc\//},args=>({path:path.join(frontend,'src',args.path.slice(2))+'.ts',namespace:'fixture-api'}));
    b.onLoad({filter:/.*/,namespace:'fixture-api'},args=>{
      const names=[...fs.readFileSync(args.path,'utf8').matchAll(/^export\s+(?:(?:async\s+)?function|const)\s+(\w+)/gm)].map(m=>m[1]);
      return {contents:names.map(name=>`export const ${name}=(...args)=>window.__wbApi('${name}',args);`).join('\n'),loader:'js'};
    });
    b.onResolve({filter:/^@\//},args=>{
      const base=path.join(frontend,'src',args.path.slice(2));
      return {path:[base,base+'.ts',base+'.vue'].find(p=>fs.existsSync(p)&&fs.statSync(p).isFile())||base};
    });
    b.onLoad({filter:/\.vue$/},args=>{
      const raw=fs.readFileSync(args.path,'utf8');
      sourceHashes[path.relative(frontend,args.path).replaceAll('\\\\','/')]=createHash('sha256').update(raw).digest('hex');
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
const results=[], runtimeErrors=[]
let nextId=0
const pending=new Map()
const send=(method,params={})=>new Promise((resolve,reject)=>{
  const id=++nextId, timer=setTimeout(()=>{pending.delete(id);reject(new Error('CDP timeout '+method))},12000);
  pending.set(id,{resolve,reject,timer});ws.send(JSON.stringify({id,method,params}));
})
const evaluate=async expression=>{
  const r=await send('Runtime.evaluate',{expression,returnByValue:true,awaitPromise:true,userGesture:true});
  if(r.exceptionDetails) throw new Error(r.exceptionDetails.exception?.description||r.exceptionDetails.text);
  return r.result.value;
}
const waitFor=async expression=>{for(let i=0;i<70;i++){if(await evaluate(expression))return;await delay(100)}throw new Error('Fixture wait timed out: '+expression)}
const check=async(name,expression)=>{const pass=!!await evaluate(expression);results.push({name,pass});if(!pass)throw new Error('FAIL '+name);console.log('PASS '+name)}
const shot=async name=>{const r=await send('Page.captureScreenshot',{format:'png'});fs.writeFileSync(path.join(output,name+'.png'),Buffer.from(r.data,'base64'))}
// 指针命中断言：元素中心必须命中自身（不被同位面板/浮层遮挡）
const hit=async(name,selector)=>{
  const findExpr=selector.startsWith('expr:')?`(${selector.slice(5)})`:`document.querySelector(${JSON.stringify(selector)})`;
  const expr=`(()=>{const e=${findExpr};if(!e)return null;e.scrollIntoView({block:'nearest'});const r=e.getBoundingClientRect();const hitEl=document.elementFromPoint(r.x+r.width/2,r.y+r.height/2);const chain=[];let p=e;while(p&&chain.length<6){chain.push((p.className&&String(p.className).split(' ')[0])||p.tagName);p=p.parentElement}const vis=chain.map(c=>{const el=[...document.querySelectorAll('*')].find(x=>(x.id===c)||(x.tagName===c)||(x.className&&String(x.className).split(' ')[0]===c));return el?getComputedStyle(el).display:'?'});return {ok:!!(hitEl&&(hitEl===e||e.contains(hitEl))),hit:hitEl?String(hitEl.className||hitEl.id||hitEl.tagName).slice(0,40):'',rect:[Math.round(r.x),Math.round(r.y),Math.round(r.width),Math.round(r.height)],chain:chain.join('<'),vis:vis.join(',')}})()`;
  const p=await evaluate(expr);
  if(!p)throw new Error('hit: no element '+selector);
  results.push({name:'hit '+name,pass:p.ok,info:selector+' -> '+p.hit+' rect='+JSON.stringify(p.rect)+' chain='+p.chain+' vis='+p.vis});
  console.log((p.ok?'PASS ':'FAIL ')+'hit '+name+' -> '+p.hit);
  if(!p.ok)throw new Error('HIT FAILED: '+selector+' occluded by '+p.hit);
}
try {
  server=http.createServer((req,res)=>{if(req.url==='/fixture.js'){res.setHeader('content-type','text/javascript; charset=utf-8');res.end(js)}else{res.setHeader('content-type','text/html; charset=utf-8');res.end('<!doctype html><meta charset="utf-8"><title>工作台布局合成验证</title><div id="app"></div><script src="/fixture.js"></script>')}});
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
  const url=`http://127.0.0.1:${server.address().port}/`;
  child=spawn(edge,['--headless=new','--remote-debugging-port=0',`--user-data-dir=${profile}`,'--no-first-run','--window-size=1440,1000',url],{windowsHide:true,stdio:'ignore'});
  let port;for(let i=0;i<70;i++){try{port=Number(fs.readFileSync(path.join(profile,'DevToolsActivePort'),'utf8').split(/\r?\n/)[0]);if(port)break}catch{}await delay(150)}
  if(!port)throw Error('Edge DevTools port missing');
  const targets=await(await fetch(`http://127.0.0.1:${port}/json/list`)).json();
  ws=new WebSocket(targets.find(x=>x.type==='page').webSocketDebuggerUrl);
  await new Promise((resolve,reject)=>{ws.addEventListener('open',resolve,{once:true});ws.addEventListener('error',reject,{once:true})});
  ws.addEventListener('message',event=>{const m=JSON.parse(event.data);if(m.method==='Runtime.exceptionThrown')runtimeErrors.push(m.params.exceptionDetails.text);const job=pending.get(m.id);if(job){clearTimeout(job.timer);pending.delete(m.id);m.error?job.reject(Error(JSON.stringify(m.error))):job.resolve(m.result)}});
  await send('Runtime.enable');await send('Page.enable');
  await send('Emulation.setDeviceMetricsOverride',{width:1440,height:1000,deviceScaleFactor:1,mobile:false});
  await waitFor("window.ready&&document.body.textContent.includes('合成工作台项目')");
  await evaluate("[...document.querySelectorAll('.project-list button')].find(b=>b.textContent.includes('合成工作台项目')).click()");
  await waitFor("!!document.querySelector('#chapter-body')");
  await check('L1 三栏外壳：章节目录两章、页签三枚、待核徽标为 2',"document.querySelectorAll('.wb-chapters > button').length===2&&document.querySelectorAll('.wb-tabs [role=tab]').length===3&&document.querySelector('.wb-tab-count').textContent==='2'");
  await check('L2 默认来源页签：单滚动容器内同时含绑定与检索',"getComputedStyle(document.querySelector('.wb-pane-sources')).display==='block'&&!!document.querySelector('.wb-pane-sources .assets')&&!!document.querySelector('.wb-pane-sources .sources')");
  await evaluate("document.querySelector('.wb-tabs [role=tab]:nth-child(2)').click()");
  await check('L3 AI 页签显示生成候选稿面板',"getComputedStyle(document.querySelector('.generation')).display==='block'");
  await evaluate("document.querySelector('.wb-tabs [role=tab]:nth-child(3)').click()");
  await check('L4 待核页签：单滚动容器内含逐项处置（两条）',"getComputedStyle(document.querySelector('.wb-pane-checks')).display==='block'&&document.querySelectorAll('.wb-pane-checks .review-item').length===2");
  // R1 命中回归：同页签控件中心不被遮挡（真实指针可达，1440）
  await evaluate("document.querySelector('.wb-tabs [role=tab]:nth-child(1)').click()");
  await delay(200);
  await hit('绑定输入框 #knowledge-id', '#knowledge-id');
  await hit('绑定提交按钮', '.asset-bind-form button[type="submit"]');
  await hit('检索输入框', '#source-query');
  await hit('检索提交按钮', '.source-search-form button[type="submit"]');
  await evaluate("document.querySelector('.wb-tabs [role=tab]:nth-child(3)').click()");
  await delay(200);
  await hit('待核处理结果选择框', '.wb-pane-checks .review-item select');
  await hit('待核理由输入框', '.wb-pane-checks .review-item textarea');
  await evaluate("document.querySelector('.wb-tabs [role=tab]:nth-child(1)').click()");
  await delay(150);
  await hit('文档列确认按钮', 'expr:[...document.querySelectorAll(".chapters button")].find(b=>b.textContent.includes("确认当前章节版本"))');
  await evaluate("document.querySelector('.wb-tabs [role=tab]:nth-child(1)').click()");
  await evaluate("(()=>{const ed=document.querySelector('#chapter-body');ed.value='未保存布局输入';ed.dispatchEvent(new Event('input',{bubbles:true}));})()");
  for(const n of [2,3,1]){await evaluate(`document.querySelector('.wb-tabs [role=tab]:nth-child(${n})').click()`);await delay(80)}
  await check('L5 切页签往返不丢正文输入',"document.querySelector('#chapter-body').value==='未保存布局输入'");
  await evaluate("document.querySelectorAll('.wb-rail-entries button')[0].click()");
  const l6dbg = await evaluate("(()=>{const m=document.querySelector('main.lingdoc-workspace');const el=document.querySelector('.spec-form');const cs=getComputedStyle(el);const r=el.getBoundingClientRect();return JSON.stringify({cls:m.className,vis:cs.visibility,transform:cs.transform,right:r.right,iw:innerWidth})})()");
  results.push({ name: 'L6-debug ' + l6dbg, pass: true });
  await delay(300);
  await check('L6 研究条件抽屉打开（spec-form 可见且在视口内）',"(()=>{const el=document.querySelector('.spec-form');const r=el.getBoundingClientRect();return getComputedStyle(el).visibility==='visible'&&r.right<=innerWidth&&r.left<innerWidth})()");
  await shot('L6-conditions-drawer');
  await delay(250);
  await evaluate("document.querySelector('.spec-form .wb-drawer-close').click()");
  await check('L7 研究条件抽屉关闭',"getComputedStyle(document.querySelector('.spec-form')).visibility==='hidden'");
  await delay(250);
  await evaluate("document.querySelectorAll('.wb-rail-entries button')[1].click()");
  await delay(300);
  await check('L8 模板抽屉打开',"getComputedStyle(document.querySelector('.template-copy-panel')).visibility==='visible'");
  await evaluate("document.querySelector('.template-copy-panel .wb-drawer-close').click()");
  await delay(250);
  await evaluate("document.querySelector('.wb-top-actions .primary').click()");
  await delay(300);
  await check('L9 交付抽屉打开且含检查按钮',"getComputedStyle(document.querySelector('.wb-delivery')).visibility==='visible'&&!!document.querySelector('#dlv-check, .delivery button')");
  await shot('L9-delivery-drawer');
  await send('Input.dispatchKeyEvent',{type:'keyDown',key:'Escape',code:'Escape',windowsVirtualKeyCode:27});
  await send('Input.dispatchKeyEvent',{type:'keyUp',key:'Escape',code:'Escape',windowsVirtualKeyCode:27});
  await delay(300);
  await check('L10 Esc 关闭交付抽屉',"getComputedStyle(document.querySelector('.wb-delivery')).visibility==='hidden'");
  await evaluate("document.querySelectorAll('.wb-mode button')[1].click()");
  await check('L11 专注模式隐藏检查器页签',"getComputedStyle(document.querySelector('.wb-tabs')).display==='none'");
  await evaluate("document.querySelectorAll('.wb-mode button')[0].click()");
  await check('L12 返回研究模式恢复检查器',"getComputedStyle(document.querySelector('.wb-tabs')).display==='flex'");
  // 章节切换守卫（confirm 在 headless 下会阻塞 JS，预先置真并记录调用）
  await evaluate("window.confirm=()=>{window.__confirmCalled=true;return true}");
  await evaluate("(()=>{const ed=document.querySelector('#chapter-body');ed.value='未保存切换输入';ed.dispatchEvent(new Event('input',{bubbles:true}));})()");
  await delay(60); // 防抖窗口内切章
  await evaluate("document.querySelectorAll('.wb-chapters > button')[1].click()");
  await check('L13 切到第二章触发未保存守卫并切换',"window.__confirmCalled===true&&document.querySelector('#chapter-body').value.includes('第二章')");
  await evaluate("document.querySelectorAll('.wb-chapters > button')[0].click()");
  await check('L14 切回第一章：更早输入已被自动保存，仅守卫丢弃防抖窗口内输入',"document.querySelector('#chapter-body').value==='未保存布局输入'");
  // 候选宽对照：生成→展开→返回→重开→采纳
  await delay(200);
  await evaluate("(()=>{const ed=document.querySelector('#chapter-body');ed.focus();ed.setSelectionRange(0,4);})()");
  await evaluate("document.querySelector('.wb-tabs [role=tab]:nth-child(2)').click()");
  await evaluate("[...document.querySelectorAll('button')].find(b=>b.textContent.includes('改写选中段落')).click()");
  await delay(500);
  results.push({ name: 'L15-debug ' + (await evaluate("JSON.stringify({dot:document.querySelector('.save-dot')?.className,rewriteDisabled:[...document.querySelectorAll('button')].find(b=>b.textContent.includes('改写选中段落'))?.disabled,commitDisabled:[...document.querySelectorAll('button')].find(b=>b.textContent.trim()==='提交为正式版本')?.disabled,searchDisabled:[...document.querySelectorAll('button')].find(b=>b.textContent.trim()==='检索')?.disabled,calls:window.controls.calls})")), pass: true });
  await waitFor("document.body.textContent.includes('候选状态：ready')");
  await evaluate("[...document.querySelectorAll('button')].find(b=>b.textContent.trim()==='展开宽对照').click()");
  await check('L15 宽对照打开（双栏 + 底栏）',"!!document.querySelector('.wb-compare')&&getComputedStyle(document.querySelector('.wb-compare')).display==='grid'");
  await shot('L15-wide-compare');
  await evaluate("[...document.querySelectorAll('.wb-compare-bar button')].find(b=>b.textContent.includes('返回编辑位置')).click()");
  await check('L16 返回编辑位置后候选仍 ready 且宽对照关闭',"!document.querySelector('.wb-compare')&&document.body.textContent.includes('候选状态：ready')");
  await evaluate("[...document.querySelectorAll('button')].find(b=>b.textContent.trim()==='展开宽对照').click()");
  await evaluate("[...document.querySelectorAll('.wb-compare-bar button')].find(b=>b.textContent.includes('采纳选段改写')).click()");
  await delay(300);
  await check('L17 宽对照重开后采纳成功（正文替换、对照关闭）',"!document.querySelector('.wb-compare')&&document.querySelector('#chapter-body').value.includes('合成改写候选正文')");
  // 响应式
  await send('Emulation.setDeviceMetricsOverride',{width:390,height:844,deviceScaleFactor:1,mobile:false});
  await delay(250);
  results.push({ name: 'L18-debug ' + (await evaluate("JSON.stringify({sw:document.documentElement.scrollWidth,iw:innerWidth,bw:document.body.scrollWidth,railVis:getComputedStyle(document.querySelector('.workspace-grid>aside.panel')).visibility,toggle:!!document.querySelector('.wb-rail-toggle')})")), pass: true });
  await check('L18 390 无横向溢出且目录默认收起',"document.documentElement.scrollWidth<=innerWidth+1&&document.querySelector('.wb-rail-toggle')&&getComputedStyle(document.querySelector('.workspace-grid>aside.panel')).visibility==='hidden'");
  // R1 命中回归（390px：来源绑定/检索/待核控件真实指针可达）
  await evaluate("document.querySelector('.wb-tabs [role=tab]:nth-child(1)').click()");
  await delay(250);
  await hit('390 绑定输入框', '#knowledge-id');
  await hit('390 检索提交按钮', '.source-search-form button[type="submit"]');
  await evaluate("document.querySelector('.wb-tabs [role=tab]:nth-child(3)').click()");
  await delay(200);
  await hit('390 待核理由输入框', '.wb-pane-checks .review-item textarea');
  await evaluate("document.querySelector('.wb-tabs [role=tab]:nth-child(1)').click()");
  await delay(150);
  await evaluate("document.querySelector('.wb-rail-toggle').click()");
  await check('L19 目录按钮打开项目浮层',"getComputedStyle(document.querySelector('.workspace-grid>aside.panel')).visibility==='visible'");
  await shot('L19-390-rail');
  results.push({name:'L20 全程无运行时异常',pass:runtimeErrors.length===0});
  console.log((runtimeErrors.length===0?'PASS ':'FAIL ')+'L20 全程无运行时异常');
} catch(error) {
  let page;try{page=await evaluate('document.body.innerText')}catch{}
  results.push({name:'runner completion',pass:false,error:error.stack,page});
} finally {
  fs.writeFileSync(path.join(output,'layout-result.json'),JSON.stringify({pass:results.length>0&&results.every(x=>x.pass),mode:'actual Vue SFC via esbuild; synthetic API; layout only',sourceHashes,runtimeErrors,results},null,2));
  if(ws?.readyState===WebSocket.OPEN){try{await send('Browser.close')}catch{}ws.close()}
  if(child?.pid&&child.exitCode===null){await delay(300);if(child.exitCode===null)await new Promise(resolve=>{const stop=spawn('taskkill.exe',['/PID',String(child.pid),'/T','/F'],{windowsHide:true,stdio:'ignore'});stop.once('exit',resolve);stop.once('error',resolve)})}
  server?.close();for(const job of pending.values())clearTimeout(job.timer);
}
console.log(JSON.stringify({pass:results.every(x=>x.pass),checks:results.length,evidence:output,failed:results.filter(x=>!x.pass).map(x=>x.name)}));
if(results.some(x=>!x.pass))process.exitCode=1;
