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
          <!-- 绑定成功但资料还没就绪时，它随后会从下面的列表里消失（listAssets 只回允许集合）。
               不说这一句，用户看到的是「绑定成功了但什么都没发生，再绑一次还是这样」。 -->
          <p v-if="bindingNoticeText" class="binding-notice" role="status">{{ bindingNoticeText }}</p>
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
          <!-- 被拒明细（F07）：服务端对有未授权项的检索整批答 422、一条都不处理，而那句概括
               里没说**是哪一份、为什么**。明细比概括具体，所以它替掉顶部的通用错误提示。 -->
          <section v-if="deniedRows.length" class="denied-sources" role="alert" aria-label="未获授权的资料">
            <p>{{ DENIED_NOTICE }}</p>
            <ul>
              <li v-for="row in deniedRows" :key="row.assetId">
                <span><strong>{{ row.assetId }}</strong> —— {{ row.label }}</span>
                <small>{{ row.next }}</small>
              </li>
            </ul>
            <p class="muted">这里只给得出资料 ID：项目资料列表只列已就绪且已授权的资料，
              被拒的那一份在里面没有痕迹，界面上也就没有它的标题可显示。</p>
          </section>
          <p v-if="searchedNothing" class="muted">暂无可定位来源。</p>
          <ul v-else-if="sources.length" class="source-list">
            <li v-for="source in sources" :key="source.id">
              <div class="source-head">
                <span><strong>{{ source.locator }}</strong><small>{{ sourceStatusLabel(source.status) }}</small></span>
                <div class="source-actions">
                  <button type="button" :disabled="busy" @click="refreshSource(source.id)">重新定位</button>
                  <!-- 同页展开，不跳知识库页：那条路要 kbId，而 Asset 与 Source 都带不了它。 -->
                  <button type="button" :disabled="contextBusy" :aria-expanded="contextOpenFor === source.id"
                    @click="toggleSourceContext(source.id)">查看原文</button>
                </div>
              </div>
              <p class="source-quote">{{ quotedTextOf(source) }}</p>
              <section v-if="contextOpenFor === source.id" class="source-context" aria-label="引用处的原文上下文">
                <h4>{{ contextHeading }}</h4>
                <p v-if="contextReason" class="muted">{{ contextReason }}</p>
                <!-- 窗口开得出来但一段都没有：引用就在文档的两头。空列表配着「前后各 1 段」
                     的抬头，看上去像加载失败了，所以这一句要说出来。 -->
                <p v-else-if="!contextRows.length" class="muted">这一段前后都没有同族的邻居段，它可能就在文档的两头。</p>
                <ol v-else class="source-context__segments">
                  <li v-for="line in contextRows" :key="line.id" :class="{ 'is-verbatim': line.verbatim }">
                    <small>{{ line.label }}</small>
                    <p v-if="line.text">{{ line.text }}</p>
                    <p v-else class="muted">这一段没有正文可摆。</p>
                    <small v-if="line.note">{{ line.note }}</small>
                  </li>
                </ol>
                <p class="muted">展开的是分块记录里的文本，不是重新解析原文件得到的逐字原文。</p>
              </section>
            </li>
          </ul>
        </section>

        <section v-if="project.status === 'active' && chapter" class="generation">
          <h3>生成候选稿</h3>
          <fieldset class="generation-assets" :disabled="generationPending || busy">
            <legend>本次允许使用的资料</legend>
            <label v-for="asset in readyAssets" :key="asset.id" class="asset-choice">
              <input v-model="selectedAssetIds" type="checkbox" :value="asset.id" />
              {{ asset.title || asset.knowledge_id }}（版本 {{ asset.asset_revision }}）
            </label>
            <p v-if="readyAssets.length === 0" class="muted">请先绑定已就绪的项目资料。</p>
          </fieldset>
          <form class="generation-form" @submit.prevent="startDraft">
            <label for="generation-instruction">写作要求</label>
            <textarea id="generation-instruction" v-model="generationInstruction" rows="3"
              :disabled="busy || generationPending" placeholder="说明本章要回答的问题和需要关注的重点" />
            <div class="actions">
              <button type="submit" :disabled="busy || generationPending || !selectedAssetIds.length || !generationInstruction.trim()">生成候选稿</button>
              <button v-if="generationRun" type="button" :disabled="generationBusy" @click="refreshGeneration()">刷新任务状态</button>
              <button v-if="generationRun?.status === 'queued' || generationRun?.status === 'running'" type="button"
                :disabled="generationBusy" @click="cancelGenerationRun()">取消生成</button>
            </div>
          </form>
          <p v-if="generationRun" class="muted">任务 {{ generationRun.id }} · 状态：{{ generationRun.status }}</p>
          <p v-if="generationRun?.error" role="alert" class="warning">{{ generationRun.error.message }}</p>
          <section v-if="generationCandidates.length" class="candidate-list" aria-label="本章候选历史">
            <h4>本章候选历史</h4>
            <ul>
              <li v-for="candidate in generationCandidates" :key="candidate.id">
                <button type="button" :aria-pressed="generationCandidate?.id === candidate.id"
                  @click="showGenerationCandidate(candidate.run_id)">
                  候选 {{ candidate.id.slice(0, 8) }} · {{ candidate.validity }} · {{ new Date(candidate.created_at).toLocaleString() }}
                </button>
              </li>
            </ul>
          </section>
          <article v-if="generationCandidate" class="candidate-preview">
            <h4>候选稿预览（不会自动覆盖章节）</h4>
            <pre>{{ generationCandidate.body_markdown }}</pre>
            <p>引用 {{ generationCandidate.source_ids.length }} 条来源 · 待核事项 {{ generationCandidate.review_items.length }} 条 · 生成时状态：{{ generationCandidate.validity }}（采纳时仍会复核当前版本）</p>
            <div class="actions">
              <button type="button" :disabled="busy" @click="openAdoption">采纳到本章</button>
            </div>
          </article>
        </section>

        <!-- v-if 而不是 :open 传布尔：对话框自带本地状态（幂等键、错误文案、提交中），
             v-if 让每次打开都是干净的一份；用 :open 隐藏再显示，会把上一次的失败文案和
             用过的键一起带回来——那个键对应的是上一次的请求体。 -->
        <LingDocCandidateAdoptionDialog
          v-if="adoptionOpen && project && chapter && generationCandidate"
          :project-id="project.id" :chapter="chapter" :candidate="generationCandidate"
          :expected-spec-revision="project.spec_revision"
          @adopted="onAdopted" @cancel="adoptionOpen = false" />

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

        <!-- 撤权提示（§8）。只在服务端判 restricted 时出现，且它是一条**提示**而不是拦截：
             本轮只有 access-status 这一条读路径带资料层判定，别的端点仍会照常返回内容
             （差额记在 02-接口与Mock约定 §8）。所以这里给的是恢复入口，不是封锁——写成
             封锁，界面就在声称一个它并没有执行的限制。 -->
        <section v-if="restricted" class="access-warning" role="status" aria-label="资料访问状态">
          <h3>{{ RESTRICTED_NOTICE }}</h3>
          <ul v-if="recoveryActions.length" class="access-warning__actions">
            <li v-for="action in recoveryActions" :key="action.code">
              <strong>{{ action.label }}</strong>
              <span v-if="action.detail">{{ action.detail }}</span>
            </li>
          </ul>
          <button type="button" :disabled="accessBusy" @click="recheckAccess">
            {{ accessBusy ? '检查中…' : '重新检查' }}
          </button>
        </section>

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
            <p v-if="draftCitations.malformed" class="warning">{{ MALFORMED_CITATION_MESSAGE }}</p>
            <p v-else-if="draftCitations.sourceIds.length" class="muted">
              本章引用 {{ draftCitations.sourceIds.length }} 条来源。保存时会逐条复核；删掉正文里的标记就等于放弃那一条。
            </p>
            <button type="submit" :disabled="busy || !bodyChanged">保存为新版本</button>
          </form>
        </div>

        <DeliveryPanel v-if="project.status === 'active'" :project="project" :chapters="chapters"
          @refresh-project="refreshProjectVersion" />
      </section>
      <section v-else class="panel empty-work"><p>选一个项目开始；也可以先创建项目。</p></section>
    </div>
  </main>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import type { Candidate } from '@/api/lingdoc/candidateAdoption'
import {
  cancelGeneration, getGeneratedCandidate, getGeneration, listGenerationCandidates, startGeneration,
  type GenerationCandidateSummary, type GenerationRun,
} from '@/api/lingdoc/generation'
import DeliveryPanel from './DeliveryPanel.vue'
import LingDocCandidateAdoptionDialog from '@/components/LingDocCandidateAdoptionDialog.vue'
import { RESTRICTED_NOTICE, isRestricted, recoveryActionsOf } from './accessStatus'
import { chapterCitations, MALFORMED_CITATION_MESSAGE } from './chapterCitations'
import { clearGenerationAttempt, generationIdempotencyKey } from './generationAttempt'
import {
  CONTEXT_UNAVAILABLE_NOTICE, contextLines, contextNotice, quotedTextOf, sourceStatusLabel, windowLabel,
} from './sourceContext'
import { DENIED_NOTICE, bindingNotice, denyReasonOf, deniedSourcesOf } from './sourceNotices'
import {
  activateProject, bindAsset, confirmChapter, createProject, getAccessStatus, getProject, getSource,
  getSourceContext, listAssets, listChapters, listProjects, retrieveSources, saveChapter, saveSpec,
  type AccessStatus, type Asset, type Chapter, type Project, type ReviewDecision, type Source, type SourceContext,
} from '@/api/lingdoc/workspace'

const projects = ref<Project[]>([])
const truncated = ref(false)
const project = ref<Project | null>(null)
const chapters = ref<Chapter[]>([])
const assets = ref<Asset[]>([])
const selectedAssetIds = ref<string[]>([])
const knowledgeId = ref('')
const sourceQuery = ref('')
const sources = ref<Source[]>([])
// 上一次**真的问过**的那个问题。空结果与「还没检索」在界面上必须分开：只看 sources.length
// 的话，刚打开项目就会显示一句「暂无可定位来源」。
const searchedQuery = ref('')
const deniedRows = ref<Array<{ assetId: string; label: string; next: string }>>([])
const bindingNoticeText = ref('')
const contextSourceId = ref('')
const sourceContext = ref<SourceContext | null>(null)
const contextBusy = ref(false)
const chapter = ref<Chapter | null>(null)
const newName = ref('')
const subject = ref('')
const goal = ref('')
const bodyDraft = ref('')
const busy = ref(false)
const loading = ref(false)
const errorMessage = ref('')
const reviewDrafts = ref<Record<string, { disposition: ReviewDecision['disposition']; reason: string }>>({})
const generationInstruction = ref('根据已允许的项目资料起草本章，引用来源并列出所有待核事项。')
const generationRun = ref<GenerationRun | null>(null)
const generationCandidate = ref<Candidate | null>(null)
const generationCandidates = ref<GenerationCandidateSummary[]>([])
const generationBusy = ref(false)
const accessStatus = ref<AccessStatus | null>(null)
const accessBusy = ref(false)
const adoptionOpen = ref(false)
const restricted = computed(() => isRestricted(accessStatus.value))
const recoveryActions = computed(() => recoveryActionsOf(accessStatus.value))
const readyAssets = computed(() => assets.value.filter(item => item.processing_state === 'ready'))
const generationPending = computed(() => generationRun.value?.status === 'queued' || generationRun.value?.status === 'running')

// 「上一次检索确实什么都没找到」——而且提问没被改过。改了提问而没重新检索，
// 屏幕上那句话说的已经不是现在这个问题了。
const searchedNothing = computed(() => !!searchedQuery.value && searchedQuery.value === sourceQuery.value.trim()
  && sources.value.length === 0)

// 面板真的开着 = 记住了是哪一条 **且** 拿到了内容。只看 id 的话，一次失败的展开会让
// 再点一次变成「收起」：什么都不会发生，而用户以为按钮坏了。
const contextOpenFor = computed(() => (sourceContext.value ? contextSourceId.value : ''))
const contextHeading = computed(() => (sourceContext.value?.context_available
  ? windowLabel(sourceContext.value) : CONTEXT_UNAVAILABLE_NOTICE))
const contextReason = computed(() => contextNotice(sourceContext.value))
// 这一层只是把判定搬到渲染旁边：窗口里最多三段，且同时只有一条来源的面板开着。
const contextRows = computed(() => contextLines(sourceContext.value))

let generationTimer: ReturnType<typeof setTimeout> | undefined

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

// 正文里的来源标记：提取与体检一次算完。模板因此不必自己去拆这个联合类型。
const draftCitations = computed(() => {
  const parsed = chapterCitations(bodyDraft.value)
  return parsed.kind === 'ok'
    ? { sourceIds: parsed.sourceIds, malformed: false }
    : { sourceIds: [] as string[], malformed: true }
})
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
  // 被拒明细比服务端那句概括更具体（逐条说了是哪一份、为什么），所以它**替掉**概括：
  // 两句话说的是同一件事，并排显示只会让人读两遍再去找哪一句带细节。
  deniedRows.value = deniedSourcesOf(error).map(entry => ({ assetId: entry.assetId, ...denyReasonOf(entry.reason) }))
  if (deniedRows.value.length) {
    errorMessage.value = ''
    return
  }
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
  if (generationTimer) clearTimeout(generationTimer)
  generationRun.value = null
  generationCandidate.value = null
  generationCandidates.value = []
  adoptionOpen.value = false
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
    selectedAssetIds.value = readyAssets.value.map(item => item.id)
    // 检索结果、上一次的提问、被拒明细与绑定的提示都只属于**上一个项目**：
    // 留着它们，新项目一打开就会带着别人的结论（包括那句「暂无可定位来源」）。
    sources.value = []
    searchedQuery.value = ''
    deniedRows.value = []
    bindingNoticeText.value = ''
    closeSourceContext()
    chapter.value = chapters.value[0] ?? null
    bodyDraft.value = chapter.value?.body_markdown ?? ''
    await loadGenerationCandidates(chapter.value?.id)
    await resumeGeneration()
  } catch (error) { failure(error) }
}

// 交付面板的写入都要交 expected_project_version，过期时它请这里重读一次。
//
// 只读项目、不动别的：走 selectProject 会重选章节、清掉未保存的正文草稿，而用户要的只是
// 把版本号更新到当前。协作编辑是这条路的现实来源——别人改了项目，版本就旧了。
async function refreshProjectVersion() {
  if (!project.value) return
  try {
    const result = await getProject(project.value.id)
    project.value = result.data
  } catch (error) { failure(error) }
}

// 打开项目时问一次访问状态，决定要不要给出撤权恢复入口（§8）。
//
// 只跟项目 ID 走，不跟 project_version：一条资料被撤权是资料层的事，本地保存一次正文
// 不会改这个答案。反过来，每存一次正文就重问一遍，会让提示条随着无关操作闪来闪去。
watch(() => project.value?.id, (projectId) => {
  // 换项目就把上一条结论丢掉：它是**别的项目**的答案。这里清掉之后如果读取失败，
  // 界面不显示提示——不知道就别说，与三态里 unknown 不显示是同一条规矩。
  accessStatus.value = null
  if (projectId) void loadAccessStatus(projectId)
}, { immediate: true })

async function loadAccessStatus(projectId: string) {
  try {
    const result = await getAccessStatus(projectId)
    // 慢响应回来时可能已经换了项目：别人的状态不能贴到当前这个项目上。
    if (project.value?.id === projectId) accessStatus.value = result.data
  } catch {
    // 「重新检查」读失败时**不**清掉上一次的结论：网络失败不是关于资料授权的证据，
    // 而悄悄撤掉一条撤权提示会变成一次假的「一切正常」，用户就没得可点了。
    // （换项目那一路已经清过了，见上面的 watch。）
  }
}

async function recheckAccess() {
  if (!project.value || accessBusy.value) return
  accessBusy.value = true
  try { await loadAccessStatus(project.value.id) }
  finally { accessBusy.value = false }
}

async function searchSources() {
  if (!project.value || busy.value || !sourceQuery.value.trim() || assets.value.length === 0) return
  busy.value = true
  errorMessage.value = ''
  // 上一次的结论先撤掉：留着它，一次正在飞行的检索会顶着一句「有资料未获授权」，
  // 而那句话说的是上一次的问题、上一次的资料范围。
  deniedRows.value = []
  const query = sourceQuery.value.trim()
  try {
    const result = await retrieveSources(project.value.id, query, assets.value.map(item => item.id))
    sources.value = result.data
    searchedQuery.value = query
    closeSourceContext()
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
    // 面板里那一份是重新定位**之前**的结论。留着不重读，标题说着「可以指回原文」
    // 而列表那行已经变成了「坐标已不可信」——同一屏上两个相反的结论。
    if (contextOpenFor.value === sourceId) await loadSourceContext(sourceId)
  } catch (error) { failure(error) }
  finally { busy.value = false }
}

// 「查看原文」：同页展开这一条引用所在分块的前后邻居，不跳转到知识库页。
//
// 展开得了与展开不了都要开面板——展不开的那两种原因（这一段是派生块 / 这条引用此刻失效）
// 本身就是用户点这一下想知道的事，闷着不显示等于让按钮看起来坏了。
async function toggleSourceContext(sourceId: string) {
  if (contextOpenFor.value === sourceId) {
    closeSourceContext()
    return
  }
  await loadSourceContext(sourceId)
}

async function loadSourceContext(sourceId: string) {
  if (!project.value || contextBusy.value) return
  const projectId = project.value.id
  contextBusy.value = true
  contextSourceId.value = sourceId
  sourceContext.value = null
  errorMessage.value = ''
  try {
    const result = await getSourceContext(projectId, sourceId)
    // 慢响应回来时可能已经换了项目、或点了另一条来源：别人的上下文不能贴到这一条上。
    if (project.value?.id === projectId && contextSourceId.value === sourceId) sourceContext.value = result.data
  } catch (error) { failure(error) }
  finally { contextBusy.value = false }
}

function closeSourceContext() {
  contextSourceId.value = ''
  sourceContext.value = null
}

async function bindProjectAsset() {
  if (!project.value || busy.value || !knowledgeId.value.trim()) return
  busy.value = true
  errorMessage.value = ''
  bindingNoticeText.value = ''
  const projectId = project.value.id
  const input = { knowledge_id: knowledgeId.value.trim() }
  const key = operationKey(`asset:${projectId}`, input)
  try {
    // 响应体就是绑定后的那一份资料（带 processing_state）。此前它被丢掉了，于是
    // 「绑定成功但还没就绪」这件事在界面上完全没有痕迹——只见它 201 之后从列表里消失。
    const bound = await bindAsset(projectId, input.knowledge_id, key)
    attempts.delete(`asset:${projectId}`)
    knowledgeId.value = ''
    bindingNoticeText.value = bindingNotice(bound.data)
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
  if (generationTimer) clearTimeout(generationTimer)
  generationTimer = undefined
  chapter.value = item
  bodyDraft.value = item.body_markdown
  errorMessage.value = ''
  generationRun.value = null
  generationCandidate.value = null
  generationCandidates.value = []
  // 对话框挂着上一个章节的幂等键与失败文案时，props 会被换成新的章节而组件不会重建
  //（同一位置、同一类型）。关掉它，保证每次打开都是干净的一份。
  adoptionOpen.value = false
  void loadGenerationCandidates(item.id)
  void resumeGeneration()
}

async function loadGenerationCandidates(chapterId = chapter.value?.id) {
  if (!project.value || !chapterId) {
    generationCandidates.value = []
    return
  }
  const projectId = project.value.id
  try {
    const result = await listGenerationCandidates(projectId, chapterId)
    if (project.value?.id === projectId && chapter.value?.id === chapterId) generationCandidates.value = result.data
  } catch (error) { failure(error) }
}

async function showGenerationCandidate(runId: string) {
  if (!project.value || generationBusy.value) return
  generationBusy.value = true
  try {
    const result = await getGeneratedCandidate(project.value.id, runId)
    generationCandidate.value = result.data
  } catch (error) { failure(error) }
  finally { generationBusy.value = false }
}

function openAdoption() {
  if (!project.value || !chapter.value || !generationCandidate.value || busy.value) return
  adoptionOpen.value = true
}

// 采纳成功后必须重读**整个**工作区，而不是只换章节。
//
// 采纳会推进项目版本；只更新 chapters/chapter 会让界面拿着旧的 project_version 去发
// 下一个请求——下一个动作必然 409，而用户什么都没做错。
//
// 这里刻意不再顺手调 saveChapter/confirmChapter：采纳已经是整章替换，下一步该由人
// 看过正文再决定（§5 要求采纳后重新确认待核项）。也不要复用 saveText 那把幂等键——
// 对话框有自己的签名，两者的「同一件事」定义不同。
async function onAdopted(adopted: Chapter) {
  adoptionOpen.value = false
  if (!project.value || !chapter.value) return
  const projectId = project.value.id
  const chapterId = chapter.value.id
  const index = chapters.value.findIndex(item => item.id === chapterId)
  if (index >= 0) chapters.value[index] = adopted
  chapter.value = adopted
  // 不同步的话 bodyChanged 为真、保存按钮亮着，用户一点就把刚采纳的正文又存成一版。
  bodyDraft.value = adopted.body_markdown
  // 待核项已经换成候选自带那一份，为本章编的逐项处置不再适用。只清本章的：
  // 别的章节的草稿是用户刚写的理由，采纳这一章不该把它抹掉。
  for (const key of Object.keys(reviewDrafts.value)) {
    if (key.startsWith(`${chapterId}:`)) delete reviewDrafts.value[key]
  }
  generationCandidate.value = null
  try {
    const [refreshed, candidates] = await Promise.all([
      getProject(projectId), listGenerationCandidates(projectId, chapterId),
    ])
    project.value = refreshed.data
    // 旧候选的 validity 是按旧版本算出来的，不重读会继续显示一条已经不成立的 fresh。
    generationCandidates.value = candidates.data
  } catch (error) { failure(error) }
}

function generationStorageKey(projectId: string, chapterId: string) {
  return `lingdoc:generation:${projectId}:${chapterId}`
}

async function resumeGeneration() {
  if (!project.value || !chapter.value) return
  const savedRunId = localStorage.getItem(generationStorageKey(project.value.id, chapter.value.id))
  if (savedRunId) await refreshGeneration(savedRunId)
}

async function refreshGeneration(runId = generationRun.value?.id) {
  if (!project.value || !runId || generationBusy.value) return
  generationBusy.value = true
  const projectId = project.value.id
  try {
    const result = await getGeneration(projectId, runId)
    if (project.value?.id !== projectId || chapter.value?.id !== result.data.chapter_id) return
    generationRun.value = result.data
    generationCandidate.value = null
    if (result.data.status === 'succeeded' && result.data.candidate_id) {
      const candidate = await getGeneratedCandidate(projectId, runId)
      if (project.value?.id !== projectId || chapter.value?.id !== candidate.data.chapter_id) return
      generationCandidate.value = candidate.data
      await loadGenerationCandidates(candidate.data.chapter_id)
    }
    if (result.data.status === 'queued' || result.data.status === 'running') {
      if (generationTimer) clearTimeout(generationTimer)
      generationTimer = setTimeout(() => { void refreshGeneration(runId) }, 2000)
    }
  } catch (error) { failure(error) }
  finally { generationBusy.value = false }
}

async function cancelGenerationRun() {
  if (!project.value || !generationRun.value || generationBusy.value) return
  const runId = generationRun.value.id
  generationBusy.value = true
  try {
    const result = await cancelGeneration(project.value.id, runId)
    generationRun.value = result.data
  } catch (error) { failure(error) }
  finally { generationBusy.value = false }
  if (generationRun.value?.status === 'queued' || generationRun.value?.status === 'running') {
    if (generationTimer) clearTimeout(generationTimer)
    generationTimer = setTimeout(() => { void refreshGeneration(runId) }, 2000)
  }
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
    // 确认会推进项目版本（确认记在项目上），所以项目必须跟着重读一次。少了这一步，
    // 紧接着的交付检查与冻结会拿着一个过期的 expected_project_version 去问，换来一个
    // 409——而用户什么都没做错。保存章节那条路径早就在重读，确认这条一直漏着。
    const refreshed = await getProject(projectId)
    project.value = refreshed.data
  } catch (error) { failure(error) }
  finally { busy.value = false }
}

async function startDraft() {
  if (!project.value || !chapter.value || busy.value || !selectedAssetIds.value.length || !generationInstruction.value.trim()) return
  busy.value = true
  errorMessage.value = ''
  const projectId = project.value.id
  const chapterId = chapter.value.id
  const input = {
    chapter_id: chapterId,
    asset_ids: [...selectedAssetIds.value].sort(),
    instruction: generationInstruction.value.trim(),
    expected_spec_revision: project.value.spec_revision,
    expected_chapter_version_id: chapter.value.current_version_id,
  }
  try {
    const key = await generationIdempotencyKey(localStorage, projectId, chapterId, input)
    const result = await startGeneration(projectId, input, key)
    generationRun.value = result.data
    generationCandidate.value = null
    localStorage.setItem(generationStorageKey(projectId, chapterId), result.data.id)
    clearGenerationAttempt(localStorage, projectId, chapterId)
    await refreshGeneration(result.data.id)
  } catch (error) { failure(error) }
  finally { busy.value = false }
}

async function saveText() {
  if (!project.value || !chapter.value || busy.value) return
  // 引用从正文里读，与服务端用同一条规则。写坏的标记在本地就拦下：服务端也会判 400，
  // 但「请求字段不符合约定」说不清是哪里坏了，而这一刻我们完全知道。
  const citations = chapterCitations(bodyDraft.value)
  if (citations.kind === 'malformed') {
    errorMessage.value = MALFORMED_CITATION_MESSAGE
    return
  }
  busy.value = true
  errorMessage.value = ''
  const projectId = project.value.id
  const chapterId = chapter.value.id
  const input = {
    expected_chapter_version_id: chapter.value.current_version_id,
    expected_spec_revision: project.value.spec_revision,
    body_markdown: bodyDraft.value,
    source_ids: citations.sourceIds,
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
onUnmounted(() => { if (generationTimer) clearTimeout(generationTimer) })
</script>

<style scoped>
.lingdoc-workspace { max-width: 1200px; margin: 0 auto; padding: 32px; color: #24342e; }
.workspace-head, .section-head, .actions { display: flex; justify-content: space-between; align-items: center; gap: 16px; }
h1 { margin: 0 0 8px; font-size: 28px; } h2 { margin: 0 0 12px; font-size: 20px; } h3 { margin: 22px 0 14px; font-size: 17px; }
p { margin: 6px 0; } .muted { color: #6b7670; font-size: 13px; } .warning { color: #8b5b10; font-size: 13px; }
.alert { padding: 12px 16px; margin: 20px 0; background: #fff1ee; border: 1px solid #eea99e; border-radius: 8px; }
.workspace-grid { display: grid; grid-template-columns: 280px minmax(0, 1fr); gap: 20px; margin-top: 24px; }
.panel { background: #fff; border: 1px solid #dbe5dd; border-radius: 12px; padding: 22px; min-width: 0; }
.create-form, .spec-form, .chapter-form, .generation-form { display: flex; flex-direction: column; gap: 10px; }
label { font-weight: 600; font-size: 14px; }
input, textarea { width: 100%; box-sizing: border-box; padding: 10px 12px; border: 1px solid #becdc3; border-radius: 7px; font: inherit; }
button { padding: 8px 12px; border: 1px solid #becdc3; border-radius: 7px; background: #fff; color: #25452f; cursor: pointer; }
button:hover:not(:disabled), button.selected { border-color: #238a52; background: #edf8f0; }
button:disabled { opacity: .55; cursor: not-allowed; }
.project-list { list-style: none; padding: 0; display: grid; gap: 7px; }
.project-list button { width: 100%; display: flex; justify-content: space-between; text-align: left; }
.project-list small, .chapter-tabs small { color: #67746a; margin-left: 8px; }
.asset-list { list-style: none; padding: 0; display: grid; gap: 8px; }
.asset-bind-form { display: flex; flex-direction: column; gap: 8px; margin-bottom: 14px; }
.asset-bind-row { display: flex; gap: 8px; }
.asset-bind-row input { flex: 1; min-width: 0; }
.asset-list li { display: flex; justify-content: space-between; gap: 12px; padding: 10px 12px; border: 1px solid #dbe5dd; border-radius: 7px; }
.asset-list span { display: flex; flex-direction: column; gap: 3px; min-width: 0; }
.asset-list small, .asset-list em { color: #67746a; font-size: 12px; font-style: normal; }
.generation-assets { display: grid; gap: 8px; margin: 0 0 12px; padding: 12px; border: 1px solid #dbe5dd; border-radius: 7px; }
.asset-choice { display: flex; align-items: center; gap: 8px; font-weight: 400; }
.asset-choice input { width: auto; }
.candidate-list { margin-top: 14px; }
.candidate-list ul { display: grid; gap: 6px; padding-left: 20px; }
.candidate-preview { margin-top: 14px; padding: 14px; border: 1px solid #dbe5dd; border-radius: 8px; background: #f7faf8; }
.candidate-preview pre { white-space: pre-wrap; overflow-wrap: anywhere; font: inherit; }
.binding-notice { padding: 10px 12px; background: #fff8ec; border: 1px solid #e6c98a; border-radius: 7px; color: #6b5a2e; font-size: 13px; }
.denied-sources { margin: 12px 0; padding: 12px 16px; background: #fff1ee; border: 1px solid #eea99e; border-radius: 8px; font-size: 13px; }
.denied-sources ul { display: grid; gap: 8px; margin: 8px 0; padding-left: 20px; }
.denied-sources li { display: grid; gap: 2px; }
.denied-sources small { color: #6b7670; font-size: 12px; }
.source-list { list-style: none; padding: 0; display: grid; gap: 10px; }
.source-list > li { display: grid; gap: 8px; padding: 10px 12px; border: 1px solid #dbe5dd; border-radius: 7px; }
.source-head { display: flex; flex-wrap: wrap; justify-content: space-between; align-items: flex-start; gap: 12px; }
.source-head span { display: flex; flex-direction: column; gap: 3px; min-width: 0; }
.source-head small { color: #67746a; font-size: 12px; }
.source-actions { display: flex; flex-wrap: wrap; gap: 8px; }
.source-quote { margin: 0; font-size: 13px; }
.source-context { display: grid; gap: 8px; padding: 12px; background: #f7faf8; border: 1px solid #dbe5dd; border-radius: 7px; }
.source-context h4 { margin: 0; font-size: 13px; }
.source-context p { margin: 0; font-size: 12px; }
.source-context__segments { display: grid; gap: 8px; margin: 0; padding-left: 20px; }
.source-context__segments li { display: grid; gap: 3px; font-size: 13px; }
.source-context__segments p { font-size: 13px; }
.source-context__segments small { color: #6b7670; font-size: 12px; }
.source-context__segments .is-verbatim > small:first-child { color: #238a52; font-weight: 600; }
.chapter-tabs { display: flex; flex-wrap: wrap; gap: 8px; margin-bottom: 14px; }
.access-warning { display: grid; gap: 10px; margin: 22px 0 0; padding: 14px 16px; background: #fff8ec; border: 1px solid #e6c98a; border-radius: 8px; }
.access-warning h3 { margin: 0; font-size: 15px; }
.access-warning__actions { display: grid; gap: 8px; margin: 0; padding-left: 20px; }
.access-warning__actions li { display: grid; gap: 2px; }
.access-warning__actions span { color: #6b7670; font-size: 13px; }
.access-warning button { justify-self: start; }
.empty-work { display: grid; place-items: center; min-height: 300px; color: #6b7670; }
@media (max-width: 760px) { .workspace-grid { grid-template-columns: 1fr; } .lingdoc-workspace { padding: 16px; } }
</style>
