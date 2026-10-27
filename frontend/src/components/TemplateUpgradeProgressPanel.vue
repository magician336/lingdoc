<script setup lang="ts">
// 模板升级进度面板（Spec #142 的前端面）。展示升级影响预览坐标、ImpactTask
// 进度、稳定化 blocked 原因、恢复动作与历史/当前副本标签；全部状态来自服务
// 端（change set、impact tasks、stabilize 结果），面板不本地推断稳定化。
// 取消只清空本地展示态，绝不发出写请求。
import { computed, ref } from 'vue'
import {
  actOnImpactTask,
  listImpactTasks,
  listMainlineChanges,
  stabilizeRevision,
  type ImpactTask,
  type MainlineChangeSet,
} from '@/api/lingdoc/mainlineChanges'
import { newIdempotencyKey } from '@/api/lingdoc/candidateAdoption'

const props = defineProps<{
  projectId: string
  changeId: string
  canManage: boolean
}>()

const change = ref<MainlineChangeSet | null>(null)
const tasks = ref<ImpactTask[]>([])
const loading = ref(false)
const failure = ref('')
const conflict = ref(false)
const stabilizeBlocked = ref<string[]>([])
const stabilizing = ref(false)
const pendingConfirmTaskId = ref('')

interface UpgradeDetails {
  base_template_copy_version?: number
  preview?: { missing_required?: string[]; orphaned?: string[] }
  impact?: { affected_chapter_ids?: string[]; affected_field_ids?: string[] }
}

const upgradeDetails = computed<UpgradeDetails | null>(() => {
  const raw = (change.value as unknown as { template_upgrade?: UpgradeDetails } | null)?.template_upgrade
  return raw ?? null
})

const historicalLabel = computed(() => {
  const base = upgradeDetails.value?.base_template_copy_version
  return base == null ? null : `历史副本 v${base}（已退役，仅可读）`
})
const currentLabel = computed(() => {
  const base = upgradeDetails.value?.base_template_copy_version
  // 升级 apply 的乐观锁声明即「基线 +1」：当前副本版本由升级自身声明可导出。
  const current = base == null ? null : base + 1
  return current == null ? null : `当前副本 v${current}（升级后）`
})

const openTasks = computed(() => tasks.value.filter((task) => task.status === 'OPEN'))
const doneTasks = computed(() => tasks.value.filter((task) => task.status === 'DONE'))
const taskProgressLabel = computed(() =>
  `${doneTasks.value.length}/${tasks.value.length} 项影响任务已闭环`)

async function load() {
  loading.value = true
  failure.value = ''
  conflict.value = false
  try {
    const changes = (await listMainlineChanges(props.projectId)).data
    change.value = changes.find((item) => item.id === props.changeId) ?? null
    tasks.value = (await listImpactTasks(props.projectId)).data
      .filter((task) => task.change_set_id === props.changeId)
  } catch (error: any) {
    failure.value = error?.error?.message ?? error?.message ?? String(error)
  } finally {
    loading.value = false
  }
}

// 修复动作：对单个 OPEN 任务走复核闭环（complete + 证据），冲突与失败都
// 保留原因；409 视为基线漂移，置 conflict 标志，恢复 = 重新读服务端状态。
async function completeTask(task: ImpactTask) {
  if (pendingConfirmTaskId.value !== task.id) {
    pendingConfirmTaskId.value = task.id
    return
  }
  pendingConfirmTaskId.value = ''
  try {
    await actOnImpactTask(props.projectId, task.id, {
      ExpectedVersion: task.version, TargetRevision: task.target_revision, Action: 'complete',
      HumanDecision: 'reviewed', Reason: '升级影响已复核',
      ClosureEvidence: [{ Kind: 'ChapterVersion', ID: task.target.id, Version: task.target.version }],
    } as any, newIdempotencyKey())
    await load()
  } catch (error: any) {
    const code = error?.error?.code ?? error?.code ?? ''
    failure.value = error?.error?.message ?? error?.message ?? String(error)
    if (code === 'version_conflict' || /version_conflict/.test(String(error))) {
      conflict.value = true
    }
  }
}

async function stabilize() {
  stabilizing.value = true
  failure.value = ''
  stabilizeBlocked.value = []
  try {
    const raw = (await stabilizeRevision(props.projectId, props.changeId, newIdempotencyKey())).data
    const result = (raw ?? {}) as { status?: string; blockers?: Array<{ code: string; target_id: string; reason: string }> }
    stabilizeBlocked.value = (result.blockers ?? []).map((blocker) =>
      `${blocker.code}（${blocker.target_id}）：${blocker.reason}`)
  } catch (error: any) {
    const code = error?.error?.code ?? error?.code ?? ''
    failure.value = error?.error?.message ?? error?.message ?? String(error)
    if (code === 'version_conflict' || /version_conflict/.test(String(error))) {
      conflict.value = true
    }
  } finally {
    stabilizing.value = false
  }
}

// 取消：只清空本地展示态（blocked 原因、确认态），不发任何请求。
function cancel() {
  stabilizeBlocked.value = []
  pendingConfirmTaskId.value = ''
  failure.value = ''
}

defineExpose({
  change, tasks, loading, failure, conflict, stabilizeBlocked, stabilizing,
  pendingConfirmTaskId, upgradeDetails, historicalLabel, currentLabel,
  openTasks, doneTasks, taskProgressLabel,
  load, completeTask, stabilize, cancel,
})
</script>

<template>
  <section class="upgrade-progress-panel" data-panel="template-upgrade-progress">
    <h3>模板升级进度</h3>
    <p v-if="loading" class="upgrade-loading">加载中…</p>
    <p v-if="failure" class="upgrade-failure" role="alert">失败：{{ failure }}</p>
    <p v-if="conflict" class="upgrade-conflict">
      检测到版本冲突：服务端状态已变化。请刷新读取最新状态后再操作。
      <button data-action="recover" @click="load">重新读取服务端状态</button>
    </p>
    <p v-if="historicalLabel" class="upgrade-label-historical">{{ historicalLabel }}</p>
    <p v-if="currentLabel" class="upgrade-label-current">{{ currentLabel }}</p>
    <p class="upgrade-task-progress">{{ taskProgressLabel }}</p>
    <p v-if="stabilizeBlocked.length > 0" class="upgrade-blocked">
      稳定化被以下事项阻断：
      <ul>
        <li v-for="reason in stabilizeBlocked" :key="reason">{{ reason }}</li>
      </ul>
    </p>
    <button v-if="canManage" :disabled="stabilizing" data-action="stabilize" @click="stabilize">
      {{ stabilizing ? '稳定化中…' : '尝试稳定化' }}
    </button>
    <button data-action="cancel" @click="cancel">取消</button>
    <ul>
      <li v-for="task in tasks" :key="task.id" :data-impact-task="task.id">
        <span class="upgrade-task-status">{{ task.status }}</span>
        <span class="upgrade-task-action">{{ task.action }}</span>
        <button v-if="canManage && task.status === 'OPEN'" :disabled="stabilizing"
                data-action="complete" @click="completeTask(task)">
          {{ pendingConfirmTaskId === task.id ? '确认闭环' : '复核闭环' }}
        </button>
        <button v-if="pendingConfirmTaskId === task.id" data-action="cancel-task" @click="cancel">
          取消
        </button>
      </li>
    </ul>
  </section>
</template>
