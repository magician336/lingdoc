// 上下文包门面：把「用户勾了什么」（意图）与「服务端组装了什么」（事实）分成
// 两个不可互换的类型，并给出从构造请求到分组事实的整套流程。
//
// 三条规则决定了这个模块的形状：
//   - 事实区的每一个值都来自服务端返回。本地不推断「这一轮带了什么」。
//   - 契约类型由传输层模块（agentApi.ts）拥有。这里只读不改，不复制第二份。
//   - 选择理由按服务端原文逐条透传，不解析成条目映射。
import { listEvidenceCards, previewAgentContext } from './agentApi'
import type {
  AgentContextInput,
  AgentContextPackage,
  AgentSourceRef,
  EvidenceCard,
  VersionRef,
} from './agentApi'

/**
 * 用户这一轮的意图：勾了哪些卡、带了哪些来源，以及请求要带的版本基线。
 *
 * 意图里只有「用户选了什么」，没有「服务端返回了什么」——两者的字段集不相交，
 * 混用在类型层就暴露。`sensitive_context_confirmed` 也不在意图里：面板不代用户
 * 确认敏感上下文，授权结论由服务端按当前授权给。
 *
 * 这份形状由本模块**显式拥有**，不从传输层契约派生：意图是**本地**的知识，形状
 * 该由本地说了算。若它跟着 `AgentContextInput` 漂，传输层哪天加一个服务端才该
 * 有的字段（例如「谁确认的」），意图就会自动多出那个字段，客户端于是就能替服务端
 * 声明它——那正是「前端不得代服务端声明」要防的事。事实那边相反：事实本就该跟随
 * 服务端，所以事实组从契约派生（见 `ContextFactGroups`）。
 */
export interface ContextIntent {
  expected_project_version: number
  expected_context_revision: number
  expected_version: number
  chapter_id?: string
  task_id?: string
  source_ids: string[]
  card_refs: VersionRef[]
}

/**
 * 一份还没有任何勾选的意图。
 *
 * 版本基线由调用方给出：服务端按它判断这次请求是不是陈旧的。
 */
export const createIntent = (basis: {
  expected_project_version: number
  expected_context_revision: number
  expected_version: number
  chapter_id?: string
  task_id?: string
}): ContextIntent => ({
  expected_project_version: basis.expected_project_version,
  expected_context_revision: basis.expected_context_revision,
  expected_version: basis.expected_version,
  ...(basis.chapter_id === undefined ? {} : { chapter_id: basis.chapter_id }),
  ...(basis.task_id === undefined ? {} : { task_id: basis.task_id }),
  source_ids: [],
  card_refs: [],
})

/**
 * 勾上一张卡：返回新意图，原意图不动。
 *
 * 同一张卡只勾一次：卡片 id 已经在意图里时结果不变。版本是勾选那一刻的快照，
 * 卡片换代之后要重建意图，而不是在这里偷偷换版本。
 */
export const withCard = (intent: ContextIntent, ref: VersionRef): ContextIntent => {
  if (intent.card_refs.some(item => item.id === ref.id)) {
    return { ...intent, card_refs: [...intent.card_refs] }
  }
  return { ...intent, card_refs: [...intent.card_refs, { ...ref }] }
}

/** 取消勾选一张卡：按卡片 id 移除；本来就没勾时结果不变。 */
export const withoutCard = (intent: ContextIntent, cardId: string): ContextIntent => ({
  ...intent,
  card_refs: intent.card_refs.filter(ref => ref.id !== cardId),
})

/** 带上一个来源：返回新意图，原意图不动；重复加入不改变结果。 */
export const withSource = (intent: ContextIntent, sourceId: string): ContextIntent => {
  if (intent.source_ids.includes(sourceId)) {
    return { ...intent, source_ids: [...intent.source_ids] }
  }
  return { ...intent, source_ids: [...intent.source_ids, sourceId] }
}

/** 去掉一个来源：本来就没有时结果不变。 */
export const withoutSource = (intent: ContextIntent, sourceId: string): ContextIntent => ({
  ...intent,
  source_ids: intent.source_ids.filter(id => id !== sourceId),
})

/**
 * 把意图变成服务端契约的请求体：字段名逐字对齐 `AgentContextInput`。
 *
 * `sensitive_context_confirmed` 恒为 false：面板不代用户确认敏感上下文，授权
 * 结论由服务端按当前授权给。可选字段即使没有值也留着键——契约里列了这些字段，
 * 空值在发到服务端之前就被丢掉，请求体的字段集合因此与服务端契约逐字一致。
 */
export const buildContextRequest = (intent: ContextIntent): AgentContextInput => ({
  expected_project_version: intent.expected_project_version,
  expected_context_revision: intent.expected_context_revision,
  expected_version: intent.expected_version,
  chapter_id: intent.chapter_id,
  task_id: intent.task_id,
  source_ids: [...intent.source_ids],
  card_refs: intent.card_refs.map(ref => ({ ...ref })),
  sensitive_context_confirmed: false,
})

/**
 * 一张卡在服务端当前的状态。
 *
 * 对不上就是「状态未知」，那时没有状态值——`status` 是 `undefined`，不是上一轮
 * 的缓存值，也不是同名卡的别的版本的状态。未知的原因分三种，面板据此措辞：
 *   - `version-moved`：这张卡在清单里，但已经不是包里的那个版本；
 *   - `not-found`：清单里根本没有这张卡；
 *   - `unavailable`：证据卡清单没读到（读清单这一步失败了），无从判断。
 */
export type ContextCardStatus =
  | { ref: VersionRef; state: 'known'; status: EvidenceCard['status']; reason: undefined }
  | { ref: VersionRef; state: 'unknown'; status: undefined; reason: 'version-moved' | 'not-found' | 'unavailable' }

/**
 * 按 id **且** version 双重对齐卡片状态。
 *
 * 服务端给的卡片引用只说「这一轮带了哪张卡的哪个版本」，不带业务状态；状态在
 * 证据卡清单里。id 与 version 都相等才用那个状态；版本不等、或者清单里根本没
 * 有这张卡，都记「状态未知」并说明原因——绝不退到同名卡的别的版本，也不用上一轮
 * 的值。
 *
 * `cards` 是 `undefined` 表示清单没读到：那时每一张卡都是「状态未知（清单读不到）」。
 */
export const alignCardStatuses = (
  refs: VersionRef[],
  cards: EvidenceCard[] | undefined,
): ContextCardStatus[] =>
  refs.map(ref => {
    if (cards === undefined) {
      return { ref: { ...ref }, state: 'unknown', reason: 'unavailable', status: undefined }
    }
    const exact = cards.find(item => item.id === ref.id && item.version === ref.version)
    if (exact) {
      return { ref: { ...ref }, state: 'known', reason: undefined, status: exact.status }
    }
    const sameId = cards.find(item => item.id === ref.id)
    return sameId
      ? { ref: { ...ref }, state: 'unknown', reason: 'version-moved', status: undefined }
      : { ref: { ...ref }, state: 'unknown', reason: 'not-found', status: undefined }
  })

/**
 * 服务端组装好的那一份，分成五组可展示的事实。
 *
 * 版本基线与权限两组是服务端包里那几个字段的投影（`Pick` 自契约类型），不是
 * 第二份契约：服务端加了字段，这里跟着契约走，不必再记一份。
 */
export interface ContextFactGroups {
  /** 这一轮带了哪几张卡，以及它们在服务端的当前状态。 */
  cards: ContextCardStatus[]
  /** 这一轮带了哪些来源，保持服务端给的顺序。 */
  sources: AgentSourceRef[]
  /** 服务端给的理由，原文逐条透传。 */
  reasons: string[]
  /** 这一轮据以组装的版本基线。 */
  version_baseline: Pick<
    AgentContextPackage,
    | 'project_version'
    | 'spec_revision'
    | 'context_revision'
    | 'chapter_version_id'
    | 'task_version'
    | 'template_id'
    | 'template_version'
  >
  /** 授权结论与产出约束，由服务端给出。 */
  authorization: Pick<AgentContextPackage, 'authorization_state' | 'output_constraints'>
}

/**
 * 事实：服务端这一轮组装好的东西。
 *
 * 这里只有服务端返回的值，没有「用户勾了什么」——那是意图那边的事。两者的字段
 * 集不相交，混用在类型层就暴露。
 */
export interface ContextFacts {
  package: AgentContextPackage
  groups: ContextFactGroups
}

/**
 * 把服务端组装好的那一份分成五组可展示的事实。
 *
 * 来源保持服务端给的顺序；理由保持服务端给的条数与顺序。理由是不透明字符串，
 * 这里原样透传——不按理由的文本去配对条目，也不按文本筛卡片。
 *
 * `cards` 是 `undefined` 表示证据卡清单没读到：范围照常来自上下文包，卡片状态
 * 一律记「状态未知」。
 */
export const groupContextFacts = (
  pkg: AgentContextPackage,
  cards: EvidenceCard[] | undefined,
): ContextFacts => ({
  package: pkg,
  groups: {
    cards: alignCardStatuses(pkg.card_refs, cards),
    sources: [...pkg.source_refs],
    reasons: [...pkg.selection_reasons],
    version_baseline: {
      project_version: pkg.project_version,
      spec_revision: pkg.spec_revision,
      context_revision: pkg.context_revision,
      chapter_version_id: pkg.chapter_version_id,
      task_version: pkg.task_version,
      template_id: pkg.template_id,
      template_version: pkg.template_version,
    },
    authorization: {
      authorization_state: pkg.authorization_state,
      output_constraints: [...pkg.output_constraints],
    },
  },
})

/**
 * 编排入口可注入的客户端。
 *
 * 默认是传输层已有的那两组函数——这里不新写 HTTP 客户端。测试注入一份假客户端，
 * 纯函数层因此完全不打网络。
 */
export interface ContextPackageClient {
  previewAgentContext: (projectId: string, input: AgentContextInput) => Promise<{ data: AgentContextPackage }>
  listEvidenceCards: (projectId: string) => Promise<{ data: EvidenceCard[] }>
}

const contextPackageClient: ContextPackageClient = { previewAgentContext, listEvidenceCards }

/**
 * 读证据卡清单；读不到就降级成「清单不可用」，不把整个预览拖垮。
 */
const listEvidenceCardsSafely = async (
  client: ContextPackageClient,
  projectId: string,
): Promise<EvidenceCard[] | undefined> => {
  try {
    return (await client.listEvidenceCards(projectId)).data
  } catch {
    return undefined
  }
}

/**
 * 预览这一轮的上下文包：请求服务端组装，再把结果分组。
 *
 * 两个请求**分别处理**：
 *   - 预览失败 ⇒ 整体失败。调用方据此清空事实区，别拿旧结果顶上。
 *   - 清单失败 ⇒ 降级。范围（来源、理由、版本基线、授权）照常来自上下文包，
 *     卡片状态一律记「状态未知（清单读不到）」。用户该看到的是「范围在这儿，
 *     但卡片状态查不到」，而不是「什么都没了」。
 *
 * 事实区**只由这一次预览的结果刷新**：上一次的结果不留、不合并、不缓存；意图
 * 里的勾选也不会渗进事实——服务端没带的，事实里就没有。
 */
export const previewContextPackage = async (
  projectId: string,
  intent: ContextIntent,
  client: ContextPackageClient = contextPackageClient,
): Promise<ContextFacts> => {
  const request = buildContextRequest(intent)
  // 两个请求同时发出去，但处理方式不同：预览的失败直接往上抛，清单的失败降级。
  const packagePromise = client.previewAgentContext(projectId, request)
  const cards = listEvidenceCardsSafely(client, projectId)
  const packageResult = await packagePromise
  return groupContextFacts(packageResult.data, await cards)
}
