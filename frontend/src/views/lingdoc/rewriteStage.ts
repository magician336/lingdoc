/**
 * 选段改写候选的「走到了哪一步」。
 *
 * 它是从两个**事实**派生的显示用结论，不是服务端的第二个状态机：
 *
 * - `validity` 说这个产出还能不能被采纳（fresh / stale / not_authorized）。
 * - `apply_pending` 说这次采纳此刻正被写进工作副本。
 *
 * 两者回答的不是同一个问题，所以它们不共用一个字段。服务端曾经用一个
 * `status` 字符串同时装这两件事，于是「状态 = 生命周期阶段」这句话解释不了
 * `validity`；字符串状态机也解释不了「撤权」与「输入变了」为什么需要不同的
 * 下一步——那一步写在下面两份文案里，不在阶段里。
 */

/** 选段改写候选的阶段。 */
export type RewriteStage =
  | 'ready'
  | 'stale'
  | 'not_authorized'
  | 'failed'
  | 'applying'
  | 'pending'

/** 判定这个阶段所需要的那几个事实。 */
export interface RewriteStageFacts {
  /** 失效判据。空值表示这次运行没有交出可采纳的产出。 */
  validity?: '' | 'fresh' | 'stale' | 'not_authorized'
  /** 这次采纳此刻正被写进工作副本。 */
  apply_pending?: boolean
  /** 这次运行失败或失效的原因。 */
  error_code?: string
}

/**
 * 算出这个候选走到了哪一步。
 *
 * 判据的次序与服务端曾经那个 `rewriteLifecycleStage` 一致：正在应用优先于
 * 失效判据——一次正在进行的采纳，无论它产出时的判据是什么，此刻都正在写。
 */
export function rewriteStage(facts: RewriteStageFacts | null | undefined): RewriteStage {
  if (!facts) return 'pending'
  if (facts.apply_pending) return 'applying'
  switch (facts.validity) {
    case 'fresh':
      return 'ready'
    case 'stale':
      return 'stale'
    case 'not_authorized':
      return 'not_authorized'
    default:
      return facts.error_code ? 'failed' : 'pending'
  }
}

/** 阶段在「候选状态」那一行里的短名。 */
const stageLabels: Record<RewriteStage, string> = {
  ready: '可采纳',
  stale: '输入已变化',
  not_authorized: '授权已失效',
  failed: '生成失败',
  applying: '正在应用',
  pending: '进行中',
}

export function rewriteStageLabel(stage: RewriteStage): string {
  return stageLabels[stage] ?? stage
}

/**
 * 阶段对应的下一步。
 *
 * 「输入变了」与「授权没了」的下一步不同：前者重新运行那个动作就好，后者
 * 重新运行也没用——授权得先恢复。这两句文案因此分开写，不能合并成一句
 * 「不能采纳，请重新生成」。
 */
const stageNotices: Record<RewriteStage, string> = {
  ready: '',
  stale: '候选基于旧的工作副本或来源，不能采纳；请重新运行该动作。',
  not_authorized: '候选所依据的授权已失效，不能采纳；请恢复授权后重新运行该动作。',
  failed: '',
  applying: '这次采纳正在写入工作副本，请稍候；写入完成前不要重复提交。',
  pending: '这次运行还没有交出可采纳的产出，请稍候或重新运行该动作。',
}

export function rewriteStageNotice(stage: RewriteStage): string {
  return stageNotices[stage] ?? ''
}

/** 这个阶段能不能采纳。 */
export function rewriteStageAdoptable(stage: RewriteStage): boolean {
  return stage === 'ready' || stage === 'applying'
}
