import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import vm from 'node:vm'
import { createRequire } from 'node:module'
const require = createRequire(import.meta.url)
const ts = require('typescript'), sfc = require('@vue/compiler-sfc')
const file = new URL('./ChapterCommitReview.vue', import.meta.url)
const descriptor = sfc.parse(fs.readFileSync(file, 'utf8'), { filename: file.pathname }).descriptor
const script = sfc.compileScript(descriptor, { id: 'chapter-commit-test', inlineTemplate: false })

test('three explicit choices compile without a default commit or scientific classification', () => {
  const template = sfc.compileTemplate({ id: 'chapter-commit-test', filename: file.pathname, source: descriptor.template.content, compilerOptions: { bindingMetadata: script.bindings } })
  assert.deepEqual(template.errors, [])
  const emitted = [], mounted = [], unmounted = [], exports = {}
  const vue = { defineComponent: x => x, ref: value => ({ value }), onMounted: fn => mounted.push(fn), onUnmounted: fn => unmounted.push(fn) }
  const js = ts.transpileModule(script.content, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  vm.runInNewContext(js, { exports, require: () => vue })
  exports.default.setup({ chapterTitle: 'Synthetic chapter' }, { expose() {}, emit: event => emitted.push(event) })
  assert.deepEqual(emitted, [])
  assert.equal(mounted.length, 1)
  assert.equal(unmounted.length, 1)
  assert.match(descriptor.template.content, /@cancel\.prevent="emit\('cancel'\)"/)
  assert.match(descriptor.template.content, /@click="emit\('wording'\)"/)
  assert.match(descriptor.template.content, /@click="emit\('semantic'\)"/)
  assert.match(descriptor.template.content, /autofocus @click="emit\('cancel'\)"/)
})

test('the suspected second state shows the server list and gates the dismissal on a reason', () => {
  const source = descriptor.template.content
  assert.match(source, /suspectedConditions && suspectedConditions\.length/)
  assert.match(source, /v-for="item in suspectedConditions"/)
  assert.match(source, /:disabled="!dismissalReason\.trim\(\)"/)
  assert.match(source, /emit\('dismiss', dismissalReason\.trim\(\)\)/)
  assert.match(source, /@wording="emit\('wording'\)"|emit\('wording'\)/)
  // The plain wording exit must NOT be offered while suspicions are showing.
  const suspectedBlock = source.slice(source.indexOf('suspectedConditions && suspectedConditions.length'), source.indexOf('<div v-else class="commit-choices">'))
  assert.doesNotMatch(suspectedBlock, /emit\('wording'\)/)
})
