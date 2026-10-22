<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import {
  applyReviewReport,
  createReviewReport,
  getReviewReport,
  rejectReviewReport,
  type ReviewDecision,
  type ReviewReport,
  type WorkingCopy,
} from '@/api/lingdoc/workspace'
import { newIdempotencyKey } from '@/api/lingdoc/candidateAdoption'

/**
 * 语言评审面板：发起一份评审报告，预览它，拒绝它，或者逐条处置后应用它。
 *
 * 报告不是候选：它没有可采纳物，产出是一份待用户逐条处置的意见清单。所以这
 * 个面板的三个动作是「发起 / 预览 / 拒绝 / 逐条处置后应用」，而不是「采纳」。
 *
 * 逐条处置是必填的：每一条评审意见都要有处置与理由，缺一即拒。面板因此不把
 * 「应用」做成一个能点的按钮——它把处置做齐之前一直是禁用的。
 */
const props = withDefaults(defineProps<{
  projectId: string
  chapterId: string
  /** 目标范围：选段或整章。 */
  scope?: 'section' | 'chapter'
  /**
   * 取当前选区。
   *
   * 它是一个**函数**，不是一个值：选区的映射要经编辑器的 UTF-16 映射与三重
   * 验证，那不是渲染页面上随手就能算的便宜东西。页面在用户点「发起语言评审」
   * 的那一刻才问一次，渲染时不问。
   */
  resolveSelection?: () => { start_utf16: number; end_utf16: number; selected_text: string } | undefined
  expectedSpecRevision: number
  expectedWorkingCopyRevision: number
  baseChapterVersionId?: string | null
  /** 补充说明：它不参与动作判定。 */
  instruction?: string
  readonly?: boolean
}>(), { scope: 'section', baseChapterVersionId: null, instruction: '', readonly: false })

const emit = defineEmits<{
  applied: [copy: WorkingCopy]
}>()

const scope = ref<'section' | 'chapter'>(props.scope)
const note = ref(props.instruction)
const report = ref<ReviewReport | null>(null)
const busy = ref(false)
const error = ref('')
const notice = ref('')

/** 逐条处置：finding id → 处置与理由。 */
const decisions = ref<Record<string, { disposition: ReviewDecision['disposition']; reason: string }>>({})

watch(() => props.scope, value => { scope.value = value })

// 换一份报告，处置就清空：上一份的处置说的不是这一份的意见。
watch(report, () => { decisions.value = {} })

const scopeLabels: Record<string, string> = { section: '选段', chapter: '整章' }
const dispositionLabels: Record<string, string> = {
  resolved: '已处理',
  retained_warning: '保留为警示',
}

/** 撤权状态。空值表示这次运行没有交出可用的授权判定。 */
const authorizationLabels: Record<string, string> = {
  '': '未判定',
  fresh: '授权有效',
  stale: '授权状态已变化',
  not_authorized: '授权已失效',
}

function authorizationLabel(state: string): string {
  return authorizationLabels[state] ?? state
}

function dispositionOf(id: string): ReviewDecision['disposition'] | '' {
  return decisions.value[id]?.disposition ?? ''
}

function reasonOf(id: string): string {
  return decisions.value[id]?.reason ?? ''
}

function setDisposition(id: string, disposition: ReviewDecision['disposition']) {
  decisions.value = { ...decisions.value, [id]: { disposition, reason: reasonOf(id) } }
}

function setReason(id: string, reason: string) {
  const disposition = dispositionOf(id) || 'resolved'
  decisions.value = { ...decisions.value, [id]: { disposition, reason } }
}

/**
 * 每一条都要有处置与理由。
 *
 * 缺任何一条，服务端就拒这一份应用——那不是「少写一句说明」，是这份意见还
 * 没有被人看过。
 */
const decisionsComplete = computed(() => {
  const findings = report.value?.findings ?? []
  if (!findings.length) return false
  return findings.every(item => dispositionOf(item.id) !== '' && reasonOf(item.id).trim() !== '')
})

const reviewDecisions = computed<ReviewDecision[]>(() =>
  (report.value?.findings ?? [])
    .filter(item => dispositionOf(item.id) !== '' && reasonOf(item.id).trim() !== '')
    .map(item => ({
      review_item_id: item.id,
      disposition: dispositionOf(item.id) as ReviewDecision['disposition'],
      reason: reasonOf(item.id).trim(),
    })),
)

const canCreate = computed(() => !props.readonly && !busy.value)

function errorMessageOf(cause: unknown): string {
  const item = cause as { error?: { message?: string }; message?: string }
  return item?.error?.message ?? item?.message ?? '操作未完成，请重新读取后重试。'
}

async function create() {
  if (busy.value || props.readonly) return
  // 选段范围要问页面要一次选区：没有就明说，不猜范围。
  const selection = scope.value === 'section' ? props.resolveSelection?.() : undefined
  if (scope.value === 'section' && !selection) {
    error.value = '选段范围需要先在正文中选中一段；也可以改成整章范围。'
    return
  }
  busy.value = true
  error.value = ''
  notice.value = ''
  try {
    const result = await createReviewReport(props.projectId, props.chapterId, {
      scope: scope.value,
      selection: scope.value === 'section' ? selection : undefined,
      expected_spec_revision: props.expectedSpecRevision,
      expected_working_copy_revision: props.expectedWorkingCopyRevision,
      base_chapter_version_id: props.baseChapterVersionId ?? null,
      instruction: note.value.trim(),
    }, newIdempotencyKey())
    report.value = result.data
    notice.value = '评审报告已生成。逐条处置后才能应用；不想要就拒绝它。'
  } catch (cause) {
    error.value = errorMessageOf(cause)
  } finally {
    busy.value = false
  }
}

async function preview() {
  if (busy.value || !report.value) return
  busy.value = true
  error.value = ''
  try {
    const result = await getReviewReport(props.projectId, report.value.report_id)
    report.value = result.data
    notice.value = '已读回最新报告。'
  } catch (cause) {
    error.value = errorMessageOf(cause)
  } finally {
    busy.value = false
  }
}

async function reject() {
  if (busy.value || !report.value) return
  busy.value = true
  error.value = ''
  try {
    const result = await rejectReviewReport(props.projectId, report.value.report_id, newIdempotencyKey())
    report.value = result.data
    notice.value = '已拒绝这份报告，工作副本一个字符都没有改动。'
  } catch (cause) {
    error.value = errorMessageOf(cause)
  } finally {
    busy.value = false
  }
}

async function apply() {
  if (busy.value || props.readonly || !report.value || !decisionsComplete.value) return
  busy.value = true
  error.value = ''
  try {
    const result = await applyReviewReport(props.projectId, props.chapterId, report.value.report_id, {
      expected_spec_revision: props.expectedSpecRevision,
      expected_working_copy_revision: props.expectedWorkingCopyRevision,
      base_chapter_version_id: props.baseChapterVersionId ?? null,
      review_decisions: reviewDecisions.value,
    }, newIdempotencyKey())
    notice.value = '评审报告已应用，待核事项已写入工作副本。'
    emit('applied', result.data)
  } catch (cause) {
    error.value = errorMessageOf(cause)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <section class="review-report-panel" aria-label="语言评审报告" :aria-busy="busy">
    <h3 class="review-report-panel__heading">语言评审</h3>

    <div class="review-report-panel__start">
      <label>
        评审范围
        <select v-model="scope" :disabled="busy || readonly" data-scope="scope">
          <option value="section">{{ scopeLabels.section }}</option>
          <option value="chapter">{{ scopeLabels.chapter }}</option>
        </select>
      </label>
      <label>
        补充说明
        <input v-model="note" type="text" :disabled="busy || readonly" maxlength="2000" data-field="instruction" />
      </label>
      <p v-if="scope === 'section'" class="review-report-panel__hint">
        选段范围需要在正文中选中一段（发起时按当时的选区计算）；也可以改成整章范围。
      </p>
      <button type="button" :disabled="!canCreate" @click="create">发起语言评审</button>
    </div>

    <p v-if="error" class="review-report-panel__error" role="alert">{{ error }}</p>
    <p v-if="notice" class="review-report-panel__notice" role="status">{{ notice }}</p>

    <section v-if="report" class="review-report-panel__report">
      <h4>评审报告</h4>
      <dl class="review-report-panel__facts">
        <dt>报告版本</dt><dd data-fact="report_version">{{ report.report_version || '未提供' }}</dd>
        <dt>基线版本</dt>
        <dd data-fact="baseline">
          工作副本第 {{ report.baseline?.working_copy_revision }} 版 · 研究条件第 {{ report.baseline?.spec_revision }} 版
          · 章节版本 {{ report.baseline?.base_chapter_version_id || '（无）' }}
        </dd>
        <dt>撤权状态</dt>
        <dd data-fact="authorization_state">{{ authorizationLabel(report.authorization_state) }}</dd>
        <dt>来源</dt>
        <dd data-fact="sources">{{ report.source_ids.length ? report.source_ids.join('、') : '无' }}</dd>
        <dt>评审范围</dt>
        <dd data-fact="scope">{{ scopeLabels[report.scope] ?? report.scope }}</dd>
        <dt>报告状态</dt>
        <dd data-fact="state">{{ report.state }}</dd>
      </dl>

      <h5>评审意见（{{ report.findings.length }} 条）</h5>
      <p v-if="!report.findings.length" class="review-report-panel__hint">这份报告没有提出待处置的意见。</p>
      <ul v-else class="review-report-panel__findings">
        <li v-for="item in report.findings" :key="item.id" :data-finding-id="item.id">
          <p class="review-report-panel__statement">{{ item.statement }}</p>
          <label>
            处置
            <select
              :value="dispositionOf(item.id)"
              :disabled="busy || readonly"
              :data-disposition="item.id"
              @change="setDisposition(item.id, ($event.target as HTMLSelectElement).value as ReviewDecision['disposition'])"
            >
              <option value="">未处置</option>
              <option value="resolved">{{ dispositionLabels.resolved }}</option>
              <option value="retained_warning">{{ dispositionLabels.retained_warning }}</option>
            </select>
          </label>
          <label>
            理由
            <input
              :value="reasonOf(item.id)"
              type="text"
              :disabled="busy || readonly"
              maxlength="2000"
              :data-reason="item.id"
              @input="setReason(item.id, ($event.target as HTMLInputElement).value)"
            />
          </label>
        </li>
      </ul>

      <div class="review-report-panel__actions">
        <button type="button" :disabled="busy" @click="preview">重新读取报告</button>
        <button type="button" :disabled="busy" @click="reject">拒绝这份报告</button>
        <button type="button" :disabled="busy || readonly || !decisionsComplete" @click="apply">
          {{ decisionsComplete ? '逐条处置后应用' : '逐条处置后应用（还有意见未处置）' }}
        </button>
      </div>
    </section>
  </section>
</template>

<style scoped>
.review-report-panel { padding: 16px; border: 1px solid #e2e8f0; border-radius: 12px; background: #f8fafc; color: #0f172a; }
.review-report-panel__heading { margin: 0 0 12px; font-size: 14px; }
.review-report-panel__start { display: grid; gap: 8px; }
.review-report-panel__start label, .review-report-panel__findings label { display: grid; gap: 4px; font-size: 13px; color: #475569; }
.review-report-panel__facts { display: grid; grid-template-columns: max-content 1fr; gap: 4px 12px; margin: 12px 0; font-size: 13px; }
.review-report-panel__facts dt { color: #64748b; }
.review-report-panel__facts dd { margin: 0; }
.review-report-panel__findings { margin: 0; padding-left: 20px; font-size: 13px; }
.review-report-panel__findings li { margin-bottom: 10px; }
.review-report-panel__statement { margin: 0 0 6px; }
.review-report-panel__actions { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 12px; }
.review-report-panel__hint { margin: 8px 0 0; color: #64748b; font-size: 13px; }
.review-report-panel__notice { margin: 8px 0 0; color: #166534; font-size: 13px; }
.review-report-panel__error { margin: 8px 0 0; color: #b91c1c; font-size: 13px; }
button { padding: 8px 14px; border: 1px solid #cbd5e1; border-radius: 8px; background: #fff; cursor: pointer; }
button:disabled { cursor: not-allowed; opacity: .5; }
</style>
