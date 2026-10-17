import assert from 'node:assert/strict'
import { readFileSync, unlinkSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import test from 'node:test'
import { pathToFileURL, fileURLToPath } from 'node:url'
import { createSSRApp } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { compileScript, parse } from '@vue/compiler-sfc'
import { transformSync } from 'esbuild'
import type { ReplayFixtureSummary } from '@/api/lingdoc/templateGovernance'

// The component under test is compiled from its real .vue source at runtime so
// the assertions exercise the same markup a browser receives, without adding
// DOM dependencies. Real checkbox interaction remains a browser check.
const here = dirname(fileURLToPath(import.meta.url))
const source = readFileSync(join(here, 'TemplateGovernanceFixturePicker.vue'), 'utf8')
const { descriptor, errors } = parse(source, { filename: 'TemplateGovernanceFixturePicker.vue' })
assert.deepEqual(errors, [])
const compiled = compileScript(descriptor, { id: 'fixture-picker-test', inlineTemplate: true })
// compileScript keeps `import type` lines, alias paths and TS annotations for
// the bundler to handle; plain Node can parse none of them. Strip the type-only
// imports and run the rest through esbuild's TS transform (already present via
// tsx). Value imports ('vue', './templateGovernanceState') resolve from here.
const withoutTypeImports = compiled.content.split('\n').filter(line => !line.startsWith('import type ')).join('\n')
const runnable = transformSync(withoutTypeImports, { loader: 'ts', format: 'esm' }).code

const tmpFile = join(here, `.fixture-picker.compiled.${process.pid}.mjs`)
writeFileSync(tmpFile, runnable)
let FixturePicker: ReturnType<typeof createSSRApp> extends never ? never : (new (...args: never[]) => unknown)
try {
  FixturePicker = (await import(pathToFileURL(tmpFile).href)).default
} finally {
  unlinkSync(tmpFile)
}

const fixture = (id: string, rule: string, scene: string, polarity: string): ReplayFixtureSummary => (
  { fixture_id: id, rule_id: rule, scene, polarity, target_ref: `target/${id}`, ruleset: { ruleset_id: 'ruleset-demo', version: '1' }, template_id: 'template-demo', template_version: '1' }
)
const FIXTURES = [fixture('demo-a-write_check-positive', 'a', 'write_check', 'positive'), fixture('demo-a-write_check-negative', 'a', 'write_check', 'negative')]

async function renderPicker(props: Record<string, unknown>) {
  return renderToString(createSSRApp(FixturePicker as never, props) as never)
}

test('with a known catalog, full selection reports covered combinations and unknown IDs are asserted as rejected', async () => {
  const html = await renderPicker({ fixtures: FIXTURES, modelValue: `${FIXTURES[0].fixture_id} ${FIXTURES[1].fixture_id} made-up-id` })
  assert.match(html, /已覆盖 2\/2 个必需正反例组合/)
  assert.match(html, /以下 ID 未在服务器注册，提交会被拒绝/)
  assert.match(html, /made-up-id/)
  assert.doesNotMatch(html, /尚未核验/)
  assert.doesNotMatch(html, /尚不能从此处确认/)
})

test('when the catalog fails, hand-typed IDs stay unverified instead of being asserted as rejected, and the input is preserved', async () => {
  const typed = 'demo-hand-typed-1 demo-hand-typed-2'
  const html = await renderPicker({ fixtures: [], modelValue: typed, catalogError: '网络错误' })
  assert.match(html, /尚未核验/)
  assert.match(html, /以服务端注册与回放结果为准/)
  assert.match(html, /demo-hand-typed-1/)
  assert.match(html, /demo-hand-typed-2/)
  assert.match(html, /原有输入已保留/)
  assert.doesNotMatch(html, /未在服务器注册，提交会被拒绝/)
  // Unknown must be stated, not faked as complete 0/0 coverage.
  assert.match(html, /尚不能从此处确认/)
})

test('a long single fixture id is wrapped by layout rules instead of breaking the 390px column', async () => {
  const longId = `demo-${'very-long-rule-identifier-'.repeat(6)}write_check-positive`
  assert.ok(longId.length > 180)
  const html = await renderPicker({ fixtures: [fixture(longId, 'a', 'write_check', 'positive')], modelValue: longId })
  assert.match(html, /fixture-entry/)
  assert.ok(html.includes(longId))
  const styleBlock = descriptor.styles.map(block => block.content).join('\n')
  assert.match(styleBlock, /overflow-wrap:\s*anywhere/)
  assert.match(styleBlock, /word-break:\s*break-all|word-break:\s*break-word/)
})
