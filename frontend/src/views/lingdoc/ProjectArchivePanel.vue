<script setup lang="ts">
/**
 * 归档入口。
 *
 * 归档是 Owner 专属的动作：Owner 的租户身份失效时它被冻结，入口置灰并把
 * 「为什么」讲清楚。冻结的事实与文案都来自 `useOwnerAvailability`，这一处
 * 只负责显示与发起，不自己判断什么是冻结。
 *
 * 归档是终态：归档之后项目不再接受任何变更，所以这里先确认再发起。
 */
import { computed, ref } from 'vue'
import { archiveProject } from '@/api/lingdoc/projectArchive'
import type { Project } from '@/api/lingdoc/workspace'
import { OWNER_ARCHIVE_UNAVAILABLE_TEXT, useOwnerAvailability } from '@/composables/useOwnerAvailability'
import OwnerUnavailableNotice from './OwnerUnavailableNotice.vue'

const props = defineProps<{ project: Project }>()
const emit = defineEmits<{ (event: 'archived', project: Project): void }>()

const busy = ref(false)
const errorMessage = ref('')

const { frozen, reason, recovery } = useOwnerAvailability(() => props.project?.id, { reasonText: OWNER_ARCHIVE_UNAVAILABLE_TEXT })

/** 只有 active 项目能归档：draft 项目只能丢弃。 */
const archivable = computed(() => props.project?.status === 'active')
/** 冻结中、进行中、或者项目本来就不能归档时，入口都不可用。 */
const disabled = computed(() => busy.value || frozen.value || !archivable.value)

async function archive() {
  const current = props.project
  if (!current || disabled.value) return
  if (!window.confirm('归档是终态：归档之后项目不再接受任何变更。确定归档这个项目吗？')) return
  busy.value = true
  errorMessage.value = ''
  try {
    const result = await archiveProject(current.id, current.project_version, `archive:${current.id}`)
    emit('archived', result.data)
  } catch {
    errorMessage.value = '归档失败，请读取当前项目版本后重试。'
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <section class="project-archive-panel" aria-label="项目归档">
    <h3>归档</h3>
    <p class="muted">归档是终态：归档之后项目不再接受任何变更。只有 active 项目能归档，draft 项目只能丢弃。</p>
    <button type="button" :disabled="disabled" @click="archive">归档项目</button>
    <OwnerUnavailableNotice :reason="reason" :recovery="recovery" />
    <p v-if="errorMessage" class="muted" role="status">{{ errorMessage }}</p>
  </section>
</template>

<style scoped>
.project-archive-panel { margin: 12px 0; }
.project-archive-panel h3 { margin: 0 0 6px; font-size: 14px; }
</style>
