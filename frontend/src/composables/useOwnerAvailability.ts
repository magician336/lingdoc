import { computed, ref } from 'vue'
import { getEffectivePermission } from '@/api/lingdoc/permissions'

/**
 * Owner 专属动作被冻结时的原因码。
 *
 * 服务端在两处给出同一个值：权限读出口的 `reason_code`，与写操作拒绝里
 * 带出的原因。界面据此置灰入口，并把「Owner 的租户身份失效」讲清楚，
 * 而不是让用户以为自己没有权限。
 */
export const OWNER_UNAVAILABLE_REASON = 'owner_unavailable'

/**
 * 变更集应用与驳回所要求的能力。
 *
 * 这两个动作是 Owner 专属的；权限读出口对这个能力回答「可否执行 + 原因」。
 */
export const OWNER_CHANGE_SET_CAPABILITY = 'manage'

/** 冻结原因文案。 */
export const OWNER_UNAVAILABLE_TEXT = '项目 Owner 的租户身份已失效，变更集应用与驳回已冻结。'

/** 恢复指引文案。 */
export const OWNER_RECOVERY_TEXT = '可等待租户恢复 Owner 的身份，或由租户 Owner 指定受让人完成一次所有权转让。'

/**
 * Owner 可用性事实。
 *
 * 它是派生只读的：服务端读的是 Owner 当前的租户成员状态与项目成员状态，
 * 没有落库，也没有「解冻」动作。租户身份恢复后，同一个判定自动变回可执行。
 */
export interface OwnerAvailability {
  /** 冻结中：Owner 专属动作现在不可执行。 */
  frozen: boolean
  /** 冻结原因码；未冻结时是空串。 */
  reasonCode: string
  /** 冻结原因文案；未冻结时是空串。 */
  reason: string
  /** 恢复指引文案；未冻结时是空串。 */
  recovery: string
}

/**
 * 持有 Owner 可用性事实与恢复指引文案。
 *
 * 按钮的置灰条件、提示条的文案都从这里取，不在各页面各写一遍。
 */
export function useOwnerAvailability(projectId: () => string | undefined) {
  const reasonCode = ref('')
  const loading = ref(false)

  const frozen = computed(() => reasonCode.value === OWNER_UNAVAILABLE_REASON)
  const reason = computed(() => (frozen.value ? OWNER_UNAVAILABLE_TEXT : ''))
  const recovery = computed(() => (frozen.value ? OWNER_RECOVERY_TEXT : ''))

  const refresh = async () => {
    const id = projectId()
    if (!id) {
      reasonCode.value = ''
      return
    }
    loading.value = true
    try {
      const answer = await getEffectivePermission(id, OWNER_CHANGE_SET_CAPABILITY)
      reasonCode.value = answer?.data?.reason_code ?? ''
    } catch {
      // 读出口答不出来的时候，界面不谎称有冻结原因：入口保持原样。
      reasonCode.value = ''
    } finally {
      loading.value = false
    }
  }

  return { frozen, reasonCode, reason, recovery, loading, refresh }
}
