import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
const require = createRequire(import.meta.url)
const __filename = fileURLToPath(import.meta.url)
const __dirname = require('node:path').dirname(__filename)

// Compiles the actual editor SFC and executes its installed ProseMirror plugins.
// Only Tiptap's browser view is replaced with an in-memory real EditorState.
const test = require('node:test'), assert = require('node:assert/strict'), fs = require('node:fs'), path = require('node:path'), Module = require('node:module')
const frontend = path.resolve(__dirname, '../../../..'), load = Module.createRequire(path.join(frontend, 'package.json'))
const ts = load('typescript'), compiler = load('@vue/compiler-sfc'), vue = load('vue'), { renderToString } = load('@vue/server-renderer')
const { getSchema } = load('@tiptap/core'), { EditorState, TextSelection } = load('@tiptap/pm/state'), { history, undo, redo } = load('@tiptap/pm/history')
const cache = new Map()
function compile(filename, source, resolve) {
  const item = new Module(filename); item.paths = Module._nodeModulePaths(frontend); item.require = resolve || localRequire(filename)
  item._compile(ts.transpileModule(source ?? fs.readFileSync(filename, 'utf8'), { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText, filename)
  return item.exports
}
function localRequire(filename) {
  return id => {
    if (!id.startsWith('.')) return load(id)
    const target = path.resolve(path.dirname(filename), id) + '.ts'
    if (!cache.has(target)) cache.set(target, compile(target))
    return cache.get(target)
  }
}
const source = path.join(__dirname, 'LingDocEditor.vue'), parsed = compiler.parse(fs.readFileSync(source, 'utf8'))
assert.deepEqual(parsed.errors, [])
const script = compiler.compileScript(parsed.descriptor, { id: 'actual-chapter-scope-test' })
const template = compiler.compileTemplate({ source: parsed.descriptor.template.content, filename: source, id: 'actual-chapter-scope-test', compilerOptions: { bindingMetadata: script.bindings }, isTS: true })
assert.deepEqual(template.errors, [])
let inMemory
const tiptap = { ...load('@tiptap/vue-3'), EditorContent: { name: 'SyntheticBrowserView', render: () => null }, useEditor(config) {
  const schema = getSchema(config.extensions), plugins = [history()]
  for (const name of ['ldChapterGuard', 'ldChapterScopeMarkers', 'ldReviewMarkers']) {
    const extension = config.extensions.find(item => item.name === name)
    plugins.push(...extension.config.addProseMirrorPlugins.call(extension))
  }
  const state = vue.shallowRef(EditorState.create({ schema, doc: schema.nodeFromJSON(config.content), plugins }))
  const editor = { schema, get state() { return state.value }, getJSON() { return state.value.doc.toJSON() }, view: { dispatch(tr) { state.value = state.value.applyTransaction(tr).state } }, on() {}, setEditable() {}, can() { return { undo: () => undo(state.value), redo: () => redo(state.value) } }, commands: { undo: () => undo(state.value, tr => editor.view.dispatch(tr)), redo: () => redo(state.value, tr => editor.view.dispatch(tr)), scrollIntoView() {}, setTextSelection(range) { editor.view.dispatch(state.value.tr.setSelection(TextSelection.create(state.value.doc, typeof range === 'number' ? range : range.from, typeof range === 'number' ? range : range.to))); return true } } }
  inMemory = { editor, config }; return vue.shallowRef(editor)
} }
const component = compile(source, script.content, id => id === '@tiptap/vue-3' ? tiptap : localRequire(source)(id)).default
component.render = compile(source + '.template', template.code).render
function chapters() { return [{ id: 'c1', title: '可写章节', body_markdown: '独立正文' }, { id: 'c2', title: '只读章节', body_markdown: '目标 **文字** [[source:s1]]' }] }
function position(doc, id) { let current = '', at; doc.forEach((node, offset) => { if (node.type.name === 'chapterTitle') current = node.attrs.chapterId; else if (current === id && at === undefined) at = offset + 1 }); return at }
async function render(props, action) {
  let controller; const events = []
  const wrapped = { ...component, setup(input, context) { controller = component.setup(input, context); return action ? Promise.resolve(action(controller, inMemory.editor)).then(() => controller) : controller } }
  const app = vue.createSSRApp(wrapped, { chapters: chapters(), ...props, onChapterDirty: id => events.push(id) })
  const html = await renderToString(app)
  return { controller, editor: inMemory.editor, html, events }
}

test('actual SFC filter blocks readonly typing and formatting but keeps navigation and writable editing', async () => {
  const { controller, editor, html } = await render({ chapterWritePermissions: { c1: true, c2: false } }, (s, ed) => {
    const before = ed.state.doc, readonly = position(before, 'c2')
    ed.view.dispatch(ed.state.tr.insertText('forbidden', readonly)); assert(ed.state.doc.eq(before))
    ed.view.dispatch(ed.state.tr.addMark(readonly, readonly + 2, ed.schema.marks.bold.create())); assert(ed.state.doc.eq(before))
    ed.commands.setTextSelection({ from: readonly, to: readonly + 2 }); assert.equal(ed.state.selection.from, readonly)
    ed.view.dispatch(ed.state.tr.insertText('合法', position(ed.state.doc, 'c1'))); assert(!ed.state.doc.eq(before))
  })
  assert.match(html, /1 个章节只读/); assert.equal(controller.selectionWritable.value, false)
  const decorations = editor.state.plugins.find(plugin => plugin.key.startsWith('ld-chapter-scope-markers')).props.decorations(editor.state).find()
  assert(decorations.length >= 2); assert(decorations.every(item => item.type.attrs['data-ld-readonly-chapter'] === 'c2'))
})
test('actual synchronizeServer privately permits server hydration and closes that permission immediately', async () => {
  const { controller, editor, html } = await render({ chapterWritePermissions: {}, chapterWritePermissionReasons: { c2: 'Observer 仅可阅读' } }, (s, ed) => {
    const next = chapters().map(ch => ({ ...ch, body_markdown: '服务器新版本' }))
    assert.equal(s.synchronizeServer(next, { c1: 5, c2: 8 }, { c1: 4, c2: 7 }), true)
    assert.equal(s.currentMarkdown('c2'), '服务器新版本')
    const before = ed.state.doc
    ed.view.dispatch(ed.state.tr.insertText('伪造', position(before, 'c2')).setMeta('ld-authorized-external-snapshot$', true).setMeta('applyingExternal', true))
    assert(ed.state.doc.eq(before)); assert.equal(s.savedRevisions.get('c2'), 8)
  })
  assert.match(html, /文稿只读/); assert.match(html, /Observer 仅可阅读/)
  assert.equal(controller.applyingExternal, false); assert.equal(editor.state.doc.textContent.includes('伪造'), false)
})
test('an exceptional server hydration cannot leak the private permission into later local transactions', async () => {
  await render({ chapterWritePermissions: {} }, (s, ed) => {
    const dispatch = ed.view.dispatch; ed.view.dispatch = () => { throw new Error('synthetic browser-view failure') }
    assert.throws(() => s.synchronizeServer(chapters().map(ch => ({ ...ch, body_markdown: '新正文' })), {}, {}), /synthetic browser-view failure/)
    ed.view.dispatch = dispatch
    const before = ed.state.doc; ed.view.dispatch(ed.state.tr.insertText('越权', position(before, 'c2')))
    assert(ed.state.doc.eq(before)); assert.equal(s.applyingExternal, false)
  })
})
test('actual save queue retains a revoked chapter draft and sends no save request', async () => {
  let requests = 0
  const { controller } = await render({ chapterWritePermissions: {}, saveChapter: async () => { requests++; return { ok: true, newWorkingCopyRevision: 1 } } }, async s => {
    s.queue.markDirty('c2', '权限撤销前保留的稿件')
    const result = await s.flushSave(); assert.equal(result.ok, false); assert.equal(s.queue.state('c2'), 'error')
    s.queue.dispose()
  })
  assert.equal(requests, 0); assert.equal(controller.hasUnsaved.value, true)
})
test('metadata-only server readback keeps the exact selection and does not replace the document', async () => {
  await render({ chapterWritePermissions: {} }, (s, ed) => {
    const at = position(ed.state.doc, 'c2'); ed.commands.setTextSelection({ from: at, to: at + 2 })
    const before = ed.state.doc; assert.equal(s.synchronizeServer(chapters(), { c1: 1, c2: 1 }, {}), true)
    assert.equal(ed.state.doc, before); assert.equal(ed.state.selection.from, at); assert.equal(ed.state.selection.to, at + 2)
  })
})
test('readonly selection still uses the existing triple Markdown/semantic/uniqueness validation', async () => {
  await render({ chapterWritePermissions: {} }, (s, ed) => {
    const at = position(ed.state.doc, 'c2'); ed.commands.setTextSelection({ from: at, to: at + 2 })
    const selected = s.selectionToUtf16(); assert.equal(selected.ok, true); assert.equal(selected.chapterId, 'c2'); assert.equal(selected.quote, '目标')
  })
})
test('the actual editor keeps writable undo/redo and denies the same history write after permission revocation', async () => {
  const permissions = { c1: true, c2: false }
  await render({ chapterWritePermissions: permissions }, (s, ed) => {
    const original = ed.state.doc
    ed.view.dispatch(ed.state.tr.insertText('合法修改', position(original, 'c1')))
    const changed = ed.state.doc; assert(!changed.eq(original))
    ed.commands.undo(); assert(ed.state.doc.eq(original))
    ed.commands.redo(); assert(ed.state.doc.eq(changed))
    permissions.c1 = false; ed.commands.undo(); assert(ed.state.doc.eq(changed))
    permissions.c1 = true; ed.commands.undo(); assert(ed.state.doc.eq(original))
  })
})
test('server hydration refuses to replace a real dirty draft, preserving its queued content', async () => {
  await render({ chapterWritePermissions: { c1: true, c2: false } }, (s, ed) => {
    s.queue.markDirty('c1', '尚未保存的原稿')
    const before = ed.state.doc
    assert.equal(s.synchronizeServer(chapters().map(ch => ({ ...ch, body_markdown: '远端新版本' })), { c1: 9, c2: 9 }, {}), false)
    assert.equal(ed.state.doc, before); assert.equal(s.queue.state('c1'), 'dirty'); s.queue.dispose()
  })
})