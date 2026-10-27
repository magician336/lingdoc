<script setup lang="ts">
// 规则建议面板（Spec #141 的前端面）。模型只提供建议与解释：面板展示建议
// 标识、建议边界（模板/RuleSet/项目版本与资料范围）、接受与拒绝的人工确认、
// 失败原因与重试、以及 stale 提示；接受只落草稿，发布仍走规则管理员的
// 确定性校验（服务端状态机，面板不复制）。
import { computed, ref } from 'vue'
import { newIdempotencyKey } from '@/api/lingdoc/candidateAdoption'
import {
  acceptRuleSuggestion,
  listRuleSuggestions,
  rejectRuleSuggestion,
  requestRuleSuggestion,
  type RuleSuggestionRun,
} from '@/api/lingdoc/templateGovernance'

const props = defineProps<{
  projectId: string
  canManageSuggestions: boolean
}>()

const runs = ref<RuleSuggestionRun[]>([])
const loading = ref(false)
const failure = ref('')
const busy = ref(false)
const pendingAction = ref<'' | 'accept' | 'reject'>('')
const pendingRunId = ref('')
const actionNote = ref('')

function isStale(run: RuleSuggestionRun): boolean {
  return run.status === 'stale' || (run as unknown as Record<string, unknown>).stale === true
}

function isPending(run: RuleSuggestionRun): boolean {
  return pendingRunId.value === run.id && pendingAction.value !== ''
}

async function load() {
  loading.value = true
  failure.value = ''
  try {
    runs.value = (await listRuleSuggestions(props.projectId)).data
  } catch (error: any) {
    failure.value = error?.error?.message ?? error?.message ?? String(error)
  } finally {
    loading.value = false
  }
}

async function request(scopeBasis: Record<string, unknown>) {
  busy.value = true
  failure.value = ''
  try {
    const run = (await requestRuleSuggestion(
      { project_id: props.projectId, ...(scopeBasis as any) } as any,
      newIdempotencyKey(),
    )).data
    runs.value = [run, ...runs.value.filter((existing) => existing.id !== run.id)]
  } catch (error: any) {
    // 失败原因保留在面板上，供重试或手工处理；不静默清空。
    failure.value = error?.error?.message ?? error?.message ?? String(error)
  } finally {
    busy.value = false
  }
}

// 接受/拒绝都必须先经过一次显式确认（pendingAction 置位后再次调用才执行），
// 未确认的调用只置位而不发请求。
async function act(run: RuleSuggestionRun, action: 'accept' | 'reject', payload: Record<string, unknown>) {
  if (isStale(run)) {
    failure.value = '该建议已过期（模板或 RuleSet 已变化），不能接受到当前版本'
    return
  }
  if (pendingAction.value !== action || pendingRunId.value !== run.id) {
    pendingAction.value = action
    pendingRunId.value = run.id
    actionNote.value = ''
    return
  }
  busy.value = true
  failure.value = ''
  try {
    const saved = (action === 'accept'
      ? await acceptRuleSuggestion(props.projectId, run.id, {
          expected_revision: run.revision,
          ruleset_id: String(payload.ruleset_id ?? run.scope.ruleset.ruleset_id),
          version: String(payload.version ?? 'suggestion-draft'),
          bindings: (payload.bindings ?? []) as any,
          reason: actionNote.value || '人工复核后接受建议草稿',
        }, newIdempotencyKey())
      : await rejectRuleSuggestion(props.projectId, run.id, {
          expected_revision: run.revision,
          reason: actionNote.value || '人工复核后拒绝建议',
        }, newIdempotencyKey())).data
    runs.value = runs.value.map((existing) => (existing.id === saved.id ? saved : existing))
    pendingAction.value = ''
    pendingRunId.value = ''
    actionNote.value = ''
  } catch (error: any) {
    failure.value = error?.error?.message ?? error?.message ?? String(error)
  } finally {
    busy.value = false
  }
}

function cancelPending() {
  // 取消只清空本地确认态，不发任何请求。
  pendingAction.value = ''
  pendingRunId.value = ''
  actionNote.value = ''
}

const suggestionCount = computed(() => runs.value.length)
const staleCount = computed(() => runs.value.filter((run) => isStale(run)).length)

defineExpose({
  runs, loading, failure, busy, pendingAction, pendingRunId, actionNote,
  suggestionCount, staleCount, isStale, isPending,
  load, request, act, cancelPending,
})
</script>

<template>
  <section class="rule-suggestion-panel" data-panel="rule-suggestions">
    <h3>规则建议（模型辅助，仅草稿）</h3>
    <p v-if="failure" class="suggestion-failure" role="alert">失败：{{ failure }}</p>
    <p v-if="staleCount > 0" class="suggestion-stale-hint">
      有 {{ staleCount }} 条建议已过期：模板或 RuleSet 变化后旧建议不能接受到当前版本。
    </p>
    <p v-if="loading" class="suggestion-loading">加载中…</p>
    <p v-if="suggestionCount === 0 && !loading" class="suggestion-empty">当前没有规则建议。</p>
    <ul>
      <li v-for="run in runs" :key="run.id" :data-suggestion-run="run.id"
          :class="{ 'is-stale': isStale(run), 'is-suggestion': true }">
        <span class="suggestion-badge">建议 · {{ run.status }}</span>
        <span v-if="isStale(run)" class="suggestion-stale">已过期，仅可查看</span>
        <span class="suggestion-scope">
          模板 {{ run.scope.template_id }}/{{ run.scope.template_version }} ·
          RuleSet {{ run.scope.ruleset.ruleset_id }}/{{ run.scope.ruleset.version }} ·
          目标 {{ run.scope.target_kind }}:{{ run.scope.target_id }}
        </span>
        <span v-if="run.output" class="suggestion-explanation">{{ run.output.explanation }}</span>
        <button v-if="canManageSuggestions && !isStale(run)" :disabled="busy || (isPending(run) && pendingAction !== 'accept')"
                data-action="accept" @click="act(run, 'accept', {})">
          {{ isPending(run) && pendingAction === 'accept' ? '确认接受（落草稿）' : '接受' }}
        </button>
        <button v-if="canManageSuggestions && !isStale(run)" :disabled="busy || (isPending(run) && pendingAction !== 'reject')"
                data-action="reject" @click="act(run, 'reject', {})">
          {{ isPending(run) && pendingAction === 'reject' ? '确认拒绝' : '拒绝' }}
        </button>
        <button v-if="isPending(run)" data-action="cancel" @click="cancelPending">取消</button>
      </li>
    </ul>
  </section>
</template>
