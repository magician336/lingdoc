export const BASELINE_ID = 'LD-V1-20261103-R1'

export type View = 'overview' | 'write' | 'review'
export type Decision = 'modify_document' | 'update_plan' | 'adjust_both' | 'no_change' | 'defer'
export type Progress = 'awaiting_decision' | 'pending' | 'deferred' | 'completed'
export type SaveState = 'saved' | 'saving' | 'unsaved' | 'unconnected' | 'saving_dirty' | 'failed' | 'conflict' | 'offline' | 'update_available'

export type Section = {
  id: string
  title: string
  text: string
}

export type TaskRecord = {
  decision: Decision | null
  progress: Progress
  reason: string
  draftRecorded?: boolean
}

export const initialSections: Section[] = [
  {
    id: 'SAMPLE',
    title: '样本选择',
    text: '计划进入四个社区开展问卷与访谈，据此了解全市老年人使用数字公共服务的情况。',
  },
  {
    id: 'SCHEDULE',
    title: '进度安排',
    text: '首月完成四个社区的联系与招募，随后开展问卷与访谈。',
  },
  {
    id: 'METHOD',
    title: '访谈方法',
    text: '采用半结构访谈，围绕服务接触、使用障碍与支持需求开展交流。',
  },
]

export const comment = {
  id: 'C01',
  author: '导师意见',
  baseRevision: 12,
  sectionId: 'SAMPLE',
  text: '说明社区选择标准，不要据此概括全市情况。',
  excerpt: '据此了解全市老年人使用数字公共服务的情况',
}

export const sourceVersions = {
  v1: {
    versionId: 'CONTACT-v1',
    title: '社区联系记录',
    version: 1,
    paragraphId: 'P1',
    text: '四个社区表示愿意在本期协助研究团队开展联系与招募。',
  },
  v2: {
    versionId: 'CONTACT-v2',
    title: '社区联系记录',
    version: 2,
    paragraphId: 'P1',
    text: '其中两个社区本期不能配合，目前可进入的场地由四个社区变为两个社区。',
  },
}

export const reviewTasks = [
  {
    id: 'R1',
    target: 'SAMPLE',
    title: '样本范围与适用边界',
    reason: '样本场地范围依据联系记录，需要检查样本范围与适用边界。',
    dependencyId: 'DEP1',
  },
  {
    id: 'R2',
    target: 'SCHEDULE',
    title: '招募与进度安排',
    reason: '招募安排依赖可进入场地，需要检查原招募和进度安排。',
    dependencyId: 'DEP2',
  },
  {
    id: 'R3',
    target: 'FIG01',
    title: '技术路线图',
    reason: '路线图包含四社区招募节点，可能需要调整。',
    dependencyId: 'DEP3',
  },
]

export const initialTaskRecords: Record<string, TaskRecord> = {
  C01: { decision: null, progress: 'awaiting_decision', reason: '' },
  R1: { decision: null, progress: 'awaiting_decision', reason: '' },
  R2: { decision: null, progress: 'awaiting_decision', reason: '' },
  R3: { decision: null, progress: 'awaiting_decision', reason: '' },
  METHOD: { decision: null, progress: 'awaiting_decision', reason: '' },
}

export const decisionLabels: Record<Decision, string> = {
  modify_document: '修改正文',
  update_plan: '更新研究方案',
  adjust_both: '两者调整',
  no_change: '无需调整',
  defer: '暂不判断',
}

export const progressLabels: Record<Progress, string> = {
  awaiting_decision: '待判断',
  pending: '待处理',
  deferred: '暂缓',
  completed: '已完成',
}

export const saveStateCopy: Record<SaveState, { label: string; detail: string }> = {
  saved: { label: '当前文稿已保存', detail: '当前原生修订已保存并读回核对。意见完成和交付另行记录。' },
  saving: { label: '正在保存', detail: '正在通过原生保存事务写入并核对。保存期间的新编辑仍需再次保存。' },
  unsaved: { label: '有未保存内容', detail: '请保存当前文稿。切换页面不会重载原生编辑器。' },
  unconnected: {
    label: '正在连接原生宿主',
    detail: '等待真实 WordCraft 打开文稿并读取保存状态。',
  },
  saving_dirty: {
    label: '旧修订保存中 · 当前仍有新编辑',
    detail: '保存期间继续输入时，旧请求成功也不能清除当前未保存状态。此分支仅演示状态规则。',
  },
  failed: {
    label: '保存结果未知',
    detail: '保留当前编辑内容。重试必须沿用原幂等键，不能生成新键重复写入。',
  },
  conflict: {
    label: '写入冲突 · 本地稿已保留',
    detail: '服务器版本已经变化，需要重新比较；不得读取 latest 后静默重放写入。',
  },
  offline: {
    label: '当前离线',
    detail: '最后同步时间仅为场景信息。编辑内容应保留，恢复连接后再校准服务器状态。',
  },
  update_available: {
    label: '发现更新 · 尚未应用',
    detail: '最新资料为两社区，正文仍依据四社区方案。不会用服务器新稿覆盖未保存正文。',
  },
}

export const stateLinks = [
  { id: 'S01', label: '打开项目与继续写作' },
  { id: 'S02', label: '专注写作' },
  { id: 'S03', label: '正文与依据并排' },
  { id: 'S04', label: '导师意见与定位失效' },
  { id: 'S05', label: 'AI 实际上下文' },
  { id: 'S06', label: '建议与原稿对照' },
  { id: 'S07', label: '保存、失败与冲突' },
  { id: 'S08', label: '条件变化与关联章节' },
  { id: 'S09', label: '回看决定与处理' },
  { id: 'S10', label: '交付预览' },
]
