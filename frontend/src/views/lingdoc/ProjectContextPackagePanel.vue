<script setup lang="ts">
import { onUnmounted, ref, shallowRef, watch } from 'vue'
import ContextPackagePanel from './ContextPackagePanel.vue'
import { getProject, type Project } from '@/api/lingdoc/workspace'
import { listEvidenceCards, previewAgentContext } from '@/api/lingdoc/agentApi'
import { createIntent, type ContextCardSummary, type ContextIntent, type ContextPackageClient } from '@/api/lingdoc/agentContext'

const props = defineProps<{ project: Project; identity: string }>()
const emit = defineEmits<{ research: [] }>()
// This host owns only the live baseline and lifecycle. The PF-6 panel still owns
// the editable selection and all server facts; never feed intentChange back to it.
const preview = shallowRef<{ key: number; projectId: string; intent: ContextIntent; client: ContextPackageClient } | null>(null)
const cards = ref<ContextCardSummary[]>([])
const loading = ref(false)
const error = ref('')
const catalogError = ref('')
const catalogLoading = ref(false)
let epoch = 0
let catalogSequence = 0
let selected: ContextIntent['card_refs'] = []
let currentIdentity = ''

function identity() { return JSON.stringify([props.identity, props.project.id]) }
function rememberIntent(intent: ContextIntent) { selected = intent.card_refs.map(ref => ({ ...ref })) }

async function refresh() {
  const token = ++epoch, scope = identity(), id = props.project.id
  if (scope !== currentIdentity) { selected = []; currentIdentity = scope }
  // Unmount before reading: a failed refresh must not leave a previously allowed
  // package visible, and a late child request must not refill the candidate list.
  preview.value = null
  cards.value = []
  catalogSequence++
  catalogLoading.value = true
  loading.value = true
  error.value = ''
  catalogError.value = ''
  const current = () => token === epoch && scope === identity()
  try {
    const result = (await getProject(id)).data
    if (!current()) return
    if (result.id !== id || !Number.isSafeInteger(result.project_version) || result.project_version < 1 || !Number.isSafeInteger(result.current_context_revision) || result.current_context_revision < 0) {
      throw new Error('课题身份或版本返回无效')
    }
    const intent = createIntent({ expected_project_version: result.project_version, expected_context_revision: result.current_context_revision, expected_version: 0 })
    intent.card_refs = selected.map(ref => ({ ...ref }))
    // Reuse the facade's own single card read per preview, rather than fetching
    // another list just to populate the host's candidate selector.
    const client: ContextPackageClient = {
      previewAgentContext,
      async listEvidenceCards(projectId) {
        const sequence = ++catalogSequence
        const accepts = () => current() && sequence === catalogSequence
        if (current()) { cards.value = []; catalogLoading.value = true }
        try {
          const response = await listEvidenceCards(projectId)
          if (accepts()) {
            cards.value = response.data.map(card => ({ id: card.id, version: card.version, status: card.status }))
            catalogError.value = ''
          }
          return response
        } catch (cause) {
          if (accepts()) { cards.value = []; catalogError.value = '证据卡清单暂不可读，不能据此判断没有证据卡；已返回的包中卡片状态将标为未知。请刷新重试。' }
          throw cause
        } finally {
          if (accepts()) catalogLoading.value = false
        }
      },
    }
    preview.value = { key: token, projectId: id, intent, client }
  } catch {
    if (current()) error.value = '无法读取当前课题，可能是权限或服务状态已变化；旧上下文包已清空。请刷新重试。'
  } finally {
    if (current()) loading.value = false
  }
}

watch(() => [props.identity, props.project.id, props.project.project_version, props.project.current_context_revision], () => { void refresh() }, { immediate: true })
onUnmounted(() => { epoch++; selected = [] })
</script>

<template>
  <section class="project-context" aria-label="当前课题上下文包">
    <header class="project-context__header">
      <div><h2>上下文包</h2><p>{{ project.name }} · 按当前权限预览，不创建对话、不调用模型。</p></div>
      <button type="button" :disabled="loading" @click="refresh">刷新上下文包</button>
    </header>
    <p v-if="loading" role="status">正在读取当前课题与版本基线…</p>
    <p v-else-if="error" role="alert">{{ error }}</p>
    <template v-else-if="preview">
      <p>增删卡片后会重新读取服务端范围。版本冲突请刷新；不会自动替换所选卡片的版本，也不会替你确认敏感内容。</p>
      <p v-if="catalogError" role="alert">{{ catalogError }}</p>
      <p v-else-if="catalogLoading" role="status">正在读取当前证据卡清单…</p>
      <p v-else-if="!cards.length">尚无可加入的证据卡。可先在“调研与证据”整理资料与证据卡；无卡片的预览仍可核对项目基线和输出约束。</p>
      <button type="button" @click="emit('research')">前往调研与证据</button>
      <ContextPackagePanel :key="preview.key" :project-id="preview.projectId" agent="task_assistant" :session-key="'workspace-preview:' + preview.key" :intent="preview.intent" :available-cards="cards" :client="preview.client" @intent-change="rememberIntent" />
    </template>
  </section>
</template>

<style scoped>
.project-context{padding:16px;min-width:0;color:#1f2933}
.project-context__header{display:flex;gap:16px;justify-content:space-between;align-items:start;flex-wrap:wrap}
.project-context h2{margin:0}.project-context p{font-size:13px;line-height:1.6}
.project-context button{font:inherit;border:1px solid #c9d3df;border-radius:6px;background:#fff;padding:6px 10px;margin-bottom:12px;cursor:pointer}
.project-context button:disabled{opacity:.6;cursor:wait}
.project-context [role=alert]{color:#a82d26}
</style>
