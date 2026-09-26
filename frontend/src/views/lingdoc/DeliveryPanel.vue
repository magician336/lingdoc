<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import type { Chapter, Project } from '@/api/lingdoc/workspace'
import {
  checkDelivery, downloadExport, getExport, getRelease, listExports, listReleases, newDeliveryKey,
  prepareRelease, startExport,
  type CheckResult, type CheckStatus, type ExportArtifact, type ReleaseSnapshot,
} from '@/api/lingdoc/delivery'
import { currencyOf, issueTargetLabel, viewOf, type Currency } from './deliveryState'

// T13 与 T14 的界面切片：交付检查、冻结快照、导出与下载。
//
// 这一块整体挂在工作区的章节区之后，用的是工作台自己那套朴素样式（内联 role="alert"、
// BEM 类名、硬编码中文），不走全仓默认的 TDesign 与 vue-i18n——整个 lingdoc 工作台
// 是一套自洽的界面。

const props = defineProps<{
  project: Project
  chapters: Chapter[]
}>()

// 项目是上层传进来的 prop，面板改不了它。而检查与冻结都要交一个 expected_project_version
// 上去，过期了就只能请上层重读——这件事面板自己做不了。
const emit = defineEmits<{ refreshProject: [] }>()

const statusLabels: Record<CheckStatus, string> = {
  passed: '通过',
  blocked: '已阻断',
  not_evaluated: '未评估',
}

/**
 * 一行产物的渲染数据。
 *
 * 把 ArtifactView 那个联合在这里摊平成模板直接读的字段，是因为模板里没法收窄联合类型：
 * 不摊平的话，渲染失败原因的每个位置都得写一次类型断言，而断言写错了编译器不会说话。
 * 判定仍旧只由 viewOf 做一次。
 */
interface ArtifactRow {
  id: string
  snapshotID: string
  currency: Currency
  /** 空串 = 没有可下载的文件。 */
  downloadPath: string
  fileSHA256: string
  failure: { message: string; retryable: boolean } | null
}

const checks = ref<CheckResult | null>(null)
const snapshots = ref<ReleaseSnapshot[]>([])
const artifacts = ref<ExportArtifact[]>([])
const historyTruncated = ref(false)
const artifactTruncated = ref(false)

/**
 * 每个快照的当前性，**只**来自 getRelease 的读取结果。
 *
 * 契约 §7 要求按 snapshot_id 调用 getRelease 来判定当前性，所以这里不拿冻结响应的
 * is_current（那是冻结当时存下的值），也不拿列表里的（列表虽然后端重算过，但界面要
 * 展示或下载的那一条必须以这次读取为准）。一个键存在但没有值 = 这次没读到。
 */
const currencyReads = ref<Record<string, ReleaseSnapshot | undefined>>({})

/** 正在进行的动作。按钮的 loading 就是「导出进度」——导出是同步的，没有轮询可做。 */
const pending = ref('')
const busy = computed(() => pending.value !== '')
const errorMessage = ref('')

/** 冻结：同一个待冻结状态重试时复用同一个键，成功之后下一次冻结是一个新动作。 */
const freezeAttempt = ref<{ signature: string; key: string } | null>(null)

/** 导出：每个快照各自一条。键在成功后清掉，所以「再导一份」是新动作而不是重放。 */
const exportAttempts = new Map<string, { signature: string; key: string }>()

const rowsBySnapshot = computed(() => {
  const map: Record<string, ArtifactRow[]> = {}
  for (const artifact of artifacts.value) {
    const view = viewOf(artifact)
    const row: ArtifactRow = {
      id: artifact.id,
      snapshotID: artifact.snapshot_id,
      currency: currencyOf(currencyReads.value[artifact.snapshot_id]),
      downloadPath: view.kind === 'downloadable' ? view.downloadPath : '',
      fileSHA256: view.kind === 'downloadable' ? view.fileSHA256 : '',
      failure: view.kind === 'failed' ? { message: view.message, retryable: view.retryable } : null,
    }
    ;(map[artifact.snapshot_id] ??= []).push(row)
  }
  return map
})

function fail(error: unknown, fallback: string) {
  const item = error as { message?: string }
  errorMessage.value = item?.message || fallback
}

/**
 * 契约 §3：409 version_conflict 说的是「你手上的项目版本已经过期」。交付面板没有要保留的
 * 本地编辑，所以正确的动作是重新读一遍当前项目。少了这一步，用户会卡在一个自己解不开的
 * 409 上——面板手上的版本永远不会变，每次重试都是同一个错。协作编辑是这条路径的现实来源。
 */
function recoverVersionConflict(error: unknown): boolean {
  const item = error as { status?: number; error?: { code?: string } }
  if (item?.status !== 409 || item?.error?.code !== 'version_conflict') return false
  errorMessage.value = '项目内容已被修改。已重新读取当前版本，请再试一次。'
  emit('refreshProject')
  return true
}

function freezeKey(expected: number): string {
  const signature = JSON.stringify({ project_version: expected })
  if (freezeAttempt.value?.signature === signature) return freezeAttempt.value.key
  const key = newDeliveryKey()
  freezeAttempt.value = { signature, key }
  return key
}

function exportKey(snapshotId: string, signature: unknown): string {
  const serialized = JSON.stringify(signature)
  const old = exportAttempts.get(snapshotId)
  if (old?.signature === serialized) return old.key
  const key = newDeliveryKey()
  exportAttempts.set(snapshotId, { signature: serialized, key })
  return key
}

/**
 * 读一次快照的当前性。读取失败**必须**落成 undefined：§7 说那种情况显示「当前性未知」，
 * 而沿用上一次的「当前内容」正是它点名要避免的事——用户会以为一份已经过期的文件还代表
 * 着工作区。
 */
async function refreshCurrency(snapshotId: string) {
  const projectId = props.project.id
  try {
    const result = await getRelease(projectId, snapshotId)
    // 读取期间用户可能已经换了项目；这一条属于上一个项目，不能写进当前界面。
    if (projectId !== props.project.id) return
    currencyReads.value[snapshotId] = result.data
  } catch (error) {
    if (projectId !== props.project.id) return
    currencyReads.value[snapshotId] = undefined
    fail(error, '无法读取交付快照的当前状态。')
  }
}

/** 重拉两份历史，并把界面上会展示的每一个快照的当前性重新读一遍。 */
async function reload() {
  const projectId = props.project.id
  const [releases, exports] = await Promise.all([
    listReleases(projectId),
    listExports(projectId),
  ])
  // 同一条守卫：慢的那次读取不能把上一个项目的历史盖到新项目上。
  if (projectId !== props.project.id) return
  snapshots.value = releases.data.items
  historyTruncated.value = releases.data.truncated
  artifacts.value = exports.data.items
  artifactTruncated.value = exports.data.truncated
  // 并集而不是只看快照列表：历史被截断时，产物仍可能指向一份没列出来的快照。
  const ids = [...new Set([
    ...snapshots.value.map((item) => item.id),
    ...artifacts.value.map((item) => item.snapshot_id),
  ])]
  await Promise.all(ids.map(refreshCurrency))
}

async function load() {
  try {
    await reload()
  } catch (error) { fail(error, '无法读取交付历史。') }
}

// 只有用户明确要求重来一次时才清掉上一条报错。自动重读不清：版本冲突刚提示「已重新
// 读取当前版本，请再试一次」，紧接着的自动刷新会把那句话抹掉，用户只看到按钮什么都没
// 发生——而这个提示正是他唯一能知道发生了什么的地方。
function refreshHistory() {
  errorMessage.value = ''
  void load()
}

async function runCheck() {
  if (busy.value) return
  pending.value = 'check'
  errorMessage.value = ''
  try {
    const result = await checkDelivery(props.project.id, props.project.project_version)
    checks.value = result.data
  } catch (error) { if (!recoverVersionConflict(error)) fail(error, '交付检查失败，请重试。') }
  finally { pending.value = '' }
}

async function freeze() {
  if (busy.value) return
  pending.value = 'freeze'
  errorMessage.value = ''
  const expected = props.project.project_version
  try {
    // 返回值不接：重放回来的快照带着**冻结当时**写下的 is_current，拿它渲染当前性正是
    // §7 点名要避免的事。
    await prepareRelease(props.project.id, expected, freezeKey(expected))
    freezeAttempt.value = null
  } catch (error) {
    if (!recoverVersionConflict(error)) fail(error, '冻结交付快照失败，请重试。')
    return
  } finally { pending.value = '' }
  // 重读放在 try 外面：冻结**已经成了**，这一步失败绝不能报成「冻结失败」——用户会
  // 再冻一次，而那是另一份快照。这次重读同时满足 §6 对重放的要求：界面先取当前状态。
  await load()
}

async function exportSnapshot(snapshot: ReleaseSnapshot) {
  if (busy.value) return
  pending.value = `export:${snapshot.id}`
  errorMessage.value = ''
  try {
    await startExport(props.project.id, snapshot.id, exportKey(snapshot.id, snapshot.snapshot_digest))
    exportAttempts.delete(snapshot.id)
  } catch (error) {
    if (!recoverVersionConflict(error)) fail(error, '生成交付文件失败，请重试。')
    return
  } finally { pending.value = '' }
  // 同 freeze：文件已经渲出来了，重读失败不能报成「生成失败」，否则用户会再导一份。
  await load()
}

/**
 * 重读一份产物与它所属快照的当前性。
 *
 * 查询顺序是**规格**不是实现细节：契约的验证器对 F21 有一条静态断言，界面必须先拿到
 * 产物、再用它的 snapshot_id 去读 getRelease。所以这里认证与取引用都从产物出发，不从
 * 列表里的快照反查。
 */
async function refreshArtifact(exportId: string): Promise<ExportArtifact> {
  const result = await getExport(props.project.id, exportId)
  const fresh = result.data
  const index = artifacts.value.findIndex((item) => item.id === exportId)
  if (index >= 0) artifacts.value[index] = fresh
  // 不在这里先把当前性抹成未知再读：那样每次下载都会闪一下「当前性未知」再变回来，
  // 而 refreshCurrency 两条分支都会写这个键，抹与不抹是同一个结果。
  await refreshCurrency(fresh.snapshot_id)
  return fresh
}

async function download(row: ArtifactRow) {
  if (busy.value) return
  pending.value = `download:${row.id}`
  errorMessage.value = ''
  try {
    // §7：「展示下载入口时」也要更新当前性。所以下载前先重读一次——这条读取失败时标签
    // 会落回「当前性未知」，但仍然照常把文件下载下来：能不能下载与是不是当前内容，
    // 是两个问题。
    const fresh = await refreshArtifact(row.id)
    const view = viewOf(fresh)
    if (view.kind !== 'downloadable') {
      errorMessage.value = '这份交付文件当前不可下载，状态已刷新。'
      return
    }
    // 每次都真发一次请求：后端在**下载那一刻**重查授权。所以这里不缓存 Blob。
    const blob = await downloadExport(view.downloadPath)
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.style.display = 'none'
    link.href = url
    link.download = `lingdoc-delivery-${row.snapshotID.slice(0, 8)}.docx`
    document.body.appendChild(link)
    link.click()
    nextTick(() => {
      document.body.removeChild(link)
      URL.revokeObjectURL(url)
    })
  } catch (error) { fail(error, '下载失败，请重试。') }
  finally { pending.value = '' }
}

// 覆盖「页面刷新」「重新打开」（挂载即读）与「本地编辑成功」：保存章节后
// Workspace.vue 会重新读取项目，project_version 随之变化，旧快照于是自动标成历史。
watch(
  () => [props.project.id, props.project.project_version] as const,
  (current, previous) => {
    if (current[0] !== previous?.[0]) {
      checks.value = null
      currencyReads.value = {}
      freezeAttempt.value = null
      exportAttempts.clear()
    }
    void load()
  },
  { immediate: true },
)
</script>

<template>
  <section class="delivery" aria-labelledby="delivery-title">
    <h3 id="delivery-title">交付</h3>
    <p class="muted">检查当前内容能否交付，冻结一份不再随编辑变化的快照，再导出成文件。</p>

    <p v-if="errorMessage" role="alert" class="alert">{{ errorMessage }}</p>

    <div class="delivery__actions">
      <button type="button" :disabled="busy" @click="runCheck">
        {{ pending === 'check' ? '检查中…' : '交付检查' }}
      </button>
      <button type="button" :disabled="busy" @click="freeze">
        {{ pending === 'freeze' ? '冻结中…' : '冻结当前内容' }}
      </button>
      <button type="button" :disabled="busy" @click="refreshHistory">刷新交付历史</button>
    </div>

    <section v-if="checks" class="delivery__result" aria-label="交付检查结果">
      <p :class="['delivery__status', `delivery__status--${checks.status}`]">
        检查结果：{{ statusLabels[checks.status] }}
        <small>（项目版本 {{ checks.project_version }}）</small>
      </p>
      <ul v-if="checks.issues.length" class="delivery__issues">
        <li v-for="issue in checks.issues" :key="issue.id">
          <span class="delivery__target">{{ issueTargetLabel(issue, project.id, chapters) }}</span>
          <em :class="['delivery__severity', { 'delivery__severity--blocking': issue.severity === 'blocking' }]">
            {{ issue.severity === 'blocking' ? '阻断' : '提示' }}
          </em>
          <p>{{ issue.message }}</p>
        </li>
      </ul>
      <p v-else class="muted">没有发现问题。</p>
    </section>

    <p v-if="!snapshots.length" class="muted">还没有冻结过交付快照。</p>
    <template v-else>
      <p v-if="historyTruncated" class="muted">只显示最近 {{ snapshots.length }} 份快照，更早的未列出。</p>
      <ul class="delivery__history">
        <li v-for="snapshot in snapshots" :key="snapshot.id" class="delivery__snapshot">
          <header class="delivery__snapshot-head">
            <strong>快照 {{ snapshot.id.slice(0, 8) }}</strong>
            <span class="delivery__currency">{{ currencyOf(currencyReads[snapshot.id]) }}</span>
          </header>
          <p class="muted">
            冻结于项目版本 {{ snapshot.frozen_input.project_version }} ·
            研究条件第 {{ snapshot.frozen_input.spec_revision }} 版 ·
            章节 {{ snapshot.frozen_input.chapters.length }} 章 ·
            资料 {{ snapshot.frozen_input.sources.length }} 份 ·
            摘要 {{ snapshot.snapshot_digest.slice(0, 12) }}
          </p>
          <p class="muted">
            交付检查：{{ statusLabels[snapshot.check.status] }} ·
            冻结时间 {{ new Date(snapshot.created_at).toLocaleString() }}
          </p>
          <p v-if="snapshot.check.issues.length" class="warning">
            这份快照带着 {{ snapshot.check.issues.length }} 条检查记录。
          </p>

          <div class="delivery__actions">
            <button type="button" :disabled="busy" @click="exportSnapshot(snapshot)">
              {{ pending === `export:${snapshot.id}` ? '生成中…' : '生成 DOCX' }}
            </button>
          </div>

          <ul v-if="rowsBySnapshot[snapshot.id]?.length" class="delivery__artifacts">
            <li v-for="row in rowsBySnapshot[snapshot.id]" :key="row.id">
              <span class="delivery__artifact-id">{{ row.id.slice(0, 8) }}</span>
              <em>{{ row.currency }}</em>

              <template v-if="row.downloadPath">
                <button type="button" :disabled="busy" @click="download(row)">
                  {{ pending === `download:${row.id}` ? '下载中…' : '下载' }}
                </button>
                <small>摘要 {{ row.fileSHA256.slice(0, 12) }}</small>
              </template>

              <template v-else-if="row.failure">
                <p role="alert" class="warning">
                  {{ row.failure.message }}
                  <template v-if="row.failure.retryable">可以再生成一次；这会是一份新的导出。</template>
                </p>
              </template>

              <small v-else>这份产物当前没有可下载的文件。</small>
            </li>
          </ul>
        </li>
      </ul>
      <p v-if="artifactTruncated" class="muted">只显示最近 {{ artifacts.length }} 份交付文件。</p>
    </template>
  </section>
</template>

<style scoped>
.delivery { margin-top: 28px; padding-top: 20px; border-top: 1px solid #dbe5dd; }
.delivery__actions { display: flex; flex-wrap: wrap; gap: 10px; align-items: center; margin: 12px 0; }
.delivery__result { padding: 14px 16px; border: 1px solid #dbe5dd; border-radius: 10px; background: #f7faf8; }
.delivery__status { margin: 0 0 10px; font-weight: 600; }
.delivery__status--passed { color: #1c6b3f; }
.delivery__status--blocked { color: #b3261e; }
.delivery__status--not_evaluated { color: #6b7670; }
.delivery__issues { margin: 0; padding-left: 20px; }
.delivery__issues li { margin-bottom: 10px; }
.delivery__target { font-weight: 600; }
.delivery__severity { margin-left: 8px; padding: 1px 7px; border-radius: 999px; background: #eef2f0; color: #4a564f; font-size: 12px; font-style: normal; }
.delivery__severity--blocking { background: #fdecea; color: #b3261e; }
.delivery__issues p { margin: 4px 0; }
.delivery__history { margin: 12px 0 0; padding: 0; list-style: none; }
.delivery__snapshot { margin-bottom: 14px; padding: 16px; border: 1px solid #dbe5dd; border-radius: 10px; background: #fff; }
.delivery__snapshot-head { display: flex; flex-wrap: wrap; gap: 10px; align-items: center; justify-content: space-between; }
.delivery__currency { padding: 2px 10px; border-radius: 999px; background: #edf8f0; color: #25452f; font-size: 12px; }
.delivery__artifacts { margin: 8px 0 0; padding: 0; list-style: none; }
.delivery__artifacts li { display: flex; flex-wrap: wrap; gap: 10px; align-items: center; padding: 8px 0; border-top: 1px dashed #e3ebe6; }
.delivery__artifact-id { font-variant-numeric: tabular-nums; color: #4a564f; }
</style>
