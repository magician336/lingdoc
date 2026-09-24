<template>
  <main class="lingdoc-workspace">
    <header class="workspace-head">
      <div>
        <h1>灵档项目</h1>
        <p>当前使用两章演示模板，内容仅供团队验证流程。</p>
      </div>
      <button type="button" @click="loadProjects" :disabled="loading">刷新项目</button>
    </header>

    <p v-if="errorMessage" role="alert" class="alert">{{ errorMessage }}</p>
    <div class="workspace-grid">
      <aside class="panel">
        <h2>我的项目</h2>
        <form class="create-form" @submit.prevent="create">
          <label for="project-name">新项目名称</label>
          <input id="project-name" v-model="newName" maxlength="120" required placeholder="例如：演示申报项目" />
          <button type="submit" :disabled="busy || !newName.trim()">创建项目</button>
        </form>
        <p v-if="loading">正在加载…</p>
        <p v-else-if="projects.length === 0" class="muted">还没有项目。</p>
        <ul v-else class="project-list">
          <li v-for="item in projects" :key="item.id">
            <button type="button" :class="{ selected: project?.id === item.id }" @click="selectProject(item.id)">
              <strong>{{ item.name }}</strong><small>{{ item.status === 'draft' ? '草稿' : '已立项' }}</small>
            </button>
          </li>
        </ul>
        <p v-if="truncated" class="muted">只显示最近 50 个项目。</p>
      </aside>

      <section class="panel work-area" v-if="project">
        <div class="section-head">
          <div><h2>{{ project.name }}</h2><p class="muted">研究条件版本 {{ project.spec_revision }} · 项目版本 {{ project.project_version }}</p></div>
          <button type="button" @click="selectProject(project.id, true)">重新读取</button>
        </div>
        <p class="muted">项目成员 {{ project.members.length }} 人。现阶段由项目成员协作编辑。</p>

        <section class="assets">
          <h3>项目资料</h3>
          <form class="asset-bind-form" @submit.prevent="bindProjectAsset">
            <label for="knowledge-id">绑定知识资料</label>
            <div class="asset-bind-row">
              <input id="knowledge-id" v-model="knowledgeId" :disabled="busy" placeholder="输入 knowledge_id" />
              <button type="submit" :disabled="busy || !knowledgeId.trim()">绑定</button>
            </div>
          </form>
          <p v-if="assets.length === 0" class="muted">当前项目没有可用的已就绪资料。</p>
          <ul v-else class="asset-list">
            <li v-for="asset in assets" :key="asset.id">
              <span><strong>{{ asset.title || asset.knowledge_id }}</strong><small>版本 {{ asset.asset_revision }}</small></span>
              <em>{{ asset.processing_state === 'ready' ? '已就绪' : asset.processing_state }}</em>
            </li>
          </ul>
        </section>

        <section class="sources">
          <h3>资料检索</h3>
          <form class="source-search-form" @submit.prevent="searchSources">
            <label for="source-query">检索问题</label>
            <div class="asset-bind-row">
              <input id="source-query" v-model="sourceQuery" :disabled="busy || assets.length === 0" placeholder="输入要定位的内容" />
              <button type="submit" :disabled="busy || !sourceQuery.trim() || assets.length === 0">检索</button>
            </div>
          </form>
          <p v-if="sourceQuery && sources.length === 0" class="muted">暂无可定位来源。</p>
          <ul v-else-if="sources.length" class="source-list">
            <li v-for="source in sources" :key="source.id">
              <span><strong>{{ source.locator }}</strong><small>{{ source.status }}</small></span>
              <p>{{ source.quoted_text || '当前版本无法取回原文片段。' }}</p>
              <button type="button" :disabled="busy" @click="refreshSource(source.id)">重新定位</button>
            </li>
          </ul>
        </section>

        <form class="spec-form" @submit.prevent="saveConditions">
          <h3>研究条件</h3>
          <label for="subject">研究主题</label>
          <input id="subject" v-model="subject" :disabled="busy" placeholder="写清项目研究什么" />
          <label for="goal">研究目标</label>
          <textarea id="goal" v-model="goal" :disabled="busy" rows="3" placeholder="写清本轮要验证什么" />
          <div class="actions">
            <button type="submit" :disabled="busy || !specChanged">保存研究条件</button>
            <button v-if="project.status === 'draft'" type="button" @click="activate" :disabled="busy || specChanged || !subject.trim() || !goal.trim()">立项并创建章节</button>
          </div>
          <p v-if="specChanged && project.status === 'draft'" class="muted">立项前请先保存研究条件。</p>
        </form>

        <div v-if="project.status === 'active'" class="chapters">
          <h3>章节</h3>
          <p v-if="chapters.length === 0" class="muted">章节尚未加载，点击“重新读取”重试。</p>
          <div class="chapter-tabs">
            <button v-for="item in chapters" :key="item.id" type="button"
              :class="{ selected: chapter?.id === item.id }" @click="selectChapter(item)">
              {{ item.title }} <small>{{ item.current_version_id ? '已编辑' : '未填写' }}</small>
            </button>
          </div>
          <form v-if="chapter" @submit.prevent="saveText" class="chapter-form">
            <label for="chapter-body">{{ chapter.title }}正文</label>
            <textarea id="chapter-body" v-model="bodyDraft" :disabled="busy" rows="12" placeholder="从这里开始撰写章节" />
            <p v-if="chapter.review_items.length" class="warning">本章有 {{ chapter.review_items.length }} 条待核事项。保存修改会保留这些事项，仍需逐项核查。</p>
            <section v-if="chapter.review_items.length" class="review-items" aria-label="待核项处置">
              <h4>逐项处置待核事项</h4>
              <fieldset v-for="item in chapter.review_items" :key="item.id" class="review-item">
                <legend>{{ item.statement }}</legend>
                <label :for="`disposition-${item.id}`">处理结果</label>
                <select :id="`disposition-${item.id}`" v-model="reviewDraft(item.id).disposition" :disabled="busy">
                  <option value="resolved">已核实并解决</option>
                  <option value="retained_warning">保留为提示</option>
                </select>
                <label :for="`reason-${item.id}`">处理理由</label>
                <textarea :id="`reason-${item.id}`" v-model="reviewDraft(item.id).reason" :disabled="busy" rows="2" maxlength="2000" placeholder="记录核查依据或保留原因" />
              </fieldset>
            </section>
            <p v-if="!chapter.current_version_id" class="muted">空章节尚无可确认版本；仍可继续交付检查，检查结果会指出内容缺失。</p>
            <p v-else-if="chapter.confirmation_valid" class="confirmed">当前章节版本已确认。保存新版本后需要重新确认。</p>
            <p v-else-if="bodyChanged" class="muted">正文有未保存修改，请先保存为新版本，再确认当前版本。</p>
            <button v-if="chapter.current_version_id" type="button" @click="confirmCurrentChapter"
              :disabled="busy || bodyChanged || !reviewDecisionsReady">
              {{ chapter.confirmation_valid ? '重新确认当前版本' : '确认当前章节版本' }}
            </button>
            <p v-if="chapter.source_ids.length" class="warning">本章已有来源引用。资料授权接入前暂不支持修改此章，以免丢失引用。</p>
            <button type="submit" :disabled="busy || !bodyChanged || chapter.source_ids.length > 0">保存为新版本</button>
          </form>
        </div>
      </section>
      <section v-else class="panel empty-work"><p>选一个项目开始；也可以先创建项目。</p></section>
    </div>
  </main>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import {
  activateProject, bindAsset, confirmChapter, createProject, getProject, getSource, listAssets, listChapters, listProjects,
  retrieveSources, saveChapter, saveSpec, type Asset, type Chapter, type Project, type ReviewDecision, type Source,
} from '@/api/lingdoc/workspace'

const projects = ref<Project[]>([])
const truncated = ref(false)
const project = ref<Project | null>(null)
const chapters = ref<Chapter[]>([])
const assets = ref<Asset[]>([])
const knowledgeId = ref('')
const sourceQuery = ref('')
const sources = ref<Source[]>([])
const chapter = ref<Chapter | null>(null)
const newName = ref('')
const subject = ref('')
const goal = ref('')
const bodyDraft = ref('')
const busy = ref(false)
const loading = ref(false)
const errorMessage = ref('')
const reviewDrafts = ref<Record<string, { disposition: ReviewDecision['disposition']; reason: string }>>({})

// Keep one key for a retry of the exact same operation and body.
const attempts = new Map<string, { body: string; key: string }>()
function operationKey(name: string, body: unknown): string {
  const serialized = JSON.stringify(body)
  const old = attempts.get(name)
  if (old?.body === serialized) return old.key
  const key = crypto.randomUUID()
  attempts.set(name, { body: serialized, key })
  return key
}

const specChanged = computed(() => !!project.value && (
  subject.value !== (project.value.spec.research_subject ?? '') ||
  goal.value !== (project.value.spec.research_goal ?? '')
))
const bodyChanged = computed(() => !!chapter.value && bodyDraft.value !== chapter.value.body_markdown)
const reviewDecisionsReady = computed(() => !!chapter.value && chapter.value.review_items.every(item => {
  const decision = reviewDrafts.value[`${chapter.value!.id}:${item.id}`]
  return !!decision?.reason.trim()
}))

function reviewDraft(itemId: string) {
  const key = `${chapter.value?.id ?? ''}:${itemId}`
  return reviewDrafts.value[key] ??= { disposition: 'resolved', reason: '' }
}

function failure(error: unknown) {
  const item = error as { status?: number; message?: string; error?: { code?: string } }
  if (item?.status === 409 && item?.error?.code === 'version_conflict') {
    errorMessage.value = '内容已被修改。当前输入已保留；请先重新读取，再决定如何处理。'
  } else {
    errorMessage.value = item?.message || '操作失败，请重试。'
  }
}

async function loadProjects() {
  loading.value = true
  errorMessage.value = ''
  try {
    const result = await listProjects()
    projects.value = result.data.items
    truncated.value = result.data.truncated
  } catch (error) { failure(error) }
  finally { loading.value = false }
}

async function create() {
  if (busy.value) return
  busy.value = true
  errorMessage.value = ''
  const name = newName.value.trim()
  const key = operationKey('create', { name, template_id: 'template-demo' })
  try {
    const result = await createProject(name, key)
    attempts.delete('create')
    newName.value = ''
    await loadProjects()
    await selectProject(result.data.id)
  } catch (error) { failure(error) }
  finally { busy.value = false }
}

async function selectProject(id: string, force = false) {
  if (!force && ((specChanged.value && project.value?.id !== id) || bodyChanged.value) &&
      !window.confirm('当前编辑尚未保存，确定切换项目吗？')) return
  if (force && (specChanged.value || bodyChanged.value) &&
      !window.confirm('重新读取会丢弃当前未保存的输入，确定继续吗？')) return
  errorMessage.value = ''
  try {
    const result = await getProject(id)
    project.value = result.data
    subject.value = result.data.spec.research_subject ?? ''
    goal.value = result.data.spec.research_goal ?? ''
    const chapterResult = result.data.status === 'active' ? await listChapters(id) : null
    chapters.value = chapterResult?.data ?? []
    const assetResult = await listAssets(id)
    assets.value = assetResult.data ?? []
    sources.value = []
    chapter.value = chapters.value[0] ?? null
    bodyDraft.value = chapter.value?.body_markdown ?? ''
  } catch (error) { failure(error) }
}

async function searchSources() {
  if (!project.value || busy.value || !sourceQuery.value.trim() || assets.value.length === 0) return
  busy.value = true
  errorMessage.value = ''
  try {
    const result = await retrieveSources(project.value.id, sourceQuery.value.trim(), assets.value.map(item => item.id))
    sources.value = result.data
  } catch (error) { failure(error) }
  finally { busy.value = false }
}

async function refreshSource(sourceId: string) {
  if (!project.value || busy.value) return
  busy.value = true
  errorMessage.value = ''
  try {
    const result = await getSource(project.value.id, sourceId)
    sources.value = sources.value.map(item => item.id === sourceId ? result.data : item)
  } catch (error) { failure(error) }
  finally { busy.value = false }
}

async function bindProjectAsset() {
  if (!project.value || busy.value || !knowledgeId.value.trim()) return
  busy.value = true
  errorMessage.value = ''
  const projectId = project.value.id
  const input = { knowledge_id: knowledgeId.value.trim() }
  const key = operationKey(`asset:${projectId}`, input)
  try {
    await bindAsset(projectId, input.knowledge_id, key)
    attempts.delete(`asset:${projectId}`)
    knowledgeId.value = ''
    const refreshed = await listAssets(projectId)
    assets.value = refreshed.data ?? []
  } catch (error) { failure(error) }
  finally { busy.value = false }
}

async function saveConditions() {
  if (!project.value || busy.value) return
  busy.value = true
  errorMessage.value = ''
  const id = project.value.id
  const expected = project.value.spec_revision
  const fields = { ...project.value.spec, research_subject: subject.value, research_goal: goal.value }
  const key = operationKey(`spec:${id}`, { expected_spec_revision: expected, fields })
  try {
    const result = await saveSpec(id, expected, fields, key)
    attempts.delete(`spec:${id}`)
    project.value = result.data
    await loadProjects()
    if (result.meta.refresh_required) await selectProject(id)
  } catch (error) { failure(error) }
  finally { busy.value = false }
}

async function activate() {
  if (!project.value || busy.value || specChanged.value) return
  busy.value = true
  errorMessage.value = ''
  const id = project.value.id
  const expected = project.value.spec_revision
  const key = operationKey(`activate:${id}`, { expected_spec_revision: expected })
  try {
    await activateProject(id, expected, key)
    attempts.delete(`activate:${id}`)
    await loadProjects()
    await selectProject(id)
  } catch (error) { failure(error) }
  finally { busy.value = false }
}

function selectChapter(item: Chapter) {
  if (bodyChanged.value && !window.confirm('当前章节尚未保存，确定切换吗？')) return
  chapter.value = item
  bodyDraft.value = item.body_markdown
  errorMessage.value = ''
}

async function confirmCurrentChapter() {
  if (!project.value || !chapter.value?.current_version_id || busy.value || bodyChanged.value || !reviewDecisionsReady.value) return
  const current = chapter.value
  const expectedVersion = current.current_version_id
  if (!expectedVersion) return
  const projectId = project.value.id
  const decisions: ReviewDecision[] = current.review_items.map(item => ({
    review_item_id: item.id,
    disposition: reviewDraft(item.id).disposition,
    reason: reviewDraft(item.id).reason.trim(),
  }))
  if (!window.confirm(`将确认“${current.title}”的当前版本。后续保存新版本会使本次确认失效。继续吗？`)) return
  busy.value = true
  errorMessage.value = ''
  const input = {
    expected_chapter_version_id: expectedVersion,
    expected_spec_revision: project.value.spec_revision,
    review_decisions: decisions,
  }
  const key = operationKey(`confirm:${current.id}`, input)
  try {
    await confirmChapter(projectId, current.id, input, key)
    attempts.delete(`confirm:${current.id}`)
    const result = await listChapters(projectId)
    chapters.value = result.data
    chapter.value = result.data.find(item => item.id === current.id) ?? null
    if (chapter.value) bodyDraft.value = chapter.value.body_markdown
  } catch (error) { failure(error) }
  finally { busy.value = false }
}

async function saveText() {
  if (!project.value || !chapter.value || busy.value || chapter.value.source_ids.length) return
  busy.value = true
  errorMessage.value = ''
  const projectId = project.value.id
  const chapterId = chapter.value.id
  const input = {
    expected_chapter_version_id: chapter.value.current_version_id,
    expected_spec_revision: project.value.spec_revision,
    body_markdown: bodyDraft.value,
    source_ids: [] as string[],
  }
  const key = operationKey(`chapter:${chapterId}`, input)
  try {
    const result = await saveChapter(projectId, chapterId, input, key)
    attempts.delete(`chapter:${chapterId}`)
    const current = chapters.value.findIndex(item => item.id === chapterId)
    if (current >= 0) chapters.value[current] = result.data
    chapter.value = result.data
    const refreshed = await getProject(projectId)
    project.value = refreshed.data
    if (result.meta.refresh_required) await selectProject(projectId)
  } catch (error) { failure(error) }
  finally { busy.value = false }
}

onMounted(loadProjects)
</script>

<style scoped>
.lingdoc-workspace { max-width: 1200px; margin: 0 auto; padding: 32px; color: #24342e; }
.workspace-head, .section-head, .actions { display: flex; justify-content: space-between; align-items: center; gap: 16px; }
h1 { margin: 0 0 8px; font-size: 28px; } h2 { margin: 0 0 12px; font-size: 20px; } h3 { margin: 22px 0 14px; font-size: 17px; }
p { margin: 6px 0; } .muted { color: #6b7670; font-size: 13px; } .warning { color: #8b5b10; font-size: 13px; }
.alert { padding: 12px 16px; margin: 20px 0; background: #fff1ee; border: 1px solid #eea99e; border-radius: 8px; }
.workspace-grid { display: grid; grid-template-columns: 280px minmax(0, 1fr); gap: 20px; margin-top: 24px; }
.panel { background: #fff; border: 1px solid #dbe5dd; border-radius: 12px; padding: 22px; min-width: 0; }
.create-form, .spec-form, .chapter-form { display: flex; flex-direction: column; gap: 10px; }
label { font-weight: 600; font-size: 14px; }
input, textarea { width: 100%; box-sizing: border-box; padding: 10px 12px; border: 1px solid #becdc3; border-radius: 7px; font: inherit; }
button { padding: 8px 12px; border: 1px solid #becdc3; border-radius: 7px; background: #fff; color: #25452f; cursor: pointer; }
button:hover:not(:disabled), button.selected { border-color: #238a52; background: #edf8f0; }
button:disabled { opacity: .55; cursor: not-allowed; }
.project-list { list-style: none; padding: 0; display: grid; gap: 7px; }
.project-list button { width: 100%; display: flex; justify-content: space-between; text-align: left; }
.review-items { display: grid; gap: 10px; padding: 14px; border: 1px solid #e7d5a7; border-radius: 8px; background: #fffaf0; }
.review-items h4 { margin: 0; }
.review-item { display: grid; gap: 8px; min-width: 0; padding: 12px; border: 1px solid #dbe5dd; border-radius: 7px; }
.review-item legend { padding: 0 4px; font-weight: 600; }
.review-item select { padding: 9px 10px; border: 1px solid #becdc3; border-radius: 7px; font: inherit; }
.confirmed { color: #167346; font-size: 13px; }
.project-list small, .chapter-tabs small { color: #67746a; margin-left: 8px; }
.asset-list { list-style: none; padding: 0; display: grid; gap: 8px; }
.asset-bind-form { display: flex; flex-direction: column; gap: 8px; margin-bottom: 14px; }
.asset-bind-row { display: flex; gap: 8px; }
.asset-bind-row input { flex: 1; min-width: 0; }
.asset-list li { display: flex; justify-content: space-between; gap: 12px; padding: 10px 12px; border: 1px solid #dbe5dd; border-radius: 7px; }
.asset-list span { display: flex; flex-direction: column; gap: 3px; min-width: 0; }
.asset-list small, .asset-list em { color: #67746a; font-size: 12px; font-style: normal; }
.chapter-tabs { display: flex; flex-wrap: wrap; gap: 8px; margin-bottom: 14px; }
.empty-work { display: grid; place-items: center; min-height: 300px; color: #6b7670; }
@media (max-width: 760px) { .workspace-grid { grid-template-columns: 1fr; } .lingdoc-workspace { padding: 16px; } }
</style>
