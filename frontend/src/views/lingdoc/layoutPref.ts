// 工作台布局偏好（kimi-ui-20261013）：目录/辅助面板的开收状态按用户记忆。
// 只记用户显式操作；窄屏自动收起不写偏好，回到宽屏时按偏好恢复。
// 写入失败（隐私模式等）不影响使用，仅退化为不记忆。

export interface WbLayoutPref {
  rail?: boolean
  insp?: boolean
}

export function wbLayoutStorageKey(userJson: string | null): string {
  try {
    const u = JSON.parse(userJson || 'null')
    const suffix = String(u?.id || u?.user_id || '') + ':' + String(u?.tenant_id || '')
    return suffix === ':' ? 'lingdoc:wb-layout:anonymous' : 'lingdoc:wb-layout:' + suffix
  } catch {
    return 'lingdoc:wb-layout:anonymous'
  }
}

export function readWbLayoutPref(raw: string | null): WbLayoutPref {
  if (!raw) return {}
  try {
    const parsed = JSON.parse(raw)
    if (!parsed || typeof parsed !== 'object') return {}
    const pref: WbLayoutPref = {}
    if (typeof parsed.rail === 'boolean') pref.rail = parsed.rail
    if (typeof parsed.insp === 'boolean') pref.insp = parsed.insp
    return pref
  } catch {
    return {}
  }
}

export function mergeWbLayoutPref(base: WbLayoutPref, patch: WbLayoutPref): WbLayoutPref {
  return { ...base, ...patch }
}

// 窄屏边界独立于辅助区能否停靠。1440px 宿主中的工作台可能已经是浮层，
// 再进入手机宽度仍须自动关闭；自动行为不得覆盖用户显式开关偏好。
export function inspectorOnNarrowTransition(
  currentOpen: boolean, wasNarrow: boolean, isNarrow: boolean,
  preferred: WbLayoutPref, defaultWideOpen: boolean,
): boolean {
  if (wasNarrow === isNarrow) return currentOpen
  return isNarrow ? false : (preferred.insp ?? defaultWideOpen)
}
