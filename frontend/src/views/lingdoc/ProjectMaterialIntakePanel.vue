<template>
  <section class="material-intake" aria-label="项目内材料接入">
    <h3>上传新材料</h3>
    <p>目标课题：<strong>{{ projectName }}</strong>。上传、处理、绑定和检索分别确认；上传不会自动扩大资料权限。</p>
    <label :for="prefix + '-kb'">材料存储库</label>
    <select :id="prefix + '-kb'" v-model="kbId" :disabled="choosing || model.uploading || loading">
      <option value="">选择可使用的资料库</option>
      <option v-for="kb in libraries" :key="kb.id" :value="kb.id">{{ kb.name }}</option>
    </select>
    <button type="button" :disabled="loading || choosing || model.uploading" @click="loadLibraries">重新读取资料库</button>
    <label class="upload-file">
      选择文件
      <input type="file" aria-label="选择要上传到当前课题的材料" :disabled="!kbId || choosing || model.uploading" @change="chooseFile" />
    </label>
    <p class="hint">沿用平台文件类型和大小限制。首次上传在此完成，无需跳转平台；处理中的材料不会自动用于写作或模型。</p>
    <p v-if="choosing || model.uploading" role="status">正在为当前课题确认或上传文件…</p>
    <p v-if="libraryError" role="alert">{{ libraryError }}</p>
    <p v-if="model.uploadError" role="alert">{{ model.uploadError }}</p>
    <p v-if="model.storageWarning" role="alert">{{ model.storageWarning }}</p>
    <p class="hint">下列是此账号、此空间、此课题在本浏览器发起的材料记录，不是服务端的正式绑定列表。刷新时会重新核实权限和状态。</p>
    <p v-if="!model.rows.length" role="status">本浏览器尚未记录此课题的上传。</p>
    <ul class="material-rows">
      <li v-for="row in model.rows" :key="row.ref.id">
        <h4>{{ row.knowledge?.title || row.knowledge?.file_name || '材料（名称待重新核实）' }}</h4>
        <p role="status">{{ row.label }}</p>
        <p v-if="row.knowledge?.error_message" class="hint">处理原因：{{ row.knowledge.error_message }}</p>
        <p v-if="row.readError" role="alert">{{ row.readError }} 不能依靠上次状态继续操作。</p>
        <p v-if="row.actionError" role="alert">{{ row.actionError }}</p>
        <p v-if="row.notice" role="status">{{ row.notice }}</p>
        <button type="button" :disabled="row.busy" @click="model.refresh(row)">重新读取材料状态</button>
        <button type="button" :disabled="row.busy || model.uploading" @click="forget(row)">移除本机追踪记录</button>
        <button type="button" :disabled="row.busy || !row.indexReady" @click="bind(row)">
          {{ row.asset ? '核实当前项目绑定' : '绑定到当前课题' }}
        </button>
        <p v-if="!row.indexReady" class="hint">解析完成不等于索引完成；无真实的当前索引记录时，不放行绑定。请根据错误原因检查原文件和资料库配置；修正文件后可重新选择上传，重新处理已有材料需由有权限的人员进行，不会自动重传或重解析。</p>
        <form @submit.prevent="model.search(row)">
          <label :for="prefix + '-' + row.ref.id">在当前项目检索此材料</label>
          <input :id="prefix + '-' + row.ref.id" v-model="row.query" maxlength="500" placeholder="输入文件中的原文关键词" />
          <button type="submit" :disabled="row.busy || !row.asset || !row.indexReady || !row.query.trim()">核实检索命中</button>
        </form>
        <ul v-if="row.hits.length" aria-label="当前绑定材料的项目检索结果">
          <li v-for="hit in row.hits" :key="hit.id">{{ hit.locator }}：{{ hit.quoted_text }}</li>
        </ul>
      </li>
    </ul>
  </section>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { getKnowledgeBaseById, listKnowledgeBases } from '@/api/knowledge-base'
import { useUploadConfirmStore } from '@/stores/uploadConfirm'
import { kbFileTypeVerification } from '@/utils'
import { ProjectMaterialIntake, IN_PROGRESS_PARSE_STATUSES, type MaterialRow, type MaterialStorage } from './projectMaterialIntake'
import { projectMaterialPorts } from './projectMaterialsApi'

const props = defineProps<{ projectId: string; projectName: string; identity: string }>()
const emit = defineEmits<{ bound: [] }>()
let storage: MaterialStorage | undefined
try { storage = window.localStorage } catch { /* in-memory mode remains available */ }
const model = reactive(new ProjectMaterialIntake(props.projectId, props.identity, projectMaterialPorts, storage))
const prefix = 'material-' + props.projectId
const confirm = useUploadConfirmStore()
const libraries = ref<Array<{ id: string; name: string }>>([])
const kbId = ref('')
const loading = ref(false)
const libraryError = ref('')
const choosing = ref(false)
let alive = true
let librarySequence = 0
let timer: ReturnType<typeof setInterval> | undefined
let ownConfirmation: unknown

async function loadLibraries() {
  const sequence = ++librarySequence
  loading.value = true
  libraryError.value = ''
  try {
    const response = await listKnowledgeBases()
    const items = response.data?.items ?? response.data
    if (!Array.isArray(items)) throw Error('资料库响应无法识别。')
    if (!alive || sequence !== librarySequence) return
    libraries.value = items.filter(kb => kb.type !== 'faq').map(kb => ({ id: String(kb.id || kb.kb_id || ''), name: String(kb.name || kb.kb_name || '未命名资料库') })).filter(kb => kb.id)
    if (!libraries.value.some(kb => kb.id === kbId.value)) kbId.value = ''
  } catch {
    if (alive && sequence === librarySequence) {
      libraries.value = []
      kbId.value = ''
      libraryError.value = '资料库暂不可读或当前账号无权访问，请重新读取；不会沿用旧列表上传。'
    }
  } finally { if (alive && sequence === librarySequence) loading.value = false }
}
async function chooseFile(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file || choosing.value || model.uploading || !kbId.value || kbFileTypeVerification(file)) return
  const target = kbId.value
  choosing.value = true
  try {
    const kb = await getKnowledgeBaseById(target)
    if (!alive) return
    if (kb.data?.id !== target) throw Error('材料存储库身份无法确认。')
    let result
    try {
      const pending = confirm.open({ mode: 'file', kbInfo: kb.data, files: [file] })
      ownConfirmation = confirm.pendingResolve
      result = await pending
    }
    catch { return } // Dialog cancellation is not an upload failure.
    finally { ownConfirmation = undefined }
    if (!alive) return
    for (const selected of result.files || []) {
      if (!alive) return
      if (!kbFileTypeVerification(selected)) await model.upload(target, selected, result.processConfig)
    }
  } catch {
    if (alive) libraryError.value = '无法确认资料库的当前访问权限，文件未上传，请重新读取。'
  } finally { if (alive) choosing.value = false }
}
async function bind(row: MaterialRow) {
  await model.bind(row)
  if (alive && row.asset) emit('bound')
}
function forget(row: MaterialRow) {
  if (window.confirm('仅移除此浏览器的材料追踪记录？不会删除文件、撤销权限或解除服务器上的项目绑定。未绑定材料仍在原资料库。')) model.forget(row)
}
onMounted(() => {
  void loadLibraries()
  void model.refreshAll()
  timer = setInterval(() => {
    if (document.visibilityState !== 'visible') return
    for (const row of model.rows) {
      if (!row.busy && IN_PROGRESS_PARSE_STATUSES.includes(row.knowledge?.parse_status || '')) void model.refresh(row)
    }
  }, 10000)
})
onBeforeUnmount(() => {
  alive = false
  librarySequence++
  if (ownConfirmation && confirm.pendingResolve === ownConfirmation) confirm.rejectConfirm()
  if (timer) clearInterval(timer)
  model.dispose()
})
</script>

<style scoped>
.material-intake { border: 1px solid var(--td-component-border, #d9e2df); border-radius: 8px; padding: 12px; margin-block: 12px; }
.material-intake p { line-height: 1.6; overflow-wrap: anywhere; }
.material-intake .hint { color: var(--td-text-color-secondary, #596963); font-size: .85rem; }
.material-intake select, .material-intake input { max-width: 100%; margin: 6px 8px 6px 0; }
.material-intake button { margin: 4px 8px 4px 0; }
.upload-file { display: block; }
.material-rows { list-style: none; padding: 0; }
.material-rows > li { border-top: 1px solid var(--td-component-border, #d9e2df); padding-block: 12px; }
.material-rows h4 { margin-block: 4px; }
.material-rows form label { display: block; }
.material-rows input { width: min(100%, 360px); }
</style>
