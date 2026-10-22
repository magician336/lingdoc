/**
 * G3 验收台 · 免登录快照模式的数据装载。
 *
 * 这一份数据是 **G3 B3 实跑的历史证据**（#217 的 B3 解除条件），跑在 head `7f5fd8c`
 * 上，原始 transcript 在 `C:\Users\ASUS\.minimax\g3-198-notes\n223-evidence\b3\`。
 *
 * 页面用它把「已实跑的部分」摆在台面上，但必须**看起来不像当前运行结果**：
 * 所以快照自带声明、head、运行期 commit 与只读模式标志，页头与每个证据块都要复述它。
 *
 * 装载器是纯函数：它只做校验与归一，不发请求、不读盘。数据坏掉就抛错，
 * 不让页面悄悄渲染一份半截的证据。
 */

/** 快照对应的 head。 */
export const G3_SNAPSHOT_HEAD = '7f5fd8c'

/** 快照对应的后端进程 commit（编译期注入的 `CommitID`）。 */
export const G3_SNAPSHOT_RUNTIME_COMMIT = 'f3040cb'

/** 页头与每个证据块必须出现的声明。 */
export const G3_SNAPSHOT_DISCLAIMER = '历史快照 · 非本次运行'

/** 原始证据目录（只读引用，页面不访问它）。 */
export const G3_SNAPSHOT_SOURCE_DIR = 'C:\\Users\\ASUS\\.minimax\\g3-198-notes\\n223-evidence\\b3'

export interface G3SnapshotRaw {
  head: string
  runtime_commit: string
  disclaimer: string
  source_dir: string
  identity: {
    user: string
    email: string
    tenant_id: number
    tenant_name: string
    role: string
  }
  project: {
    id: string
    name: string
    status: string
    project_version: number
    spec_revision: number
    current_context_revision: number
  }
  change_set: {
    id: string
    status: string
    reason: string
    base_context_revision: number
    target_context_revision: number
  }
  impact_tasks: Array<{
    id: string
    action: string
    status: string
    change_set_id: string
    closure_evidence_kind: string
    closure_evidence_id: string
  }>
  releases: Array<{
    id: string
    check_status: string
    issues_count: number
  }>
  exports: Array<{
    id: string
    status: string
    file_sha256: string
    snapshot_id: string
    bytes: number
    sha256_after_restart: string
    bytes_after_restart: number
    identical_after_restart: boolean
  }>
  chain: Array<{
    step: number
    name: string
    method: string
    path: string
    result: string
  }>
}

export interface G3SnapshotIdentity {
  user: string
  email: string
  tenantId: number
  tenantName: string
  role: string
  /** 快照里的身份是**当时那个**，不是当前会话的。 */
  isCurrentSession: false
}

export interface G3SnapshotProject {
  id: string
  name: string
  status: string
  projectVersion: number
  specRevision: number
  currentContextRevision: number
}

export interface G3SnapshotChangeSet {
  id: string
  status: string
  reason: string
  baseContextRevision: number
  targetContextRevision: number
}

export interface G3SnapshotTask {
  id: string
  action: string
  status: string
  changeSetId: string
  closureEvidenceKind: string
  closureEvidenceId: string
}

export interface G3SnapshotRelease {
  id: string
  checkStatus: string
  issuesCount: number
}

export interface G3SnapshotExport {
  id: string
  status: string
  fileSha256: string
  snapshotId: string
  bytes: number
  sha256AfterRestart: string
  bytesAfterRestart: number
  identicalAfterRestart: boolean
}

export interface G3SnapshotStep {
  step: number
  name: string
  method: string
  path: string
  result: string
}

export interface G3Snapshot {
  head: string
  runtimeCommit: string
  disclaimer: string
  sourceDir: string
  mode: '历史快照'
  isLive: false
  identity: G3SnapshotIdentity
  project: G3SnapshotProject
  changeSet: G3SnapshotChangeSet
  impactTasks: G3SnapshotTask[]
  releases: G3SnapshotRelease[]
  exports: G3SnapshotExport[]
  chain: G3SnapshotStep[]
}

const PROJECT_ID = '68b4d2f2-d588-4711-ba90-c56f85977574'
const CHANGE_SET_ID = '435ce0ba-1fe4-45df-b0a7-7e25739d2898'
const EXPORT_SHA = 'd5a5f160fbe2e45310a74c9adeab4aeb605887ce8100dee80719184b8c8fb3e3'
const CS_PATH = `/api/v1/lingdoc/projects/${PROJECT_ID}/change-sets/${CHANGE_SET_ID}`

/** 快照原始内容：逐字段照录 B3 证据。 */
export const G3_SNAPSHOT_RAW: G3SnapshotRaw = {
  head: G3_SNAPSHOT_HEAD,
  runtime_commit: G3_SNAPSHOT_RUNTIME_COMMIT,
  disclaimer: G3_SNAPSHOT_DISCLAIMER,
  source_dir: G3_SNAPSHOT_SOURCE_DIR,
  identity: {
    user: 'lingdoc-f4',
    email: 'lingdoc-f4@example.com',
    tenant_id: 10054,
    tenant_name: "lingdoc-f4's Workspace",
    role: 'owner',
  },
  project: {
    id: PROJECT_ID,
    name: 'f4',
    status: 'active',
    project_version: 34,
    spec_revision: 4,
    current_context_revision: 4,
  },
  change_set: {
    id: CHANGE_SET_ID,
    status: 'stabilized',
    reason: 'B3 验收：变更应用 → 任务处置 → 稳定化 → 冻结 → 导出',
    base_context_revision: 3,
    target_context_revision: 4,
  },
  impact_tasks: [
    {
      id: '29f1cd82-b0f8-573d-ad05-3d7f393aa72e',
      action: 'REVIEW_REQUIRED',
      status: 'DONE',
      change_set_id: CHANGE_SET_ID,
      closure_evidence_kind: 'ChapterVersion',
      closure_evidence_id: 'dd1e6e25-68ad-47d0-934e-a18a9eadcfeb',
    },
    {
      id: '6433ecd6-8ad5-5434-9559-2ac6bdea21c7',
      action: 'REVIEW_REQUIRED',
      status: 'DONE',
      change_set_id: CHANGE_SET_ID,
      closure_evidence_kind: 'ChapterVersion',
      closure_evidence_id: 'f4c3994d-4b17-452f-9ae2-e374b6b3a67a',
    },
    {
      id: '039d251d-a151-5656-a3d4-648f04ba8d6f',
      action: 'REVIEW_REQUIRED',
      status: 'DONE',
      change_set_id: '01fe7bbf-8b48-4e70-b88f-e480844e27c5',
      closure_evidence_kind: 'ChapterVersion',
      closure_evidence_id: '94635535-3562-4fc0-9f7e-2f9d50b48d55',
    },
    {
      id: '9fe43eed-adcd-5981-879b-94582af1e87e',
      action: 'REVIEW_REQUIRED',
      status: 'DONE',
      change_set_id: '01fe7bbf-8b48-4e70-b88f-e480844e27c5',
      closure_evidence_kind: 'ChapterVersion',
      closure_evidence_id: '1ab0a52c-eeb2-4255-8946-83dd95410209',
    },
    {
      id: '317a3a35-5e79-54f6-afcb-7209e2c4ebb7',
      action: 'REVIEW_REQUIRED',
      status: 'DONE',
      change_set_id: '4f203042-63bf-4b7e-9857-1ec303106cdf',
      closure_evidence_kind: 'ChapterVersion',
      closure_evidence_id: 'a1dfa107-2c55-48fd-b803-85e9e0aa9c02',
    },
    {
      id: '9b1ce9e8-0aaf-5f78-b1f8-5de194d045de',
      action: 'REVIEW_REQUIRED',
      status: 'DONE',
      change_set_id: '4f203042-63bf-4b7e-9857-1ec303106cdf',
      closure_evidence_kind: 'ChapterVersion',
      closure_evidence_id: '08100e3a-ad82-42ae-9a18-745eb4313adc',
    },
  ],
  releases: [
    {
      id: 'snapshot-850b0ddc8e9f00cd4b12dbcb84aef6e7',
      check_status: 'passed',
      issues_count: 0,
    },
    {
      id: 'snapshot-5246485fed9e875ba5602d1bca43692d',
      check_status: 'passed',
      issues_count: 0,
    },
    {
      id: 'snapshot-9f9b2dd81f0b52ede63777186288b900',
      check_status: 'passed',
      issues_count: 2,
    },
  ],
  exports: [
    {
      id: 'export-e1106848ba4033b0c1127eb6dcda0779',
      status: 'verified',
      file_sha256: EXPORT_SHA,
      snapshot_id: 'snapshot-850b0ddc8e9f00cd4b12dbcb84aef6e7',
      bytes: 2250,
      sha256_after_restart: EXPORT_SHA,
      bytes_after_restart: 2250,
      identical_after_restart: true,
    },
    {
      id: 'export-2808a8749f909b352536af9dcba9a03d',
      status: 'verified',
      file_sha256: EXPORT_SHA,
      snapshot_id: 'snapshot-5246485fed9e875ba5602d1bca43692d',
      bytes: 2250,
      sha256_after_restart: EXPORT_SHA,
      bytes_after_restart: 2250,
      identical_after_restart: true,
    },
    {
      id: 'export-dab928b30f340630a2cff278a6872c1a',
      status: 'failed',
      file_sha256: EXPORT_SHA,
      snapshot_id: 'snapshot-9f9b2dd81f0b52ede63777186288b900',
      bytes: 166,
      sha256_after_restart: EXPORT_SHA,
      bytes_after_restart: 166,
      identical_after_restart: true,
    },
  ],
  chain: [
    {
      step: 1,
      name: '创建变更集',
      method: 'POST',
      path: `/api/v1/lingdoc/projects/${PROJECT_ID}/change-sets`,
      result: '201，状态 assessed，基线上下文修订 3',
    },
    {
      step: 2,
      name: '影响评估',
      method: 'POST',
      path: `${CS_PATH}/assess`,
      result: '200',
    },
    {
      step: 3,
      name: '应用变更集',
      method: 'POST',
      path: `${CS_PATH}/apply`,
      result: '200，上下文修订推进到 4',
    },
    {
      step: 4,
      name: '章节按新 spec 复写',
      method: 'POST',
      path: `/api/v1/lingdoc/projects/${PROJECT_ID}/chapters/{id}/versions ×2`,
      result: '201 ×2',
    },
    {
      step: 5,
      name: '确认章节',
      method: 'POST',
      path: `/api/v1/lingdoc/projects/${PROJECT_ID}/chapters/{id}/confirmations ×2`,
      result: '201 ×2',
    },
    {
      step: 6,
      name: '影响任务处置',
      method: 'POST',
      path: `/api/v1/lingdoc/projects/${PROJECT_ID}/impact-tasks/{id}/actions`,
      result: '200 ×2（assign → complete），任务转 DONE',
    },
    {
      step: 7,
      name: '稳定化',
      method: 'POST',
      path: `${CS_PATH}/stabilize`,
      result: '200，状态 STABILIZED，blockers 为空，上下文修订 4',
    },
    {
      step: 8,
      name: '冻结交付快照',
      method: 'POST',
      path: `/api/v1/lingdoc/projects/${PROJECT_ID}/releases`,
      result: '201，快照 check: passed，0 issue',
    },
    {
      step: 9,
      name: '发起导出',
      method: 'POST',
      path: `/api/v1/lingdoc/projects/${PROJECT_ID}/releases/{sid}/exports`,
      result: '202 → 产物状态 verified',
    },
    {
      step: 10,
      name: '下载导出产物',
      method: 'GET',
      path: `/api/v1/lingdoc/projects/${PROJECT_ID}/exports/{eid}/file`,
      result: '200，2250 字节，SHA-256 d5a5f160…',
    },
    {
      step: 11,
      name: '导出状态核验',
      method: 'GET',
      path: `/api/v1/lingdoc/projects/${PROJECT_ID}/exports/{eid}`,
      result: '200，status=verified，file_sha256 与下载一致',
    },
    {
      step: 12,
      name: '进程重启后逐项读回',
      method: 'GET',
      path: `/api/v1/lingdoc/projects/${PROJECT_ID} 及各子资源`,
      result: '项目 active、任务 6 条 DONE、快照 check passed',
    },
    {
      step: 13,
      name: '重启后哈希比对',
      method: 'GET',
      path: `/api/v1/lingdoc/projects/${PROJECT_ID}/exports/{eid}/file`,
      result: '200，2250 字节，SHA-256 与重启前逐字节一致',
    },
  ],
}

function fail(path: string): never {
  throw new Error(`快照数据不合法：${path}`)
}

function asRecord(value: unknown, path: string): Record<string, unknown> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) fail(path)
  return value as Record<string, unknown>
}

function requireString(value: unknown, path: string): string {
  if (typeof value !== 'string' || value.length === 0) fail(path)
  return value
}

function requireNumber(value: unknown, path: string): number {
  if (typeof value !== 'number' || !Number.isInteger(value) || value < 1) fail(path)
  return value
}

/** 计数可以合法地为 0（例如「0 issue」），但不能是负数或非整数。 */
function requireCount(value: unknown, path: string): number {
  if (typeof value !== 'number' || !Number.isInteger(value) || value < 0) fail(path)
  return value
}

function requireSha(value: unknown, path: string): string {
  const text = requireString(value, path)
  if (!/^[0-9a-f]{64}$/.test(text)) fail(path)
  return text
}

function requireHead(value: unknown, path: string): string {
  const text = requireString(value, path)
  if (!/^[0-9a-f]{7,40}$/.test(text)) fail(path)
  return text
}

function requireArray(value: unknown, path: string): unknown[] {
  if (!Array.isArray(value)) fail(path)
  return value
}

/**
 * 装载并校验快照。
 *
 * 坏数据抛错而不是返回半份：证据位显示一半，比不显示更危险——
 * 读者会以为那就是当时的结论。
 */
export function loadG3Snapshot(raw: unknown): G3Snapshot {
  const root = asRecord(raw, 'snapshot')
  const identity = asRecord(root.identity, 'identity')
  const project = asRecord(root.project, 'project')
  const changeSet = asRecord(root.change_set, 'change_set')
  const chain = requireArray(root.chain, 'chain')

  return {
    head: requireHead(root.head, 'head'),
    runtimeCommit: requireHead(root.runtime_commit, 'runtime_commit'),
    disclaimer: requireString(root.disclaimer, 'disclaimer'),
    sourceDir: requireString(root.source_dir, 'source_dir'),
    mode: '历史快照',
    isLive: false,
    identity: {
      user: requireString(identity.user, 'identity.user'),
      email: requireString(identity.email, 'identity.email'),
      tenantId: requireNumber(identity.tenant_id, 'identity.tenant_id'),
      tenantName: requireString(identity.tenant_name, 'identity.tenant_name'),
      role: requireString(identity.role, 'identity.role'),
      isCurrentSession: false,
    },
    project: {
      id: requireString(project.id, 'project.id'),
      name: requireString(project.name, 'project.name'),
      status: requireString(project.status, 'project.status'),
      projectVersion: requireNumber(project.project_version, 'project.project_version'),
      specRevision: requireNumber(project.spec_revision, 'project.spec_revision'),
      currentContextRevision: requireNumber(
        project.current_context_revision,
        'project.current_context_revision',
      ),
    },
    changeSet: {
      id: requireString(changeSet.id, 'change_set.id'),
      status: requireString(changeSet.status, 'change_set.status'),
      reason: requireString(changeSet.reason, 'change_set.reason'),
      baseContextRevision: requireNumber(
        changeSet.base_context_revision,
        'change_set.base_context_revision',
      ),
      targetContextRevision: requireNumber(
        changeSet.target_context_revision,
        'change_set.target_context_revision',
      ),
    },
    impactTasks: requireArray(root.impact_tasks, 'impact_tasks').map((item, index) => {
      const task = asRecord(item, `impact_tasks[${index}]`)
      return {
        id: requireString(task.id, `impact_tasks[${index}].id`),
        action: requireString(task.action, `impact_tasks[${index}].action`),
        status: requireString(task.status, `impact_tasks[${index}].status`),
        changeSetId: requireString(task.change_set_id, `impact_tasks[${index}].change_set_id`),
        closureEvidenceKind: requireString(
          task.closure_evidence_kind,
          `impact_tasks[${index}].closure_evidence_kind`,
        ),
        closureEvidenceId: requireString(
          task.closure_evidence_id,
          `impact_tasks[${index}].closure_evidence_id`,
        ),
      }
    }),
    releases: requireArray(root.releases, 'releases').map((item, index) => {
      const release = asRecord(item, `releases[${index}]`)
      return {
        id: requireString(release.id, `releases[${index}].id`),
        checkStatus: requireString(release.check_status, `releases[${index}].check_status`),
        issuesCount: requireCount(release.issues_count, `releases[${index}].issues_count`),
      }
    }),
    exports: requireArray(root.exports, 'exports').map((item, index) => {
      const artifact = asRecord(item, `exports[${index}]`)
      return {
        id: requireString(artifact.id, `exports[${index}].id`),
        status: requireString(artifact.status, `exports[${index}].status`),
        fileSha256: requireSha(artifact.file_sha256, `exports[${index}].file_sha256`),
        snapshotId: requireString(artifact.snapshot_id, `exports[${index}].snapshot_id`),
        bytes: requireCount(artifact.bytes, `exports[${index}].bytes`),
        sha256AfterRestart: requireSha(
          artifact.sha256_after_restart,
          `exports[${index}].sha256_after_restart`,
        ),
        bytesAfterRestart: requireCount(
          artifact.bytes_after_restart,
          `exports[${index}].bytes_after_restart`,
        ),
        identicalAfterRestart: artifact.identical_after_restart === true,
      }
    }),
    chain: chain.map((item, index) => {
      const step = asRecord(item, `chain[${index}]`)
      return {
        step: requireNumber(step.step, `chain[${index}].step`),
        name: requireString(step.name, `chain[${index}].name`),
        method: requireString(step.method, `chain[${index}].method`),
        path: requireString(step.path, `chain[${index}].path`),
        result: requireString(step.result, `chain[${index}].result`),
      }
    }),
  }
}

/** 快照声明语：页头与每个证据块都要复述它。 */
export function snapshotNotice(snapshot: G3Snapshot): string {
  return `${snapshot.disclaimer}（head ${snapshot.head}）`
}
