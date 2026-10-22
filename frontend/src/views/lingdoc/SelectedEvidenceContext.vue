<!-- 已选证据上下文：常驻展示明确选定用于正文的每张证据卡（而非只有数量）。
     只读：不自行修改 selected、不写库、不调用模型；移除/回看都发事件给父组件处理。 -->
<script setup lang="ts">
import { onBeforeUnmount, onServerPrefetch, ref, watch } from 'vue'
import { listEvidenceCards, listResearchClaims } from '@/api/lingdoc/agentApi'
import type { VersionRef } from '@/api/lingdoc/agentApi'
import type { Project } from '@/api/lingdoc/workspace'
import type { SelectedEvidenceRow } from './selectedEvidenceContextState'
import { createRequestEpoch, isAccessDenied, matchSelectedRows, relationLabel, rowProblemText } from './selectedEvidenceContextState'

const props = withDefaults(defineProps<{ project: Project; currentUserId: string; selected: VersionRef[]; readonly?: boolean }>(), { readonly: false })
const emit = defineEmits<{ remove: [ref: VersionRef]; open: [ref: VersionRef] }>()

const epoch = createRequestEpoch()
const rows = ref<SelectedEvidenceRow[]>([])
const loading = ref(false)
const fatalError = ref('')

const refKey = (ref: VersionRef) => `${ref.id}@${ref.version}`

// 当前在途读取，SSR 预取与 watch 触发共享，避免 setup 期 immediate watch
// 与 serverPrefetch 各自发起一次读取。
let currentLoad: Promise<void> | null = null

function load(): Promise<void> {
  epoch.next()
  const mine = epoch.capture()
  // 先清空旧展示：不跨课题/身份/基线缓存回退，失败也不保留旧可读内容。
  rows.value = []
  fatalError.value = ''
  // 空集合也要结束"读取中"语义，否则上一代的 loading 会卡住。
  if (!props.selected.length) { loading.value = false; currentLoad = null; return Promise.resolve() }
  loading.value = true
  currentLoad = (async () => {
    try {
      const [cardResult, claimResult] = await Promise.all([listEvidenceCards(props.project.id), listResearchClaims(props.project.id)])
      if (!epoch.accepts(mine)) return // 晚响应丢弃，不回填旧数据
      // 匹配当前最新选中集合：读取期间被取消的 ref 不会被填回。
      rows.value = matchSelectedRows(props.selected, cardResult.data, claimResult.data)
    } catch (cause) {
      if (!epoch.accepts(mine)) return
      rows.value = []
      fatalError.value = isAccessDenied(cause)
        ? '当前无法核验：来源访问受限。已清空已选证据的展示，恢复授权后请重新读取。'
        : `读取失败：${(cause as { message?: string })?.message || '无法获取当前证据卡'}。已清空旧展示，请重新读取。`
    } finally {
      if (epoch.accepts(mine)) loading.value = false
    }
  })()
  return currentLoad
}

// 课题/身份/版本基线任一变化都重新读取；晚响应由代次丢弃。
watch([() => props.project.id, () => props.currentUserId, () => props.project.project_version, () => props.project.current_context_revision], () => { void load() }, { immediate: true })
// 选中集合变化：先按当前集合同步过滤旧展示（删除/清空/替换立即生效，不等读取、
// 不自动更改 selected 版本），再只为缺失就绪行的 ref 补读；空集合时使在途请求失效。
watch(() => props.selected.map(refKey).join('|'), () => {
  rows.value = rows.value.filter(row => props.selected.some(ref => refKey(ref) === refKey(row.ref)))
  if (!props.selected.length) { epoch.next(); loading.value = false; fatalError.value = ''; return }
  const ready = new Set(rows.value.filter(row => row.state === 'ready').map(row => refKey(row.ref)))
  if (props.selected.some(ref => !ready.has(refKey(ref)))) void load()
})

// SSR 渲染需要等只读取数完成；setup 期 immediate watch 已发起读取时直接
// 等在途请求，不重复读取。浏览器端由上面的 watch 驱动。
let serverPrefetched = false
if (typeof window === 'undefined') onServerPrefetch(() => {
  if (serverPrefetched) return currentLoad ?? undefined
  serverPrefetched = true
  return currentLoad ?? load()
})

// 卸载时停止旧代的"读取中"语义，晚响应不得再触碰状态。
onBeforeUnmount(() => { epoch.next(); loading.value = false; currentLoad = null })

const requestOpen = (row: SelectedEvidenceRow) => { emit('open', row.ref) }
const requestRemove = (row: SelectedEvidenceRow) => { emit('remove', row.ref) }

// 只读诊断面：供父组件与测试观察当前展示/读取状态、触发与按钮相同的
// open/remove 处理。不提供任何修改选中或数据的入口。
defineExpose({ rows, loading, fatalError, requestOpen, requestRemove })
</script>

<template>
  <section class="selected-evidence" aria-label="已选证据上下文">
    <h3>已选证据{{ rows.length ? `（${rows.length}）` : '' }}</h3>
    <p v-if="fatalError" class="problem" role="alert">{{ fatalError }}</p>
    <p v-else-if="loading" class="hint">正在读取当前证据卡…</p>
    <p v-else-if="!rows.length" class="hint">尚未选择用于正文的证据卡。</p>
    <ul v-if="rows.length" class="evidence-list">
      <li v-for="row in rows" :key="`${row.ref.id}@${row.ref.version}`" class="evidence-entry">
        <template v-if="row.state === 'ready' && row.card">
          <p class="claim-line">
            <span class="relation" :data-relation="row.card.support_type">{{ relationLabel(row.card.support_type) }}</span>
            <span class="claim-text">{{ row.claim?.text || '尚未关联主张' }}</span>
          </p>
          <p class="meta">证据卡 v{{ row.card.version }} · 范围：{{ row.card.scope || '未记录' }}</p>
          <details>
            <summary>局限与依据</summary>
            <p>局限：{{ row.card.limitations || '未记录' }}</p>
            <p v-if="row.card.gaps">缺口：{{ row.card.gaps }}</p>
            <p>原文定位 {{ row.card.anchor_refs.length }} 条 · 来源 {{ row.card.source_ids.length }} 个</p>
          </details>
        </template>
        <p v-else class="problem">{{ rowProblemText(row) }}</p>
        <div class="entry-actions">
          <button type="button" class="open-btn" :data-open="row.ref.id" @click="requestOpen(row)">回看证据</button>
          <button v-if="!readonly" type="button" class="remove-btn" :data-remove="row.ref.id" @click="requestRemove(row)">取消本次选用</button>
        </div>
      </li>
    </ul>
  </section>
</template>

<style scoped>
.selected-evidence{border:1px solid var(--td-component-border,#dce2eb);border-radius:10px;padding:14px 16px;min-width:0}
.selected-evidence h3{margin:0 0 8px;font-size:15px}
.hint{color:var(--td-text-color-secondary,#667085);margin:0;line-height:1.6}
.problem{color:var(--td-error-color,#a82d26);margin:0 0 8px;line-height:1.6;overflow-wrap:anywhere;word-break:break-word}
.evidence-list{list-style:none;margin:0;padding:0;display:flex;flex-direction:column;gap:10px}
.evidence-entry{border-top:1px solid var(--td-component-border,#e4e9f0);padding-top:10px;min-width:0}
.claim-line{margin:0 0 4px;line-height:1.6;display:flex;gap:8px;align-items:baseline;flex-wrap:wrap;min-width:0}
.relation{flex:none;font-size:12px;border:1px solid var(--td-component-border,#c9d3df);border-radius:999px;padding:1px 8px;background:var(--td-bg-color-secondarycontainer,#f4f7fa)}
.relation[data-relation=supports]{border-color:#9ec3a0;background:#eef7ee}
.relation[data-relation=refutes]{border-color:#d8a3a0;background:#fbeeee}
.relation[data-relation=background]{border-color:#c3cbd8;background:#f2f4f8}
.claim-text{min-width:0;overflow-wrap:anywhere;word-break:break-word}
.meta{margin:0 0 6px;font-size:12px;color:var(--td-text-color-secondary,#667085);overflow-wrap:anywhere;word-break:break-word}
details{margin:6px 0;font-size:13px}
details p{margin:4px 0;overflow-wrap:anywhere;word-break:break-word}
.entry-actions{display:flex;gap:8px;flex-wrap:wrap;margin-top:6px}
.entry-actions button{font:inherit;font-size:13px;border:1px solid var(--td-component-border,#c9d3df);border-radius:6px;padding:6px 10px;background:var(--td-bg-color-container,#fff);color:inherit;cursor:pointer}
.entry-actions button:focus-visible{outline:3px solid #679ce5;outline-offset:2px}
@media (max-width:480px){.selected-evidence{padding:10px 12px}.entry-actions button{flex:1 1 auto}}
</style>
