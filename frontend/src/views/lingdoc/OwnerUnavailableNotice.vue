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
    <p>{{ props.reason }}</p>
    <p v-if="props.recovery" class="muted">{{ props.recovery }}</p>
  </div>
</template>
