import { computed, ref } from 'vue'
import { getEffectivePermission } from '@/api/lingdoc/permissions'
import { getOwnerAvailability } from '@/api/lingdoc/collaboration'

/**
 * Owner 专属动作被冻结时的原因码。
 *
 * 服务端在两处给出同一个值：权限读出口的 `reason_code`，与写操作拒绝里
 * 带出的原因。界面据此置灰入口，并把「Owner 的租户身份失效」讲清楚，
 * 而不是让用户以为自己没有权限。
 */
export const OWNER_UNAVAILABLE_REASON = 'owner_unavailable'

/**
 * Owner 专属决策所要求的能力。
 *
 * 立项确认、模板升级与变更集应用都是 Owner 专属的决策：它们没有第二个
 * 权威。权限读出口对这个能力回答「可否执行 + 原因」，所以三处入口读的是
 * 同一个答案——读一次，就知道这些入口现在是不是冻结的。
 */
export const OWNER_ONLY_CAPABILITY = 'manage'

/** 冻结原因文案。 */
export const OWNER_UNAVAILABLE_TEXT = '项目 Owner 的租户身份已失效，该入口已冻结。'

/** Owner 专属决策被冻结时的文案。 */
export const OWNER_DECISION_UNAVAILABLE_TEXT = '项目 Owner 的租户身份已失效，立项确认、模板升级与变更集应用已冻结。'

/** 恢复指引文案。 */
export const OWNER_RECOVERY_TEXT = '可等待租户恢复 Owner 的身份，或由 WeKnora 租户 Owner 指定受让人完成一次所有权转让。'

/**
 * 恢复路径的文案。
 *
 * 出口只给可识别的标识，具体怎么跟用户说由前端持有：服务端不决定文案，
 * 前端也不猜标识的含义。标识不认识时，界面退回标识本身，不谎称知道怎么办。
 */
export const OWNER_RECOVERY_PATH_TEXTS: Record<string, string> = {
  restore_tenant_identity: '等待租户恢复 Owner 的租户身份；身份恢复后冻结自动解除。',
  tenant_owner_nomination: '由 WeKnora 租户 Owner 指定受让人并发起一次所有权转让。',
}

/**
 * 冻结文案的选项。
 *
 * 文案由这一处持有，各入口只说自己在讲哪一个动作：页面与按钮不各写一遍。
 */
export interface OwnerAvailabilityOptions {
  /** 入口要问的能力；缺省是 Owner 专属决策所用的 manage。 */
  capability?: string
  /** 冻结原因文案；缺省是通用的那一句。 */
  reasonText?: string
}

/**
 * Owner 可用性事实。
 *
 * 它是派生只读的：服务端读的是 Owner 当前的租户成员状态与项目成员状态，
 * 没有落库，也没有「解冻」动作。租户身份恢复后，同一个判定自动变回可执行。
 *
 * 两级事实各问各的出口：入口级问权限读出口（「这个入口被冻了吗」），项目级
 * 问项目级只读出口（「这个项目现在出了什么事、怎么恢复」）。两个出口读的是
 * 同一份判定，所以两处口径一致。
 */
export interface OwnerAvailability {
  /** 冻结中：这个入口现在不可执行。 */
  frozen: boolean
  /** 冻结原因码；未冻结时是空串。 */
  reasonCode: string
  /** 冻结原因文案；未冻结时是空串。 */
  reason: string
  /** 恢复指引文案；未冻结时是空串。 */
  recovery: string
  /** 项目 Owner 是谁；读出口答不出来时是空串。 */
  ownerUserId: string
  /** 项目级事实：Owner 现在不可用。 */
  ownerUnavailable: boolean
  /** 恢复路径的可识别标识；未冻结时是空数组。 */
  recoveryPaths: string[]
  /** 恢复路径的文案，与 `recoveryPaths` 一一对应。 */
  recoveryTexts: string[]
  /** 项目级横幅的原因文案；Owner 可用时是空串。 */
  noticeReason: string
  /** 项目级横幅的恢复指引文案；Owner 可用时是空串。 */
  noticeRecovery: string
}

/**
 * 持有 Owner 可用性事实与恢复指引文案。
 *
 * 按钮的置灰条件、提示条的文案都从这里取，不在各页面各写一遍。
 */
export function useOwnerAvailability(
  projectId: () => string | undefined,
  options: OwnerAvailabilityOptions = {},
) {
  const reasonCode = ref('')
  const ownerUserId = ref('')
  const ownerReasonCode = ref('')
  const recoveryPaths = ref<string[]>([])
  const loading = ref(false)

  const frozen = computed(() => reasonCode.value === OWNER_UNAVAILABLE_REASON)
  const reason = computed(() => (frozen.value ? (options.reasonText ?? OWNER_UNAVAILABLE_TEXT) : ''))
  const recovery = computed(() => (frozen.value ? OWNER_RECOVERY_TEXT : ''))

  // 项目级事实：横幅说清「Owner 不可用」与「怎么恢复」。
  const ownerUnavailable = computed(() => ownerReasonCode.value === OWNER_UNAVAILABLE_REASON)
  const recoveryTexts = computed(() => recoveryPaths.value.map(path => OWNER_RECOVERY_PATH_TEXTS[path] ?? path))
  const noticeReason = computed(() => {
    if (!ownerUnavailable.value) return ''
    const owner = ownerUserId.value ? `（${ownerUserId.value}）` : ''
    return `项目 Owner ${owner}的租户身份已失效，Owner 专属操作已冻结。`
  })
  const noticeRecovery = computed(() => (ownerUnavailable.value ? recoveryTexts.value.join(' ') : ''))

  const forget = () => {
    reasonCode.value = ''
    ownerUserId.value = ''
    ownerReasonCode.value = ''
    recoveryPaths.value = []
  }

  const refresh = async () => {
    const id = projectId()
    if (!id) {
      forget()
      return
    }
    loading.value = true
    try {
      // 入口级：这个入口被冻了吗。问权限读出口，答案带着原因码。
      try {
        const answer = await getEffectivePermission(id, options.capability ?? OWNER_ONLY_CAPABILITY)
        reasonCode.value = answer?.data?.reason_code ?? ''
      } catch {
        // 读出口答不出来的时候，界面不谎称有冻结原因：入口保持原样。
        reasonCode.value = ''
      }
      // 项目级：这个项目现在出了什么事、怎么恢复。问项目级只读出口。
      try {
        const answer = await getOwnerAvailability(id)
        const fact = answer?.data
        ownerUserId.value = typeof fact?.owner_user_id === 'string' ? fact.owner_user_id : ''
        ownerReasonCode.value = typeof fact?.reason_code === 'string' ? fact.reason_code : ''
        recoveryPaths.value = fact && Array.isArray(fact.recovery_paths)
          ? fact.recovery_paths.filter(path => typeof path === 'string')
          : []
      } catch {
        ownerUserId.value = ''
        ownerReasonCode.value = ''
        recoveryPaths.value = []
      }
    } finally {
      loading.value = false
    }
  }

  return {
    frozen, reasonCode, reason, recovery,
    ownerUserId, ownerUnavailable, recoveryPaths, recoveryTexts, noticeReason, noticeRecovery,
    loading, refresh,
  }
}
