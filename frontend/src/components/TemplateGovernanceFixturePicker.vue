<script setup lang="ts">
import { computed } from 'vue'
import type { ReplayFixtureSummary } from '@/api/lingdoc/templateGovernance'
import { fixtureCoverage } from './templateGovernanceState'

// FixturePicker lists only server-registered replay fixtures for the exact
// replay target (template version or ruleset context). Selection edits the same
// ID text the panel submits, so manual paste and checkbox picking never drift.
// When the fixture catalog cannot be read, user-typed IDs stay unverified
// instead of being asserted as rejected — the server remains the authority.
const props = defineProps<{ fixtures: ReplayFixtureSummary[]; modelValue: string; disabled?: boolean; catalogError?: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const selectedIds = computed(() => [...new Set(props.modelValue.split(/[\s,，]+/).map(item => item.trim()).filter(Boolean))])
const catalogKnown = computed(() => !props.catalogError && props.fixtures.length > 0)
const coverage = computed(() => fixtureCoverage(selectedIds.value, catalogKnown.value ? props.fixtures : null))
const unverifiedIds = computed(() => catalogKnown.value ? selectedIds.value.filter(id => !props.fixtures.some(fixture => fixture.fixture_id === id)) : [])
const pendingIds = computed(() => catalogKnown.value ? [] : selectedIds.value)
function apply(ids: string[]) { emit('update:modelValue', [...new Set(ids)].join(' ')) }
function toggle(id: string, on: boolean) { apply(on ? [...selectedIds.value, id] : selectedIds.value.filter(existing => existing !== id)) }
const sceneLabel = (scene: string) => scene === 'write_check' ? '写作检查' : scene === 'preflight' ? '交付预检' : scene
const polarityLabel = (polarity: string) => polarity === 'positive' ? '正例' : polarity === 'negative' ? '反例' : polarity
</script>

<template>
  <div class="fixture-picker">
    <p v-if="coverage.unknown" class="subtle">所需正反例组合尚不能从此处确认（样例目录不可用或无匹配样例），以服务端注册与回放核验为准。</p>
    <p v-else class="subtle">服务端将实际执行所选样例；不绑定当前回放目标的样例会被拒绝。已覆盖 {{ coverage.covered }}/{{ coverage.required }} 个必需正反例组合{{ coverage.extra ? `；另有 ${coverage.extra} 个同组合注册样例` : '' }}。</p>
    <div class="actions">
      <button type="button" :disabled="disabled || !fixtures.length" @click="apply(fixtures.map(fixture => fixture.fixture_id))">选中全部匹配样例</button>
      <button type="button" :disabled="disabled || !selectedIds.length" @click="apply([])">清空</button>
    </div>
    <p v-if="catalogError" class="notice error">样例目录读取失败：{{ catalogError }}。仍可手动填写服务器已注册的样例 ID，原有输入已保留。</p>
    <p v-else-if="!fixtures.length" class="notice">没有绑定当前回放目标的已注册样例。请核对模板与规则版本，或由服务侧注册复核样例后再发布。</p>
    <p v-if="unverifiedIds.length" class="notice error">以下 ID 未在服务器注册，提交会被拒绝：{{ unverifiedIds.join('、') }}</p>
    <p v-else-if="pendingIds.length" class="notice">以下 ID 尚未核验（样例目录不可用），提交时以服务端注册与回放结果为准：{{ pendingIds.join('、') }}</p>
    <p v-else-if="!coverage.unknown && coverage.missing.length && selectedIds.length" class="notice">还缺这些必需组合：{{ coverage.missing.join('、') }}</p>
    <div v-if="fixtures.length" class="fixture-list">
      <label v-for="fixture in fixtures" :key="fixture.fixture_id" class="checkbox fixture-entry">
        <input type="checkbox" :checked="selectedIds.includes(fixture.fixture_id)" :disabled="disabled" @change="toggle(fixture.fixture_id, ($event.target as HTMLInputElement).checked)" />
        <span><code>{{ fixture.fixture_id }}</code><small>{{ fixture.rule_id }} · {{ sceneLabel(fixture.scene) }} · {{ polarityLabel(fixture.polarity) }} · {{ fixture.target_ref }}</small></span>
      </label>
    </div>
  </div>
</template>

<style scoped>
.fixture-picker { display: grid; gap: 10px; }
.actions { display: flex; flex-wrap: wrap; gap: 10px; }
button { border: 1px solid #d6dee7; border-radius: 7px; background: #fff; color: #24374f; padding: 8px 12px; cursor: pointer; }
button:disabled { opacity: .5; cursor: default; }
.subtle { color: #68778a; font-size: 13px; line-height: 1.65; margin: 0; overflow-wrap: anywhere; }
.notice { background: #f0f6f2; border: 1px solid #d2e3d7; padding: 10px 13px; border-radius: 8px; font-size: 13px; line-height: 1.7; margin: 0; overflow-wrap: anywhere; word-break: break-word; }
.notice.error { background: #fff6f0; border-color: #ecd3c1; color: #8a4c2a; }
.fixture-list { display: grid; grid-template-columns: repeat(auto-fill, minmax(260px, 1fr)); gap: 8px; }
.fixture-entry { display: flex; align-items: flex-start; gap: 8px; border: 1px solid #e2e8ed; border-radius: 8px; padding: 9px 11px; font-size: 13px; color: #24374f; min-width: 0; }
.fixture-entry input { width: auto; margin-top: 3px; }
.fixture-entry span { display: grid; gap: 3px; min-width: 0; }
.fixture-entry code { font-family: ui-monospace, monospace; font-size: 12px; overflow-wrap: anywhere; word-break: break-all; }
.fixture-entry small { color: #68778a; line-height: 1.5; overflow-wrap: anywhere; }
</style>
