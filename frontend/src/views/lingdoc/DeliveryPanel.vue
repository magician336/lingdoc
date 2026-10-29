<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import type { Chapter, Project } from '@/api/lingdoc/workspace'
import {
  checkDelivery, checkTemplate, downloadExport, getExport, getRelease, listExports, listReleases, newDeliveryKey, setIssueDisposition,
  prepareRelease, startExport,
  type CheckResult, type CheckStatus, type ExportArtifact, type ReleaseSnapshot, type TemplateCheckResult, type ValidationIssueDisposition,
} from '@/api/lingdoc/delivery'
import { canExportSnapshot, currencyOf, issueTargetLabel, viewOf, type Currency } from './deliveryState'

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
const emit = defineEmits<{ refreshProject: [done?: (ok: boolean) => void];computedReceipt:[id:string];reviewChangeReceipt:[id:string] }>()

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
const templateChecks = ref<TemplateCheckResult | null>(null)
const snapshots = ref<ReleaseSnapshot[]>([])
const artifacts = ref<ExportArtifact[]>([])
const historyTruncated = ref(false)
const artifactTruncated = ref(false)
const historyLoading = ref(false)
const historyLoaded = ref(false)
const historyError = ref(false)

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
const refreshingProject = ref(false)
let refreshSequence = 0
const busy = computed(() => pending.value !== '' || refreshingProject.value)
const errorMessage = ref('')
const operationNotice = ref('')
const checkNotice = ref('')
const templateCheckNotice = ref('')

// Bind asynchronous reads to the inputs displayed by this panel. A project
// version normally advances when these change; keeping the explicit pins also
// handles a refreshed template, chapter, or membership arriving separately.
function deliveryBasis(): string {
  const project = props.project
  return JSON.stringify([
    accessSequence, project.id, project.project_version, project.template_id, project.template_version,
    project.template_copy_version, project.template_copy?.content_hash, project.template_copy?.ruleset_hash,
    project.members, props.chapters.map(chapter => [chapter.id, chapter.current_version_id, chapter.confirmation_valid]),
  ])
}

let accessSequence = 0
let operationSequence = 0
let historySequence = 0
let currencySequence = 0
const currencyRequests = new Map<string, number>()

function frozenTemplateReference(snapshot: ReleaseSnapshot): string {
  const input = snapshot.frozen_input as typeof snapshot.frozen_input & {
    template?: { id?: string; version?: string }
    template_copy_version?: number
    template_copy_content_hash?: string
  }
  const definition = input.template?.id && input.template.version
    ? input.template.id + ' / ' + input.template.version : '服务端未提供模板版本'
  return definition + (input.template_copy_version ? ' · 项目副本第 ' + input.template_copy_version + ' 版' : '') +
    (input.template_copy_content_hash ? ' · 内容摘要 ' + input.template_copy_content_hash : '')
}

// The service may return an immutable RuleSet reference in addition to its
// effective hash. Older or legacy results need not have that optional field.
function ruleSetReference(value: unknown): string {
  const ref = (value as { ruleset_ref?: { ruleset_id?: string; version?: string; ruleset_hash?: string } } | null)?.ruleset_ref
  if (!ref?.ruleset_id || !ref.version) return '服务端未提供规则集版本'
  return `${ref.ruleset_id} / ${ref.version}${ref.ruleset_hash ? ` · 来源摘要 ${ref.ruleset_hash}` : ''}`
}

/** 冻结：同一个待冻结状态重试时复用同一个键，成功之后下一次冻结是一个新动作。 */
const freezeAttempts = new Map<string, string>()
const dispositionActions = ref<Record<string, ValidationIssueDisposition['action']>>({})
const dispositionReasons = ref<Record<string, string>>({})
const dispositionAttempts = new Map<string, string>()

/** 导出：每个快照各自一条。键在成功后清掉，所以「再导一份」是新动作而不是重放。 */
const exportAttempts = new Map<string, string>()

function toRow(artifact: ExportArtifact): ArtifactRow {
  const view = viewOf(artifact)
  return {
    id: artifact.id,
    snapshotID: artifact.snapshot_id,
    currency: currencyOf(currencyReads.value[artifact.snapshot_id]),
    downloadPath: view.kind === 'downloadable' ? view.downloadPath : '',
    fileSHA256: view.kind === 'downloadable' ? view.fileSHA256 : '',
    failure: view.kind === 'failed' ? { message: view.message, retryable: view.retryable } : null,
  }
}

/** 历史里的一条：一份快照，以及挂在它名下的那些产物。 */
interface DeliveryGroup {
  /** 快照不在当前列表里时为 null——这一组只有产物，没有快照可描述。 */
  snapshot: ReleaseSnapshot | null
  snapshotID: string
  rows: ArtifactRow[]
}

/**
 * 把产物按 snapshot_id 挂到各自的快照下面。
 *
 * 挂不上的那些必须单独成组，不能丢：快照列表是服务端截断过的（上限 50），而产物列表
 * 不跟着快照截断，所以一份早期导出完全可能指向一份没被列出来的快照。丢了它的话，
 * listExports 明明返回了它、reload 甚至为它读了一次当前性，界面上却一个像素都没有——
 * 用户会以为它从没导出过，而后端里它还在、按 ID 也还下得下来。
 */
const groups = computed<DeliveryGroup[]>(() => {
  const rowsBySnapshot: Record<string, ArtifactRow[]> = {}
  for (const artifact of artifacts.value) {
    (rowsBySnapshot[artifact.snapshot_id] ??= []).push(toRow(artifact))
  }
  const listed = new Set(snapshots.value.map((item) => item.id))
  return [
    ...snapshots.value.map((snapshot) => ({
      snapshot, snapshotID: snapshot.id, rows: rowsBySnapshot[snapshot.id] ?? [],
    })),
    ...Object.keys(rowsBySnapshot)
      .filter((snapshotID) => !listed.has(snapshotID))
      .map((snapshotID) => ({ snapshot: null, snapshotID, rows: rowsBySnapshot[snapshotID] })),
  ]
})

function fail(error: unknown, fallback: string) {
  const item = error as { message?: string; status?: number; $httpStatus?: number }
  const status = item?.$httpStatus ?? item?.status
  if (status === 401 || status === 403 || status === 404) {
    accessSequence++
    // A prior result or currency read cannot be carried across a denial.
    historySequence++
    currencySequence++
    checks.value = null
    templateChecks.value = null
    historyLoading.value = false
    historyLoaded.value = false
    historyError.value = true
    currencyReads.value = {}
    snapshots.value = []
    artifacts.value = []
    errorMessage.value = status === 401
      ? '登录状态已失效，请重新登录后重试。'
      : status === 404
        ? '课题或交付资源不存在，或当前账号无法访问。请重新读取课题和交付历史。'
        : '当前账号无权读取或执行此操作。请核对课题与资料权限后重试。'
    return
  }
  errorMessage.value = item?.message ? fallback + '（' + item.message + '）' : fallback
}

// Access denials are scoped to the project, not to the content version. An
// older request can still reveal that the current session lost access.
function handleAccessDenial(error: unknown, projectId: string): boolean {
  const item = error as { status?: number; $httpStatus?: number }
  const status = item?.$httpStatus ?? item?.status
  if (projectId !== props.project.id || ![401, 403, 404].includes(status ?? 0)) return false
  fail(error, '')
  return true
}

/**
 * 契约 §3：409 version_conflict 说的是「你手上的项目版本已经过期」。交付面板没有要保留的
 * 本地编辑，所以正确的动作是重新读一遍当前项目。少了这一步，用户会卡在一个自己解不开的
 * 409 上——面板手上的版本永远不会变，每次重试都是同一个错。协作编辑是这条路径的现实来源。
 */
watch(() => props.project.id, () => {
  refreshSequence++
  refreshingProject.value = false
})

function recoverVersionConflict(error: unknown): boolean {
  const item = error as { status?: number; $httpStatus?: number; code?: string; error?: { code?: string } }
  if ((item?.status !== 409 && item?.$httpStatus !== 409) || (item?.error?.code !== 'version_conflict' && item?.code !== 'version_conflict')) return false
  const projectId = props.project.id
  const sequence = ++refreshSequence
  refreshingProject.value = true
  errorMessage.value = '项目内容已被修改，正在重新读取当前版本…'
  emit('refreshProject', (ok) => {
    if (props.project.id !== projectId || refreshSequence !== sequence) return
    refreshingProject.value = false
    errorMessage.value = ok
      ? '项目内容已被修改。已重新读取当前版本，请再试一次。'
      : '项目版本重新读取失败。请重新读取项目后再试；本次操作未自动重试。'
  })
  return true
}

// Keep unresolved attempts per project and input across project switches. A
// response can be lost after the server commits, so returning to the project
// must retry the same operation with the same idempotency key.
function keyForAttempt(attempts: Map<string, string>, signature: string): string {
  const old = attempts.get(signature)
  if (old) return old
  const key = newDeliveryKey()
  attempts.set(signature, key)
  return key
}
function finishAttempt(attempts: Map<string, string>, signature: string, key: string) {
  if (attempts.get(signature) === key) attempts.delete(signature)
}

/**
 * 读一次快照的当前性。读取失败**必须**落成 undefined：§7 说那种情况显示「当前性未知」，
 * 而沿用上一次的「当前内容」正是它点名要避免的事——用户会以为一份已经过期的文件还代表
 * 着工作区。
 */
async function refreshCurrency(snapshotId: string, basis = deliveryBasis(), sequence = currencySequence): Promise<boolean> {
  const projectId = props.project.id
  const request = (currencyRequests.get(snapshotId) ?? 0) + 1
  currencyRequests.set(snapshotId, request)
  const current = () => projectId === props.project.id && basis === deliveryBasis() &&
    sequence === currencySequence && currencyRequests.get(snapshotId) === request
  try {
    const result = await getRelease(projectId, snapshotId)
    if (!current()) return false
    currencyReads.value[snapshotId] = result.data
    return true
  } catch (error) {
    if (handleAccessDenial(error, projectId)) return false
    if (!current()) return false
    currencyReads.value[snapshotId] = undefined
    historyError.value = true
    fail(error, '无法读取交付快照的当前状态。')
    return false
  }
}

/** 重拉两份历史，并把界面上会展示的每一个快照的当前性重新读一遍。 */
async function reload(basis: string, sequence: number): Promise<boolean> {
  const projectId = props.project.id
  const currencyEpoch = ++currencySequence
  // A previously current snapshot is unknown until this refresh completes.
  currencyReads.value = {}
  const [releases, exports] = await Promise.all([
    listReleases(projectId),
    listExports(projectId),
  ])
  const current = () => projectId === props.project.id && basis === deliveryBasis() && sequence === historySequence
  if (!current()) return false
  historyLoaded.value = true
  snapshots.value = releases.data.items
  historyTruncated.value = releases.data.truncated
  artifacts.value = exports.data.items
  artifactTruncated.value = exports.data.truncated
  const ids = [...new Set([
    ...snapshots.value.map((item) => item.id),
    ...artifacts.value.map((item) => item.snapshot_id),
  ])]
  const results = await Promise.all(ids.map(id => refreshCurrency(id, basis, currencyEpoch)))
  return current() && results.every(Boolean)
}

async function load(): Promise<boolean> {
  const projectId = props.project.id
  const basis = deliveryBasis()
  const sequence = ++historySequence
  historyLoading.value = true
  historyError.value = false
  try {
    return await reload(basis, sequence)
  } catch (error) {
    if (handleAccessDenial(error, projectId)) return false
    if (basis === deliveryBasis() && sequence === historySequence) {
      historyError.value = true
      fail(error, '无法读取交付历史。')
    }
    return false
  } finally {
    if (sequence === historySequence) historyLoading.value = false
  }
}

// 只有用户明确要求重来一次时才清掉上一条报错。自动重读不清：版本冲突刚提示「已重新
// 读取当前版本，请再试一次」，紧接着的自动刷新会把那句话抹掉，用户只看到按钮什么都没
// 发生——而这个提示正是他唯一能知道发生了什么的地方。
function refreshHistory() {
  errorMessage.value = ''
  void load().then(refreshed => {
    if (refreshed && operationNotice.value) operationNotice.value = '交付历史已刷新，请查看各快照与产物状态。'
  })
}

async function runCheck() {
  if (busy.value) return
  const projectId = props.project.id
  const expected = props.project.project_version
  const basis = deliveryBasis()
  const operation = ++operationSequence
  pending.value = 'check'
  errorMessage.value = ''
  checkNotice.value = ''
  checks.value = null
  try {
    const result = await checkDelivery(projectId, expected)
    if (operation !== operationSequence || basis !== deliveryBasis()) return
    if (result.data.project_version !== expected) {
      checkNotice.value = '服务端返回的检查版本与当前课题不一致，请重新读取课题后再检查。'
      return
    }
    checks.value = result.data
    emit('computedReceipt', result.data.receipt_id ?? '')
  } catch (error) {
    if (handleAccessDenial(error, projectId)) return
    if (operation !== operationSequence || basis !== deliveryBasis()) return
    if (!recoverVersionConflict(error)) fail(error, '交付检查失败，请重试。')
  } finally { if (operation === operationSequence) pending.value = '' }
}

async function runTemplateCheck() {
  if (busy.value) return
  const projectId = props.project.id
  const expected = props.project.project_version
  const basis = deliveryBasis()
  const operation = ++operationSequence
  pending.value = 'template-check'
  errorMessage.value = ''
  templateCheckNotice.value = ''
  templateChecks.value = null
  try {
    const result = await checkTemplate(projectId, expected)
    if (operation !== operationSequence || basis !== deliveryBasis()) return
    if (result.data.project_version !== expected) {
      templateCheckNotice.value = '服务端返回的模板检查版本与当前课题不一致，请重新读取课题后再检查。'
      return
    }
    templateChecks.value = result.data
  } catch (error) {
    if (handleAccessDenial(error, projectId)) return
    if (operation !== operationSequence || basis !== deliveryBasis()) return
    if (!recoverVersionConflict(error)) fail(error, '模板规则检查失败，请重试。')
  } finally { if (operation === operationSequence) pending.value = '' }
}

async function submitDisposition(issue: CheckResult['issues'][number]) {
  if (busy.value || issue.disposition) return
  const projectId = props.project.id
  const expected = props.project.project_version
  const basis = deliveryBasis()
  const action = dispositionActions.value[issue.id] || 'resolve'
  const reason = (dispositionReasons.value[issue.id] || '').trim()
  if (!reason) {
    errorMessage.value = '请填写处置原因。'
    return
  }
  if (action === 'waive' && issue.severity === 'blocking') {
    errorMessage.value = '阻断问题不能豁免。'
    return
  }
  const signature = JSON.stringify({ project: projectId, issueId: issue.id, expected, action, reason })
  const requestKey = keyForAttempt(dispositionAttempts, signature)
  const operation = ++operationSequence
  pending.value = 'issue:' + issue.id
  errorMessage.value = ''
  operationNotice.value = ''
  try {
    try {
      await setIssueDisposition(projectId, issue.id, expected, action, reason,
        requestKey)
    } catch (error) {
      if (handleAccessDenial(error, projectId)) return
      if (operation !== operationSequence || basis !== deliveryBasis()) return
      if (!recoverVersionConflict(error)) fail(error, '提交问题处置失败；请检查权限和当前版本后重试。')
      return
    }
    if (operation !== operationSequence) return
    finishAttempt(dispositionAttempts, signature, requestKey)
    if (operation !== operationSequence || projectId !== props.project.id) return
    checks.value = null
    operationNotice.value = '问题处置已记录；请以重新读取的交付检查为准。'
    if (operation !== operationSequence || basis !== deliveryBasis()) return
    try {
      const refreshed = await checkDelivery(projectId, expected)
      if (operation !== operationSequence || basis !== deliveryBasis()) return
      if (refreshed.data.project_version !== expected) {
        checkNotice.value = '问题处置已记录，但检查版本已变化；请重新读取课题并检查。'
        return
      }
      checks.value = refreshed.data
      checkNotice.value = ''
    } catch (error) {
      if (handleAccessDenial(error, projectId)) return
      if (operation !== operationSequence || basis !== deliveryBasis()) return
      if (!recoverVersionConflict(error)) fail(error, '问题处置已记录，但检查结果重新读取失败。请再次运行交付检查。')
    }
  } finally { if (operation === operationSequence) pending.value = '' }
}

async function freeze() {
  if (busy.value) return
  const projectId = props.project.id
  const expected = props.project.project_version
  const basis = deliveryBasis()
  const signature = JSON.stringify({ project: projectId, project_version: expected })
  const requestKey = keyForAttempt(freezeAttempts, signature)
  const operation = ++operationSequence
  pending.value = 'freeze'
  errorMessage.value = ''
  operationNotice.value = ''
  try {
    try {
      await prepareRelease(projectId, expected, requestKey)
    } catch (error) {
      if (handleAccessDenial(error, projectId)) return
      if (operation !== operationSequence || basis !== deliveryBasis()) return
      if (!recoverVersionConflict(error)) fail(error, '冻结交付快照失败，请重试。')
      return
    }
    if (operation !== operationSequence) return
    finishAttempt(freezeAttempts, signature, requestKey)
    if (operation !== operationSequence || projectId !== props.project.id) return
    operationNotice.value = '已冻结项目版本 ' + expected + ' 的快照，正在读取交付历史。'
    const refreshed = await load()
    if (operation !== operationSequence || projectId !== props.project.id) return
    operationNotice.value = refreshed
      ? '已冻结项目版本 ' + expected + ' 的快照，交付历史已刷新。'
      : '已冻结项目版本 ' + expected + ' 的快照，但交付历史尚未确认；请刷新交付历史，不必重复冻结。'
  } finally { if (operation === operationSequence) pending.value = '' }
}

async function exportSnapshot(snapshot: ReleaseSnapshot) {
  if (busy.value) return
  if (!canExportSnapshot(snapshot, currencyReads.value[snapshot.id])) {
    errorMessage.value = '这份快照已阻断、属于历史版本或当前性尚未确认，不能生成当前 DOCX。请检查并冻结当前内容后再导出。'
    return
  }
  const projectId = props.project.id
  const basis = deliveryBasis()
  const signature = JSON.stringify({ project: projectId, snapshot: snapshot.id, digest: snapshot.snapshot_digest })
  const requestKey = keyForAttempt(exportAttempts, signature)
  const operation = ++operationSequence
  pending.value = 'export:' + snapshot.id
  errorMessage.value = ''
  operationNotice.value = ''
  try {
    try {
      await startExport(projectId, snapshot.id, requestKey)
    } catch (error) {
      if (handleAccessDenial(error, projectId)) return
      if (operation !== operationSequence || basis !== deliveryBasis()) return
      if (!recoverVersionConflict(error)) fail(error, '生成交付文件失败，请重试。')
      return
    }
    if (operation !== operationSequence) return
    finishAttempt(exportAttempts, signature, requestKey)
    if (operation !== operationSequence || projectId !== props.project.id) return
    operationNotice.value = '导出请求已完成，正在读取快照 ' + snapshot.id.slice(0, 8) + ' 的交付记录。'
    const refreshed = await load()
    if (operation !== operationSequence || projectId !== props.project.id) return
    operationNotice.value = refreshed
      ? '导出请求已完成，交付记录已刷新；请查看对应产物的状态。'
      : '导出请求已完成，但交付记录尚未确认；请刷新交付历史，不必重复导出。'
  } finally { if (operation === operationSequence) pending.value = '' }
}

/**
 * 重读一份产物与它所属快照的当前性。
 *
 * 查询顺序是**规格**不是实现细节：契约的验证器对 F21 有一条静态断言，界面必须先拿到
 * 产物、再用它的 snapshot_id 去读 getRelease。所以这里认证与取引用都从产物出发，不从
 * 列表里的快照反查。
 */
async function refreshArtifact(exportId: string): Promise<ExportArtifact | null> {
  const projectId = props.project.id
  const basis = deliveryBasis()
  const sequence = currencySequence
  const result = await getExport(projectId, exportId)
  if (basis !== deliveryBasis() || sequence !== currencySequence) return null
  const fresh = result.data
  const index = artifacts.value.findIndex((item) => item.id === exportId)
  if (index >= 0) artifacts.value[index] = fresh
  await refreshCurrency(fresh.snapshot_id, basis, sequence)
  if (basis !== deliveryBasis() || sequence !== currencySequence) return null
  return fresh
}

async function download(row: ArtifactRow) {
  if (busy.value) return
  const projectId = props.project.id
  const basis = deliveryBasis()
  const operation = ++operationSequence
  pending.value = 'download:' + row.id
  errorMessage.value = ''
  try {
    // Currentness and download permission are separate: a failed currentness
    // read leaves the label unknown, while download still rechecks permission.
    const fresh = await refreshArtifact(row.id)
    if (operation !== operationSequence || !fresh || basis !== deliveryBasis()) return
    const view = viewOf(fresh)
    if (view.kind !== 'downloadable') {
      errorMessage.value = '这份交付文件当前不可下载，状态已刷新。'
      return
    }
    const blob = await downloadExport(view.downloadPath)
    if (operation !== operationSequence || basis !== deliveryBasis()) return
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.style.display = 'none'
    link.href = url
    link.download = 'lingdoc-delivery-' + row.snapshotID.slice(0, 8) + '.docx'
    document.body.appendChild(link)
    link.click()
    nextTick(() => {
      document.body.removeChild(link)
      URL.revokeObjectURL(url)
    })
  } catch (error) {
    if (handleAccessDenial(error, projectId)) return
    if (operation === operationSequence && basis === deliveryBasis()) fail(error, '下载失败，请重试。')
  } finally { if (operation === operationSequence) pending.value = '' }
}

// 覆盖「页面刷新」「重新打开」（挂载即读）与「本地编辑成功」：保存章节后
// Workspace.vue 会重新读取项目，project_version 随之变化，旧快照于是自动标成历史。
watch(
  () => [props.project.id, deliveryBasis()] as const,
  (current, previous) => {
    if (current[0] !== previous?.[0]) {
      operationSequence++
      pending.value = ''
      currencyReads.value = {}
      currencyRequests.clear()
      historyLoaded.value = false
      historyError.value = false
      snapshots.value = []
      artifacts.value = []
      dispositionActions.value = {}
      dispositionReasons.value = {}
      errorMessage.value = ''
      operationNotice.value = ''
      checkNotice.value = ''
      templateCheckNotice.value = ''
    } else if (previous && current[1] !== previous[1]) {
      if (checks.value || pending.value === 'check') {
        checkNotice.value = '课题、模板、章节或成员信息已变化，之前的交付检查已过期；请重新检查。'
      }
      if (templateChecks.value || pending.value === 'template-check') {
        templateCheckNotice.value = '课题、模板、章节或成员信息已变化，之前的模板检查已过期；请重新检查。'
      }
    }
    checks.value = null
    templateChecks.value = null
    void load()
  },
  { immediate: true },
)
</script>

<template>
  <section class="delivery" aria-labelledby="delivery-title">
    <h3 id="delivery-title">交付</h3>
    <p class="muted">写作期的 Template Check 帮助发现模板问题，本身不授予导出权限。交付检查供当前修订参考；冻结时服务端会基于冻结输入重新执行 Preflight，只有当前快照的 Preflight 通过后才能生成 DOCX。</p>

    <p v-if="errorMessage" role="alert" class="alert">{{ errorMessage }}</p>
    <p v-if="operationNotice" role="status" class="delivery__success">{{ operationNotice }}</p>
    <p v-if="checkNotice" role="status" class="warning">{{ checkNotice }}</p>
    <p v-if="templateCheckNotice" role="status" class="warning">{{ templateCheckNotice }}</p>

    <div class="delivery__actions">
      <button type="button" :disabled="busy" @click="runCheck">
        {{ pending === 'check' ? '检查中…' : '交付检查' }}
      </button>
      <button type="button" :disabled="busy" @click="runTemplateCheck">
        {{ pending === 'template-check' ? '规则检查中…' : '写作期 Template Check' }}
      </button>
      <button type="button" :disabled="busy" @click="freeze">
        {{ pending === 'freeze' ? '冻结并检查中…' : '冻结当前内容（执行 Preflight）' }}
      </button>
      <button type="button" :disabled="busy" @click="refreshHistory">刷新交付历史</button>
    </div>

    <section v-if="checks" class="delivery__result" aria-label="交付检查结果">
      <p :class="['delivery__status', `delivery__status--${checks.status}`]">
        当前检查结果：{{ statusLabels[checks.status] }}
        <small>（项目版本 {{ checks.project_version }}）</small>
      </p>
      <p class="muted">课题模板 {{ project.template_id }} / {{ project.template_version }} · 项目副本第 {{ project.template_copy_version ?? '未提供' }} 版 · {{ ruleSetReference(checks) }} · 有效规则摘要 {{ checks.ruleset_hash || '服务端未提供' }}。该结果对应已读取的项目版本，冻结时会重新检查。</p>
      <p v-if="checks.receipt_id" class="delivery__receipt">当前检查回执：<code>{{checks.receipt_id}}</code><button type="button" @click="emit('reviewChangeReceipt',checks!.receipt_id!)">用此回执处理正式变更</button></p>
      <ul v-if="checks.issues.length" class="delivery__issues">
        <li v-for="issue in checks.issues" :key="issue.id">
          <span class="delivery__target">{{ issueTargetLabel(issue, project.id, chapters) }}</span>
          <em :class="['delivery__severity', { 'delivery__severity--blocking': issue.severity === 'blocking' }]">
            {{ issue.severity === 'blocking' ? '阻断' : issue.severity === 'warning' ? '警告' : '提示' }}
          </em>
          <p>{{ issue.message }}</p>
          <small>规则 {{ issue.rule_id }} · 目标版本 {{ issue.target_version || '服务端未提供' }} · 规则摘要 {{ issue.ruleset_hash || '服务端未提供' }} · 求值器版本 {{ issue.evaluator_version || '服务端未提供' }}</small>
          <p class="muted">证据：{{ issue.evidence?.length ? issue.evidence.join('；') : '服务端未提供额外证据' }}</p>
          <p v-if="issue.disposition" class="delivery__disposition">
            已记录「{{ issue.disposition.action }}」：{{ issue.disposition.reason }} · {{ issue.disposition.actor_id }}
          </p>
          <div v-else class="delivery__disposition-form">
            <label>处理方式
              <select v-model="dispositionActions[issue.id]" :disabled="busy">
                <option value="resolve">已修复</option>
                <option value="dismiss">误报 / 不适用</option>
                <option value="waive" :disabled="issue.severity === 'blocking'">接受风险（非阻断）</option>
              </select>
            </label>
            <label>原因
              <input v-model="dispositionReasons[issue.id]" :disabled="busy" maxlength="2000" placeholder="说明处置依据" />
            </label>
            <button type="button" :disabled="busy || !dispositionReasons[issue.id]?.trim()" @click="submitDisposition(issue)">
              {{ pending === `issue:${issue.id}` ? '提交中…' : '记录处置' }}
            </button>
          </div>
        </li>
      </ul>
      <p v-else class="muted">没有发现问题。</p>
    </section>

    <section v-if="templateChecks" class="delivery__result" aria-label="模板规则证据">
      <h4>写作期 Template Check：{{ statusLabels[templateChecks.status] }}</h4>
      <p>项目版本 {{ templateChecks.project_version }} · 来源模板 {{ project.template_id }} / {{ project.template_version }} · 模板副本第 {{ templateChecks.template_copy_version ?? '未提供' }} 版 · {{ ruleSetReference(templateChecks) }}。</p>
      <p class="muted">模板内容摘要 {{ templateChecks.content_hash || '服务端未提供' }} · 有效规则摘要 {{ templateChecks.ruleset_hash || '服务端未提供' }}。结果仅供修订参考，不授予导出权限。</p>
      <ul v-if="templateChecks.evaluations.length" class="delivery__issues">
        <li v-for="item in templateChecks.evaluations" :key="item.id || `${item.rule_id}:${item.target_ref}:${item.target_version}`">
          <span class="delivery__target">{{ item.rule_id }} · {{ item.target_ref }}（{{ item.target_version }}）</span>
          <em :class="['delivery__severity', { 'delivery__severity--blocking': item.severity === 'blocking' && item.status === 'ISSUE' }]">
            {{ item.status }} · {{ item.severity }}
          </em>
          <p>{{ item.message }}</p>
          <small>规则摘要 {{ item.ruleset_hash || '服务端未提供' }} · 求值器版本 {{ item.evaluator_version || '服务端未提供' }}</small>
          <p class="muted">证据：{{ item.evidence?.length ? item.evidence.join('；') : '服务端未提供额外证据' }}</p>
        </li>
      </ul>
      <p v-else class="muted">本次没有返回可执行规则的求值记录。</p>
    </section>

    <p v-if="historyLoading" role="status" class="muted">正在读取交付历史与快照当前性…</p>
    <p v-if="!historyLoading && historyLoaded && !historyError && !groups.length" class="muted">还没有冻结过交付快照。</p>
    <template v-if="groups.length">
      <p v-if="historyTruncated" class="muted">只显示最近 {{ snapshots.length }} 份快照，更早的未列出。</p>
      <ul class="delivery__history">
        <li v-for="group in groups" :key="group.snapshotID" class="delivery__snapshot">
          <template v-if="group.snapshot">
            <header class="delivery__snapshot-head">
              <strong>快照 {{ group.snapshot.id.slice(0, 8) }}</strong>
              <span class="delivery__currency">{{ currencyOf(currencyReads[group.snapshot.id]) }}</span>
            </header>
            <p class="muted">
              冻结于项目版本 {{ group.snapshot.frozen_input.project_version }} ·
              研究条件第 {{ group.snapshot.frozen_input.spec_revision }} 版 ·
              章节 {{ group.snapshot.frozen_input.chapters.length }} 章 ·
              资料 {{ group.snapshot.frozen_input.sources.length }} 份 ·
              摘要 {{ group.snapshot.snapshot_digest.slice(0, 12) }}
            </p>
            <p class="muted">
              冻结时的交付检查：{{ statusLabels[group.snapshot.check.status] }} · Preflight ·
              冻结时间 {{ new Date(group.snapshot.created_at).toLocaleString() }}
            </p>
            <p class="muted">冻结模板 {{ frozenTemplateReference(group.snapshot) }} · {{ ruleSetReference(group.snapshot.check) }} · 有效规则摘要 {{ group.snapshot.check.ruleset_hash || '服务端未提供' }}</p>
            <p class="muted">Preflight 固定于冻结输入；后续编辑不会更新这份快照。当前性未知或已过期时，请刷新历史并按需要重新冻结。</p>
            <p v-if="!canExportSnapshot(group.snapshot, currencyReads[group.snapshot.id])" role="note" class="warning">
              该快照已阻断、属于历史版本或当前性尚未确认。请检查并冻结当前内容，再生成 DOCX。
            </p>
            <details v-if="group.snapshot.check.issues.length">
              <summary>查看冻结时的 {{ group.snapshot.check.issues.length }} 条 Preflight 记录</summary>
              <ul class="delivery__issues">
                <li v-for="issue in group.snapshot.check.issues" :key="issue.id">
                  <strong>{{ issue.rule_id }} · {{ issueTargetLabel(issue, project.id, chapters) }}</strong>
                  <em :class="['delivery__severity', { 'delivery__severity--blocking': issue.severity === 'blocking' }]">{{ issue.severity === 'blocking' ? '阻断' : issue.severity === 'warning' ? '警告' : '提示' }}</em>
                  <p>{{ issue.message }}</p>
                  <small>目标版本 {{ issue.target_version || '服务端未提供' }} · 规则哈希 {{ issue.ruleset_hash || '服务端未提供' }} · 求值器版本 {{ issue.evaluator_version || '服务端未提供' }}</small>
                  <p class="muted">证据：{{ issue.evidence?.length ? issue.evidence.join('；') : '服务端未提供额外证据' }}</p>
                </li>
              </ul>
            </details>

            <div class="delivery__actions">
              <button type="button" :disabled="busy || !canExportSnapshot(group.snapshot, currencyReads[group.snapshot.id])"
                :title="!canExportSnapshot(group.snapshot, currencyReads[group.snapshot.id]) ? '请重新检查并冻结当前内容后再生成 DOCX。' : undefined"
                @click="exportSnapshot(group.snapshot)">
                {{ pending === `export:${group.snapshot.id}` ? '生成中…' : '生成 DOCX' }}
              </button>
            </div>
          </template>

          <!-- 快照自己掉出了列表，它名下的文件还在。只描述得快照 ID：它这会儿没有可读的
               冻结输入，编一份出来就等于把界面的猜测当成事实。 -->
          <template v-else>
            <header class="delivery__snapshot-head">
              <strong>快照 {{ group.snapshotID.slice(0, 8) }}</strong>
              <span class="delivery__currency">{{ currencyOf(currencyReads[group.snapshotID]) }}</span>
            </header>
            <p class="muted">这份快照不在上面的列表里（历史只显示最近 {{ snapshots.length }} 份），它的交付文件仍列在下面。</p>
          </template>

          <p v-if="group.rows.some(row => row.downloadPath)" role="status" class="delivery__success">
              DOCX 已生成，可在本快照下点击“下载 DOCX”。
          </p>
          <ul v-if="group.rows.length" class="delivery__artifacts">
            <li v-for="row in group.rows" :key="row.id">
              <span class="delivery__artifact-id">{{ row.id.slice(0, 8) }}</span>
              <em>{{ row.currency }}</em>

              <template v-if="row.downloadPath">
                <button type="button" :disabled="busy" @click="download(row)">
                  {{ pending === `download:${row.id}` ? '下载中…' : '下载 DOCX' }}
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
.delivery__success { color: #1c6b3f; font-weight: 600; }
.delivery__issues { margin: 0; padding-left: 20px; }
.delivery__issues li { margin-bottom: 10px; }
.delivery__target { font-weight: 600; }
.delivery__severity { margin-left: 8px; padding: 1px 7px; border-radius: 999px; background: #eef2f0; color: #4a564f; font-size: 12px; font-style: normal; }
.delivery__severity--blocking { background: #fdecea; color: #b3261e; }
.delivery__issues p { margin: 4px 0; }
.delivery__disposition-form { display: flex; flex-wrap: wrap; align-items: end; gap: 8px; margin-top: 8px; }
.delivery__disposition-form label { display: grid; gap: 4px; }
.delivery__disposition-form input { min-width: 220px; }
.delivery__disposition { color: #52616b; font-size: 0.92em; }
.delivery__history { margin: 12px 0 0; padding: 0; list-style: none; }
.delivery__snapshot { margin-bottom: 14px; padding: 16px; border: 1px solid #dbe5dd; border-radius: 10px; background: #fff; }
.delivery__snapshot-head { display: flex; flex-wrap: wrap; gap: 10px; align-items: center; justify-content: space-between; }
.delivery__currency { padding: 2px 10px; border-radius: 999px; background: #edf8f0; color: #25452f; font-size: 12px; }
.delivery__artifacts { margin: 8px 0 0; padding: 0; list-style: none; }
.delivery__artifacts li { display: flex; flex-wrap: wrap; gap: 10px; align-items: center; padding: 8px 0; border-top: 1px dashed #e3ebe6; }
.delivery__artifact-id { font-variant-numeric: tabular-nums; color: #4a564f; }
</style>
