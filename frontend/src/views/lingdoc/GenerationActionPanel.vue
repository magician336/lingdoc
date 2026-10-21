<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import {
  fetchWritingActions,
  outputParamsFit,
  type WritingActionAdoptionTarget,
  type WritingActionDescription,
  type WritingActionOutputParams,
  type WritingActionProduce,
  type WritingActionScope,
} from '@/api/lingdoc/writingAction'

/**
 * 动作面板：执行前把这次动作的五类信息摆出来。
 *
 * 动作、允许的目标范围、可用的产出参数、产出形状、允许的采纳目标——五类
 * 信息全部来自服务端的动作表，组件里没有第二份动作清单。产出参数的取值
 * 落在闭合取值集之外时，面板不发请求。
 */
const props = withDefaults(defineProps<{
  projectId: string
  scope?: WritingActionScope
  action?: string
  instruction?: string
  /** 动作表。给了就用它，不再去读服务端；没给就在挂载后读。 */
  actionTable?: WritingActionDescription[]
}>(), { scope: 'section' })

const emit = defineEmits<{
  submit: [payload: {
    action: string
    scope: WritingActionScope
    output_params: WritingActionOutputParams
    instruction: string
  }]
}>()

const loaded = ref<WritingActionDescription[]>([])
const selectedAction = ref<string>(props.action ?? '')
const chosen = ref<Record<string, string>>({})
const ratio = ref<number | undefined>(undefined)
const note = ref<string>(props.instruction ?? '')
const loadError = ref('')

onMounted(async () => {
  if (props.actionTable) return
  try {
    const response = await fetchWritingActions(props.projectId, props.scope)
    loaded.value = response.data
    if (!selectedAction.value && response.data.length > 0) {
      selectedAction.value = response.data[0].action
    }
  } catch {
    loadError.value = '动作表读取失败，暂时不能按动作发起。'
  }
})

const descriptions = computed<WritingActionDescription[]>(() =>
  props.actionTable ? props.actionTable : loaded.value,
)

const current = computed<WritingActionDescription | null>(() =>
  descriptions.value.find(item => item.action === selectedAction.value) ?? null,
)

const params = computed<WritingActionOutputParams>(() => {
  const value: WritingActionOutputParams = {}
  for (const option of current.value?.output_params ?? []) {
    if (option.name === 'ratio') {
      if (ratio.value !== undefined) value.ratio = ratio.value
      continue
    }
    const picked = chosen.value[option.name]
    if (picked === undefined) continue
    if (option.name === 'length') value.length = picked
    else if (option.name === 'tone') value.tone = picked
    else if (option.name === 'transform_mode') value.transform = { ...(value.transform ?? {}), mode: picked }
    else if (option.name === 'target_language') value.transform = { ...(value.transform ?? {}), target_language: picked }
    else if (option.name === 'audience') value.transform = { ...(value.transform ?? {}), audience: picked }
  }
  return value
})

/** 产出参数是否合法。取值不合法时面板不发请求。 */
const legal = computed<boolean>(() =>
  current.value !== null && outputParamsFit(current.value, params.value),
)

const scopeLabels: Record<string, string> = { section: '选段', chapter: '整章' }
const produceLabels: Record<string, string> = {
  prose: '候选正文',
  outline: '章节骨架',
  findings: '评审意见',
}
const targetLabels: Record<string, string> = {
  working_copy_partial: '工作副本·部分',
  working_copy_whole: '工作副本·整段',
  formal_version_whole: '正式版本·整段',
}

function scopeLabel(scope: string) {
  return scopeLabels[scope] ?? scope
}
function produceLabel(produce?: WritingActionProduce) {
  return produce ? (produceLabels[produce] ?? produce) : '—'
}
function targetLabel(target: WritingActionAdoptionTarget) {
  return targetLabels[target] ?? target
}

function submit() {
  if (!legal.value || !current.value) return
  emit('submit', {
    action: current.value.action,
    scope: props.scope,
    output_params: params.value,
    instruction: note.value,
  })
}
</script>

<template>
  <section class="action-panel" aria-label="动作面板">
    <h3 class="action-panel__heading">动作</h3>
    <p class="action-panel__action">
      动作：{{ current ? current.label : '未选择' }}<span v-if="current">（{{ current.action }}）</span>
    </p>

    <h3 class="action-panel__heading">允许的目标范围</h3>
    <ul class="action-panel__scopes">
      <li v-for="scope in current?.scopes ?? []" :key="scope">{{ scopeLabel(scope) }}</li>
      <li v-if="!current">—</li>
    </ul>

    <h3 class="action-panel__heading">可用的产出参数</h3>
    <ul class="action-panel__params">
      <li v-for="option in current?.output_params ?? []" :key="option.name">
        <span class="action-panel__param-name">{{ option.name }}</span>
        <select v-if="option.values.length" v-model="chosen[option.name]" :data-param="option.name">
          <option value="">未选择</option>
          <option v-for="value in option.values" :key="value" :value="value">{{ value }}</option>
        </select>
        <input
          v-else
          v-model.number="ratio"
          type="number"
          :min="option.min"
          :max="option.max"
          :data-param="option.name"
        >
      </li>
      <li v-if="!current?.output_params.length">—</li>
    </ul>

    <h3 class="action-panel__heading">产出形状</h3>
    <p class="action-panel__produce">产出形状：{{ produceLabel(current?.produce) }}</p>

    <h3 class="action-panel__heading">允许的采纳目标</h3>
    <ul class="action-panel__targets">
      <li v-for="target in current?.targets ?? []" :key="target">{{ targetLabel(target) }}</li>
      <li v-if="!current?.targets.length">—</li>
    </ul>

    <label class="action-panel__note">
      补充说明
      <input v-model="note" type="text" data-param="instruction">
    </label>

    <p v-if="loadError" class="action-panel__error" role="alert">{{ loadError }}</p>
    <p v-else-if="current && !legal" class="action-panel__error" role="alert">
      产出参数还没填齐或取值不合法，暂时不能发起。
    </p>

    <button type="button" class="action-panel__submit" :disabled="!legal" @click="submit">执行</button>
  </section>
</template>

<style scoped>
.action-panel { padding: 16px; border: 1px solid #e2e8f0; border-radius: 12px; background: #f8fafc; color: #0f172a; }
.action-panel__heading { margin: 12px 0 4px; font-size: 13px; color: #64748b; }
.action-panel__heading:first-child { margin-top: 0; }
.action-panel__action, .action-panel__produce { margin: 0; font-size: 14px; }
.action-panel__scopes, .action-panel__params, .action-panel__targets { margin: 0; padding-left: 20px; font-size: 13px; }
.action-panel__params li { display: flex; gap: 8px; align-items: center; margin-bottom: 4px; }
.action-panel__param-name { min-width: 120px; color: #475569; }
.action-panel__note { display: block; margin-top: 12px; font-size: 13px; color: #475569; }
.action-panel__error { margin: 8px 0 0; color: #b91c1c; font-size: 13px; }
.action-panel__submit { margin-top: 12px; padding: 8px 16px; border: 0; border-radius: 8px; background: #2563eb; color: #fff; cursor: pointer; }
.action-panel__submit:disabled { cursor: not-allowed; opacity: .5; }
</style>
