<!--
上下文包面板：只读地展示服务端组装好的那一份，并允许在发出请求前增删引用的卡片。

面板这一层的接口约定：

- `props.intent` 是这一轮的**基线与来源**：版本基线（expected_*）与已带的来源归
  宿主所有，面板每次预览都取宿主当前的那一份；`intent.card_refs` 只作面板打开
  时的勾选起点，之后的勾选集合归面板管，改完通过 `intentChange` 告诉宿主。
  宿主不要把 intent 再塞回来（那样会形成回环）。
- `props.availableCards` 是宿主给的候选卡摘要，只有「加入一张卡」与意图区的
  状态提示用它。事实区的卡片状态一律来自服务端包，不看这份清单。
- `props.client` 是可注入的预览客户端：生产用门面的默认客户端，测试注入假件，
  因此面板这一层不打网络。
- 事件只有 `intentChange` 一个：勾选集合变了。面板不替宿主发请求。

四条规则决定了这个面板的形状：

- 事实区只吃服务端返回的那一份。用户的勾选只是意图，两者在界面上分开显示。
- 任何增删之后必须重新预览，事实区只由预览结果刷新。
- 预览失败或版本冲突 ⇒ 事实区清空，不留下上一次成功的包。
- 面板不自己判断「要不要确认」：授权结论是服务端给的原值，原样显示。

依赖方向：面板只依赖门面模块（agentContext.ts）。传输层模块（自己写 HTTP 客户端
的那一份）归门面所有，面板不引它——这条由测试里的静态源码断言守住。
-->
<script setup lang="ts">
import { computed, getCurrentInstance, onServerPrefetch, ref, watch } from 'vue'
import { previewContextPackage, withCard, withoutCard } from '@/api/lingdoc/agentContext'
import type { ContextCardStatus, ContextFacts, ContextIntent, ContextPackageClient } from '@/api/lingdoc/agentContext'

/** 卡片引用：门面的卡片状态里带着它，这里从门面取形状，不引传输层。 */
type CardRef = ContextCardStatus['ref']

/** 候选卡：宿主给的证据卡摘要，只有面板用得到的三个字段。 */
interface CardCandidate {
  id: string
  version: number
  status: string
}

const props = withDefaults(defineProps<{
  /** 项目 id：预览按它取这一轮的范围。 */
  projectId: string
  /** 这一轮的意图起点：版本基线 + 已勾选的卡 + 已带的来源。 */
  intent: ContextIntent
  /** 可加入的候选卡（宿主给的清单摘要）。 */
  availableCards?: CardCandidate[]
  /** 可注入的预览客户端；不给就用门面的默认客户端。 */
  client?: ContextPackageClient
}>(), { availableCards: () => [] })

const emit = defineEmits<{ intentChange: [intent: ContextIntent] }>()

/**
 * 勾选集合：面板自己的一份副本。
 *
 * 宿主给的 `props.intent.card_refs` 只作起点；之后的勾选归面板管，改动通过
 * `intentChange` 告诉宿主。宿主把 intent 塞回来会形成回环，所以这里不跟它同步。
 */
const selectedCards = ref<CardRef[]>(props.intent.card_refs.map(card => ({ ...card })))

/**
 * 这一轮要发给服务端的意图：版本基线与来源取宿主那份，勾选取面板这份。
 *
 * 基线不在面板手里，所以它一直取宿主当前的那一份——宿主重读了项目、基线变了，
 * 面板下一次预览就得用新基线，否则用户会卡在一个解不开的版本冲突里。
 */
const intent = computed<ContextIntent>(() => ({
  ...props.intent,
  card_refs: selectedCards.value.map(card => ({ ...card })),
}))

/** 事实：服务端这一轮组装好的那一份。没有预览结果时是 null——事实区因此是空的。 */
const facts = ref<ContextFacts | null>(null)
const errorMessage = ref('')
const busy = ref(false)
/** 「加入一张卡」里选中的那张卡。 */
const pendingCardKey = ref('')

/** 卡片状态的中文说法。服务端给的是状态值，面板只给它一个说法。 */
const CARD_STATUS_TEXT: Record<string, string> = {
  CANDIDATE: '候选',
  CONFIRMED: '已人工确认',
  NEEDS_REVIEW: '需复核',
  INSUFFICIENT: '证据不足',
  STALE: '已过期',
  SUPERSEDED: '已被替代',
}

function statusText(status: string): string {
  return CARD_STATUS_TEXT[status] ?? status
}

/**
 * 状态未知的三种说法：版本变了、清单里没有这张卡、清单读不到。
 *
 * 三种都只说「状态未知」，不给任何状态值——版本变了不回退到同名卡的别的版本，
 * 清单读不到也不拿上一轮的值顶上。
 */
function unknownStatusText(reason: 'version-moved' | 'not-found' | 'unavailable'): string {
  if (reason === 'version-moved') return '状态未知（版本已变）'
  if (reason === 'not-found') return '状态未知（清单里没有这张卡）'
  return '状态未知（清单读不到）'
}

/** 事实区里一张卡的当前状态：已知的给状态值，未知的给未知的原因。 */
function factStatusText(row: ContextCardStatus): string {
  return row.state === 'known' ? statusText(row.status) : unknownStatusText(row.reason)
}

/** 意图区里一张卡在候选清单里的状态：按 id **且** version 对齐。 */
function candidateStatusText(card: CardRef): string {
  const hit = props.availableCards.find(item => item.id === card.id && item.version === card.version)
  return hit ? statusText(hit.status) : '候选清单里没有这个版本'
}

/** 还能加入的候选卡：已经勾上的不再列出来。 */
const addableCards = computed(() =>
  props.availableCards.filter(card => !selectedCards.value.some(item => item.id === card.id)),
)

/** 请求代次：后发起的预览先回来时，晚响应不得回填旧事实。 */
let previewSeq = 0
/** 在途的预览：SSR 与测试都要等它。 */
let currentPreview: Promise<void> | null = null

/**
 * 预览失败的说法。
 *
 * 版本冲突（409 version_conflict）说的是「你手上的版本基线已经过期」。面板手上
 * 的基线不会自己变，所以正确的动作是让人重新读一遍当前版本——面板不自动重试，
 * 那样每次重试都是同一个错。这一支只提示，不替人改基线。
 *
 * 其它失败一律是「预览失败」：事实区已经清空，说清楚为什么、让人重试。
 */
function failureText(cause: unknown): string {
  const item = cause as { message?: string; status?: number; error?: { code?: string } }
  if (item?.status === 409 && item?.error?.code === 'version_conflict') {
    return '版本基线已变，本轮范围已失效，事实区已清空。请重新读取项目当前版本后再预览；本次没有自动重试。'
  }
  return `预览失败：${item?.message || '无法读取本轮上下文'}。事实区已清空，请重新预览。`
}

async function refresh(): Promise<void> {
  const seq = ++previewSeq
  busy.value = true
  errorMessage.value = ''
  try {
    const result = await previewContextPackage(props.projectId, intent.value, props.client)
    if (seq !== previewSeq) return
    facts.value = result
  } catch (cause) {
    if (seq !== previewSeq) return
    // 失败 ⇒ 事实区清空。留着上一次成功的包会让人以为那就是当前范围。
    facts.value = null
    errorMessage.value = failureText(cause)
  } finally {
    if (seq === previewSeq) busy.value = false
  }
}

function startPreview(): Promise<void> {
  currentPreview = refresh()
  return currentPreview
}

/** 勾上一张卡：意图变了，重新预览。事实区只由这次预览刷新。 */
async function addCard(card: CardRef): Promise<void> {
  if (selectedCards.value.some(item => item.id === card.id)) return
  selectedCards.value = withCard(intent.value, card).card_refs
  emit('intentChange', intent.value)
  await startPreview()
}

/** 取消勾选一张卡：意图变了，重新预览。事实区只由这次预览刷新。 */
async function removeCard(cardId: string): Promise<void> {
  const next = withoutCard(intent.value, cardId)
  if (next.card_refs.length === selectedCards.value.length) return
  selectedCards.value = next.card_refs
  emit('intentChange', intent.value)
  await startPreview()
}

/** 「加入一张卡」按钮：把选中的那张卡加进意图。 */
function addPendingCard(): void {
  const hit = addableCards.value.find(card => `${card.id}@${card.version}` === pendingCardKey.value)
  if (!hit) return
  pendingCardKey.value = ''
  void addCard({ id: hit.id, version: hit.version })
}

// 预览的触发条件：项目与版本基线变了就重新预览。意图的增删走上面的两个入口。
watch(
  () => [
    props.projectId,
    props.intent.expected_project_version,
    props.intent.expected_context_revision,
    props.intent.expected_version,
    props.intent.chapter_id ?? '',
    props.intent.task_id ?? '',
  ].join('|'),
  () => { void startPreview() },
  { immediate: true },
)

// SSR 渲染要等预览完成：setup 期的 immediate watch 已经发起了预览，这里等在途
// 那一次，不重复发起。浏览器端由上面的 watch 驱动。
if (typeof window === 'undefined' && getCurrentInstance()) {
  onServerPrefetch(() => currentPreview ?? startPreview())
}

/** 只读诊断面：供宿主与测试观察当前状态、触发与按钮相同的增删处理。 */
defineExpose({ intent, selectedCards, facts, errorMessage, busy, addCard, removeCard, preview: startPreview, settle: async () => { await currentPreview } })
</script>

<template>
  <section class="context-package-panel" aria-label="上下文包">
    <header class="context-package-panel__header">
      <h3>本轮上下文包</h3>
      <p class="context-package-panel__hint">事实区只显示服务端这一轮组装好的范围；勾选只是意图，两者分开显示。</p>
    </header>

    <section class="context-package-panel__intent" aria-label="意图：勾选的证据卡">
      <h4>意图：勾选的证据卡</h4>
      <ul v-if="intent.card_refs.length" class="context-package-panel__list">
        <li v-for="item in intent.card_refs" :key="item.id" class="context-intent" :data-intent-card="item.id">
          <span class="context-intent__id">{{ item.id }}</span>
          <span class="context-intent__version">v{{ item.version }}</span>
          <span class="context-intent__status">{{ candidateStatusText(item) }}</span>
          <button type="button" class="context-intent__remove" :data-remove="item.id" @click="removeCard(item.id)">移除这张卡</button>
        </li>
      </ul>
      <p v-else class="context-package-panel__empty">还没有勾选任何证据卡。</p>
      <div class="context-package-panel__add">
        <label class="context-package-panel__add-label">
          加入一张卡
          <select v-model="pendingCardKey" :disabled="!addableCards.length">
            <option v-for="card in addableCards" :key="`${card.id}@${card.version}`" :value="`${card.id}@${card.version}`">{{ card.id }} · v{{ card.version }}</option>
          </select>
        </label>
        <button type="button" class="context-package-panel__add-button" :disabled="!pendingCardKey" @click="addPendingCard">加入这张卡</button>
      </div>
    </section>

    <section class="context-package-panel__facts" aria-label="事实：服务端组装的结果" data-facts="1">
      <h4>事实：服务端组装的结果</h4>
      <p v-if="busy" class="context-package-panel__loading">正在预览本轮范围…</p>
      <p v-else-if="errorMessage" class="context-package-panel__error" role="alert">{{ errorMessage }}</p>
      <template v-else-if="facts">
        <section class="context-facts__cards" aria-label="本轮带的卡片">
          <h5>本轮带的卡片</h5>
          <ul v-if="facts.groups.cards.length" class="context-package-panel__list">
            <li v-for="row in facts.groups.cards" :key="`${row.ref.id}@${row.ref.version}`" class="context-fact-card" :data-fact-card="row.ref.id">
              <span class="context-fact-card__id">{{ row.ref.id }}</span>
              <span class="context-fact-card__version">v{{ row.ref.version }}</span>
              <span class="context-fact-card__status" :data-fact-state="row.state" :data-fact-reason="row.state === 'unknown' ? row.reason : undefined">{{ factStatusText(row) }}</span>
            </li>
          </ul>
          <p v-else class="context-package-panel__empty">服务端这一轮没有带任何证据卡。</p>
        </section>

        <section class="context-facts__reasons" aria-label="选择理由">
          <h5>选择理由</h5>
          <ul v-if="facts.groups.reasons.length" class="context-package-panel__list">
            <li v-for="(reason, index) in facts.groups.reasons" :key="index" class="context-fact-reason">{{ reason }}</li>
          </ul>
          <p v-else class="context-package-panel__empty">服务端没有给出选择理由。</p>
        </section>

        <section class="context-facts__sources" aria-label="本轮带的来源">
          <h5>本轮带的来源</h5>
          <ul v-if="facts.groups.sources.length" class="context-package-panel__list">
            <li v-for="source in facts.groups.sources" :key="source.source_id" class="context-fact-source" :data-fact-source="source.source_id">
              <span class="context-fact-source__locator">{{ source.locator || source.source_id }}</span>
              <span class="context-fact-source__revision">资料版本 v{{ source.asset_revision }}</span>
              <span class="context-fact-source__permission">{{ source.model_use_allowed ? '模型使用已许可' : '模型使用未许可' }}</span>
            </li>
          </ul>
          <p v-else class="context-package-panel__empty">服务端这一轮没有带任何来源。</p>
        </section>

        <section class="context-facts__baseline" aria-label="版本基线">
          <h5>版本基线</h5>
          <p class="context-fact-baseline">项目版本 v{{ facts.groups.version_baseline.project_version }} · 规范修订 {{ facts.groups.version_baseline.spec_revision }} · 上下文修订 {{ facts.groups.version_baseline.context_revision }}</p>
          <p class="context-fact-baseline">章节版本 {{ facts.groups.version_baseline.chapter_version_id || '未关联' }} · 任务版本 {{ facts.groups.version_baseline.task_version ?? '未关联' }} · 模板 {{ facts.groups.version_baseline.template_id }} v{{ facts.groups.version_baseline.template_version }}</p>
        </section>

        <section class="context-facts__authorization" aria-label="授权结论与输出约束">
          <h5>授权结论与输出约束</h5>
          <p class="context-fact-authorization" :data-authorization-state="facts.groups.authorization.authorization_state">授权结论：{{ facts.groups.authorization.authorization_state }}</p>
          <ul v-if="facts.groups.authorization.output_constraints.length" class="context-package-panel__list">
            <li v-for="constraint in facts.groups.authorization.output_constraints" :key="constraint" class="context-fact-constraint">{{ constraint }}</li>
          </ul>
          <p v-else class="context-package-panel__empty">服务端没有给出输出约束。</p>
        </section>
      </template>
      <p v-else class="context-package-panel__empty">还没有预览结果。</p>
    </section>
  </section>
</template>

<style scoped>
.context-package-panel{border:1px solid #dce2eb;border-radius:10px;padding:14px 16px;min-width:0;color:#1f2933}
.context-package-panel__header h3{margin:0 0 4px;font-size:15px}
.context-package-panel__hint{margin:0 0 10px;font-size:12px;color:#667085;line-height:1.6}
.context-package-panel h4{margin:12px 0 6px;font-size:14px}
.context-package-panel h5{margin:10px 0 4px;font-size:13px}
.context-package-panel__list{margin:0;padding-left:18px;display:flex;flex-direction:column;gap:6px}
.context-package-panel__empty{margin:0;font-size:13px;color:#667085}
.context-package-panel__loading{margin:0;font-size:13px;color:#667085}
.context-package-panel__error{margin:0;font-size:13px;color:#a82d26;line-height:1.6;overflow-wrap:anywhere;word-break:break-word}
.context-intent{display:flex;gap:8px;align-items:baseline;flex-wrap:wrap;font-size:13px}
.context-intent__id{font-weight:600}
.context-intent__version,.context-intent__status{color:#667085;font-size:12px}
.context-intent__remove{font:inherit;font-size:12px;border:1px solid #c9d3df;border-radius:6px;padding:2px 8px;background:#fff;color:inherit;cursor:pointer}
.context-intent__remove:focus-visible{outline:3px solid #679ce5;outline-offset:2px}
.context-package-panel__add{display:flex;gap:8px;align-items:flex-end;flex-wrap:wrap;margin-top:8px}
.context-package-panel__add-label{display:flex;flex-direction:column;gap:4px;font-size:13px}
.context-package-panel__add-button{font:inherit;font-size:13px;border:1px solid #c9d3df;border-radius:6px;padding:6px 10px;background:#fff;color:inherit;cursor:pointer}
.context-package-panel__add-button:disabled{color:#98a2b3;cursor:not-allowed}
.context-package-panel__add-button:focus-visible{outline:3px solid #679ce5;outline-offset:2px}
.context-fact-card,.context-fact-source{display:flex;gap:8px;align-items:baseline;flex-wrap:wrap;font-size:13px}
.context-fact-card__id{font-weight:600}
.context-fact-card__version,.context-fact-source__revision,.context-fact-source__permission{color:#667085;font-size:12px}
.context-fact-reason,.context-fact-constraint{font-size:13px;line-height:1.6}
.context-fact-baseline{margin:4px 0;font-size:13px;line-height:1.6}
.context-fact-authorization{margin:4px 0;font-size:13px;line-height:1.6}
@media (max-width:480px){.context-package-panel{padding:10px 12px}.context-package-panel__add-button,.context-intent__remove{flex:1 1 auto}}
</style>
