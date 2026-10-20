<template>
  <section class="candidate-panel" aria-label="AI 依赖候选">
    <header>
      <div>
        <h3>AI 依赖候选</h3>
        <p class="muted">候选不是依赖边：未确认的候选不进入正式依赖图，也不单独使章节失效或阻断导出。</p>
      </div>
      <button type="button" :disabled="loading" @click="refresh">{{ loading ? '读取中…' : '重新读取' }}</button>
    </header>

    <p v-if="error" role="alert" class="error">{{ error }}</p>
    <p v-if="message" role="status" class="notice">{{ message }}</p>
    <p v-if="!batches.length && !loading">当前没有待处理的依赖候选。</p>

    <article v-for="batch in batches" :key="batch.id" class="batch">
      <h4>候选批次</h4>
      <p class="muted">编号 {{ batch.id }} · 范围 {{ batch.scope?.reason || '未记录' }} · 项目版本 {{ batch.project_version }} · 基线 {{ batch.context_revision }} · {{ batch.reason }}</p>

      <article v-for="candidate in batch.candidates" :key="candidate.id" class="candidate">
        <h5>{{ kindLabel(candidate.source.kind) }}/{{ candidate.source.id }} @ {{ candidate.source.version }} → {{ kindLabel(candidate.target.kind) }}/{{ candidate.target.id }} @ {{ candidate.target.version }}</h5>
        <p>{{ candidate.relation }} · {{ actionLabel(candidate.action) }} · 置信度 {{ candidate.confidence }} · <strong>{{ statusLabel(candidate.status) }}</strong></p>
        <p><strong>依据片段</strong>：{{ candidate.evidence }}</p>
        <p><strong>建议理由</strong>：{{ candidate.reason }}</p>
        <p v-if="candidate.decision" class="muted">人工结论：{{ decisionLabel(candidate.decision) }} · {{ candidate.decision_reason }}<template v-if="candidate.superseded_relation">（已替换 {{ candidate.superseded_relation }} / {{ candidate.superseded_action }}）</template></p>
        <p v-if="candidate.status === 'stale'" class="notice">依据的对象版本已变化：该候选需要重新评估，不能直接确认。</p>

        <div v-if="!readonly && candidate.status === 'proposed'" class="actions">
          <label>人工理由<input v-model="reasons[candidate.id]" maxlength="2000" placeholder="确认、修正或拒绝这条候选的依据" /></label>
          <button type="button" :disabled="busy" @click="decide(candidate, 'confirm')">确认依赖</button>
          <button type="button" :disabled="busy" @click="openModify(candidate)">修正候选</button>
          <button type="button" :disabled="busy" @click="decide(candidate, 'reject')">拒绝候选</button>
        </div>

        <form v-if="!readonly && modifyingId === candidate.id" class="modify" @submit.prevent="decide(candidate, 'modify')">
          <label>关系<input v-model="modify.relation" maxlength="128" placeholder="留空保持当前" /></label>
          <label>影响动作<select v-model="modify.action"><option value="">保持当前</option><option v-for="action in impactActions" :key="action" :value="action">{{ actionLabel(action) }}</option></select></label>
          <label>置信度<input v-model="modify.confidence" type="number" min="0" max="1" step="0.05" placeholder="留空保持当前" /></label>
          <button type="submit" :disabled="busy">保存修正并确认</button>
          <button type="button" :disabled="busy" @click="closeModify">取消</button>
        </form>
      </article>

      <details v-if="batch.suppressed && batch.suppressed.length">
        <summary>本次未再次提出的候选（{{ batch.suppressed.length }}）</summary>
        <ul>
          <li v-for="(item, index) in batch.suppressed" :key="index">
            {{ kindLabel(item.source.kind) }}/{{ item.source.id }} @ {{ item.source.version }} → {{ kindLabel(item.target.kind) }}/{{ item.target.id }} @ {{ item.target.version }} · {{ item.relation }} / {{ actionLabel(item.action) }} · {{ suppressionLabel(item.reason) }}
          </li>
        </ul>
      </details>
    </article>
  </section>
</template>

<script setup lang="ts">
import { reactive, ref, watch } from 'vue'
import {
  decideDependencyCandidate,
  listDependencyCandidateBatches,
  type DependencyCandidate,
  type DependencyCandidateBatch,
  type DependencyCandidateDecision,
} from '@/api/lingdoc/dependencyCandidates'

// The AI-proposed dependency candidates (#252, AC-G3-15).
//
// The panel shows what a human needs in order to decide: the two objects, the
// object VERSIONS the proposal was made against, the excerpt that triggered
// it and the proposed rationale. The three decisions are the ones the
// dependency edge decision already uses: confirm, modify, reject.
//
// The status of a candidate is server-decided: the client never says whether
// a candidate was confirmed or refused, it only sends the decision.

const props = withDefaults(defineProps<{ projectId: string; readonly?: boolean }>(), { readonly: true })

const batches = ref<DependencyCandidateBatch[]>([])
const loading = ref(false)
const busy = ref(false)
const error = ref('')
const message = ref('')
const reasons = reactive<Record<string, string>>({})
const modifyingId = ref('')
const modify = reactive({ relation: '', action: '', confidence: '' })

const operationKeys = new Map<string, { payload: string; key: string }>()

const impactActions = ['REVALIDATE_EVIDENCE', 'RETRIEVE_CANDIDATES', 'REVIEW_REQUIRED', 'REGENERATE', 'RECOMPUTE', 'INVALIDATE_CONFIRMATION', 'BLOCK_RELEASE', 'NO_ACTION']
const kindLabels: Record<string, string> = {
  ProjectSpec: '研究条件', ProjectAsset: '资料', SourceAnchor: '原文锚点', EvidenceCard: '证据卡',
  ResearchClaim: '研究主张', ChapterVersion: '章节版本', TemplateCopy: '模板副本', RuleSet: '规则集',
  RuleBinding: '规则绑定', RouteNode: '路线节点', RouteEdge: '路线边', ResearchRun: '调研运行',
  ComputationReceipt: '计算回执',
}
const actionLabels: Record<string, string> = {
  REVALIDATE_EVIDENCE: '重新核验证据', RETRIEVE_CANDIDATES: '重新检索候选', REVIEW_REQUIRED: '需要人工复核',
  REGENERATE: '重新生成', RECOMPUTE: '重新计算', INVALIDATE_CONFIRMATION: '确认失效', BLOCK_RELEASE: '阻断交付',
  NO_ACTION: '只提示，不创建任务',
}
const statusLabels: Record<string, string> = {
  proposed: '待人工决定', confirmed: '已确认', modified: '已修正后确认', rejected: '已拒绝', stale: '需要重新评估',
}
const decisionLabels: Record<string, string> = { confirm: '确认', modify: '修正后确认', reject: '拒绝' }
const suppressionLabels: Record<string, string> = {
  rejected_in_same_version: '当前项目版本已拒绝，不再重复推荐',
  modified_in_same_version: '当前项目版本已修正，不再重复推荐',
  confirmed_in_same_version: '当前项目版本已确认',
  already_proposed: '当前项目版本已在等待决定',
}

function kindLabel(kind: string) { return kindLabels[kind] ?? kind }
function actionLabel(action: string) { return actionLabels[action] ?? action }
function statusLabel(status: string) { return statusLabels[status] ?? status }
function decisionLabel(decision: string) { return decisionLabels[decision] ?? decision }
function suppressionLabel(reason: string) { return suppressionLabels[reason] ?? reason }

function fail(cause: unknown) {
  const item = cause as { error?: { message?: string }; message?: string }
  error.value = item.error?.message ?? item.message ?? '操作未完成；请重新读取当前候选。'
}

async function refresh() {
  if (!props.projectId) return
  loading.value = true
  error.value = ''
  try {
    const result = await listDependencyCandidateBatches(props.projectId)
    batches.value = result.data ?? []
  } catch (cause) {
    fail(cause)
  } finally {
    loading.value = false
  }
}

// mutate keeps one idempotency key per slot while the payload is unchanged,
// and a new key as soon as the payload changes. A retry of the same request
// therefore replays, while a different correction is a new operation.
async function mutate<T>(slot: string, payload: unknown, call: (key: string) => Promise<T>): Promise<T | undefined> {
  const stamp = JSON.stringify(payload)
  let entry = operationKeys.get(slot)
  if (!entry || entry.payload !== stamp) {
    entry = { payload: stamp, key: `lingdoc-candidate-${crypto.randomUUID()}` }
    operationKeys.set(slot, entry)
  }
  busy.value = true
  error.value = ''
  try {
    return await call(entry.key)
  } catch (cause) {
    fail(cause)
    return undefined
  } finally {
    busy.value = false
  }
}

function openModify(candidate: DependencyCandidate) {
  modifyingId.value = candidate.id
  modify.relation = ''
  modify.action = ''
  modify.confidence = ''
}

function closeModify() {
  modifyingId.value = ''
  modify.relation = ''
  modify.action = ''
  modify.confidence = ''
}

async function decide(candidate: DependencyCandidate, decision: DependencyCandidateDecision) {
  const reason = (reasons[candidate.id] ?? '').trim()
  if (!reason) { error.value = '请填写人工判断理由。'; return }
  const input: Record<string, unknown> = { expected_version: candidate.version, decision, reason }
  if (decision === 'modify') {
    const relation = modify.relation.trim()
    const confidence = Number(modify.confidence)
    const confidenceValue = modify.confidence.trim() !== '' && Number.isFinite(confidence) ? confidence : undefined
    if (!relation && !modify.action && confidenceValue === undefined) {
      error.value = '修正候选至少要改动关系、影响动作或置信度中的一项。'
      return
    }
    if (relation) input.relation = relation
    if (modify.action) input.action = modify.action
    if (confidenceValue !== undefined) input.confidence = confidenceValue
  }
  const result = await mutate(`candidate-${candidate.id}`, input, key => decideDependencyCandidate(props.projectId, candidate.id, input as never, key))
  if (!result) return
  message.value = decision === 'reject' ? '候选已拒绝，正式依赖图未改变。' : '候选的人工结论已写入正式依赖图。'
  closeModify()
  await refresh()
}

watch(() => props.projectId, () => { void refresh() }, { immediate: true })
</script>

<style scoped>
.candidate-panel { display: grid; gap: 12px; }
.candidate-panel header { display: flex; gap: 12px; align-items: flex-start; justify-content: space-between; }
.batch { border: 1px solid #dbe5dd; border-radius: 8px; padding: 10px 12px; display: grid; gap: 10px; }
.candidate { border-top: 1px solid #eef2ee; padding-top: 8px; display: grid; gap: 4px; }
.candidate h5 { margin: 0; }
.actions { display: flex; flex-wrap: wrap; gap: 8px; align-items: end; }
.modify { display: flex; flex-wrap: wrap; gap: 8px; align-items: end; }
.muted { color: #607165; }
.error { color: #a13d3d; }
.notice { color: #8a6d3b; }
</style>
