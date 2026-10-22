import assert from 'node:assert/strict'
import fs from 'node:fs'
import test from 'node:test'
import vm from 'node:vm'
import { createRequire } from 'node:module'

const require = createRequire(import.meta.url)
const ts = require('typescript')
const sfc = require('@vue/compiler-sfc')

/**
 * 归档入口的界面契约。
 *
 * 断言的是**界面看得到的事实**：Owner 的租户身份失效时归档入口置灰，冻结
 * 原因与恢复指引都在；Owner 可用时 active 项目的归档入口可用；draft 项目
 * 只能丢弃，所以入口不可用。
 *
 * 冻结事实与文案由 `useOwnerAvailability` 持有，这里把它换成一份合成夹具：
 * 界面关心的是「拿到冻结事实之后怎么办」，不是读出口本身。
 */

const SOURCE = new URL('./ProjectArchivePanel.vue', import.meta.url)

type Availability = {
  frozen: { value: boolean }
  reason: { value: string }
  recovery: { value: string }
}

type PanelState = {
  disabled: { value: boolean }
  archivable: { value: boolean }
  reason: { value: string }
  recovery: { value: string }
  archive: () => Promise<void>
}

function load(availability: Availability) {
  const source = fs.readFileSync(SOURCE, 'utf8')
  const parsed = sfc.parse(source, { filename: SOURCE.pathname })
  assert.deepEqual(parsed.errors, [])
  const descriptor = parsed.descriptor
  const script = sfc.compileScript(descriptor, { id: 'project-archive-panel-test', inlineTemplate: false })
  const template = sfc.compileTemplate({
    id: 'project-archive-panel-test',
    filename: SOURCE.pathname,
    source: descriptor.template.content,
    compilerOptions: { bindingMetadata: script.bindings },
    isTS: true,
  })
  assert.deepEqual(template.errors, [])

  const vue = {
    defineComponent: (component: unknown) => component,
    ref: (value: unknown) => ({ value }),
    computed: (fn: () => unknown) => ({ get value() { return fn() } }),
  }
  const exports: Record<string, unknown> = {}
  const js = ts.transpileModule(script.content, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText
  vm.runInNewContext(js, {
    exports,
    require(name: string) {
      if (name === 'vue') return vue
      if (name === '@/api/lingdoc/projectArchive') return { archiveProject: () => Promise.resolve({ data: {} }) }
      if (name === '@/api/lingdoc/workspace') return {}
      if (name === './OwnerUnavailableNotice.vue') return {}
      if (name === '@/composables/useOwnerAvailability') {
        return {
          useOwnerAvailability: () => availability,
          OWNER_ARCHIVE_UNAVAILABLE_TEXT: '项目 Owner 的租户身份已失效，归档已冻结。',
        }
      }
      throw new Error(`unexpected import ${name}`)
    },
    console,
    window: { confirm: () => true },
  })
  const component = exports as {
    default: { setup: (props: unknown, context: unknown) => PanelState }
  }
  return { component, template: descriptor.template.content }
}

function panelState(availability: Availability, status: string): PanelState {
  const { component } = load(availability)
  return component.default.setup(
    { project: { id: 'project-1', name: '合成项目', status, project_version: 3 } },
    { emit: () => {}, expose: () => {} },
  )
}

function frozen(): Availability {
  return {
    frozen: { value: true },
    reason: { value: '项目 Owner 的租户身份已失效，归档已冻结。' },
    recovery: { value: '可等待租户恢复 Owner 的身份，或由租户 Owner 指定受让人完成一次所有权转让。' },
  }
}

function available(): Availability {
  return { frozen: { value: false }, reason: { value: '' }, recovery: { value: '' } }
}

test('the archive entry is disabled while the Owner is unavailable and says why', () => {
  const state = panelState(frozen(), 'active')
  assert.equal(state.disabled.value, true, 'the archive entry must be greyed out while the Owner is unavailable')
  assert.match(state.reason.value, /Owner/)
  assert.match(state.recovery.value, /恢复|转让/)
})

test('the archive entry is available for an active project when the Owner can act', () => {
  const state = panelState(available(), 'active')
  assert.equal(state.archivable.value, true)
  assert.equal(state.disabled.value, false)
})

test('a draft project is not offered for archiving', () => {
  const state = panelState(available(), 'draft')
  assert.equal(state.archivable.value, false, 'a draft project may only be discarded')
  assert.equal(state.disabled.value, true)
})

test('the panel binds the entry state and the notice to the freeze fact', () => {
  const { template } = load(frozen())
  assert.match(template, /:disabled="disabled"/)
  assert.match(template, /<OwnerUnavailableNotice :reason="reason" :recovery="recovery"/)
})
