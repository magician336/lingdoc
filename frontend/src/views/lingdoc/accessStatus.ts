import type { AccessStatus } from '@/api/lingdoc/workspace'

// 撤权提示的判定与文案，与渲染分开（同 deliveryState.ts / chapterCitations.ts 的做法）。
//
// 这个模块只认一件事：服务端说 `restricted` 时给出一条恢复路径。它**不**做拦截——
// 本轮只有 access-status 这一条读路径带资料层判定，别的端点仍会照常返回 200
//（差额见 docs/08-本轮实施方案/02-接口与Mock约定.md §8）。把提示写成封锁，界面就会
// 声称一个它并没有执行的限制。

/** 提示条的抬头。三态里只有 restricted 会走到这里。 */
export const RESTRICTED_NOTICE = '这个项目绑定的资料已不可用。'

/**
 * 用户能对这个状态做什么。
 *
 * `detail` 允许为空串：动作名的枚举由服务端定，认不出来的那种只能原样报出动作名，
 * 编一段中文解释等于替服务端猜它的语义。
 */
export interface RecoveryAction {
  code: string
  label: string
  detail: string
}

/**
 * 已知动作的中文说明。
 *
 * 键写成 `string` 而不是动作码的字面量联合，与 `AccessStatus.recovery_actions` 一致：
 * 服务端将来多一个动作时，界面必须把它**原样列出来**，不能因为自己的类型里没有
 * 这个取值就把它当成不存在。代价是漏翻一个已知动作不会有编译错误——那由测试兜
 * （见 accessStatus.test.ts 里对这两条枚举逐字比对的用例）。
 */
const KNOWN_ACTIONS: Record<string, { label: string; detail: string }> = {
  restore_source_authorization: {
    label: '恢复原资料授权',
    detail: '请资料的所有者把原资料重新授权给这个项目，然后点“重新检查”。',
  },
  create_clean_project: {
    label: '新建干净项目',
    detail: '重新建一个项目，只导入本次允许使用的资料，重新输入研究条件与正文。本版本不会把旧的派生正文搬过去。',
  },
}

/**
 * 服务端是否判为受限。
 *
 * 严格等于 `restricted`：`unknown`（答不出）与字段缺席都不是受限。把 `unknown` 拼进
 * 这个判断，会让每一次读取失败都弹出一条「资料不可用」——一个我们没有依据下的结论。
 */
export function isRestricted(status: AccessStatus | null | undefined): boolean {
  return status?.content_access === 'restricted'
}

/**
 * 恢复动作列表。不受限时为空——调用方用 `isRestricted` 决定要不要显示提示条，
 * 不要用这里的长度：受限但服务端没列出任何动作时，提示条仍然该出现（那时可走的路
 * 只剩「重新检查」），而长度为 0 会让它整个消失。
 */
export function recoveryActionsOf(status: AccessStatus | null | undefined): RecoveryAction[] {
  if (!isRestricted(status)) return []
  // 顺序照服务端给的抄，不重排：§8 的正文里「恢复原资料授权」在前，这个次序是有意的
  // （先给代价小的那条路），重排等于界面替用户做了取舍。
  return (status?.recovery_actions ?? []).map((code) => {
    const known = KNOWN_ACTIONS[code]
    return known
      ? { code, label: known.label, detail: known.detail }
      : { code, label: code, detail: '' }
  })
}
