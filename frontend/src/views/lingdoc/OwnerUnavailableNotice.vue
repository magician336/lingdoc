<script setup lang="ts">
/**
 * Owner 不可用时的提示条。
 *
 * 文案由 `useOwnerAvailability` 持有：这一处只负责把事实显示出来，
 * 不自己判断什么是冻结。
 */
const props = withDefaults(defineProps<{
  /** 冻结原因文案；空串表示没有冻结。 */
  reason?: string
  /** 恢复指引文案；空串表示不显示指引。 */
  recovery?: string
}>(), { reason: '', recovery: '' })
</script>

<template>
  <div v-if="props.reason" class="owner-unavailable-notice">
    <p class="owner-unavailable-notice__reason">{{ props.reason }}</p>
    <p v-if="props.recovery" class="owner-unavailable-notice__recovery">{{ props.recovery }}</p>
  </div>
</template>

<style scoped>
.owner-unavailable-notice {
  margin: 10px 0;
  padding: 10px 12px;
  border-radius: 8px;
  background: #edf3ee;
  font-size: 13px;
}
.owner-unavailable-notice__reason { margin: 0; }
.owner-unavailable-notice__recovery { margin: 6px 0 0; color: #64748b; }
</style>
