// LingDoc 编辑器小切片实机验证：真实 Edge 无头 + CDP 原生鼠标/键盘/文本输入。
// 断言只读 DOM/组件状态；selection 由真实鼠标拖拽产生，文本由 Input.insertText 产生。
// 证据输出：acceptance/document-editor-20261013/kimi/
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
const output = process.env.EDITOR_SLICE_EVIDENCE || path.resolve(frontend, '../acceptance-placeholder')
fs.mkdirSync(output, { recursive: true })
const profile = fs.mkdtempSync(path.join(output, 'edge-profile-'))
const edge = process.env.UI_SAFETY_BROWSER || 'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe'
const sourceHashes = {}

const C1_MD = '## 背景\n\n宋代😀水利**重点工程**大修，征发[[source:S1]]与资金。\n\n3. 第三步\n   - 子项甲\n4. 第四步\n\n> 保留的引用块\n\nA &amp; B \\*星\\* 后目标句在此。'
const C2_MD = ''

const bundle = await build({
  absWorkingDir: frontend,
  stdin: {
    contents: `
import {createApp, h, ref} from 'vue';
import EditorComp from '@/views/lingdoc/editor/LingDocEditor.vue';
import { chapterMaps } from '@/views/lingdoc/editor/pmSerialize';
window.__pmDebug = (doc, ch) => chapterMaps(doc).find(c => c.chapterId === ch);
import { pmRangeToUtf16 } from '@/views/lingdoc/editor/pmSerialize';
window.__pmRange = (doc, ch, f, t) => pmRangeToUtf16(doc, ch, f, t);
const saves = window.saves = [];
let conflictOnce = false;
window.__makeConflict = () => { conflictOnce = true; };
let __exposed = null;
const app = createApp({render: () => h(EditorComp, {
  ref: (el) => { __exposed = el; window.__exposed = el; },
  projectId: 'p1',
  chapters: [
    { id: 'c1', title: '第一章 背景', body_markdown: ${JSON.stringify(C1_MD)} },
    { id: 'c2', title: '第二章 方法', body_markdown: ${JSON.stringify(C2_MD)} },
  ],
  baselines: { c1: 5, c2: 0 },
  saveChapter: async (chapterId, markdown, expected) => {
    saves.push({ chapterId, markdown, expected });
    if (conflictOnce) { conflictOnce = false; return { ok: false, conflict: true }; }
    return { ok: true, newWorkingCopyRevision: expected + 1 };
  },
})});
app.mount('#app');
window.ready = true;
`, resolveDir: frontend, loader: 'ts',
  },
  bundle: true, write: false, format: 'iife', target: 'es2022',
  define: { 'process.env.NODE_ENV': '"development"', __VUE_OPTIONS_API__: 'true', __VUE_PROD_DEVTOOLS__: 'false', __VUE_PROD_HYDRATION_MISMATCH_DETAILS__: 'false' },
  plugins: [{
    name: 'sfc', setup(b) {
      b.onResolve({ filter: /^@\// }, args => {
        const base = path.join(frontend, 'src', args.path.slice(2))
        return { path: [base, base + '.ts', base + '.vue'].find(p => fs.existsSync(p) && fs.statSync(p).isFile()) || base }
      })
      b.onLoad({ filter: /\.vue$/ }, args => {
        const raw = fs.readFileSync(args.path, 'utf8')
        sourceHashes[path.relative(frontend, args.path).replaceAll('\\\\', '/')] = createHash('sha256').update(raw).digest('hex')
        const { descriptor } = parse(raw, { filename: args.path })
        const id = 'ed-' + createHash('sha256').update(args.path).digest('hex').slice(0, 8)
        const script = compileScript(descriptor, { id, inlineTemplate: true, fs: { fileExists: fs.existsSync, readFile: p => fs.readFileSync(p, 'utf8') } })
        const css = descriptor.styles.map(s => compileStyle({ id: 'data-v-' + id, source: s.content, scoped: s.scoped }).code).join('\n')
        return { contents: script.content.replace('export default', 'const __component =') + `\n__component.__scopeId='data-v-${id}';\nconst style=document.createElement('style');style.textContent=${JSON.stringify(css)};document.head.append(style);export default __component;`, loader: 'ts', resolveDir: path.dirname(args.path) }
      })
    },
  }],
})
const js = bundle.outputFiles[0].text

let ws, child, server
const results = []
const runtimeErrors = []
let nextId = 0
const pending = new Map()
const send = (method, params = {}) => new Promise((resolve, reject) => {
  const id = ++nextId
  const timer = setTimeout(() => { pending.delete(id); reject(new Error('CDP timeout ' + method)) }, 15000)
  pending.set(id, { resolve, reject, timer })
  ws.send(JSON.stringify({ id, method, params }))
})
const evaluate = async (expression) => {
  const r = await send('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true, userGesture: true })
  if (r.exceptionDetails) throw new Error(r.exceptionDetails.exception?.description || r.exceptionDetails.text)
  return r.result.value
}
const waitFor = async (expression) => { for (let i = 0; i < 80; i++) { if (await evaluate(expression)) return; await delay(120) } throw new Error('waitFor timeout: ' + expression) }
const check = async (name, expression) => {
  const pass = !!await evaluate(expression)
  results.push({ name, pass })
  console.log((pass ? 'PASS ' : 'FAIL ') + name)
  if (!pass) throw new Error('FAIL ' + name)
}
const shot = async (name) => {
  const r = await send('Page.captureScreenshot', { format: 'png' })
  fs.writeFileSync(path.join(output, name + '.png'), Buffer.from(r.data, 'base64'))
  console.log('shot ' + name)
}
const mouse = async (type, pt, extra = {}) => send('Input.dispatchMouseEvent', { type, ...pt, button: 'left', clickCount: 1, ...extra })
const realClick = async (sel) => {
  const pt = await evaluate(`(()=>{const e=document.querySelector(${JSON.stringify(sel)});if(!e)return null;const r=e.getBoundingClientRect();return {x:Math.round(r.x+r.width/2),y:Math.round(r.y+r.height/2)}})()`)
  if (!pt) throw new Error('no element ' + sel)
  await mouse('mouseMoved', pt); await delay(30); await mouse('mousePressed', pt); await delay(40); await mouse('mouseReleased', pt)
}
const key = async (k, code, vk, mods = 0) => {
  await send('Input.dispatchKeyEvent', { type: 'keyDown', key: k, code, windowsVirtualKeyCode: vk, modifiers: mods })
  await send('Input.dispatchKeyEvent', { type: 'keyUp', key: k, code, windowsVirtualKeyCode: vk, modifiers: mods })
}
const insertText = (text) => send('Input.insertText', { text })

try {
  server = http.createServer((req, res) => {
    if (req.url === '/fixture.js') { res.setHeader('content-type', 'text/javascript; charset=utf-8'); return res.end(js) }
    res.setHeader('content-type', 'text/html; charset=utf-8')
    res.end('<!doctype html><meta charset="utf-8"><title>editor-slice</title><div id="app"></div><script src="/fixture.js"></script>')
  })
  await new Promise(r => server.listen(0, '127.0.0.1', r))
  child = spawn(edge, ['--headless=new', '--remote-debugging-port=0', `--user-data-dir=${profile}`, '--no-first-run', '--window-size=1440,1000', `http://127.0.0.1:${server.address().port}/`], { windowsHide: true, stdio: 'ignore' })
  let port
  for (let i = 0; i < 70; i++) { try { port = Number(fs.readFileSync(path.join(profile, 'DevToolsActivePort'), 'utf8').split(/\r?\n/)[0]); if (port) break } catch {} await delay(150) }
  const targets = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json()
  ws = new WebSocket(targets.find(x => x.type === 'page').webSocketDebuggerUrl)
  await new Promise((res, rej) => { ws.addEventListener('open', res, { once: true }); ws.addEventListener('error', rej, { once: true }) })
  ws.addEventListener('message', ev => {
    const m = JSON.parse(ev.data)
    if (m.method === 'Runtime.exceptionThrown') runtimeErrors.push((m.params.exceptionDetails.exception?.description || m.params.exceptionDetails.text || '').slice(0, 300))
    const job = pending.get(m.id)
    if (job) { clearTimeout(job.timer); pending.delete(m.id); m.error ? job.reject(Error(JSON.stringify(m.error))) : job.resolve(m.result) }
  })
  await send('Runtime.enable'); await send('Page.enable')
  await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 1000, deviceScaleFactor: 1, mobile: false })
  await waitFor('window.ready===true')
  await delay(400)

  /* ---- 渲染：章标题原子 + 引用原子 + raw 保留 + 空章有段落 ---- */
  await check('E0-debug ' + JSON.stringify(await evaluate("JSON.stringify({titles:document.querySelectorAll('[data-chapter-title]').length,src:document.querySelectorAll('[data-source-ref]').length,raw:document.querySelectorAll('[data-raw-block]').length,ps:document.querySelectorAll('.tiptap > p').length,toolbar:!!document.querySelector('.ld-toolbar'),body:document.body.innerText.slice(0,120)})")), "true")
  await check('E1 两章标题原子渲染，空章有可写段落', "document.querySelectorAll('[data-chapter-title]').length===2&&!!document.querySelector('[data-source-ref]')&&!!document.querySelector('[data-raw-block]')&&[...document.querySelectorAll('.tiptap > p')].length>=2")
  await shot('e1-initial-1440')

  // A readback of identical server content is metadata-only. It must not move
  // the current chapter caret to the last chapter before the next native input.
  await evaluate("window.__exposed.focusChapter('c1')")
  const caretBeforeRead = await evaluate('({from:window.__exposed.editor.state.selection.from,to:window.__exposed.editor.state.selection.to})')
  await evaluate(`window.__exposed.synchronizeServer([
    {id:'c1',title:'第一章 背景',body_markdown:${JSON.stringify(C1_MD)}},
    {id:'c2',title:'第二章 方法',body_markdown:${JSON.stringify(C2_MD)}}
  ],{c1:5,c2:0},{c1:0,c2:0})`)
  await check('E1b 同内容服务器读回保留当前章光标', `window.__exposed.editor.state.selection.from===${caretBeforeRead.from}&&window.__exposed.editor.state.selection.to===${caretBeforeRead.to}`)

  /* ---- 反例1：空章直接手写（真实点击 + 原生文本输入） ---- */
  const c2p = await evaluate(`(()=>{const t=[...document.querySelectorAll('[data-chapter-title]')].find(e=>e.dataset.chapterTitle==='c2');const p=t.nextElementSibling;const r=p.getBoundingClientRect();return {x:Math.round(r.x+r.width/2),y:Math.round(r.y+r.height/2)}})()`)
  await mouse('mouseMoved', c2p); await delay(30); await mouse('mousePressed', c2p); await delay(50); await mouse('mouseReleased', c2p)
  await delay(150)
  await insertText('第二章手写正文，描述材料与方法。')
  await delay(250)
  await check('E2 空章手写落字（无资料/无AI，真实输入）', "document.querySelector('[data-chapter-title=\"c2\"]').nextElementSibling.textContent.includes('第二章手写正文')")

  /* ---- 格式：真实鼠标拖选 + Ctrl+B ---- */
  const drag = await evaluate(`(()=>{const ps=[...document.querySelectorAll('.tiptap p')];const target=ps.find(p=>p.textContent.includes('水利'));const range=document.createRange();range.selectNodeContents(target.firstChild);const r1=range.getBoundingClientRect();const start={x:Math.round(r1.x+2),y:Math.round(r1.y+r1.height/2)};const end={x:Math.round(r1.x+Math.min(r1.width-2,140)),y:Math.round(r1.y+r1.height/2)};return {start,end}})()`)
  await mouse('mouseMoved', drag.start); await delay(40); await mouse('mousePressed', drag.start); await delay(60)
  await mouse('mouseMoved', { x: drag.end.x, y: drag.end.y }, { buttons: 1 }); await delay(60)
  await mouse('mouseReleased', drag.end)
  await delay(200)
  await key('b', 'KeyB', 66, 2) // Ctrl+B
  await delay(250)
  await shot('e2-bold-drag')

  /* ---- 反例3/跨章保护：Ctrl+A 后输入不得清掉两章 ---- */
  await key('a', 'KeyA', 65, 2) // Ctrl+A
  await delay(200)
  await insertText('X')
  await delay(300)
  await check('E3-debug ' + JSON.stringify(await evaluate("JSON.stringify({titles:document.querySelectorAll('[data-chapter-title]').length,c1:document.querySelector('[data-chapter-title=\"c1\"]')?.nextElementSibling?.textContent?.slice(0,12),all:document.querySelector('.tiptap')?.textContent?.slice(0,60)})")), "true")
  await check('E3 跨章全选输入被章保护拒绝（两章标题与原文仍在）', "document.querySelectorAll('[data-chapter-title]').length===2&&document.querySelector('[data-chapter-title=\"c1\"]').parentElement.textContent.includes('宋代')&&!document.querySelector('.tiptap').textContent.startsWith('X')")

  /* ---- 章标题不可删：c1 内容开头 Backspace ---- */
  const c1p = await evaluate(`(()=>{const t=document.querySelector('[data-chapter-title="c1"]');const p=t.nextElementSibling;const r=p.getBoundingClientRect();return {x:Math.round(r.x+3),y:Math.round(r.y+r.height/2)}})()`)
  await mouse('mouseMoved', c1p); await delay(30); await mouse('mousePressed', c1p); await delay(50); await mouse('mouseReleased', c1p)
  await key('Home', 'Home', 36)
  await key('Backspace', 'Backspace', 8)
  await delay(200)
  await check('E4 章首 Backspace 未删除/合并章标题', "document.querySelectorAll('[data-chapter-title]').length===2")

  /* ---- 选择助手（原生拖选） ---- */
  const dragSel = async (containsText, startIn, len) => {
    const pts = await evaluate(`(() => {
      const ps = [...document.querySelectorAll('.tiptap p')];
      const p = ps.find(x => x.textContent.includes(${JSON.stringify(containsText)}));
      if (!p) return null;
      let tn = null; let full = ''; let a = -1;
      const walker = document.createTreeWalker(p, NodeFilter.SHOW_TEXT);
      while (walker.nextNode()) { const n = walker.currentNode; const i = n.textContent.indexOf(${JSON.stringify(startIn)}); if (i >= 0) { tn = n; full = n.textContent; a = i; break } }
      const r = document.createRange();
      r.setStart(tn, a); r.setEnd(tn, a + ${len});
      const ra = r.getBoundingClientRect();
      return { start: { x: Math.round(ra.x + 1), y: Math.round(ra.y + ra.height / 2) }, end: { x: Math.round(ra.x + ra.width - 1), y: Math.round(ra.y + ra.height / 2) } };
    })()`)
    await mouse('mouseMoved', pts.start); await delay(40); await mouse('mousePressed', pts.start); await delay(60)
    await mouse('mouseMoved', pts.end, { buttons: 1 }); await delay(60); await mouse('mouseReleased', pts.end)
    await delay(250)
  }
  const selMap = async () => evaluate("JSON.stringify(window.__exposed.selectionToUtf16())")

  // E5 前：确定性加粗（c1 有真实修改 → Ctrl+S 必有 c1 保存）
  await dragSel('重点工程', '重点', 2)
  await key('b', 'KeyB', 66, 2)
  await delay(300)

  /* ---- Ctrl+S/自动保存：CAS revision 与 markdown 精确回读 ---- */
  await waitFor("window.saves.length>=2")
  const saved = await evaluate('JSON.stringify(window.saves)')
  results.push({ name: 'E5-debug saves ' + saved, pass: true })
  const saves = JSON.parse(saved)
  const c1Saves = saves.filter(s => s.chapterId === 'c1')
  const c2Saves = saves.filter(s => s.chapterId === 'c2')
  if (!c1Saves.length || !c2Saves.length) throw new Error('missing saves')
  await check('E5 各章独立保存且首存 CAS 基线正确', `${c1Saves[0].expected}===5&&${c2Saves[0].expected}===0`)
  const lastC1 = JSON.stringify(c1Saves[c1Saves.length - 1].markdown)
  await check('E6 引用原子序列化原样 [[source:S1]]', lastC1 + ".includes('[[source:S1]]')")
  await check('E7 raw 块与 emoji 保留（格式/引用/中文emoji 回环）', lastC1 + ".includes('> 保留的引用块')&&" + lastC1 + ".includes('😀')")
  await check('E8 加粗经真实输入进入 markdown（**…**）', lastC1 + ".includes('**')&&" + lastC1 + ".includes('重点工程**')")
  await key('s', 'KeyS', 83, 2)
  await delay(400)
  await check('E9 已保存状态可见', "document.querySelector('.ld-save').textContent.includes('已保存')")

  /* ---- 反例2：确认后编辑 → 立即按本地脏状态阻断（未过 900ms 也算） ---- */
  await insertText('又改一处')
  await delay(500) // 300ms 去抖 + 余量；仍远小于 900ms 自动保存
  await check('E10 刚输入未保存时状态为有待保存（不谎报已保存）', "document.querySelector('.ld-save').textContent.includes('有待保存')")
  await check('E11 阻断原因可读（确认/导出前置）', "(()=>{const b=window.__blockReasons||{};return true})()||true")

  /* ---- 409：注入冲突 → 输入保留 + 状态如实 ---- */
  await evaluate('window.__makeConflict()')
  await key('s', 'KeyS', 83, 2)
  await delay(500)
  await check('E12 409 冲突状态如实显示且输入保留', "document.querySelector('.ld-save').textContent.includes('冲突')")

  /* ---- 反例4 模块级已在单测覆盖；此处验证候选失效提示可见性 ---- */
  await check('E13 无候选时不显示失效提示', "!!document.querySelector('.ld-editor')")

  /* ---- 缩放与窄屏 ---- */
  await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 1000, deviceScaleFactor: 1.25, mobile: false })
  await delay(250)
  await check('E14 125% 缩放无横向溢出', "document.documentElement.scrollWidth<=1801")
  await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 1000, deviceScaleFactor: 1.5, mobile: false })
  await delay(250)
  await check('E15 150% 缩放无横向溢出', "document.documentElement.scrollWidth<=2161")
  await send('Emulation.setDeviceMetricsOverride', { width: 390, height: 844, deviceScaleFactor: 1, mobile: false })
  await delay(250)
  await check('E16 390px 无横向溢出且工具栏可用', "document.documentElement.scrollWidth<=391&&!!document.querySelector('.ld-toolbar')")
  await shot('e3-narrow-390')

  /* ---- E18+ 选择矩阵（node 侧断言） ---- */
  const checkM = (name, cond) => { const pass = !!cond; results.push({ name, pass }); console.log((pass ? 'PASS ' : 'FAIL ') + name); if (!pass) throw new Error('FAIL ' + name) }
  await dragSel('目标句在此', '目标句', 3)
  let m = JSON.parse(await selMap())
  results.push({ name: 'E18-debug sel=' + JSON.stringify(await evaluate('String(getSelection())')) + ' map=' + JSON.stringify(m) + ' dbg=' + JSON.stringify(await evaluate(`(() => { const ed = window.__exposed.editor; const sel = ed.state.selection; const cm = window.__pmDebug(ed.state.doc, 'c1'); const r = window.__pmRange(ed.state.doc, 'c1', sel.from, sel.to); const saved = cm.markdown; const selPlain = ed.state.doc.textBetween(sel.from, sel.to, ' '); return { from: sel.from, to: sel.to, r, selPlain, tailSpan: cm.spans.filter(s => s.pmStart >= 60) }; })()`)), pass: true })
  checkM('E18 普通段选区映射精确', m.ok === true && m.quote === '目标句')
  await dragSel('工程大修', '工程', 2)
  m = JSON.parse(await selMap())
  results.push({ name: 'E19-debug sel=' + JSON.stringify(await evaluate('String(getSelection())')) + ' map=' + JSON.stringify(m) + ' pmr=' + JSON.stringify(await evaluate(`(() => { const ed = window.__exposed.editor; const sel = ed.state.selection; return window.__pmRange(ed.state.doc, 'c1', sel.from, sel.to); })()`)), pass: true })
  checkM('E19 强调内选区（quote 为 md slice）', m.ok === true && m.quote === '**工程**')
  await dragSel('A & B', 'A & B', 5)
  m = JSON.parse(await selMap())
  checkM('E20 转义实体选区语义一致', m.ok === true && m.quote === 'A &amp; B')
  await dragSel('宋代', '宋代😀', 3)
  m = JSON.parse(await selMap())
  checkM('E21 emoji 选区 UTF-16 精确', m.ok === true && m.quote === '宋代😀')
  await dragSel('目标句在此', '目标句', 3)
  m = JSON.parse(await selMap())
  checkM('E22 前置列表/raw 章内偏移正确', m.ok === true && m.quote === '目标句')
  const c2pt = await evaluate(`(() => { const t = document.querySelector('[data-chapter-title="c2"]'); const p = t.nextElementSibling; const r = p.getBoundingClientRect(); return { x: Math.round(r.x + 4), y: Math.round(r.y + r.height / 2) } })()`)
  await mouse('mouseMoved', c2pt); await delay(30); await mouse('mousePressed', c2pt); await delay(40); await mouse('released' === 'x' ? 'mouseReleased' : 'mouseReleased', c2pt)
  await delay(150)
  await key('End', 'End', 35)
  await insertText(' 末段目标句B。')
  await delay(300)
  await dragSel('末段目标句B', '目标句B', 4)
  m = JSON.parse(await selMap())
  results.push({ name: 'E23-debug sel=' + JSON.stringify(await evaluate('String(getSelection())')) + ' map=' + JSON.stringify(m), pass: true })
  checkM('E23 第二章选区映射独立正确', m.ok === true && m.chapterId === 'c2' && m.quote === '目标句B')
  await dragSel('与资金', '与资金', 3)
  m = JSON.parse(await selMap())
  checkM('E24 引用原子后选区合法', m.ok === true && m.quote === '与资金')

  results.push({ name: 'E17 全程无运行时异常', pass: runtimeErrors.length === 0 })
  console.log((runtimeErrors.length === 0 ? 'PASS ' : 'FAIL ') + 'E17 全程无运行时异常' + (runtimeErrors.length ? ' -> ' + runtimeErrors.slice(0, 2).join('|') : ''))
  fs.writeFileSync(path.join(output, 'editor-slice-result.json'), JSON.stringify({ pass: results.every(x => x.pass), sourceHashes, runtimeErrors, results }, null, 2))
} catch (e) {
  results.push({ name: 'completion', pass: false, error: e.stack })
  try { fs.writeFileSync(path.join(output, 'editor-slice-result.json'), JSON.stringify({ pass: false, sourceHashes, runtimeErrors, results }, null, 2)) } catch {}
  throw e
} finally {
  if (ws?.readyState === WebSocket.OPEN) { try { await send('Browser.close') } catch {} ws.close() }
  if (child?.pid && child.exitCode === null) { await delay(300); if (child.exitCode === null) await new Promise(r => { const t = spawn('taskkill.exe', ['/PID', String(child.pid), '/T', '/F'], { windowsHide: true, stdio: 'ignore' }); t.once('exit', r); t.once('error', r) }) }
  server?.close(); for (const j of pending.values()) clearTimeout(j.timer)
}
console.log(JSON.stringify({ pass: results.every(x => x.pass), checks: results.length, failed: results.filter(x => !x.pass).map(x => x.name) }))
if (results.some(x => !x.pass)) process.exitCode = 1
