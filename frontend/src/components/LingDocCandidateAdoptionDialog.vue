<script setup lang="ts">
import { computed, ref } from 'vue'
import type {
  AcceptCandidateInput,
  AdoptionConfirmation,
  AdoptionTarget,
  Candidate,
  Chapter,
} from '@/api/lingdoc/candidateAdoption'
import { acceptCandidate, listChapters, newIdempotencyKey } from '@/api/lingdoc/candidateAdoption'
import { keyForAttempt, type AdoptionAttempt } from '@/views/lingdoc/candidateAdoption'
import { diffWikiLines, type WikiDiffLine } from '@/utils/wikiLineDiff'
import { useAuthStore } from '@/stores/auth'

const props = withDefaults(defineProps<{
  projectId: string
  chapter: Chapter
  candidate: Candidate
  expectedSpecRevision: number
  open?: boolean
}>(), { open: true })

const emit = defineEmits<{
  adopted: [chapter: Chapter]
  cancel: []
}>()

const auth = useAuthStore()

// 三值来源文案。来源标注是候选的事实，不由界面猜。
const originLabels: Record<string, string> = {
  human: '人工撰写',
  ai_generated: 'AI 生成',
  ai_assisted_human: 'AI 辅助整理',
}

function originLabel(origin?: string) {
  if (!origin) return '未标注'
  return originLabels[origin] ?? origin
}

// 采纳目标的文案。词表归服务端所有，这里只写显示名。
const targetLabels: Record<string, string> = {
  working_copy_partial: '工作副本·选段',
  working_copy_whole: '工作副本·整段',
  formal_version_whole: '正式版本·整段',
}

function targetLabel(target: AdoptionTarget) {
  return targetLabels[target] ?? target
}

// 采纳目标。
//
// 动作自己声明它允许哪些目标，服务端据此判定越权（越权目标被拒）。这里只列
// 整段两个目标：部分采纳要一段选区，而选区是工作副本编辑器的事。
const wholeTargets: AdoptionTarget[] = ['formal_version_whole', 'working_copy_whole']
const target = ref<AdoptionTarget>('formal_version_whole')

// 确认时刻在对话框打开时定下，重试不改。
//
// 确认时刻每次都变的话，同一次采纳的重试会换一把幂等键，整章替换就做两遍。
const confirmedAt = ref(new Date().toISOString())

const submitting = ref(false)
const errorMessage = ref('')
const accepted = ref(false)
const refreshError = ref('')
const hasExistingVersion = computed(() => props.chapter.current_version_id !== null)
const attempt = ref<AdoptionAttempt | null>(null)

// 确认载荷：动作、采纳目标、确认时刻、确认人、来源。
//
// 它不是界面上的一个勾选框，而是请求体的一部分：缺任何一项，服务层拒。
const confirmation = computed<AdoptionConfirmation>(() => ({
  writing_action: props.candidate.writing_action as string,
  adoption_target: target.value,
  confirmed_at: confirmedAt.value,
  confirmed_by: auth.currentUserId,
  origin: props.candidate.origin as AdoptionConfirmation['origin'],
}))

// 候选必须记下自己是怎么来的，确认人也必须是真的。缺任何一项就不发请求。
const confirmationReady = computed(() => Boolean(
  props.candidate.writing_action && props.candidate.origin && auth.currentUserId,
))

const isWholeTarget = computed(() => target.value !== 'working_copy_partial')

// 替换范围：候选正文将替换工作副本正文的哪一段。
// 整段采纳时是整篇；部分采纳时范围来自确认载荷的 scope。
const replacementRange = computed(() => {
  const scope = confirmation.value.scope
  if (scope) return `第 ${scope.start_utf16}–${scope.end_utf16} 个 UTF-16 码元`
  return '整篇（工作副本正文全文）'
})

// 行级差异：当前章节正文 → 候选正文。
//
// diff 在前端算：服务端不新增 diff 字段，diff 只用于展示，不参与采纳判定。
const diffLines = computed<WikiDiffLine[]>(() => diffWikiLines(
  props.chapter.body_markdown ?? '',
  props.candidate.body_markdown ?? '',
))

function diffMark(type: WikiDiffLine['type']) {
  if (type === 'add') return '+'
  if (type === 'del') return '-'
  return ' '
}

// 签名与请求体出自**同一个**对象。分开构造时，改了请求却忘了改签名的那种错，
// 症状是「同一个操作键对应不同请求」——而两处代码各自看起来都对。
const input = computed<AcceptCandidateInput>(() => ({
  candidate_id: props.candidate.id,
  expected_chapter_version_id: props.chapter.current_version_id,
  expected_spec_revision: props.expectedSpecRevision,
  replace_existing: props.chapter.current_version_id !== null,
  confirmation: confirmation.value,
}))

async function readAcceptedChapter() {
  try {
    const chapters = await listChapters(props.projectId)
    const refreshed = chapters.data.find(item => item.id === props.chapter.id)
    if (!refreshed) throw new Error('章节未出现在读取结果中')
    emit('adopted', refreshed)
  } catch {
    refreshError.value = '候选已采纳，但章节状态读取失败。请重新读取；无需重复采纳。'
  }
}

async function adopt() {
  if (submitting.value || accepted.value) return
  if (!confirmationReady.value) {
    errorMessage.value = '这个候选没有记录产生它的动作或来源，无法确认采纳。请用动作化路径重新运行后再采纳。'
    return
  }
  errorMessage.value = ''
  submitting.value = true
  try {
    attempt.value = keyForAttempt(attempt.value, input.value, newIdempotencyKey)
    await acceptCandidate(props.projectId, props.chapter.id, input.value, attempt.value.key)
    accepted.value = true
    await readAcceptedChapter()
  } catch (error: any) {
    errorMessage.value = error?.message || '候选采纳失败，请重新读取后重试。'
  } finally {
    submitting.value = false
  }
}

async function retryRead() {
  if (!accepted.value || submitting.value) return
  submitting.value = true
  refreshError.value = ''
  try { await readAcceptedChapter() }
  finally { submitting.value = false }
}

function cancel() {
  if (!submitting.value && !accepted.value) emit('cancel')
}
</script>

<template>
  <div v-if="open !== false" class="candidate-adoption" role="dialog" aria-modal="true" aria-labelledby="candidate-adoption-title">
    <div class="candidate-adoption__panel">
      <header>
        <div>
          <p class="candidate-adoption__eyebrow">候选预览</p>
          <h2 id="candidate-adoption-title">采纳「{{ chapter.title }}」</h2>
        </div>
        <button type="button" class="candidate-adoption__close" :disabled="submitting || accepted" aria-label="取消" @click="cancel">×</button>
      </header>

      <p v-if="hasExistingVersion" class="candidate-adoption__warning">
        当前章节已有版本。采纳会整章替换正文、引用和待核项，旧版本仍会保留。请确认后继续。
      </p>
      <p v-else class="candidate-adoption__hint">当前章节为空，将创建一个新的章节版本。</p>

      <section class="candidate-adoption__preview">
        <div class="candidate-adoption__column">
          <h3>候选正文</h3>
          <pre>{{ candidate.body_markdown }}</pre>
        </div>
        <div class="candidate-adoption__column">
          <h3>来源</h3>
          <p>{{ candidate.source_ids.length ? candidate.source_ids.join('、') : '无' }}</p>
          <p>来源标注：{{ originLabel(candidate.origin) }}</p>
        </div>
        <div class="candidate-adoption__column candidate-adoption__column--unconfirmed">
          <h3>待核事项（未确认的依据）</h3>
          <p v-if="candidate.review_items.length" class="candidate-adoption__unconfirmed-hint">
            以下是候选自己提出的、尚未确认的依据。它们不是正式依据，采纳后需要重新确认。
          </p>
          <ul v-if="candidate.review_items.length">
            <li v-for="item in candidate.review_items" :key="item.id">{{ item.statement }}</li>
          </ul>
          <p v-else>待核事项：无</p>
        </div>
        <div class="candidate-adoption__basis">
          <p>研究条件：第 {{ candidate.basis.spec_revision }} 版；模板 {{ candidate.basis.template_id }} / {{ candidate.basis.template_version }}</p>
          <p>规则集：{{ candidate.basis.ruleset_hash || '未提供' }}</p>
          <p>资料版本：{{ candidate.basis.asset_versions.length ? candidate.basis.asset_versions.map((item) => `${item.asset_id}@${item.asset_revision}`).join('、') : '无' }}</p>
        </div>
      </section>

      <section class="candidate-adoption__confirmation">
        <h3>采纳确认</h3>
        <p>动作：{{ confirmation.writing_action || '未记录' }}</p>
        <p>写入目标：{{ targetLabel(target) }}</p>
        <p>确认人：{{ confirmation.confirmed_by || '未识别' }}</p>
        <p>确认时刻：{{ confirmedAt }}</p>
        <p>来源标注：{{ originLabel(candidate.origin) }}</p>
        <p v-if="!confirmationReady" class="candidate-adoption__error" role="alert">
          这个候选没有记录产生它的动作或来源，无法确认采纳。请用动作化路径重新运行后再采纳。
        </p>
      </section>

      <section v-if="isWholeTarget" class="candidate-adoption__replacement">
        <h3>替换范围</h3>
        <p>替换范围：{{ replacementRange }}</p>
        <ul class="candidate-adoption__diff">
          <li
            v-for="(line, index) in diffLines"
            :key="index"
            :class="`candidate-adoption__diff--${line.type}`"
          ><span class="candidate-adoption__diff-mark">{{ diffMark(line.type) }}</span>{{ line.text }}</li>
        </ul>
      </section>

      <p v-if="errorMessage" class="candidate-adoption__error" role="alert">{{ errorMessage }}</p>
      <section v-if="accepted" class="candidate-adoption__hint" role="status">
        <p>候选已采纳。</p>
        <p v-if="refreshError">{{ refreshError }}</p>
        <button v-if="refreshError" type="button" :disabled="submitting" @click="retryRead">{{ submitting ? '重新读取中…' : '重新读取章节状态' }}</button>
      </section>

      <footer>
        <button type="button" class="candidate-adoption__secondary" :disabled="submitting || accepted" @click="cancel">取消</button>
        <button type="button" class="candidate-adoption__primary" :disabled="submitting || accepted || !confirmationReady" @click="adopt">
          {{ accepted ? '已采纳' : submitting ? '采纳中…' : hasExistingVersion ? '确认整章替换' : '采纳候选' }}
        </button>
      </footer>
    </div>
  </div>
</template>

<style scoped>
.candidate-adoption { position: fixed; inset: 0; z-index: 1000; display: grid; place-items: center; padding: 24px; background: rgb(15 23 42 / 0.48); }
.candidate-adoption__panel { width: min(720px, 100%); max-height: min(760px, 92vh); overflow: auto; padding: 24px; border: 1px solid rgb(148 163 184 / 0.35); border-radius: 18px; background: #fff; color: #0f172a; box-shadow: 0 24px 80px rgb(15 23 42 / 0.24); }
header, footer { display: flex; align-items: center; justify-content: space-between; gap: 16px; }
.candidate-adoption__eyebrow { margin: 0 0 4px; color: #64748b; font-size: 12px; letter-spacing: .08em; text-transform: uppercase; }
h2, h3 { margin: 0; }
.candidate-adoption__close { border: 0; background: transparent; color: #64748b; font-size: 28px; cursor: pointer; }
.candidate-adoption__warning, .candidate-adoption__hint { margin: 20px 0; padding: 12px 14px; border-radius: 10px; }
.candidate-adoption__warning { background: #fff7ed; color: #9a3412; }
.candidate-adoption__hint { background: #f0fdf4; color: #166534; }
.candidate-adoption__preview { display: grid; gap: 12px; padding: 16px; border: 1px solid #e2e8f0; border-radius: 12px; background: #f8fafc; }
.candidate-adoption__column { padding: 10px 12px; border: 1px solid #e2e8f0; border-radius: 10px; background: #fff; }
.candidate-adoption__column--unconfirmed { border-color: #fed7aa; background: #fff7ed; }
.candidate-adoption__unconfirmed-hint { color: #9a3412; }
.candidate-adoption__basis { padding: 10px 12px; border-radius: 10px; background: #f1f5f9; }
.candidate-adoption__preview pre { max-height: 260px; overflow: auto; white-space: pre-wrap; font: inherit; line-height: 1.6; }
.candidate-adoption__preview p { margin: 8px 0 0; color: #475569; font-size: 13px; }
.candidate-adoption__reviews ul { margin: 6px 0 0; padding-left: 20px; color: #475569; font-size: 13px; }
.candidate-adoption__confirmation { margin-top: 16px; padding: 12px 14px; border: 1px solid #bfdbfe; border-radius: 12px; background: #eff6ff; }
.candidate-adoption__confirmation p { margin: 6px 0 0; color: #1e3a8a; font-size: 13px; }
.candidate-adoption__replacement { margin-top: 16px; padding: 12px 14px; border: 1px solid #e2e8f0; border-radius: 12px; background: #f8fafc; }
.candidate-adoption__replacement p { margin: 6px 0 0; color: #475569; font-size: 13px; }
.candidate-adoption__diff { margin: 8px 0 0; padding: 0; list-style: none; max-height: 260px; overflow: auto; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 12px; line-height: 1.6; }
.candidate-adoption__diff li { white-space: pre-wrap; color: #334155; }
.candidate-adoption__diff--add { color: #166534; background: #f0fdf4; }
.candidate-adoption__diff--del { color: #9a3412; background: #fff7ed; }
.candidate-adoption__diff-mark { display: inline-block; width: 1.2em; color: #94a3b8; }
.candidate-adoption__error { color: #b91c1c; }
footer { justify-content: flex-end; margin-top: 24px; }
footer button { min-width: 96px; padding: 10px 16px; border-radius: 9px; cursor: pointer; }
.candidate-adoption__secondary { border: 1px solid #cbd5e1; background: #fff; color: #334155; }
.candidate-adoption__primary { border: 1px solid #1d4ed8; background: #2563eb; color: #fff; }
button:disabled { cursor: wait; opacity: .6; }
</style>
