<script setup lang="ts">
import { computed, ref } from 'vue'
import type { Candidate, Chapter } from '@/api/lingdoc/candidateAdoption'
import { acceptCandidate, listChapters, newIdempotencyKey } from '@/api/lingdoc/candidateAdoption'

const props = defineProps<{
  projectId: string
  chapter: Chapter
  candidate: Candidate
  expectedSpecRevision: number
  open?: boolean
}>()

const emit = defineEmits<{
  adopted: [chapter: Chapter]
  cancel: []
}>()

const submitting = ref(false)
const errorMessage = ref('')
const hasExistingBody = computed(() => props.chapter.body_markdown.trim().length > 0)

async function adopt() {
  if (submitting.value) return
  errorMessage.value = ''
  submitting.value = true
  try {
    const response = await acceptCandidate(
      props.projectId,
      props.chapter.id,
      {
        candidate_id: props.candidate.id,
        expected_chapter_version_id: props.chapter.current_version_id,
        expected_spec_revision: props.expectedSpecRevision,
        replace_existing: hasExistingBody.value,
      },
      newIdempotencyKey(),
    )
    // The write response is an acknowledgement. Read the chapter again so
    // the UI observes the server's current version and review state.
    const chapters = await listChapters(props.projectId)
    const refreshed = chapters.data.find((item) => item.id === props.chapter.id)
    emit('adopted', refreshed || response.data)
  } catch (error: any) {
    errorMessage.value = error?.message || '候选采纳失败，请重新读取后重试。'
  } finally {
    submitting.value = false
  }
}

function cancel() {
  if (!submitting.value) emit('cancel')
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
        <button type="button" class="candidate-adoption__close" :disabled="submitting" aria-label="取消" @click="cancel">×</button>
      </header>

      <p v-if="hasExistingBody" class="candidate-adoption__warning">
        当前章节已有正文。采纳会整章替换正文、引用和待核项，旧版本仍会保留。请确认后继续。
      </p>
      <p v-else class="candidate-adoption__hint">当前章节为空，将创建一个新的章节版本。</p>

      <section class="candidate-adoption__preview">
        <h3>候选正文</h3>
        <pre>{{ candidate.body_markdown }}</pre>
        <p>来源：{{ candidate.source_ids.length ? candidate.source_ids.join('、') : '无' }}</p>
        <p>待核项：{{ candidate.review_items.length }} 条（采纳后需要重新确认）</p>
      </section>

      <p v-if="errorMessage" class="candidate-adoption__error" role="alert">{{ errorMessage }}</p>

      <footer>
        <button type="button" class="candidate-adoption__secondary" :disabled="submitting" @click="cancel">取消</button>
        <button type="button" class="candidate-adoption__primary" :disabled="submitting" @click="adopt">
          {{ submitting ? '采纳中…' : hasExistingBody ? '确认整章替换' : '采纳候选' }}
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
.candidate-adoption__preview { padding: 16px; border: 1px solid #e2e8f0; border-radius: 12px; background: #f8fafc; }
.candidate-adoption__preview pre { max-height: 260px; overflow: auto; white-space: pre-wrap; font: inherit; line-height: 1.6; }
.candidate-adoption__preview p { margin: 8px 0 0; color: #475569; font-size: 13px; }
.candidate-adoption__error { color: #b91c1c; }
footer { justify-content: flex-end; margin-top: 24px; }
footer button { min-width: 96px; padding: 10px 16px; border-radius: 9px; cursor: pointer; }
.candidate-adoption__secondary { border: 1px solid #cbd5e1; background: #fff; color: #334155; }
.candidate-adoption__primary { border: 1px solid #1d4ed8; background: #2563eb; color: #fff; }
button:disabled { cursor: wait; opacity: .6; }
</style>
