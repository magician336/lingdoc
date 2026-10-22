<template>
  <section class="ops-panel" aria-label="运行观测与真实试用">
    <header><div><h2>运行观测与真实试用</h2><p>{{ project.name }} · 基线 {{ project.current_context_revision }}</p></div><button @click="$emit('close')">关闭</button></header>
    <p v-if="readonly" class="notice">当前仅可查看。业务操作仍由服务端校验当前权限、来源与版本。</p>
    <nav><button v-for="entry in tabs" :key="entry.id" :aria-pressed="tab === entry.id" @click="tab = entry.id">{{ entry.label }}</button><button :disabled="loading" @click="refresh">{{ loading ? '读取中…' : '重新读取' }}</button></nav>
    <p v-if="error" role="alert" class="error">{{ error }}</p><p v-if="message" role="status" class="notice">{{ message }}</p>
    <label v-if="canWrite">本次操作说明<textarea :disabled="busy" v-model="reason" maxlength="2000" placeholder="暂停、恢复、告警处置或试用决策的原因" /></label>
    <template v-if="tab === 'runtime'">
      <div v-if="metrics" class="grid metrics"><article><strong>{{ metrics.event_count }}</strong>已记录事件</article><article><strong>{{ metrics.p95_ms ?? '缺样本' }}</strong>P95 毫秒</article><article><strong>{{ metrics.p99_ms ?? '缺样本' }}</strong>P99 毫秒</article><article><strong>{{ metrics.queue_depth }}</strong>观测队列深度</article></div>
      <div v-if="metrics" class="grid metrics" aria-label="运行指标"><article v-for="item in runtimeMetricItems" :key="item.key"><strong>{{ item.unavailableReason ? '后端未提供' : item.total === null ? '无观测' : item.total }}</strong>{{ item.label }}<small v-if="item.unavailableReason">{{ item.unavailableReason }}</small><small v-else-if="item.note">{{ item.note }}</small></article></div>
      <p v-if="metrics && metrics.missing && metrics.missing.length"><small>服务端标注缺失的观测：{{ metrics.missing.join('、') }}。</small></p>
      <details v-if="metrics && runtimeCountRows.length"><summary>服务端运行计数明细（权威视图）</summary><table><thead><tr><th>资源类别</th><th>状态</th><th>计数</th><th>原始键</th></tr></thead><tbody><tr v-for="row in runtimeCountRows" :key="row.key"><td>{{ row.categoryLabel }}</td><td>{{ row.statusLabel }}</td><td>{{ row.count }}</td><td><small>{{ row.key }}</small></td></tr></tbody></table></details>
      <form v-if="canManage" @submit.prevent="pauseRuntime"><h3>暂停运行范围</h3><label>作用范围<select :disabled="busy" v-model="runtimeScopeKind"><option value="project">当前课题</option><option value="tenant" :disabled="!auth.effectiveTenantId">当前租户</option><option value="template_version" :disabled="!project.template_version">当前模板版本</option><option value="ruleset_hash" :disabled="!project.template_copy?.ruleset_hash">当前规则摘要</option><option value="runtime_mode">指定运行模式</option></select></label><label v-if="runtimeScopeKind==='runtime_mode'">模式<select :disabled="busy" v-model="runtimeModeScope" required><option value="">选择实际范围</option><option value="mock">mock</option><option value="real_api_fake_model">real_api_fake_model</option><option value="real">real</option></select></label><p>实际范围：{{ runtimeScope.kind }} / {{ runtimeScope.value || '尚未选择' }}。租户、版本、摘要和模式范围可影响当前租户内其他匹配课题；服务端核对租户管理权限。</p><button :disabled="busy || !runtimeReadReady || !runtimeScope.value || selectedRuntimeControl?.status === 'PAUSED' || !reason.trim()">请求暂停此范围的新写入、生成和交付</button></form>
      <p v-if="evidenceReadError" role="alert" class="error">{{ evidenceReadError }}</p>
      <article v-for="control in controls" :key="control.id"><h3>{{ control.status === 'PAUSED' ? '已暂停' : '已恢复' }} · {{ control.scope.kind }}/{{ control.scope.value }}</h3><p>{{ control.reason }} · {{ control.actor_id }} · {{ control.updated_at }}</p><p v-if="control.recovery_verified">已记录服务端权限、当前性和下载复验。</p><form v-if="canManage && control.status === 'PAUSED'" @submit.prevent="resumeRuntime(control)"><label>已有复验快照<select :disabled="busy || evidenceLoading" v-model="recoverySnapshot" required><option value="">选择快照，提交时重新读取当前性</option><option v-for="snapshot in releaseOptions" :key="snapshot.id" :value="snapshot.id">{{ snapshot.id }} · {{ snapshot.check.status }} · {{ snapshot.created_at }}</option></select></label><label>该快照的已有导出<select :disabled="busy || evidenceLoading" v-model="recoveryExport" required><option value="">选择已验证文件</option><option v-for="artifact in recoveryExports" :key="artifact.id" :value="artifact.id">{{ artifact.id }} · {{ artifact.status }}</option></select></label><button :disabled="busy || !runtimeReadReady || !reason.trim() || !recoverySnapshot || !recoveryExport">完成服务端复验后恢复</button></form></article>
      <form v-if="canManage" @submit.prevent="addAlert"><h3>绑定告警处置</h3><p>此告警规则只处理当前课题。其他范围的暂停请使用上方独立运行控制。重试和回退仅登记申请；当前没有可由此面板执行的回退器。正式变更回退须经过逆向草稿、评估与 Owner 应用。</p><label>信号代码<input :disabled="busy" v-model="alertSignal" placeholder="例如 dependency/failed" required /></label><label>严重度<select :disabled="busy" v-model="alertSeverity" required><option value="">选择真实故障级别</option><option>P0</option><option>P1</option><option>P2</option><option>P3</option></select></label><label>动作<select :disabled="busy" v-model="alertAction"><option value="notify">通知责任人</option><option value="pause">暂停当前课题</option><option value="block_export">阻止交付</option><option value="retry">申请重试（未执行）</option><option value="rollback">申请回退（未执行）</option><option value="human_confirm">等待人工确认</option></select></label><label>责任人<select :disabled="busy" v-model="alertResponsible" required><option value="">选择责任人</option><option v-for="member in activeMembers" :key="member.user_id">{{ member.user_id }}</option></select></label><button :disabled="busy || !alertSeverity || !reason.trim()">保存告警处置规则</button></form>
      <ul><li v-for="alert in alerts" :key="alert.id">{{ alert.severity }} · {{ alert.action }} · {{ alert.status }} · {{ alert.responsible_id }}</li></ul><form @submit.prevent="loadEvents"><label>关联编号<input :disabled="busy" v-model="correlation" placeholder="查看一次真实操作的事件链" /></label><button :disabled="loading">读取事件链</button></form><table><thead><tr><th>事件 / 关联编号</th><th>来源</th><th>模式</th><th>状态</th><th>耗时</th></tr></thead><tbody><tr v-for="event in events" :key="event.id"><td>{{ event.resource_category }}<small>{{ event.correlation_id }}</small></td><td>{{ event.origin === 'runtime' ? '实际领域路径' : '人工上报' }}</td><td>{{ event.runtime_mode }}</td><td>{{ event.status }} {{ event.error_code }}</td><td>{{ event.duration_ms }} ms</td></tr></tbody></table>
    </template>
    <template v-if="tab === 'trial'">
      <p class="notice">只统计证据完整的 real 样本。人工复盘、Microsoft Word 往返及产品 / QA / 安全签署须由真实责任人提供。</p>
      <form v-if="canManage" @submit.prevent="addWindow"><h3>建立真实试用窗口</h3><div class="grid"><label>开始时间<input :disabled="busy" v-model="trialStart" type="datetime-local" required /></label><label>结束时间<input :disabled="busy" v-model="trialEnd" type="datetime-local" required /></label><label>目标样本<input :disabled="busy" v-model.number="targetSamples" type="number" min="1" required /></label><label>最低样本<input :disabled="busy" v-model.number="minimumSamples" type="number" min="1" :max="targetSamples" required /></label></div><label>纳入人员<select :disabled="busy" v-model="includedUsers" multiple required><option v-for="member in activeMembers" :key="member.user_id">{{ member.user_id }}</option></select></label><div class="grid"><label v-for="role in decisionRoles" :key="role">{{ roleLabel(role) }}责任人<select :disabled="busy" v-model="decisionOwners[role]" required><option value="">选择责任人</option><option v-for="member in activeMembers" :key="member.user_id">{{ member.user_id }}</option></select></label></div><label>人工支持方案编号<input :disabled="busy" v-model="trialSupport" required /></label><label>脱敏方案编号<input :disabled="busy" v-model="trialRedaction" required /></label><label>退出条件代码<input :disabled="busy" v-model="trialExit" placeholder="逗号分隔" required /></label><button :disabled="busy">保存窗口草稿</button></form>
      <article v-for="window in windows" :key="window.id"><h3>窗口 {{ window.id }} · {{ label(window.status) }}</h3><p>{{ window.starts_at }} → {{ window.ends_at }} · 样本 {{ window.minimum_samples }}/{{ window.target_samples }}</p><div class="actions"><button @click="selectWindow(window)">查看样本与统计</button><button v-if="canManage && window.status === 'DRAFT'" :disabled="busy" @click="windowAction(window, 'lock')">锁定并开启</button><button v-if="canManage && window.status === 'LOCKED'" :disabled="busy" @click="windowAction(window, 'pause')">暂停试用</button><button v-if="canManage && window.status !== 'CLOSED'" :disabled="busy" @click="windowAction(window, 'close')">关闭并保留历史</button></div></article>
      <section v-if="selectedWindow"><h3>当前窗口 {{ selectedWindow.id }}</h3><article v-if="trialReport"><strong>{{ trialReport.readiness }}</strong><p>提交 {{ trialReport.submitted }} · 效果分母 {{ trialReport.eligible }}</p><ul><li v-for="blocker in trialReport.blockers" :key="blocker">{{ blocker }}</li><li v-for="(ids, why) in trialReport.excluded" :key="why">排除 {{ why }}：{{ ids.length }} 个</li></ul><table><thead><tr><th>指标</th><th>分子</th><th>分母</th><th>可报告值</th></tr></thead><tbody><tr v-for="metric in trialReport.metrics" :key="metric.name"><td>{{ metric.name }}</td><td>{{ metric.numerator }}</td><td>{{ metric.denominator }}</td><td>{{ metric.value ?? '不足以报告' }}</td></tr></tbody></table></article>
        <form v-if="canWrite && selectedWindow.status === 'LOCKED'" @submit.prevent="submitSample"><h3>记录已有执行样本</h3><p>选择真实回执后填写人工记录。补录字段尚未由系统验证；提交后的质量状态仍是 NOT RUN，真实试用分母由服务端检查。</p><button type="button" :disabled="busy || evidenceLoading" @click="loadEvidenceCatalog">重新读取已有运行和文件</button><p v-if="evidenceReadError" role="alert" class="error">{{ evidenceReadError }}</p><label>已有运行、快照或导出<select :disabled="busy || evidenceLoading" v-model="selectedEvidenceKey" @change="selectTrialEvidence" required><option value="">选择现有记录</option><option v-for="item in evidenceOptions" :key="evidenceKey(item)" :value="evidenceKey(item)">{{ item.label }}</option></select></label><p v-if="sampleFacts">已读取实际引用：{{ sampleFacts.reference }}。提交前再次读取；旧身份、版本或历史快照不能继承验证。</p>
          <div class="grid"><label>样本版本编号<input :disabled="busy" v-model="sampleForm.sampleVersion" placeholder="已有执行记录的版本，不自动生成" required /></label><label>执行时间<input :disabled="busy" v-model="sampleForm.executedAt" type="datetime-local" step="1" required /></label><label>实际运行模式<select :disabled="busy || !!sampleFacts?.runtimeMode" v-model="sampleForm.runtimeMode" required><option value="">回执未声明，请按实际证据填写</option><option value="mock">mock（不进入真实效果分母）</option><option value="real_api_fake_model">real_api_fake_model（不进入真实效果分母）</option><option value="real">real（仍须完整运行链和人工审核）</option></select></label><label>发现的故障级别<select :disabled="busy" v-model="sampleForm.severity"><option value="">尚未判定故障级别</option><option>P0</option><option>P1</option><option>P2</option><option>P3</option></select></label></div>
          <div class="grid"><label>夹具或样本输入摘要<input :disabled="busy" :readonly="!!sampleFacts?.fixtureDigest" v-model="sampleForm.fixtureDigest" required /></label><label>实际输入摘要<input :disabled="busy" :readonly="!!sampleFacts?.inputHash" v-model="sampleForm.inputHash" required /></label><label>实际输出摘要<input :disabled="busy" :readonly="!!sampleFacts?.outputHash" v-model="sampleForm.outputHash" required /></label><label>已保存的授权快照引用<input :disabled="busy" v-model="sampleForm.permissionSnapshot" placeholder="填写实际授权证据编号，不粘贴凭证或原文" required /></label></div>
          <div class="grid"><label>模板版本<input v-model="sampleForm.templateVersion" readonly /></label><label>规则摘要<input v-model="sampleForm.rulesetHash" readonly /></label><label>当前基线<input :disabled="busy" :readonly="sampleFacts?.contextRevision !== null && !!sampleFacts" v-model.number="sampleForm.contextRevision" type="number" min="1" required /></label><label>实际关联编号<input :disabled="busy" :readonly="!!sampleFacts?.correlationId" v-model="sampleForm.correlationId" required /></label></div>
          <dl v-if="sampleFacts"><template v-for="(version, name) in sampleForm.dependencies" :key="name"><dt>{{ name }}</dt><dd>{{ version }}</dd></template></dl>
          <fieldset><legend>实际运行事件</legend><button type="button" :disabled="busy || evidenceLoading || !sampleForm.correlationId" @click="loadSampleEvents">读取此关联编号的事件</button><label>已有领域事件<select :disabled="busy || evidenceLoading" v-model="sampleForm.runtimeEventIds" multiple><option v-for="event in sampleEventOptions" :key="event.id" :value="event.id">{{ event.resource_category }} / {{ event.status }} · {{ event.runtime_mode }} · {{ event.id }}</option></select></label><p>无事件、人工上报事件或缺少导出与下载链时，不自动视为真实案例完成。</p></fieldset>
          <fieldset><legend>过程证据引用（尚未人工核验）</legend><label v-for="entry in processNames" :key="entry.name">{{ entry.label }}<input :disabled="busy" v-model="sampleForm.process[entry.name]" placeholder="实际脱敏记录编号；未执行则留空" /></label></fieldset>
          <fieldset><legend>人工测量指标</legend><p>未测量项保留缺失；分子、分母和测量版本由执行者填写，系统不补零。</p><article v-for="metric in sampleForm.metrics" :key="metric.name"><label><input :disabled="busy" v-model="metric.measured" type="checkbox" />已实际测量：{{ metric.label }}</label><div v-if="metric.measured" class="grid"><label>分子<input :disabled="busy" v-model="metric.numerator" type="number" min="0" step="any" required /></label><label>分母<input :disabled="busy" v-model="metric.denominator" type="number" min="0.000001" step="any" required /></label><label>测量记录版本<input :disabled="busy" v-model="metric.version" required /></label></div><p v-else>未测量 / 未验证</p></article></fieldset>
          <label><input :disabled="busy" v-model="sampleOwnershipConfirmed" type="checkbox" required />我确认该次执行由我完成，补录信息来自实际记录；尚未测量或执行的事项已保留为空。</label><button :disabled="busy || evidenceLoading || !sampleFacts || !sampleOwnershipConfirmed">保存为尚未人工审核</button><button type="button" :disabled="busy" @click="resetSampleForm">清空样本表单</button></form><article v-for="sample in samples" :key="sample.id"><h3>{{ sample.sample_version }} · {{ sample.runtime_mode }} · {{ sample.quality_result }}</h3><p>执行 {{ sample.executor_id }} · 审核 {{ sample.reviewer_id || '尚未审核' }}</p><button v-if="canManage && !sample.reviewer_id" :disabled="busy" @click="openReview(sample)">填写真实人工裁定</button></article>
        <form v-if="reviewingSample && canManage" @submit.prevent="submitReview"><h3>人工裁定 {{ reviewingSample.sample_version }}</h3><label>质量结果<select :disabled="busy" v-model="reviewResult" required><option value="">选择真实结果</option><option>PASS</option><option>FAIL</option><option>BLOCKED</option><option>NOT RUN</option></select></label><label>人工决定<select :disabled="busy" v-model="humanDecision" required><option value="">选择真实决定</option><option value="accept">接受</option><option value="reject">拒绝</option><option value="needs_work">需要处理</option></select></label><label>Microsoft Word 实际往返结果记录（未执行须提供未执行说明）<input :disabled="busy" v-model="wordEvidence" required /></label><fieldset v-if="reviewingSample.severity === 'P2'"><legend>P2 补救和复跑</legend><label>责任人<select :disabled="busy" v-model="repairOwner" required><option value="">选择实际修复责任人</option><option v-for="member in activeMembers" :key="member.user_id">{{ member.user_id }}</option></select></label><label>修复版本<input :disabled="busy" v-model="repairVersion" required /></label><label>复跑证据<input :disabled="busy" v-model="rerunEvidence" required /></label></fieldset><button :disabled="busy || !reason.trim()">提交人工裁定</button><button type="button" @click="reviewingSample = null">取消</button></form>
        <form v-if="canManage && trialReport" @submit.prevent="proposeDecision"><h3>提出共同决策</h3><label>决定<select :disabled="busy" v-model="trialDecision" required><option value="">选择决定</option><option value="continue" :disabled="trialReport.readiness === 'BLOCKED'">继续试用</option><option value="pause">暂停</option><option value="expand" :disabled="trialReport.readiness !== 'READY'">扩大试用</option></select></label><button :disabled="busy || !reason.trim()">保存待共同签署的提案</button></form><article v-for="decision in decisions" :key="decision.id"><h3>{{ decision.decision }} · {{ label(decision.status) }}</h3><p>{{ decision.reason }}</p><ul><li v-for="role in decisionRoles" :key="role">{{ roleLabel(role) }}：{{ decision.signatures[role] || '等待责任人签署' }} <button v-if="canManage && decision.status === 'WAITING_HUMAN' && !decision.signatures[role] && selectedWindow.decision_owners[role] === currentUserId" :disabled="busy || !reason.trim()" @click="signDecision(decision, role)">签署此责任项</button></li></ul></article>
      </section>
    </template>
  </section>
</template>
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { Project } from '@/api/lingdoc/workspace'
import { listChapters } from '@/api/lingdoc/workspace'
import * as runtimeApi from '@/api/lingdoc/runtimeControl'
import * as trialApi from '@/api/lingdoc/trialWindow'
import { useAuthStore } from '@/stores/auth'
import { projectRuntimeMetrics, expandRuntimeCounts } from './runtimeMetrics'

// This panel owns the G6 operations surface: running observations (pause /
// resume, event chain, metrics, alerts) and the real trial window.  It used
// to be the third and fourth tab of the change-and-impact panel; the split
// gives the on-call operator a door of its own instead of a tab behind
// another panel.  The behaviour is the one that was there: the same guards,
// the same idempotency keys and the same honest wording about what the
// server did not measure.
const props = withDefaults(defineProps<{ project: Project; readonly?: boolean; currentUserId?: string }>(), { readonly: true, currentUserId: '' })
const emit = defineEmits<{ close: []; changed: []; dirty: [boolean] }>()
const auth = useAuthStore()
const currentIdentity = computed(() => `${auth.effectiveTenantId}:${props.currentUserId}`)
const tabs = [{ id: 'runtime', label: '运行与恢复' }, { id: 'trial', label: '真实试用' }]
const tab = ref('runtime'), error = ref(''), message = ref(''), busy = ref(false), loading = ref(false), reason = ref('')
const controls = ref<runtimeApi.RuntimeControl[]>([]), metrics = ref<runtimeApi.RuntimeMetrics | null>(null), events = ref<runtimeApi.RuntimeEvent[]>([]), alerts = ref<runtimeApi.RuntimeAlert[]>([])
// The server reports raw counts keyed by resource category and status; the
// on-call view needs them grouped by the G6 minimum metric set.  The grouping
// lives in runtimeMetrics.ts so the wording has a single owner, and a metric
// the server never measures reads as "not observed" instead of zero.
const runtimeMetricItems = computed(() => projectRuntimeMetrics(metrics.value?.counts))
const runtimeCountRows = computed(() => expandRuntimeCounts(metrics.value?.counts))
const windows = ref<trialApi.TrialWindow[]>([]), selectedWindow = ref<trialApi.TrialWindow | null>(null), samples = ref<trialApi.TrialSample[]>([]), trialReport = ref<trialApi.TrialReport | null>(null), decisions = ref<trialApi.TrialDecision[]>([]), reviewingSample = ref<trialApi.TrialSample | null>(null)
const activeMembers = computed(() => props.project.members.filter(member => !member.status || member.status === 'active'))
const ownMember = computed(() => activeMembers.value.find(member => member.user_id === props.currentUserId))
const canWrite = computed(() => !props.readonly)
const canOwner = computed(() => canWrite.value && ownMember.value?.role === 'owner')
const canManage = computed(() => canOwner.value || canWrite.value && ownMember.value?.governance_role === 'admin')
const recoverySnapshot = ref(''), recoveryExport = ref(''), correlation = ref('')
const alertSignal = ref(''), alertResponsible = ref(''), alertAction = ref<runtimeApi.RuntimeAlertRule['action']>('notify')
const runtimeScopeKind = ref<runtimeApi.RuntimeScope['kind']>('project'), runtimeModeScope = ref('')
const runtimeReadReady = ref(false), evidenceLoading = ref(false), evidenceReadError = ref('')
const evidenceOptions = ref<trialApi.TrialEvidenceOption[]>([]), releaseOptions = ref<trialApi.TrialRelease[]>([]), exportOptions = ref<trialApi.TrialExport[]>([])
const selectedEvidenceKey = ref('')
let evidenceRequest = 0
const runtimeScope = computed<runtimeApi.RuntimeScope>(() => ({ kind: runtimeScopeKind.value, value: ({ project: props.project.id, tenant: String(auth.effectiveTenantId || ''), template_version: props.project.template_version || '', ruleset_hash: props.project.template_copy?.ruleset_hash || '', runtime_mode: runtimeModeScope.value } as Record<runtimeApi.RuntimeScope['kind'], string>)[runtimeScopeKind.value] }))
const selectedRuntimeControl = computed(() => controls.value.find(control => control.scope.kind === runtimeScope.value.kind && control.scope.value === runtimeScope.value.value))
const recoveryExports = computed(() => exportOptions.value.filter(item => item.status === 'verified' && (!recoverySnapshot.value || item.snapshot_id === recoverySnapshot.value)))
const alertSeverity = ref<runtimeApi.RuntimeAlertRule['severity'] | ''>('')
const metricNames = [{name:'manual_effort_minutes',label:'人工投入分钟 / 实际样本数'}, {name:'source_support_rate',label:'有来源支持项 / 实际核查项'}, {name:'missed_edits',label:'漏改项 / 实际核查范围'}, {name:'rule_false_positive_rate',label:'规则误报项 / 实际规则发现'}, {name:'model_false_positive_rate',label:'模型误报项 / 实际模型发现'}, {name:'unconfirmed_items',label:'未确认项 / 实际核查范围'}, {name:'export_loss_items',label:'导出丢失项 / 实际核查范围'}]
const processNames = [{name:'authorization',label:'实际授权检查'}, {name:'main_path',label:'实际主流程'}, {name:'failure_path',label:'失败路径'}, {name:'manual_review',label:'人工复盘'}, {name:'repair_or_rollback',label:'修复或已执行回退'}, {name:'key_scenario_rerun',label:'关键场景复跑'}]
const freshSampleForm = () => ({ sampleVersion:'', fixtureDigest:'', permissionSnapshot:'', inputHash:'', outputHash:'', executedAt:'', runtimeMode:'' as '' | trialApi.TrialSample['runtime_mode'], contextRevision:null as number | null, templateVersion:'', rulesetHash:'', correlationId:'', severity:'' as trialApi.TrialSample['severity'], runtimeEventIds:[] as string[], dependencies:{} as Record<string,string>, process:{} as Record<string,string>, metrics:metricNames.map(metric=>({...metric, measured:false, numerator:'', denominator:'', version:''})) })
const sampleForm = ref(freshSampleForm())
const sampleFacts = ref<TrialEvidenceFacts | null>(null)
const sampleOwnershipConfirmed = ref(false), sampleFactsIdentity = ref('')
const sampleEventOptions = computed(() => events.value.filter(event => event.origin === 'runtime' && event.correlation_id === sampleForm.value.correlationId))
interface TrialEvidenceFacts { reference:string; fingerprint:string; projectId:string; contextRevision:number|null; templateVersion:string; rulesetHash:string; runtimeMode:''|trialApi.TrialSample['runtime_mode']; executedAt:string; correlationId:string; fixtureDigest:string; inputHash:string; outputHash:string; dependencies:Record<string,string> }
const decisionRoles = ['product', 'qa', 'safety'] as const
const decisionOwners = ref<Record<typeof decisionRoles[number], string>>({ product: '', qa: '', safety: '' }), includedUsers = ref<string[]>([])
const trialStart = ref(''), trialEnd = ref(''), targetSamples = ref(10), minimumSamples = ref(10), trialSupport = ref(''), trialRedaction = ref(''), trialExit = ref('')
const reviewResult = ref<trialApi.TrialSample['quality_result'] | ''>(''), humanDecision = ref<trialApi.TrialReviewInput['human_decision'] | ''>(''), wordEvidence = ref(''), repairOwner = ref(''), repairVersion = ref(''), rerunEvidence = ref(''), trialDecision = ref<trialApi.TrialDecision['decision'] | ''>('')
const operationKeys = new Map<string, { payload: string; key: string }>()
let epoch = 0
// The wording of a state word is the same one the change-and-impact panel
// uses, so an operator sees one vocabulary across both panels.  The map is
// copied verbatim: the split must not change a single label.
function label(state: string) { return ({ draft: '草稿', assessed: '已评估', applied: '已应用', stabilized: '已稳定', satisfied: '目标已满足', rejected: '已驳回', stale: '已陈旧', confirmed: '已确认', suggested: '建议，等待人工判断', DRAFT: '草稿', LOCKED: '已锁定', PAUSED: '已暂停', CLOSED: '已关闭', OPEN: '待处理', IN_PROGRESS: '处理中', WAITING_USER: '等待人工', DONE: '已完成', WAITING_HUMAN: '等待责任签署', DECIDED: '已共同决定', DISMISSED: '已接受不处理', STALE: '已失效' } as Record<string, string>)[state] || state }
function roleLabel(role: typeof decisionRoles[number]) { return { product: '产品', qa: 'QA', safety: '安全' }[role] }
function errorText(err: unknown) { const item = err as { message?: string; error?: { message?: string } }; return item.error?.message || item.message || '读取或提交失败，请检查当前权限和版本后重试。' }
async function mutate<T>(slot: string, payload: unknown, call: (key: string) => Promise<T>, success: string, preserveReason = false): Promise<T | undefined> {
  if (props.readonly || busy.value) return
  const projectId = props.project.id, requestEpoch=epoch, identity=currentIdentity.value, submittedReason=reason.value, stamp = JSON.stringify(payload)
  let entry = operationKeys.get(slot)
  if (!entry || entry.payload !== stamp) { entry = { payload: stamp, key: `lingdoc-${crypto.randomUUID()}` }; operationKeys.set(slot, entry) }
  busy.value = true; error.value = ''; message.value = ''
  try { const result = await call(entry.key); if (props.project.id !== projectId || currentIdentity.value!==identity) return; emit('changed');if(epoch!==requestEpoch)return;operationKeys.delete(slot);if(!preserveReason&&reason.value===submittedReason)reason.value='';message.value = success; await refresh(); return result }
  catch (err) { if (props.project.id === projectId && epoch===requestEpoch && currentIdentity.value===identity) error.value = errorText(err) }
  finally { busy.value = false }
}
async function refresh() {
  const stamp = ++epoch, id = props.project.id, identity=currentIdentity.value
  if(tab.value==='runtime')runtimeReadReady.value=false
  loading.value = true; error.value = ''
  try {
    if (tab.value === 'runtime') { const result = await Promise.all([runtimeApi.listRuntimeControls(id), runtimeApi.getRuntimeMetrics(id), runtimeApi.listRuntimeEvents(id, correlation.value), runtimeApi.listRuntimeAlerts(id)]); if (epoch === stamp && identity===currentIdentity.value) { controls.value = result[0].data || []; metrics.value = result[1].data; events.value = result[2].data || []; alerts.value = result[3].data || [];runtimeReadReady.value=true;await loadEvidenceCatalog() } }
    if (tab.value === 'trial') { const result = await trialApi.listTrialWindows(id); if (epoch === stamp && identity===currentIdentity.value) { windows.value = result.data || []; const next = windows.value.find(window => window.id === selectedWindow.value?.id); selectedWindow.value = next || null; if (next) await selectWindow(next);await loadEvidenceCatalog() } }
  } catch (err) { if (epoch === stamp) error.value = errorText(err) }
  finally { if (epoch === stamp) loading.value = false }
}
async function pauseRuntime() {
 if(!canManage.value||!runtimeReadReady.value||!runtimeScope.value.value||runtimeScopeKind.value==='runtime_mode'&&!['mock','real_api_fake_model','real'].includes(runtimeScope.value.value)||!reason.value.trim()){error.value='请先读取当前运行状态、选择范围并填写原因。';return}
 const input:runtimeApi.RuntimeControlInput={expected_version:selectedRuntimeControl.value?.version||0,scope:{...runtimeScope.value},action:'pause',reason:reason.value}
 await mutate(`pause-${input.scope.kind}-${input.scope.value}`,input,key=>runtimeApi.controlRuntime(props.project.id,input,key),'服务端暂停结果已记录；权限以当前服务端结果为准。')
}
async function resumeRuntime(control:runtimeApi.RuntimeControl){
 if(!canManage.value||!runtimeReadReady.value||!reason.value.trim())return
 const projectId=props.project.id,identity=currentIdentity.value,stamp=epoch,snapshotId=recoverySnapshot.value,exportId=recoveryExport.value
 if(!releaseOptions.value.some(item=>item.id===snapshotId)||!exportOptions.value.some(item=>item.id===exportId&&item.snapshot_id===snapshotId)){error.value='请选择此课题已有的快照和对应导出。';return}
 try{
  const [snapshot,artifact]=await Promise.all([trialApi.getTrialRelease(projectId,snapshotId),trialApi.getTrialExport(projectId,exportId)])
  if(projectId!==props.project.id||identity!==currentIdentity.value||stamp!==epoch)return
  if(!snapshot.data||snapshot.data.id!==snapshotId||snapshot.data.project_id!==projectId||snapshot.data.is_current!==true||snapshot.data.check.status!=='passed'||!artifact.data||artifact.data.id!==exportId||artifact.data.project_id!==projectId||artifact.data.snapshot_id!==snapshotId||artifact.data.status!=='verified'||!artifact.data.file_sha256)throw new Error('复验引用已陈旧、跨课题或尚未验证；不能恢复。')
  const current=controls.value.find(item=>item.id===control.id)
  if(!current||current.version!==control.version||current.status!=='PAUSED')throw new Error('运行控制版本已改变，请重新读取。')
  const input:runtimeApi.RuntimeControlInput={expected_version:control.version,scope:{...control.scope},action:'resume',reason:reason.value,snapshot_id:snapshotId,export_id:exportId}
  const result=await mutate(`resume-${control.id}`,input,key=>runtimeApi.controlRuntime(projectId,input,key),'服务端已完成权限、当前性与下载复验后恢复。')
  if(result){recoverySnapshot.value='';recoveryExport.value=''}
 }catch(err){if(projectId===props.project.id&&identity===currentIdentity.value&&stamp===epoch)error.value=errorText(err)}
}
async function addAlert(){
 if(!canManage.value||!alertSeverity.value||!safeTrialRef(alertSignal.value)||!activeMembers.value.some(member=>member.user_id===alertResponsible.value)||!reason.value.trim()){error.value='请选择当前课题故障级别、责任人和有效信号，并填写原因。';return}
 const input:runtimeApi.RuntimeAlertRule={signal:alertSignal.value,action:alertAction.value,responsible_id:alertResponsible.value,severity:alertSeverity.value,scope:{kind:'project',value:props.project.id}}
 const result=await mutate('alert',input,key=>runtimeApi.saveRuntimeAlertRule(props.project.id,input,key),input.action==='rollback'||input.action==='retry'?'处置申请规则已保存；尚未执行回退或重试。':'故障信号已绑定处置与责任人。')
 if(result){alertSignal.value='';alertResponsible.value='';alertAction.value='notify';alertSeverity.value=''}
}
async function loadEvents() { const projectId=props.project.id,identity=currentIdentity.value,stamp=epoch,requestedCorrelation=correlation.value;try { const result=await runtimeApi.listRuntimeEvents(projectId,requestedCorrelation);if(props.project.id===projectId&&currentIdentity.value===identity&&epoch===stamp&&correlation.value===requestedCorrelation)events.value=result.data||[] } catch(err){if(props.project.id===projectId&&currentIdentity.value===identity&&epoch===stamp)error.value=errorText(err)} }
async function addWindow() { const start = new Date(trialStart.value), end = new Date(trialEnd.value); if (!Number.isFinite(start.getTime()) || !Number.isFinite(end.getTime()) || end <= start) { error.value = '请填写有效的试用起止时间。'; return }; const input: trialApi.TrialWindowInput = { starts_at: start.toISOString(), ends_at: end.toISOString(), included_project_ids: [props.project.id], included_user_ids: [...includedUsers.value], decision_owners: { ...decisionOwners.value }, target_samples: targetSamples.value, minimum_samples: minimumSamples.value, template_version: props.project.template_version, ruleset_hash: props.project.template_copy?.ruleset_hash || '', runtime_modes: ['real'], human_support: trialSupport.value, redaction: trialRedaction.value, exit_conditions: trialExit.value.split(',').map(value => value.trim()).filter(Boolean) }; const result=await mutate('window', input, key => trialApi.createTrialWindow(props.project.id, input, key), '窗口草稿已保存，锁定后才能纳入样本。');if(result)resetWindowForm() }
async function windowAction(window: trialApi.TrialWindow, action: 'lock' | 'pause' | 'close') { const input = { expected_version: window.version, action, reason: reason.value }; await mutate(`window-${window.id}`, input, key => trialApi.actOnTrialWindow(props.project.id, window.id, input, key), '窗口状态已更新，历史仍保留。') }
async function selectWindow(window: trialApi.TrialWindow) { const projectId = props.project.id, requestEpoch=epoch,identity=currentIdentity.value; if(selectedWindow.value?.id!==window.id){sampleFacts.value=null;sampleFactsIdentity.value='';sampleOwnershipConfirmed.value=false;reviewingSample.value=null}selectedWindow.value = window; try { const result = await Promise.all([trialApi.listTrialSamples(projectId, window.id), trialApi.getTrialReport(projectId, window.id), trialApi.listTrialDecisions(projectId, window.id)]); if (props.project.id === projectId && identity===currentIdentity.value && epoch===requestEpoch && selectedWindow.value?.id === window.id) { samples.value = result[0].data || []; trialReport.value = result[1].data; decisions.value = result[2].data || [] } } catch (err) { if (props.project.id === projectId && identity===currentIdentity.value && epoch===requestEpoch) error.value = errorText(err) } }
async function submitSample(){
 const window=selectedWindow.value,item=evidenceOptions.value.find(option=>evidenceKey(option)===selectedEvidenceKey.value),original=sampleFacts.value
 if(!canWrite.value||!window||window.status!=='LOCKED'||!item||!original||!sampleOwnershipConfirmed.value||sampleFactsIdentity.value!==currentIdentity.value){error.value='请选择已读取的真实记录，并明确确认本次执行与补录信息。';return}
 const projectId=props.project.id,identity=currentIdentity.value,stamp=epoch,formStamp=JSON.stringify(sampleForm.value)
 try{
  const actual=await readTrialEvidence(item)
  if(projectId!==props.project.id||identity!==currentIdentity.value||stamp!==epoch||selectedWindow.value?.id!==window.id||selectedWindow.value.version!==window.version||selectedWindow.value.status!=='LOCKED')return
  if(actual.fingerprint!==original.fingerprint||actual.reference!==original.reference)throw new Error('回执版本或内容已改变；表单已保留，请重新选择并核对实际记录。')
  if(formStamp!==JSON.stringify(sampleForm.value))throw new Error('读取期间表单已修改，已保留输入；请重新提交。')
  const form=sampleForm.value,executed=new Date(form.executedAt)
  if(!Number.isFinite(executed.getTime())||executed.getTime()>Date.now()||executed<new Date(window.starts_at)||executed>new Date(window.ends_at))throw new Error('执行时间须来自实际记录，并处于该窗口内。')
  if(!['mock','real_api_fake_model','real'].includes(form.runtimeMode)||actual.runtimeMode&&form.runtimeMode!==actual.runtimeMode)throw new Error('运行模式与真实回执不一致，不能声明为 real。')
  if(form.contextRevision!==props.project.current_context_revision||form.templateVersion!==props.project.template_version||form.rulesetHash!==props.project.template_copy?.ruleset_hash||actual.contextRevision!==null&&form.contextRevision!==actual.contextRevision)throw new Error('样本与当前课题的版本或基线不一致。')
  if(!window.included_user_ids.includes(props.currentUserId)||!window.included_project_ids.includes(projectId)||window.template_version!==form.templateVersion||window.ruleset_hash!==form.rulesetHash)throw new Error('当前身份、课题或版本不在该试用窗口内。')
  for(const value of [form.sampleVersion,form.fixtureDigest,form.permissionSnapshot,form.inputHash,form.outputHash,form.correlationId])if(!safeTrialRef(value))throw new Error('请填写实际脱敏记录编号、摘要、权限快照及关联编号；缺失项不能补造。')
  const process=Object.fromEntries(Object.entries(form.process).filter(([,value])=>value.trim()))
  if(Object.values(process).some(value=>!safeTrialRef(value)))throw new Error('过程证据必须是实际脱敏引用编号。')
  const metrics:trialApi.MetricObservation[]=form.metrics.filter(metric=>metric.measured).map(metric=>{
   const numerator=Number(metric.numerator),denominator=Number(metric.denominator)
   if(!metric.numerator.trim()||!metric.denominator.trim()||!Number.isFinite(numerator)||numerator<0||!Number.isFinite(denominator)||denominator<=0||!safeTrialRef(metric.version))throw new Error('已测量指标需要实际分子、正分母和测量版本。')
   return {name:metric.name,numerator,denominator,missing:false,version:metric.version}
  })
  if(form.runtimeEventIds.length){
   const response=await runtimeApi.listRuntimeEvents(projectId,form.correlationId)
   if(projectId!==props.project.id||identity!==currentIdentity.value||stamp!==epoch||formStamp!==JSON.stringify(sampleForm.value))return
   const rows=response.data||[]
   for(const id of form.runtimeEventIds){const event=rows.find(row=>row.id===id);if(!event||event.origin!=='runtime'||event.correlation_id!==form.correlationId||event.runtime_mode!==form.runtimeMode||event.context_revision!==form.contextRevision||event.template_version!==form.templateVersion||event.ruleset_hash!==form.rulesetHash)throw new Error('实际运行事件缺失或与样本模式、版本和基线不一致。')}
  }
  const input:trialApi.TrialSampleInput={sample_version:form.sampleVersion,fixture_digest:form.fixtureDigest,project_id:projectId,template_version:form.templateVersion,ruleset_hash:form.rulesetHash,context_revision:form.contextRevision,runtime_mode:form.runtimeMode as trialApi.TrialSample['runtime_mode'],dependency_versions:{...actual.dependencies},permission_snapshot:form.permissionSnapshot,input_hash:form.inputHash,output_hash:form.outputHash,executed_at:executed.toISOString(),correlation_id:form.correlationId,runtime_event_ids:[...form.runtimeEventIds],process_evidence:process,severity:form.severity,metrics}
  const result=await mutate(`sample-${window.id}`,input,key=>trialApi.recordTrialSample(projectId,window.id,input,key),'样本已保存为 NOT RUN，补录信息与质量尚未人工审核。')
  if(result&&formStamp===JSON.stringify(sampleForm.value))resetSampleForm()
 }catch(err){if(projectId===props.project.id&&identity===currentIdentity.value&&stamp===epoch)error.value=errorText(err)}
}
function openReview(sample: trialApi.TrialSample) { reviewingSample.value = sample; reviewResult.value = ''; humanDecision.value = ''; wordEvidence.value = ''; repairOwner.value = ''; repairVersion.value = ''; rerunEvidence.value = '' }
async function submitReview() { if (!selectedWindow.value || !reviewingSample.value || !reviewResult.value || !humanDecision.value) return; const windowId = selectedWindow.value.id, sampleId = reviewingSample.value.id; const input: trialApi.TrialReviewInput = { expected_version: reviewingSample.value.version, quality_result: reviewResult.value, human_decision: humanDecision.value, reason: reason.value, word_roundtrip_evidence: wordEvidence.value, repair_owner_id: repairOwner.value, repair_version: repairVersion.value, rerun_evidence: rerunEvidence.value }; const result = await mutate('review', input, key => trialApi.reviewTrialSample(props.project.id, windowId, sampleId, input, key), '真实人工裁定已记录。'); if (result) resetReviewForm() }
async function proposeDecision() { if (!selectedWindow.value || !trialReport.value || !trialDecision.value) return; const windowId = selectedWindow.value.id, input = { decision: trialDecision.value, reason: reason.value, report_hash: trialReport.value.input_hash }; const result=await mutate('decision', input, key => trialApi.createTrialDecision(props.project.id, windowId, input, key), '决策提案等待三责任人共同签署。');if(result)trialDecision.value='' }
async function signDecision(decision: trialApi.TrialDecision, responsibility: typeof decisionRoles[number]) { const input = { expected_version: decision.version, responsibility, reason: reason.value }; await mutate(`sign-${decision.id}-${responsibility}`, input, key => trialApi.signTrialDecision(props.project.id, decision.id, input, key), '本责任项签署已记录。') }

function safeTrialRef(value:string){return /^[a-zA-Z0-9_.:/-]{1,128}$/.test(value)&&!value.toLowerCase().includes('bearer')}
function evidenceKey(item:trialApi.TrialEvidenceOption){return `${item.kind}/${item.id}`}
function localDateTime(value:string){const date=new Date(value);if(!Number.isFinite(date.getTime()))return '';const local=new Date(date.getTime()-date.getTimezoneOffset()*60000);return local.toISOString().slice(0,19)}
async function loadEvidenceCatalog(){
 const projectId=props.project.id,identity=currentIdentity.value,stamp=epoch,request=++evidenceRequest
 evidenceLoading.value=true;evidenceReadError.value=''
 try{
  const results=await Promise.allSettled([trialApi.listTrialResearchRuns(projectId),trialApi.listTrialReleases(projectId),trialApi.listTrialExports(projectId),listChapters(projectId)])
  if(projectId!==props.project.id||identity!==currentIdentity.value||stamp!==epoch||request!==evidenceRequest)return
  const options:trialApi.TrialEvidenceOption[]=[],failures:string[]=[]
  const runs=results[0],releases=results[1],exports=results[2],chapterRows=results[3]
  if(runs.status==='fulfilled'&&Array.isArray(runs.value.data)){for(const run of runs.value.data)if(run.project_id===projectId)options.push({kind:'research_run',id:run.id,label:`调研 ${run.id} · ${run.status} · ${run.run_mode}`})}
  else failures.push(runs.status==='rejected'?`调研记录：${errorText(runs.reason)}`:'调研返回格式未知，未列为证据')
  if(releases.status==='fulfilled'&&Array.isArray(releases.value.data?.items)){releaseOptions.value=releases.value.data.items.filter(item=>item.project_id===projectId);for(const item of releaseOptions.value)options.push({kind:'snapshot',id:item.id,label:`快照 ${item.id} · ${item.check.status}`});if(releases.value.data.truncated)failures.push('快照历史已截断，仅显示实际返回部分')}
  else {releaseOptions.value=[];failures.push(releases.status==='rejected'?`快照：${errorText(releases.reason)}`:'快照返回格式未知，未列为证据')}
  if(exports.status==='fulfilled'&&Array.isArray(exports.value.data?.items)){exportOptions.value=exports.value.data.items.filter(item=>item.project_id===projectId);for(const item of exportOptions.value)options.push({kind:'export',id:item.id,label:`导出 ${item.id} · ${item.status}`});if(exports.value.data.truncated)failures.push('导出历史已截断，仅显示实际返回部分')}
  else {exportOptions.value=[];failures.push(exports.status==='rejected'?`导出：${errorText(exports.reason)}`:'导出返回格式未知，未列为证据')}
  if(chapterRows.status==='fulfilled'&&Array.isArray(chapterRows.value.data)){
   const generation=await Promise.allSettled(chapterRows.value.data.map(chapter=>trialApi.listTrialGenerationCandidates(projectId,chapter.id)))
   if(projectId!==props.project.id||identity!==currentIdentity.value||stamp!==epoch||request!==evidenceRequest)return
   const seen=new Set<string>()
   generation.forEach((result,index)=>{if(result.status==='fulfilled'&&Array.isArray(result.value.data)){for(const candidate of result.value.data)if(candidate.run_id&&!seen.has(candidate.run_id)){seen.add(candidate.run_id);options.push({kind:'generation_run',id:candidate.run_id,chapter_id:chapterRows.value.data[index]!.id,label:`生成 ${candidate.run_id} · ${candidate.validity}`})}}else failures.push('部分生成记录读取失败，未列为证据')})
  }else failures.push('章节或生成记录未能读取')
  evidenceOptions.value=options;evidenceReadError.value=failures.join('；')
 }catch(err){if(projectId===props.project.id&&identity===currentIdentity.value&&request===evidenceRequest)evidenceReadError.value=errorText(err)}
 finally{if(request===evidenceRequest)evidenceLoading.value=false}
}
async function readTrialRelease(projectId:string,id:string){
 const response=await trialApi.getTrialRelease(projectId,id),snapshot=response.data
 if(!snapshot||snapshot.id!==id||snapshot.project_id!==projectId||snapshot.frozen_input?.project_id!==projectId||snapshot.is_current!==true)throw new Error('快照未知、跨课题或已是历史版本，请保留输入并重新选择。')
 const frozen=snapshot.frozen_input
 if(!frozen.template||frozen.template.version!==props.project.template_version||(frozen.template_copy_ruleset_hash||frozen.template.ruleset_hash)!==props.project.template_copy?.ruleset_hash||frozen.spec_revision!==props.project.spec_revision)throw new Error('快照模板、规则或研究条件与当前版本不一致。')
 return snapshot
}
async function readTrialEvidence(item:trialApi.TrialEvidenceOption):Promise<TrialEvidenceFacts>{
 const projectId=props.project.id,empty:TrialEvidenceFacts={reference:evidenceKey(item),fingerprint:'',projectId,contextRevision:null,templateVersion:'',rulesetHash:'',runtimeMode:'',executedAt:'',correlationId:'',fixtureDigest:'',inputHash:'',outputHash:'',dependencies:{}}
 if(item.kind==='research_run'){
  const run=(await trialApi.getTrialResearchRun(projectId,item.id)).data
  if(!run||run.id!==item.id||run.project_id!==projectId||!Number.isInteger(run.version)||run.version<1||!['completed','failed','cancelled','paused','stale'].includes(run.status)||!['mock','real_api_fake_model','real'].includes(run.run_mode))throw new Error('调研回执未知、尚未结束或模式未声明。')
  if(run.created_by!==props.currentUserId||run.basis.context_revision!==props.project.current_context_revision||run.basis.spec_revision!==props.project.spec_revision)throw new Error('调研执行者或版本与当前身份、基线不一致。')
  empty.fingerprint=JSON.stringify(run);empty.contextRevision=run.basis.context_revision;empty.templateVersion=props.project.template_version;empty.rulesetHash=props.project.template_copy?.ruleset_hash||'';empty.runtimeMode=run.run_mode;empty.executedAt=run.finished_at||run.started_at;empty.correlationId=run.correlation_id
  empty.dependencies[`research-run/${run.id}`]=String(run.version);empty.dependencies[`research-task/${run.task_id}`]=String(run.task_version)
  for(const asset of run.basis.asset_versions)empty.dependencies[`asset/${asset.asset_id}`]=String(asset.asset_revision)
 }else if(item.kind==='generation_run'){
  const [response,candidateResponse]=await Promise.all([trialApi.getTrialGenerationRun(projectId,item.id),trialApi.getTrialGenerationCandidate(projectId,item.id)])
  const run=response.data,candidate=candidateResponse.data
  if(!run||run.id!==item.id||run.project_id!==projectId||run.status!=='succeeded'||run.chapter_id!==item.chapter_id||!candidate||candidate.run_id!==item.id||candidate.id!==run.candidate_id||candidate.project_id!==projectId||candidate.chapter_id!==item.chapter_id||candidate.validity!=='fresh')throw new Error('生成回执或候选未知、尚未成功或已陈旧。')
  if(candidate.basis.spec_revision!==props.project.spec_revision||candidate.basis.template_version!==props.project.template_version||candidate.basis.ruleset_hash!==props.project.template_copy?.ruleset_hash||candidate.basis.context_revision!==undefined&&candidate.basis.context_revision!==props.project.current_context_revision)throw new Error('生成候选的模板、规则或基线已改变。')
  empty.fingerprint=JSON.stringify([run,candidate]);empty.contextRevision=candidate.basis.context_revision??null;empty.templateVersion=candidate.basis.template_version;empty.rulesetHash=candidate.basis.ruleset_hash;empty.executedAt=candidate.created_at||''
  empty.dependencies['project-spec']=String(candidate.basis.spec_revision)
  if(candidate.basis.chapter_version_id)empty.dependencies[`chapter/${candidate.chapter_id}`]=candidate.basis.chapter_version_id
  for(const asset of candidate.basis.asset_versions)empty.dependencies[`asset/${asset.asset_id}`]=String(asset.asset_revision)
 }else{
  const artifact=item.kind==='export'?(await trialApi.getTrialExport(projectId,item.id)).data:null
  if(item.kind==='export'&&(!artifact||artifact.id!==item.id||artifact.project_id!==projectId||!['verified','failed'].includes(artifact.status)))throw new Error('导出回执未知、跨课题或尚未完成。')
  const snapshot=await readTrialRelease(projectId,artifact?.snapshot_id||item.id),frozen=snapshot.frozen_input
  empty.fingerprint=JSON.stringify([snapshot,artifact]);empty.templateVersion=frozen.template!.version;empty.rulesetHash=frozen.template_copy_ruleset_hash||frozen.template!.ruleset_hash;empty.fixtureDigest=snapshot.snapshot_digest;empty.inputHash=snapshot.snapshot_digest;empty.outputHash=artifact?.file_sha256||'';empty.executedAt=artifact?artifact.created_at||'':snapshot.created_at
  empty.dependencies[`snapshot/${snapshot.id}`]=snapshot.snapshot_digest
  if(frozen.template_copy_version!==undefined)empty.dependencies['template-copy']=String(frozen.template_copy_version)
  for(const asset of frozen.asset_versions||[])empty.dependencies[`asset/${asset.asset_id}`]=String(asset.asset_revision)
  if(artifact?.file_sha256)empty.dependencies[`export/${artifact.id}`]=artifact.file_sha256
 }
 return empty
}
async function selectTrialEvidence(){
 const projectId=props.project.id,identity=currentIdentity.value,stamp=epoch,request=++evidenceRequest,key=selectedEvidenceKey.value,item=evidenceOptions.value.find(option=>evidenceKey(option)===key),formStamp=JSON.stringify(sampleForm.value)
 sampleFacts.value=null;sampleFactsIdentity.value='';sampleOwnershipConfirmed.value=false
 if(!item){error.value='请选择实际返回的运行、快照或导出记录。';return}
 evidenceLoading.value=true
 try{
  const actual=await readTrialEvidence(item)
  if(projectId!==props.project.id||identity!==currentIdentity.value||stamp!==epoch||request!==evidenceRequest||selectedEvidenceKey.value!==key)return
  if(formStamp!==JSON.stringify(sampleForm.value)){error.value='读取期间表单已修改，已保留全部输入；请重新选择并核对回执。';return}
  sampleFacts.value=actual;sampleFactsIdentity.value=identity
  sampleForm.value={...sampleForm.value,fixtureDigest:actual.fixtureDigest,inputHash:actual.inputHash,outputHash:actual.outputHash,executedAt:localDateTime(actual.executedAt),runtimeMode:actual.runtimeMode,contextRevision:actual.contextRevision,templateVersion:actual.templateVersion,rulesetHash:actual.rulesetHash,correlationId:actual.correlationId,runtimeEventIds:[],dependencies:{...actual.dependencies},process:{...sampleForm.value.process,main_path:actual.reference}}
 }catch(err){if(projectId===props.project.id&&identity===currentIdentity.value&&request===evidenceRequest)error.value=errorText(err)}
 finally{if(request===evidenceRequest)evidenceLoading.value=false}
}
async function loadSampleEvents(){
 const projectId=props.project.id,identity=currentIdentity.value,stamp=epoch,correlationId=sampleForm.value.correlationId
 if(!safeTrialRef(correlationId)){error.value='请输入实际关联编号。';return}
 try{const response=await runtimeApi.listRuntimeEvents(projectId,correlationId);if(props.project.id===projectId&&identity===currentIdentity.value&&stamp===epoch&&correlationId===sampleForm.value.correlationId)events.value=response.data||[]}
 catch(err){if(props.project.id===projectId&&identity===currentIdentity.value&&stamp===epoch)error.value=errorText(err)}
}
function resetSampleForm(){selectedEvidenceKey.value='';sampleFacts.value=null;sampleFactsIdentity.value='';sampleOwnershipConfirmed.value=false;sampleForm.value=freshSampleForm()}
function resetWindowForm(){trialStart.value='';trialEnd.value='';targetSamples.value=10;minimumSamples.value=10;includedUsers.value=[];decisionOwners.value={product:'',qa:'',safety:''};trialSupport.value='';trialRedaction.value='';trialExit.value=''}
function resetReviewForm(){reviewingSample.value=null;reviewResult.value='';humanDecision.value='';wordEvidence.value='';repairOwner.value='';repairVersion.value='';rerunEvidence.value=''}
function resetAllForms(){recoverySnapshot.value='';recoveryExport.value='';alertSignal.value='';alertResponsible.value='';alertAction.value='notify';resetWindowForm();resetSampleForm();runtimeScopeKind.value='project';runtimeModeScope.value='';alertSeverity.value='';resetReviewForm();trialDecision.value='';reason.value=''}
const hasUnsavedChanges=computed(()=>Boolean(
 reason.value.trim()||recoverySnapshot.value||recoveryExport.value||alertSignal.value||alertResponsible.value||alertAction.value!=='notify'||trialStart.value||trialEnd.value||targetSamples.value!==10||minimumSamples.value!==10||includedUsers.value.length||Object.values(decisionOwners.value).some(Boolean)||trialSupport.value||trialRedaction.value||trialExit.value||selectedEvidenceKey.value||sampleOwnershipConfirmed.value||JSON.stringify(sampleForm.value)!==JSON.stringify(freshSampleForm())||runtimeScopeKind.value!=='project'||runtimeModeScope.value||alertSeverity.value||reviewingSample.value||reviewResult.value||humanDecision.value||wordEvidence.value||repairOwner.value||repairVersion.value||rerunEvidence.value||trialDecision.value
))
watch(hasUnsavedChanges,value=>emit('dirty',value),{immediate:true})
defineExpose({hasUnsavedChanges,busy})

watch(() => props.project.id, () => { epoch++; evidenceRequest++; runtimeReadReady.value=false; evidenceOptions.value=[];releaseOptions.value=[];exportOptions.value=[];evidenceReadError.value='';evidenceLoading.value=false;resetAllForms(); operationKeys.clear(); windows.value = []; selectedWindow.value = null; trialReport.value = null; samples.value = []; decisions.value = []; controls.value = []; events.value = []; metrics.value = null; reviewingSample.value = null; reason.value = ''; void refresh() }, { immediate: true })
watch(currentIdentity,()=>{epoch++;evidenceRequest++;runtimeReadReady.value=false;controls.value=[];events.value=[];alerts.value=[];metrics.value=null;windows.value=[];selectedWindow.value=null;samples.value=[];trialReport.value=null;decisions.value=[];reviewingSample.value=null;evidenceOptions.value=[];releaseOptions.value=[];exportOptions.value=[];sampleFacts.value=null;sampleFactsIdentity.value='';evidenceLoading.value=false;operationKeys.clear();error.value='当前身份已改变，已保留未提交输入；请重新读取并核对当前身份的证据。'})
watch(()=>props.project.current_context_revision,()=>{sampleFacts.value=null;sampleFactsIdentity.value='';runtimeReadReady.value=false})
watch(tab, () => { void refresh() })
</script>

<style scoped>
.ops-panel{max-width:1100px;margin:auto;padding:24px;background:#fbfcfa;color:#20352c;line-height:1.6}.ops-panel header,.actions,nav{display:flex;gap:10px;align-items:center;flex-wrap:wrap}.ops-panel header{justify-content:space-between;border-bottom:1px solid #dce4dd}.ops-panel h2{font-size:24px;margin:0}.ops-panel h3{font-size:17px;margin:0 0 10px}.ops-panel p{margin:8px 0}.ops-panel nav{margin:20px 0}.ops-panel button{border:1px solid #bfcec3;border-radius:7px;padding:8px 12px;background:#fff;color:inherit;cursor:pointer}.ops-panel button[aria-pressed=true]{background:#e8f0ea;border-color:#477058}.ops-panel button:disabled{opacity:.45;cursor:not-allowed}.ops-panel article,.ops-panel form,.ops-panel details{padding:18px;margin:16px 0;background:white;border:1px solid #dce4dd;border-radius:10px}.ops-panel label{display:flex;flex-direction:column;gap:5px;margin:0 0 12px;font-size:13px;color:#52645a}.ops-panel input,.ops-panel select,.ops-panel textarea{min-width:0;padding:9px;border:1px solid #cbd6ce;border-radius:6px;background:white;color:#20352c}.ops-panel textarea{min-height:80px;resize:vertical;font-family:inherit}.ops-panel pre{white-space:pre-wrap;overflow-wrap:anywhere;font-size:12px;background:#f4f7f3;padding:10px;border-radius:6px}.ops-panel input[type=checkbox]{align-self:flex-start}.ops-panel output{padding:10px;background:#f4f7f3;white-space:pre-wrap}.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(200px,1fr));gap:16px}.ops-panel fieldset{border:1px solid #dce4dd;border-radius:8px}.ops-panel table{width:100%;border-collapse:collapse;table-layout:fixed;overflow-wrap:anywhere;font-size:13px}.ops-panel th,.ops-panel td{padding:9px;text-align:left;vertical-align:top;border-bottom:1px solid #e7ece8}.ops-panel small{display:block;color:#708075}.notice,.error{padding:12px;border-radius:7px}.notice{background:#edf3ee}.error{background:#fcecea;color:#8b3025}.metrics article{display:flex;flex-direction:column;margin:0}.metrics strong{font-size:24px}.ops-panel :focus-visible{outline:2px solid #467158;outline-offset:3px}@media(max-width:640px){.ops-panel{padding:14px}.ops-panel table{font-size:11px}.ops-panel td,.ops-panel th{padding:6px}}
</style>
