<template>
  <section class="ops-panel" aria-label="变更与运行质量">
    <header><div><h2>变更与运行质量</h2><p>{{ project.name }} · 基线 {{ project.current_context_revision }}</p></div><button @click="$emit('close')">关闭</button></header>
    <p v-if="readonly" class="notice">当前仅可查看。业务操作仍由服务端校验当前权限、来源与版本。</p>
    <nav><button v-for="entry in tabs" :key="entry.id" :aria-pressed="tab === entry.id" @click="tab = entry.id">{{ entry.label }}</button><button :disabled="loading" @click="refresh">{{ loading ? '读取中…' : '重新读取' }}</button></nav>
    <p v-if="error" role="alert" class="error">{{ error }}</p><p v-if="message" role="status" class="notice">{{ message }}</p>
    <label v-if="canWrite">本次操作说明<textarea :disabled="busy" v-model="reason" maxlength="2000" placeholder="变更、依赖决定或任务处置的原因" /></label>
    <template v-if="tab === 'changes'">
      <form v-if="canWrite && project.status === 'active'" @submit.prevent="saveDraft"><h3>{{ editId ? '编辑拟议变更' : '起草研究条件变更' }}</h3><label>研究条件<select :disabled="busy" v-model="draftField"><option value="">选择字段</option><option v-for="(_, name) in project.spec" :key="name" :value="name">{{ name }}</option></select></label><label>当前值<output>{{ project.spec[draftField] || '尚未选择' }}</output></label><label>拟议值<textarea :disabled="busy" v-model="draftValue" :required="!!draftField" /></label><fieldset><legend>语义修改</legend><p>先加入要修改的对象，再保存整份草稿。对象编号、版本和原值由服务端核对。</p>
 <label>对象类型<select :disabled="busy" v-model="semanticKind" @change="resetSemanticEditor"><option value="ResearchClaim">研究主张</option><option value="EvidenceCard">已确认的证据关系</option><option value="ProjectAssetScope">资料使用范围</option><option value="RuleBinding">独立规则绑定</option><option value="RouteNode">技术路线节点</option></select></label>
 <label>已有对象<select :disabled="busy" v-model="semanticTarget" @change="selectSemanticTarget"><option value="">选择对象，或在下方填写新路线编号</option><option v-for="item in semanticOptions" :key="item.id" :value="item.id">{{ item.label }} · {{ item.id }} @ {{ item.version }}</option></select></label>
 <div class="grid"><label>对象编号<input :disabled="busy" v-model="semanticTarget" /></label><label>当前版本<input :disabled="busy" v-model.number="semanticVersion" type="number" min="0" /></label></div>
 <template v-if="semanticKind==='ResearchClaim'"><label>主张内容<textarea :disabled="busy" v-model="claimText" /></label><label>适用范围<input :disabled="busy" v-model="claimScope" /></label><label>状态<select :disabled="busy" v-model="claimStatus"><option value="active">当前有效</option><option value="disputed">存在争议</option><option value="superseded">已被替代</option></select></label><label>解释<textarea :disabled="busy" v-model="claimExplanation" /></label><label><input :disabled="busy" v-model="claimConsent" type="checkbox" />我确认提交这些语义修改；主张仍须按证据复核。</label></template>
 <template v-if="semanticKind==='EvidenceCard'"><p>编辑主张与原文锚点的精确编号和版本、关系、范围及限制。保存仅建立变更草稿；Owner 应用后新卡为待确认候选，旧版核对记录保留。</p><label>拟议证据关系（JSON）<textarea :disabled="busy" v-model="cardEditor" /></label><label><input :disabled="busy" v-model="cardConsent" type="checkbox" />我确认提交以上证据语义修改；不沿用旧版来源确认。</label></template>
 <template v-if="semanticKind==='ProjectAssetScope'"><label>范围<select :disabled="busy" v-model="assetScope"><option value="personal">个人</option><option value="project">课题</option><option value="team">团队</option></select></label><label>明确使用目的<input :disabled="busy" v-model="assetPurpose" /></label><label>收窄到指定成员<select :disabled="busy" v-model="assetUsers" multiple><option v-for="member in activeMembers" :key="member.user_id">{{ member.user_id }}</option></select></label><label><input :disabled="busy" v-model="assetModelConsent" type="checkbox" />明确允许用于模型处理</label><p>服务端继续核对当前底座资料授权。</p></template>
 <template v-if="semanticKind==='RuleBinding'"><p v-if="!currentBindingPolicy">当前副本未返回可覆盖政策；提交将由服务端政策审核。</p><p v-else>已审政策 {{ bindingView?.source_policy_hash }} · 可修改参数 {{ currentBindingPolicy.allowed_parameter_keys?.join('、') || '无' }}</p><label>启用状态<select :disabled="busy" v-model="bindingEnabled"><option value="">保持当前</option><option value="true">启用</option><option value="false">申请停用</option></select></label><label>严重度<select :disabled="busy" v-model="bindingSeverity"><option value="">保持当前</option><option value="blocking">阻断</option><option value="warning">警告</option><option value="info">提示</option></select></label><div class="grid"><label>新目标类型<input :disabled="busy" v-model="bindingTargetKind" placeholder="留空保持当前" /></label><label>新目标编号<input :disabled="busy" v-model="bindingTargetId" placeholder="留空保持当前" /></label></div><label>声明式参数<textarea :disabled="busy" v-model="bindingParameters" /></label><label>删除的已审参数键<input :disabled="busy" v-model="bindingRemoveKeys" placeholder="逗号分隔，留空不删除" /></label></template>
 <template v-if="semanticKind==='RouteNode'"><p>新节点当前版本填 0；已有节点保持读取的当前版本。</p><label>路线名称<input :disabled="busy" v-model="routeName" /></label><label>就绪状态<select :disabled="busy" v-model="routeStatus"><option value="planned">拟定</option><option value="ready">已人工复核就绪</option><option value="blocked">被阻断</option><option value="retired">停用</option></select></label><label><input :disabled="busy" v-model="routeRequired" type="checkbox" />必经节点</label><div class="grid"><label>依赖类型<select :disabled="busy" v-model="routeInput.kind"><option v-for="kind in objectKinds" :key="kind">{{ kind }}</option></select></label><label>依赖编号<input :disabled="busy" v-model="routeInput.id" /></label><label>依赖当前版本<input :disabled="busy" v-model="routeInput.version" /></label></div><button type="button" @click="addRouteInput">加入真实版本依赖</button><ul><li v-for="(input,index) in routeInputs" :key="index">{{ kindLabel(input.kind) }}/{{ input.id }} @ {{ input.version }} <button type="button" @click="routeInputs=routeInputs.filter((_,i)=>i!==index)">移除</button></li></ul></template>
 <button type="button" :disabled="busy" @click="addSemanticDelta">加入拟议变更</button><article v-for="(delta,index) in semanticDraft" :key="delta.kind+'/'+delta.target_id"><strong>{{ kindLabel(delta.kind) }}/{{ delta.target_id }} @ {{ delta.expected_version }}</strong><pre>{{ semanticText(delta) }}</pre><button type="button" @click="semanticDraft=semanticDraft.filter((_,i)=>i!==index)">移出草稿</button></article>
 </fieldset><label>明确受影响章节<select :disabled="busy" v-model="affectedIds" multiple><option v-for="chapter in chapters" :key="chapter.id" :value="chapter.id">{{ chapter.title }}</option></select></label><button :disabled="busy || !reason.trim()">保存拟议草稿</button><button v-if="editId" type="button" @click="editId = ''">取消编辑</button></form>
      <p v-if="!changes.length && !loading">还没有拟议变更。</p>
      <article v-for="change in changes" :key="change.id" :id="changeAnchor(change.id)">
        <h3>{{ change.reason }}</h3><p>{{ label(change.status) }} · 基线 {{ change.base_context_revision }}<span v-if="change.target_context_revision"> → {{ change.target_context_revision }}</span></p>
        <p>变更编号：{{ change.id }}</p>
        <p v-if="change.decision_by || change.decided_at">{{ decisionText(change) }}</p>
        <p v-if="change.reassessed_from_id">重评来源：<a :href="changeLink(change.reassessed_from_id)">{{ change.reassessed_from_id }}</a></p>
        <p v-if="change.reverses_change_set_id">逆向来源：<a :href="changeLink(change.reverses_change_set_id)">{{ change.reverses_change_set_id }}</a></p>
        <p v-if="change.stale_reason" class="notice">陈旧原因：{{ change.stale_reason }}</p>
        <p v-if="change.status === 'satisfied'" class="notice">当前目标已满足，未新增正式基线。</p>
        <table><thead><tr><th>条件</th><th>原值</th><th>拟议值</th></tr></thead><tbody><tr v-for="field in change.fields" :key="field.key"><td>{{ field.key }}</td><td>{{ field.old_value }}</td><td>{{ field.new_value }}</td></tr></tbody></table>
        <details v-for="delta in change.semantic_deltas || []" :key="delta.kind+'/'+delta.target_id"><summary>{{ kindLabel(delta.kind) }}/{{ delta.target_id }} @ {{ delta.expected_version }}</summary><div class="grid"><div><strong>服务器原值</strong><pre>{{ semanticText(delta,true) }}</pre></div><div><strong>拟议修改</strong><pre>{{ semanticText(delta) }}</pre></div></div></details>
        <div class="actions">
          <button :disabled="busy || ['draft','satisfied'].includes(change.status)" @click="showAssessment(change)">查看影响依据</button>
          <button v-if="canEditDraft(change)" :disabled="busy || loading" @click="editDraft(change)">编辑草稿</button>
          <button v-if="changeActionAllowed(change, 'assess')" :disabled="busy || loading" @click="changeAction(change, 'assess')">评估当前影响</button>
          <button v-if="changeActionAllowed(change, 'reassess')" :disabled="busy || loading" @click="changeAction(change, 'reassess')">按当前基线重评</button>
          <button v-if="changeActionAllowed(change, 'apply')" :disabled="busy || loading || ownerActionsFrozen" @click="changeAction(change, 'apply')">Owner 应用</button>
          <button v-if="changeActionAllowed(change, 'reject')" :disabled="busy || loading || ownerActionsFrozen" @click="changeAction(change, 'reject')">Owner 驳回</button>
          <button v-if="changeActionAllowed(change, 'stabilize')" :disabled="busy || loading" @click="changeAction(change, 'stabilize')">检查稳定化</button>
          <button v-if="changeActionAllowed(change, 'reverse')" :disabled="busy || loading" @click="changeAction(change, 'reverse')">起草逆向变更</button>
        </div>
        <OwnerUnavailableNotice :reason="ownerUnavailableReason" :recovery="ownerRecoveryGuidance" />
      </article>
      <article v-if="assessment"><h3>影响预览 · 基线 {{ assessment.context_revision }}</h3><p>评估指纹 {{ assessment.input_hash }}</p><h4>本次必须处理的对象</h4><ul v-if="assessment.objects && assessment.objects.length"><li v-for="(object, index) in assessment.objects" :key="index">{{ kindLabel(object.ref.kind) }} · {{ object.ref.kind }}/{{ object.ref.id }} @ {{ object.ref.version }} · {{ objectStateLabel(object.state) }} · {{ object.confirmed ? '已确认' : '未确认' }} · 基线 {{ object.context_revision }}</li></ul><p v-else>本次评估没有要求处理的对象。</p><h4>确定影响</h4><ul v-if="definiteImpacts.length"><li v-for="(impact, index) in definiteImpacts" :key="'definite-'+index"><strong>确定影响</strong>：{{ kindLabel(impact.target.kind) }} · {{ impact.target.kind }}/{{ impact.target.id }} @ {{ impact.target.version }} · {{ actionLabel(impact.action) }}<span v-if="impact.action === 'NO_ACTION'">（不创建影响任务）</span><ul><li v-for="cause in impact.causes" :key="cause.edge_id">{{ cause.reason }}<template v-if="cause.source"> · 来源 {{ kindLabel(cause.source.kind) }} · {{ cause.source.kind }}/{{ cause.source.id }} @ {{ cause.source.version }}</template> · 边 {{ cause.edge_id }}</li></ul></li></ul><p v-else>本次没有此类影响。</p><h4>可能影响，等待人工判断</h4><ul v-if="possibleImpacts.length"><li v-for="(impact, index) in possibleImpacts" :key="'possible-'+index"><strong>可能影响，等待人工判断</strong>：{{ kindLabel(impact.target.kind) }} · {{ impact.target.kind }}/{{ impact.target.id }} @ {{ impact.target.version }} · {{ actionLabel(impact.action) }}<span v-if="impact.action === 'NO_ACTION'">（不创建影响任务）</span><ul><li v-for="cause in impact.causes" :key="cause.edge_id">{{ cause.reason }}<template v-if="cause.source"> · 来源 {{ kindLabel(cause.source.kind) }} · {{ cause.source.kind }}/{{ cause.source.id }} @ {{ cause.source.version }}</template> · 边 {{ cause.edge_id }}</li></ul></li></ul><p v-else>本次没有此类影响。</p><h4>本次评估依据的依赖边</h4><p>以下依赖边来自本次评估响应的快照，不是人工登记的依赖全集。</p><ul v-if="assessment.edges && assessment.edges.length"><li v-for="(edge, index) in assessment.edges" :key="index">{{ kindLabel(edge.source.kind) }} · {{ edge.source.kind }}/{{ edge.source.id }} → {{ kindLabel(edge.target.kind) }} · {{ edge.target.kind }}/{{ edge.target.id }} · {{ actionLabel(edge.action) }} · {{ edge.relation }} · {{ label(edge.status) }} · {{ edge.reason }}</li></ul><p v-else>本次评估没有记录依赖边。<button v-if="canWrite" type="button" :disabled="busy || !reason.trim()" @click="recommendDependencies">AI 推荐依赖边</button></p></article>
      <article v-if="stabilization"><h3>{{ stabilization.status === 'STABILIZED' ? '当前变更已稳定' : '仍有稳定化阻断' }}</h3><p>稳定后仍需冻结快照与交付 Preflight。</p><p>判定时间 {{ stabilization.checked_at }}</p><ul><li v-for="(blocker, index) in stabilization.blockers" :key="index">{{ blocker.reason }} · {{ blocker.target_id }}</li></ul><p v-if="stabilization.stale_task_ids.length">已失效任务 {{ stabilization.stale_task_ids.join('、') }}：依据已消失，既不算闭环也不算阻断。</p></article>
      <details><summary>正式基线历史</summary><p v-for="revision in revisions" :key="revision.revision">基线 {{ revision.revision }} · 条件 {{ revision.spec_revision }} · 模板副本 {{ revision.template_copy_version }} · {{ revision.created_at }}</p></details>
    </template>
    <template v-if="tab === 'impacts'">
      <form v-if="canWrite" @submit.prevent="addDependency"><h3>登记版本依赖</h3><div class="grid"><fieldset v-for="(object, index) in [sourceRef, targetRef]" :key="index"><legend>{{ index === 0 ? '被依赖对象' : '使用它的对象' }}</legend><label>类型<select :disabled="busy" v-model="object.kind"><option v-for="kind in objectKinds" :key="kind" :value="kind">{{ kindLabel(kind) }}</option></select></label><label>对象编号<input :disabled="busy" v-model="object.id" required /></label><label>当前版本<input :disabled="busy" v-model="object.version" required /></label></fieldset></div><label>影响动作<select :disabled="busy" v-model="edgeAction"><option v-for="action in impactActions" :key="action" :value="action">{{ actionLabel(action) }}</option></select></label><button :disabled="busy || !reason.trim()">登记人工依赖</button><button type="button" :disabled="busy || !reason.trim()" @click="recommendDependencies">AI 推荐依赖边</button><p>AI 推荐只产生候选：服务端分析项目信号（正文引用、证据卡范围、模板结构、路线声明）后把建议放进下方候选队列，每条仍需人工确认。</p></form>
      <h3>人工登记的依赖（可确认或拒绝）</h3><p>这些依赖来自人工登记的依赖全集，用于确认依赖、修正候选或拒绝建议；不是本次评估响应的依据快照。</p>
      <article v-for="edge in edges" :key="edge.id"><h3>{{ kindLabel(edge.source.kind) }}/{{ edge.source.id }} → {{ kindLabel(edge.target.kind) }}/{{ edge.target.id }}</h3><p>{{ edge.source.version }} → {{ edge.target.version }} · {{ actionLabel(edge.action) }} · {{ label(edge.status) }}</p><p>{{ edge.reason }}</p><div v-if="canWrite && edge.status === 'suggested'" class="actions"><button :disabled="busy" @click="decideEdge(edge, 'confirm')">确认依赖</button><button :disabled="busy" @click="openEdgeModify(edge)">修正候选</button><button :disabled="busy" @click="decideEdge(edge, 'reject')">拒绝建议</button></div><form v-if="canWrite && modifyOpen && modifyingEdgeId === edge.id" @submit.prevent="decideEdge(edge, 'modify')"><h3>人工修正候选</h3><label>关系<select :disabled="busy" v-model="modifyRelation"><option v-for="relation in edgeRelations" :key="relation" :value="relation">{{ relation }}</option></select></label><label>影响动作<select :disabled="busy" v-model="modifyAction"><option v-for="action in impactActions" :key="action" :value="action">{{ actionLabel(action) }}</option></select></label><label>置信度<input :disabled="busy" v-model="modifyConfidence" type="number" min="0" max="1" step="0.05" /></label><button :disabled="busy || !reason.trim()">保存修正并确认</button><button type="button" :disabled="busy" @click="closeEdgeModify">取消</button></form></article>
      <DependencyCandidatePanel ref="candidatePanel" :project-id="project.id" :readonly="!canWrite" />
      <h3>影响任务</h3><p v-if="!tasks.length && !loading">没有当前可见任务。</p><article v-for="task in tasks" :key="task.id"><h3>{{ actionLabel(task.action) }} · {{ label(task.status) }}</h3><p><strong>{{ severityLabel(task.severity) }}</strong> · {{ kindLabel(task.target.kind) }}/{{ task.target.id }} @ {{ task.target.version }} · 基线 {{ task.target_revision }} · 责任人 {{ task.assignee_id || '未分配' }}</p><ul><li v-for="(cause, index) in task.causes" :key="index">{{ cause.reason }} · {{ cause.edge_id }}</li></ul><ul v-if="task.location_hints?.length" class="hint-list"><li v-for="(hint, index) in task.location_hints" :key="'hint-'+index"><span>第 {{ hint.paragraph_index }} 段 · “{{ hint.excerpt }}”</span><button type="button" :disabled="busy" @click="emit('locate', { chapterId: task.target.id, value: hint.value, referenceOffset: hint.start_offset })">在编辑器中定位</button></li></ul><div v-if="canManage" class="actions"><select :disabled="busy" v-model="assignees[task.id]" aria-label="任务责任人"><option value="">选择责任人</option><option v-for="member in activeMembers" :key="member.user_id">{{ member.user_id }}</option></select><button :disabled="busy || !assignees[task.id]" @click="taskAction(task, 'assign')">分配</button></div><div v-if="canWrite && task.assignee_id === currentUserId && !['DONE','STALE','DISMISSED'].includes(task.status)" class="actions"><button :disabled="busy" @click="taskAction(task, 'start')">开始处理</button><button :disabled="busy" @click="taskAction(task, 'wait')">等待人工</button><button v-if="task.action !== 'BLOCK_RELEASE'" :disabled="busy" @click="openTaskClosure(task)">提交闭环证据</button><button v-if="task.severity !== 'blocking'" :disabled="busy" @click="taskAction(task, 'dismiss')">接受本次不处理</button></div><p v-if="task.severity === 'blocking' && !['DONE','STALE','DISMISSED'].includes(task.status)">此影响阻断交付，只能通过闭环证据完成，不能接受不处理。</p><p v-if="task.status === 'STALE'">任务依据已消失，不算闭环也不再阻断：{{ staleReasonLabel(task.stale_reason) }}</p><p v-if="task.status === 'DISMISSED'">已由责任人接受本次不处理：{{ task.disposition }}</p><p v-if="task.action === 'BLOCK_RELEASE'">必须由当前交付 Preflight 解除。</p></article>
      <form v-if="closingTask" @submit.prevent="closeTask"><h3>当前证据 · {{ actionLabel(closingTask.action) }}</h3><label v-if="closingTask.target.kind === 'ResearchClaim'">人工复核处置<select :disabled="busy" v-model="closureDecision" required><option value="">选择本次处置</option><option value="reviewed">已人工复核当前主张版本</option></select></label><template v-if="closingTask.action==='RETRIEVE_CANDIDATES'"><p>请先按范围 impact:{{ closingTask.id }} 建立并批准真实调研任务，至少完成两轮检索。</p><label>完整 ResearchRun 回执编号<input :disabled="busy" v-model="closureReceipt" required /></label></template><template v-if="closingTask.action==='RECOMPUTE'"><p>先运行当前交付检查，使用实际规则计算回执。未完成或 UNKNOWN 不能关闭任务。</p><label>实际检查回执编号<input :disabled="busy || receiptLoading" v-model="closureReceipt" required @input="computationReceipt=null" /></label><button type="button" :disabled="busy || receiptLoading || !closureReceipt" @click="loadComputationReceipt">读取当前回执</button><p v-if="receiptLoading">正在复核当前回执…</p><dl v-if="computationReceipt"><dt>真实版本 / 基线</dt><dd>{{ computationReceipt.version }} / {{ computationReceipt.context_revision }}</dd><dt>当前来源基线</dt><dd>{{ computationReceipt.source_basis_hash }}</dd><dt>规则 / 检查状态</dt><dd>{{ computationReceipt.ruleset_hash }} / {{ computationReceipt.status }}</dd></dl></template><label v-if="closingTask.action!=='RECOMPUTE'">{{ closingTask.action==='RETRIEVE_CANDIDATES' ? '回执真实版本' : '当前目标版本' }}<input :disabled="busy" v-model="closureVersion" required /></label><button :disabled="busy || receiptLoading || !reason.trim()">按当前证据尝试完成</button><button type="button" @click="closingTask = null">取消</button></form>
      <form v-if="canManage" @submit.prevent="preflightClosure"><h3>关联已有交付检查</h3><p>先执行交付检查，凭检查回执更新影响任务；稳定化通过后，再冻结快照并导出。</p><label>当前正式变更<select :disabled="busy" v-model="preflightChange" required><option value="">选择已应用的当前变更</option><option v-for="change in preflightChanges" :key="change.id" :value="change.id">{{ change.reason }} · {{ change.id }}</option></select></label><label>已有检查回执编号<input :disabled="busy" v-model="preflightSnapshot" required /></label><button :disabled="busy">服务端复验 Preflight 并闭环</button></form>
    </template>
  </section>
</template>
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { Chapter, Project } from '@/api/lingdoc/workspace'
import { listChapters, applyChangeSet, rejectChangeSet } from '@/api/lingdoc/workspace'
import * as api from '@/api/lingdoc/mainlineChanges'
import { recommendDependencyCandidates } from '@/api/lingdoc/dependencyCandidates'
import { listResearchClaims, listEvidenceCards, type ResearchClaim, type EvidenceCard } from '@/api/lingdoc/agentApi'
import { listAssets, type Asset } from '@/api/lingdoc/workspace'
import { useAuthStore } from '@/stores/auth'
import { useOwnerAvailability } from '@/composables/useOwnerAvailability'
import DependencyCandidatePanel from './DependencyCandidatePanel.vue'
import OwnerUnavailableNotice from './OwnerUnavailableNotice.vue'
const props = withDefaults(defineProps<{ project: Project; readonly?: boolean; currentUserId?: string; preflightReceiptId?: string }>(), { readonly: true, currentUserId: '', preflightReceiptId: '' })
const emit = defineEmits<{ close: []; changed: []; dirty: [boolean]; locate: [{ chapterId: string; value: string; referenceOffset: number }] }>()
const auth = useAuthStore()
const currentIdentity = computed(() => `${auth.effectiveTenantId}:${props.currentUserId}`)
const tabs = [{ id: 'changes', label: '正式变更' }, { id: 'impacts', label: '依赖与影响' }]
const tab = ref('changes'), error = ref(''), message = ref(''), busy = ref(false), loading = ref(false), reason = ref('')
const changes = ref<api.MainlineChangeSet[]>([]), chapters = ref<Chapter[]>([]), revisions = ref<api.ContextRevisionRecord[]>([])
const assessment = ref<api.ImpactAssessment | null>(null), stabilization = ref<api.StabilizationResult | null>(null)
// Certainty is a server decision, so the preview groups the impacts the server
// marked 'definite' apart from the 'possible' ones instead of letting the
// operator read both in one undifferentiated list.
const definiteImpacts = computed(() => (assessment.value?.impacts || []).filter(impact => impact.certainty === 'definite'))
const possibleImpacts = computed(() => (assessment.value?.impacts || []).filter(impact => impact.certainty !== 'definite'))
const edges = ref<api.DependencyEdge[]>([]), tasks = ref<api.ImpactTask[]>([]), closingTask = ref<api.ImpactTask | null>(null), closureVersion = ref(''), closureReceipt = ref(''), closureDecision = ref(''), assignees = ref<Record<string, string>>({})
const activeMembers = computed(() => props.project.members.filter(member => !member.status || member.status === 'active'))
const ownMember = computed(() => activeMembers.value.find(member => member.user_id === props.currentUserId))
const canWrite = computed(() => !props.readonly)
const canOwner = computed(() => canWrite.value && ownMember.value?.role === 'owner')
const canManage = computed(() => canOwner.value || canWrite.value && ownMember.value?.governance_role === 'admin')
// Owner 可用性事实与恢复指引文案都在组合式函数里持有：置灰条件与提示文案
// 从同一处取，不在本面板另写一遍。
const {
  frozen: ownerActionsFrozen,
  reason: ownerUnavailableReason,
  recovery: ownerRecoveryGuidance,
  refresh: refreshOwnerAvailability,
} = useOwnerAvailability(() => props.project.id)
const draftField = ref(''), draftValue = ref(''), affectedIds = ref<string[]>([]), editId = ref('')
const semanticKind = ref<api.SemanticDelta['kind']>('ResearchClaim'), semanticTarget = ref(''), semanticVersion = ref(1), semanticDraft = ref<api.SemanticDelta[]>([])
const claimText = ref(''), claimScope = ref(''), claimStatus = ref<'active' | 'disputed' | 'superseded'>('active'), claimExplanation = ref(''), claimConsent = ref(false)
const cardEditor = ref('{}'), cardConsent = ref(false), cards = ref<EvidenceCard[]>([])
const assetScope = ref<'personal' | 'project' | 'team'>('project'), assetPurpose = ref(''), assetUsers = ref<string[]>([]), assetModelConsent = ref(false)
const bindingEnabled = ref(''), bindingSeverity = ref(''), bindingTargetKind = ref(''), bindingTargetId = ref(''), bindingParameters = ref('{}'), bindingRemoveKeys = ref('')
const routeName = ref(''), routeStatus = ref<api.RouteNode['status']>('planned'), routeRequired = ref(false), routeInputs = ref<api.ObjectVersionRef[]>([]), routeInput = ref<api.ObjectVersionRef>({ kind: 'ChapterVersion', id: '', version: '' })
const claims = ref<ResearchClaim[]>([]), assets = ref<Asset[]>([]), routes = ref<api.RouteNode[]>([])
const bindingView = computed(() => props.project.template_copy as (NonNullable<typeof props.project.template_copy> & api.ProjectBindingView) | undefined)
const semanticOptions = computed(() => {
 if (semanticKind.value === 'ResearchClaim') return claims.value.map(item => ({ id: item.id, version: item.version, label: item.text }))
 if (semanticKind.value === 'EvidenceCard') return cards.value.map(item => ({ id: item.id, version: item.version, label: `${item.support_type} · ${item.scope}` }))
 if (semanticKind.value === 'ProjectAssetScope') return assets.value.map(item => ({ id: item.id, version: item.asset_revision, label: item.title }))
 if (semanticKind.value === 'RuleBinding') return (bindingView.value?.rule_set_snapshot?.bindings || []).map(item => ({ id: item.rule_id, version: props.project.template_copy_version || props.project.template_copy?.version || 0, label: item.rule_id }))
 return routes.value.map(item => ({ id: item.id, version: item.version, label: item.name }))
})
const currentBindingPolicy = computed(() => bindingView.value?.binding_policies?.find(item => item.rule_id === semanticTarget.value))
function resetSemanticEditor() { cardEditor.value='{}';cardConsent.value=false;claimText.value='';claimScope.value='';claimExplanation.value='';claimStatus.value='active';semanticTarget.value='';semanticVersion.value=semanticKind.value==='RouteNode'?0:1;claimConsent.value=false;assetModelConsent.value=false;assetPurpose.value='';assetUsers.value=[];routeStatus.value='planned';routeInputs.value=[];routeName.value='';routeRequired.value=false;bindingParameters.value='{}';bindingRemoveKeys.value='';bindingEnabled.value='';bindingSeverity.value='';bindingTargetKind.value='';bindingTargetId.value='' }
function selectSemanticTarget() {
 const selected = semanticOptions.value.find(item => item.id === semanticTarget.value); if (selected) semanticVersion.value = selected.version
 if (semanticKind.value === 'ResearchClaim') { const claim=claims.value.find(item=>item.id===semanticTarget.value);if(claim){claimText.value=claim.text;claimScope.value=claim.scope;claimStatus.value=claim.status as typeof claimStatus.value;claimExplanation.value=claim.explanation||''};claimConsent.value=false }
 if (semanticKind.value === 'EvidenceCard') { const card=cards.value.find(item=>item.id===semanticTarget.value);if(card){cardEditor.value=JSON.stringify({claim_ref:card.claim_ref,anchor_refs:card.anchor_refs,support_type:card.support_type,scope:card.scope,scope_chapter_ids:card.scope_chapter_ids,limitations:card.limitations,gaps:card.gaps,alternative_explanation:card.alternative_explanation},null,2)};cardConsent.value=false }
 if (semanticKind.value === 'ProjectAssetScope') {assetModelConsent.value=false;assetPurpose.value='';assetUsers.value=[]}
 if (semanticKind.value === 'RouteNode') { const route=routes.value.find(item=>item.id===semanticTarget.value);if(route){routeName.value=route.name;routeStatus.value=route.status;routeRequired.value=route.required;routeInputs.value=route.inputs.map(item=>({...item}))} }
}
function addRouteInput() { if (!routeInput.value.id || !routeInput.value.version) {error.value='请提供依赖对象的真实编号与版本。';return};routeInputs.value=[...routeInputs.value,{...routeInput.value}];routeInput.value={kind:'ChapterVersion',id:'',version:''} }
function addSemanticDelta() {
 if (props.readonly) return
 if(!semanticTarget.value.trim() || !Number.isInteger(semanticVersion.value) || semanticVersion.value < 0 || (semanticKind.value !== 'RouteNode' && semanticVersion.value < 1)){error.value='请选定实际对象和当前版本，新路线节点版本为 0。';return}
 const delta:api.SemanticDelta={kind:semanticKind.value,target_id:semanticTarget.value.trim(),expected_version:semanticVersion.value}
 if(delta.kind==='ResearchClaim') {if(!claimText.value.trim()||!claimScope.value.trim()||!claimConsent.value){error.value='请填写主张和范围，并明确确认提交语义修改。';return};delta.proposed_claim={text:claimText.value,scope:claimScope.value,status:claimStatus.value,explanation:claimExplanation.value,confirmed:claimConsent.value}}
 if(delta.kind==='EvidenceCard') {if(!cardConsent.value){error.value='请明确确认提交证据语义修改。';return};try{const value=JSON.parse(cardEditor.value);if(!value||Array.isArray(value)||typeof value!=='object'||!value.claim_ref||!Array.isArray(value.anchor_refs)||!value.anchor_refs.length||!Array.isArray(value.scope_chapter_ids)||!value.scope_chapter_ids.length||!value.scope||!['supports','refutes','qualifies','background'].includes(value.support_type))throw Error();const keys=['claim_ref','anchor_refs','support_type','scope','scope_chapter_ids','limitations','gaps','alternative_explanation'];if(Object.keys(value).some(key=>!keys.includes(key)))throw Error();delta.proposed_card={...value,confirmed:cardConsent.value}}catch{error.value='请填写合法证据关系 JSON，仅包含主张、锚点、关系、范围及限制。';return}}
 if(delta.kind==='ProjectAssetScope') {if(!assetPurpose.value.trim()){error.value='请明确资料使用目的。';return};delta.proposed_asset_policy={scope:assetScope.value,purpose:assetPurpose.value,allowed_user_ids:[...assetUsers.value],model_use_allowed:assetModelConsent.value}}
 if(delta.kind==='RuleBinding') {let parameters:Record<string,unknown>;try{parameters=JSON.parse(bindingParameters.value);if(!parameters||Array.isArray(parameters)||typeof parameters!=='object')throw Error()}catch{error.value='规则参数须为合法的声明式 JSON 对象。';return};const override:api.RuleBindingOverride={rule_id:delta.target_id,parameters};if(bindingEnabled.value)override.enabled=bindingEnabled.value==='true';if(bindingSeverity.value)override.severity=bindingSeverity.value;if(bindingTargetKind.value)override.target_kind=bindingTargetKind.value;if(bindingTargetId.value)override.target_id=bindingTargetId.value;const remove=bindingRemoveKeys.value.split(',').map(item=>item.trim()).filter(Boolean);if(remove.length)override.remove_parameter_keys=remove;delta.proposed_binding=override}
 if(delta.kind==='RouteNode'){if(!routeName.value.trim()){error.value='请填写路线节点名称。';return};delta.proposed_route={name:routeName.value,status:routeStatus.value,required:routeRequired.value,inputs:routeInputs.value.map(item=>({...item}))}}
 if(semanticDraft.value.some(item=>item.kind===delta.kind&&item.target_id===delta.target_id)){error.value='同一对象不能在一份草稿中出现两个语义修改。';return}
 semanticDraft.value=[...semanticDraft.value,delta];message.value='语义修改已加入待提交草稿，正式对象尚未改变。'
}
function semanticText(delta:api.SemanticDelta,before=false) { const value=before?delta.before:delta.proposed_card||delta.proposed_claim||delta.proposed_asset_policy||delta.proposed_binding||delta.proposed_route;return JSON.stringify(value||{},null,2) }

const objectKinds = ['ProjectSpec', 'ProjectAsset', 'SourceAnchor', 'EvidenceCard', 'ResearchClaim', 'ChapterVersion', 'TemplateCopy', 'RuleSet', 'RuleBinding', 'RouteNode']
const impactActions = ['REVALIDATE_EVIDENCE', 'RETRIEVE_CANDIDATES', 'REVIEW_REQUIRED', 'REGENERATE', 'RECOMPUTE', 'INVALIDATE_CONFIRMATION', 'BLOCK_RELEASE', 'NO_ACTION']
const sourceRef = ref<api.ObjectVersionRef>({ kind: 'ProjectSpec', id: props.project.id, version: String(props.project.spec_revision) })
const targetRef = ref<api.ObjectVersionRef>({ kind: 'ChapterVersion', id: '', version: '' }), edgeAction = ref('REVIEW_REQUIRED')
const receiptLoading=ref(false), computationReceipt=ref<api.ImpactComputationReceipt|null>(null)
// `modify` is a first-class human decision on a suggested edge: it
// rebinds relation/action/confidence and leaves the edge confirmed, so the
// panel must be able to express it rather than forcing confirm-or-reject. The
// optional fields are omitted, never emptied, to leave a value untouched.
const modifyingEdgeId = ref('')
const modifyRelation = ref(''), modifyAction = ref(''), modifyConfidence = ref('')
const edgeRelations = ['supports', 'constrains', 'uses']
const modifyOpen = computed(() => Boolean(modifyingEdgeId.value))
function openEdgeModify(edge:api.DependencyEdge){ modifyingEdgeId.value=edge.id; modifyRelation.value=edge.relation; modifyAction.value=edge.action; modifyConfidence.value=String(edge.confidence) }
function closeEdgeModify(){ modifyingEdgeId.value=''; modifyRelation.value=''; modifyAction.value=''; modifyConfidence.value='' }
const modifyConfidenceValue = computed(() => { const parsed=Number(modifyConfidence.value); return Number.isFinite(parsed)&&modifyConfidence.value.trim()!==''?parsed:null })
let receiptRequest=0
const preflightReceiptBaseline=ref(props.preflightReceiptId || '')
const preflightChange = ref(''), preflightSnapshot = ref(props.preflightReceiptId || '')
const operationKeys = new Map<string, { payload: string; key: string }>()
let epoch = 0
function label(state: string) { return ({ draft: '草稿', assessed: '已评估', applied: '已应用', stabilized: '已稳定', satisfied: '目标已满足', rejected: '已驳回', stale: '已陈旧', confirmed: '已确认', suggested: '建议，等待人工判断', DRAFT: '草稿', LOCKED: '已锁定', PAUSED: '已暂停', CLOSED: '已关闭', OPEN: '待处理', IN_PROGRESS: '处理中', WAITING_USER: '等待人工', DONE: '已完成', WAITING_HUMAN: '等待责任签署', DECIDED: '已共同决定', DISMISSED: '已接受不处理', STALE: '已失效' } as Record<string, string>)[state] || state }
// Severity is shown rather than hidden: whether an impact blocks the release
// gate is the first thing a responsible person needs to know, and it is also
// what decides whether accepting it is even offered.
function severityLabel(severity: string) { return ({ blocking: '阻断交付', warning: '需要处理', info: '知会' } as Record<string, string>)[severity] || severity }
function staleReasonLabel(reason?: string) { return ({ target_version_replaced: '任务所针对的版本已被替换', assignee_revoked: '责任人已失去处理该任务的权限' } as Record<string, string>)[reason || ''] || reason || '' }
function kindLabel(kind: string) { return ({ ProjectSpec: '研究条件', ProjectAsset: '项目资料', SourceAnchor: '资料锚点', EvidenceCard: '证据卡', ResearchClaim: '研究主张', ChapterVersion: '章节版本', TemplateCopy: '模板副本', RuleSet: '规则版本', RuleBinding: '独立规则绑定', ProjectAssetScope: '资料使用范围', ResearchRun: '完整调研回执', ComputationReceipt: '实际重算回执', RouteNode: '技术路线节点' } as Record<string, string>)[kind] || kind }
function objectStateLabel(state: string) { return ({ CURRENT: '当前版本' } as Record<string, string>)[state] || state }
function actionLabel(action: string) { return ({ REVALIDATE_EVIDENCE: '重新核验证据', RETRIEVE_CANDIDATES: '重新调研候选', REVIEW_REQUIRED: '人工复核', REGENERATE: '重新生成', RECOMPUTE: '重新计算', INVALIDATE_CONFIRMATION: '重新确认', BLOCK_RELEASE: '阻止发布', NO_ACTION: '本次不产生影响' } as Record<string, string>)[action] || action }
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
  loading.value = true; error.value = ''
  void refreshOwnerAvailability()
  try {
    if (tab.value === 'changes') { const result = await Promise.all([api.listMainlineChanges(id), listChapters(id), api.listContextRevisions(id), listResearchClaims(id), listAssets(id), api.listRouteNodes(id), listEvidenceCards(id)]); if (epoch === stamp && identity===currentIdentity.value) { changes.value = result[0].data || []; chapters.value = result[1].data || []; revisions.value = result[2].data || []; claims.value = result[3].data || []; assets.value = result[4].data || []; routes.value = result[5].data || []; cards.value = result[6].data || [] } }
    if (tab.value === 'impacts') { const result = await Promise.all([api.listDependencies(id), api.listImpactTasks(id)]); if (epoch === stamp && identity===currentIdentity.value) { edges.value = result[0].data || []; tasks.value = result[1].data || [] } }
  } catch (err) { if (epoch === stamp) error.value = errorText(err) }
  finally { if (epoch === stamp) loading.value = false }
}
type ChangeAction = 'assess' | 'reassess' | 'apply' | 'reject' | 'stabilize' | 'reverse'
function changeAnchor(id: string) { return `change-${encodeURIComponent(id)}` }
function changeLink(id: string) { return `#${encodeURIComponent(changeAnchor(id))}` }
function decisionText(change: api.MainlineChangeSet) {
  const parts: string[] = []
  if (change.decision_by) parts.push(`决定人：${change.decision_by}`)
  if (change.decided_at) parts.push(`决定时间：${change.decided_at}`)
  return parts.join(' · ')
}
function currentChange(change: api.MainlineChangeSet) {
  if (change.project_id !== props.project.id) return undefined
  return changes.value.find(item => item.id === change.id && item.project_id === props.project.id)
}
function changeActionAllowed(change: api.MainlineChangeSet, action: ChangeAction) {
  const current = currentChange(change)
  if (!canWrite.value || props.project.status !== 'active' || !current) return false
  if (action === 'assess') return ['draft', 'assessed'].includes(current.status)
  if (action === 'reassess') return current.status === 'stale'
  if (!canOwner.value) return false
  if (action === 'apply' || action === 'reject') return current.status === 'assessed'
  if (action === 'stabilize') return current.status === 'applied'
  return action === 'reverse' && ['applied', 'stabilized'].includes(current.status)
}
function canEditDraft(change: api.MainlineChangeSet) {
  const current = currentChange(change)
  return Boolean(current && changeActionAllowed(current, 'assess') && (current.created_by === props.currentUserId || canOwner.value))
}
function editDraft(change: api.MainlineChangeSet) {
  if (busy.value || loading.value) return
  const current = currentChange(change)
  if (!current || !canEditDraft(current)) { error.value = '当前角色或变更状态不允许编辑此草稿。'; return }
  if (current.fields.length > 1 || current.template_upgrade) { error.value = '多字段和模板结构变更请使用对应模板入口。'; return }
  const field = current.fields[0]
  editId.value = current.id; draftField.value = field?.key || ''; draftValue.value = field?.new_value || ''
  semanticDraft.value = JSON.parse(JSON.stringify(current.semantic_deltas || [])); affectedIds.value = current.impacts.map(impact => impact.chapter_id); reason.value = current.reason
}
async function saveDraft() {
  if (!canWrite.value || props.project.status !== 'active' || busy.value || loading.value) return
  if (editId.value) {
    const current = changes.value.find(change => change.id === editId.value)
    if (!current || !canEditDraft(current)) { error.value = '草稿状态或当前编辑权限已改变，请重新读取；未提交输入已保留。'; return }
  }
  if ((!draftField.value && !semanticDraft.value.length) || !reason.value.trim()) { error.value='请加入研究条件或语义修改，并填写说明。';return }
  const input: api.ChangeDraftInput = { expected_context_revision: props.project.current_context_revision, fields: draftField.value ? { [draftField.value]: { old_value: props.project.spec[draftField.value] || '', new_value: draftValue.value } } : {}, semantic_deltas: JSON.parse(JSON.stringify(semanticDraft.value)), affected_chapter_ids: [...affectedIds.value], reason: reason.value }
  const changeId = editId.value
  const result = await mutate('draft', input, key => changeId ? api.editChangeDraft(props.project.id, changeId, input, key) : api.createChangeDraft(props.project.id, input, key), '拟议草稿已保存，尚未改变正式基线。')
  if (result) {editId.value = ''; semanticDraft.value=[];draftField.value='';draftValue.value='';affectedIds.value=[];resetSemanticEditor()}
}
async function showAssessment(change: api.MainlineChangeSet) {
  const projectId=props.project.id, requestEpoch=epoch, identity=currentIdentity.value
  if (change.project_id !== projectId) return
  if (change.status === 'draft' || change.status === 'satisfied') { assessment.value = null; return }
  try { const result = await api.getImpactAssessment(projectId, change.id); if (props.project.id===projectId && epoch===requestEpoch && currentIdentity.value===identity) assessment.value=result.data }
  catch (err) { if(props.project.id===projectId && epoch===requestEpoch && currentIdentity.value===identity) error.value=errorText(err) }
}
async function changeAction(change: api.MainlineChangeSet, action: ChangeAction) {
  if (busy.value || loading.value) return
  if (!changeActionAllowed(change, action)) { error.value = '当前角色或变更状态不允许此操作，请重新读取。'; return }
  const projectId=props.project.id, identity=currentIdentity.value
  if (action === 'stabilize') { const result = await mutate(`stabilize-${change.id}`, {}, key => api.stabilizeRevision(projectId, change.id, key), '已读取稳定化结果。'); if (result && props.project.id===projectId && currentIdentity.value===identity) stabilization.value = result.data; return }
  if (action === 'apply') { await mutate(`apply-${change.id}`, {}, key => applyChangeSet(projectId, change.id, key), 'Owner 应用结果已记录，请处理影响任务。'); return }
  if (action === 'reject') { await mutate(`reject-${change.id}`, {}, key => rejectChangeSet(projectId, change.id, key), 'Owner 驳回结果已记录。'); return }
  if (action === 'reverse') { await mutate(`reverse-${change.id}`, {}, key => api.reverseMainlineChange(projectId, change.id, key), '逆向提议已创建，需重新评估与 Owner 应用。'); return }
  if (action === 'reassess') { await mutate(`reassess-${change.id}`, {}, key => api.reassessMainlineChange(projectId, change.id, key), '已按当前基线重评，原提议保留为历史；请查看新记录状态。', true); return }
  const result = await mutate(`assess-${change.id}`, {}, key => api.assessMainlineChange(projectId, change.id, key), '影响已按当前输入评估。')
  if (result && props.project.id===projectId && currentIdentity.value===identity) {
    const assessed=changes.value.find(item=>item.id===change.id&&item.project_id===projectId&&item.status==='assessed')
    if(assessed)await showAssessment(assessed)
  }
}
async function addDependency() { if (!reason.value.trim()) return; const input: api.DependencyEdgeInput = { expected_context_revision: props.project.current_context_revision, source: { ...sourceRef.value }, target: { ...targetRef.value }, action: edgeAction.value, origin: 'user', relation: 'uses', reason: reason.value, confidence: 1 }; const result=await mutate('edge', input, key => api.createDependency(props.project.id, input, key), '人工依赖已按版本登记。');if(result){targetRef.value={kind:'ChapterVersion',id:'',version:''};sourceRef.value={kind:'ProjectSpec',id:props.project.id,version:String(props.project.spec_revision)};edgeAction.value='REVIEW_REQUIRED'} }
// The recommendation trigger (#751): the server owns the whole pipeline
// (signals, model call, review, queueing); the panel only sends the operator's
// reason and an idempotency key. Success refreshes the candidate queue the
// same decision path already uses.
const candidatePanel = ref<{ refresh: () => Promise<void> } | null>(null)
async function recommendDependencies() {
  if (!reason.value.trim()) return
  const input = { reason: reason.value, scope: {} }
  const result = await mutate('recommend-dependencies', input, key => recommendDependencyCandidates(props.project.id, input, key), 'AI 推荐完成，候选已进入队列等待人工判断。')
  if (result) {
    const dropped = result.data.dropped?.length || 0
    if (dropped > 0) message.value = `AI 推荐完成，${result.data.batch.candidates.length} 条候选进入队列；${dropped} 条建议未通过复核被丢弃。`
    await candidatePanel.value?.refresh()
  }
}
async function decideEdge(edge: api.DependencyEdge, decision: api.DependencyDecision) { if (!reason.value.trim()) { error.value = '请填写人工判断理由。'; return }; const input: api.DependencyDecisionInput = { expected_version: edge.version, decision, reason: reason.value }; if (decision === 'modify') { const confidence = modifyConfidenceValue.value; if (modifyRelation.value.trim()) input.relation = modifyRelation.value.trim(); if (modifyAction.value) input.action = modifyAction.value; if (confidence !== null) input.confidence = confidence; const changed = input.relation !== undefined || input.action !== undefined || input.confidence !== undefined; if (!changed) { error.value = '修正候选至少要改动关系、影响动作或置信度中的一项。'; return } } const result = await mutate(`edge-${edge.id}`, input, key => api.decideDependency(props.project.id, edge.id, input, key), '版本绑定的人工决定已保留。'); if (result && decision === 'modify') closeEdgeModify(); return result }
async function taskAction(task: api.ImpactTask, action: api.ImpactTaskActionInput['action'], closure: api.ObjectVersionRef[] = []) { if (!reason.value.trim()) { error.value = '请填写处置说明。'; return }; const input: api.ImpactTaskActionInput = { expected_version: task.version, target_revision: task.target_revision, action, assignee_id: assignees.value[task.id], human_decision: closureDecision.value, reason: reason.value, closure_evidence: closure }; const result=await mutate(`task-${task.id}`, input, key => api.actOnImpactTask(props.project.id, task.id, input, key), '任务处理结果已记录。');if(result&&action==='assign'){delete assignees.value[task.id]};return result }
function openTaskClosure(task:api.ImpactTask){closingTask.value=task;closureVersion.value='';closureDecision.value='';computationReceipt.value=null;closureReceipt.value=task.action==='RECOMPUTE'?(props.preflightReceiptId || ''):'';if(task.action==='RECOMPUTE'&&closureReceipt.value)void loadComputationReceipt()}
async function loadComputationReceipt():Promise<api.ImpactComputationReceipt|undefined>{
 const task=closingTask.value,id=closureReceipt.value.trim(),projectId=props.project.id,requestEpoch=epoch,stamp=++receiptRequest
 if(!task||task.action!=='RECOMPUTE'||!id)return
 receiptLoading.value=true;computationReceipt.value=null
 try{
  const result=await api.getImpactComputationReceipt(projectId,id)
  if(props.project.id!==projectId||epoch!==requestEpoch||stamp!==receiptRequest||closingTask.value?.id!==task.id||closureReceipt.value.trim()!==id)return
  const receipt=result.data
  if(!receipt||receipt.id!==id||receipt.project_id!==projectId||!Number.isInteger(receipt.version)||receipt.version<1||receipt.context_revision!==task.target_revision||receipt.context_revision!==props.project.current_context_revision||!receipt.source_basis_hash||!receipt.computation_basis_hash||!receipt.input_hash){error.value='回执与当前项目、目标基线或真实版本不一致。';return}
  computationReceipt.value=receipt
  if(!Array.isArray(receipt.evaluations)||receipt.evaluations.some(item=>item.status==='UNKNOWN')){error.value='检查含有未能判定的结果，请重新完成当前检查。';return}
  return receipt
 }catch(err){if(props.project.id===projectId&&epoch===requestEpoch&&stamp===receiptRequest)error.value=errorText(err)}
 finally{if(stamp===receiptRequest)receiptLoading.value=false}
}
async function closeTask(){
 if(!closingTask.value||props.readonly||busy.value||receiptLoading.value)return
 const task=closingTask.value
 let proof:api.ObjectVersionRef={...task.target,version:closureVersion.value}
 if(task.action==='RECOMPUTE'){
  const receipt=await loadComputationReceipt()
  if(!receipt)return
  proof={kind:'ComputationReceipt',id:receipt.id,version:String(receipt.version)}
 }else if(task.action==='RETRIEVE_CANDIDATES'){
  if(!closureReceipt.value||!closureVersion.value){error.value='请填写已经完成的服务端回执编号与真实版本。';return}
  proof={kind:'ResearchRun',id:closureReceipt.value,version:closureVersion.value}
 }
 const result=await taskAction(task,'complete',[proof])
 if(result){closingTask.value=null;closureReceipt.value='';closureVersion.value='';closureDecision.value='';computationReceipt.value=null}
}
async function preflightClosure() { const result=await mutate('preflight', { change: preflightChange.value, snapshot: preflightSnapshot.value }, key => api.acknowledgeImpactPreflight(props.project.id, preflightChange.value, preflightSnapshot.value, key), '服务端已读取当前 Preflight 的闭环结果。');if(result){preflightChange.value='';preflightSnapshot.value=preflightReceiptBaseline.value} }
function resetAllForms(){editId.value='';draftField.value='';draftValue.value='';affectedIds.value=[];semanticDraft.value=[];resetSemanticEditor();targetRef.value={kind:'ChapterVersion',id:'',version:''};sourceRef.value={kind:'ProjectSpec',id:props.project.id,version:String(props.project.spec_revision)};edgeAction.value='REVIEW_REQUIRED';assignees.value={};closeEdgeModify();closingTask.value=null;closureVersion.value='';closureReceipt.value='';closureDecision.value='';computationReceipt.value=null;receiptRequest++;receiptLoading.value=false;preflightChange.value='';preflightReceiptBaseline.value=props.preflightReceiptId || '';preflightSnapshot.value=preflightReceiptBaseline.value;reason.value=''}
const hasUnsavedChanges=computed(()=>Boolean(
 reason.value.trim()||editId.value||draftField.value||draftValue.value||affectedIds.value.length||semanticDraft.value.length||semanticTarget.value||cardConsent.value||(cardEditor.value.trim()!==''&&cardEditor.value.trim()!=='{}')||claimText.value||claimScope.value||claimExplanation.value||claimConsent.value||assetPurpose.value||assetUsers.value.length||assetModelConsent.value||bindingEnabled.value||bindingSeverity.value||bindingTargetKind.value||bindingTargetId.value||(bindingParameters.value.trim()!==''&&bindingParameters.value.trim()!=='{}')||bindingRemoveKeys.value||routeName.value||routeInputs.value.length||routeInput.value.id||routeInput.value.version||routeRequired.value||routeStatus.value!=='planned'||targetRef.value.id||targetRef.value.version||targetRef.value.kind!=='ChapterVersion'||sourceRef.value.kind!=='ProjectSpec'||sourceRef.value.id!==props.project.id||sourceRef.value.version!==String(props.project.spec_revision)||edgeAction.value!=='REVIEW_REQUIRED'||modifyOpen.value||Object.values(assignees.value).some(Boolean)||closingTask.value||closureVersion.value||closureReceipt.value||closureDecision.value||preflightChange.value||preflightSnapshot.value!==preflightReceiptBaseline.value
))
const preflightChanges=computed(()=>changes.value.filter(change=>['applied','stabilized'].includes(change.status)&&change.target_context_revision===props.project.current_context_revision))
function syncPreflightReceipt(next:string){const untouched=preflightSnapshot.value===preflightReceiptBaseline.value;if(!hasUnsavedChanges.value&&!busy.value&&untouched){preflightSnapshot.value=next}preflightReceiptBaseline.value=next}
watch(()=>props.preflightReceiptId,next=>syncPreflightReceipt(next || ''))
watch(hasUnsavedChanges,value=>emit('dirty',value),{immediate:true})
watch(()=>props.project.spec_revision,(next,previous)=>{if(sourceRef.value.kind==='ProjectSpec'&&sourceRef.value.id===props.project.id&&sourceRef.value.version===String(previous)){sourceRef.value={...sourceRef.value,version:String(next)}}})
defineExpose({hasUnsavedChanges,busy})

watch(() => props.project.id, () => { epoch++; resetAllForms(); operationKeys.clear(); changes.value = []; edges.value = []; tasks.value = []; assessment.value = null; stabilization.value = null; editId.value = ''; semanticDraft.value=[];claims.value=[];cards.value=[];assets.value=[];routes.value=[];semanticTarget.value='';claimConsent.value=false;routeInputs.value=[]; closingTask.value = null; reason.value = ''; sourceRef.value = { kind: 'ProjectSpec', id: props.project.id, version: String(props.project.spec_revision) }; void refresh() }, { immediate: true })
watch(currentIdentity,()=>{cards.value=[];epoch++;operationKeys.clear();error.value='当前身份已改变，已保留未提交输入；请重新读取并核对当前身份的证据。'})
watch(tab, () => { void refresh() })
</script>

<style scoped>
.ops-panel{max-width:1100px;margin:auto;padding:24px;background:#fbfcfa;color:#20352c;line-height:1.6}.ops-panel header,.actions,nav{display:flex;gap:10px;align-items:center;flex-wrap:wrap}.ops-panel header{justify-content:space-between;border-bottom:1px solid #dce4dd}.ops-panel h2{font-size:24px;margin:0}.ops-panel h3{font-size:17px;margin:0 0 10px}.ops-panel p{margin:8px 0}.ops-panel nav{margin:20px 0}.ops-panel button{border:1px solid #bfcec3;border-radius:7px;padding:8px 12px;background:#fff;color:inherit;cursor:pointer}.ops-panel button[aria-pressed=true]{background:#e8f0ea;border-color:#477058}.ops-panel button:disabled{opacity:.45;cursor:not-allowed}.ops-panel article,.ops-panel form,.ops-panel details{padding:18px;margin:16px 0;background:white;border:1px solid #dce4dd;border-radius:10px}.ops-panel label{display:flex;flex-direction:column;gap:5px;margin:0 0 12px;font-size:13px;color:#52645a}.ops-panel input,.ops-panel select,.ops-panel textarea{min-width:0;padding:9px;border:1px solid #cbd6ce;border-radius:6px;background:white;color:#20352c}.ops-panel textarea{min-height:80px;resize:vertical;font-family:inherit}.ops-panel pre{white-space:pre-wrap;overflow-wrap:anywhere;font-size:12px;background:#f4f7f3;padding:10px;border-radius:6px}.ops-panel input[type=checkbox]{align-self:flex-start}.ops-panel output{padding:10px;background:#f4f7f3;white-space:pre-wrap}.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(200px,1fr));gap:16px}.ops-panel fieldset{border:1px solid #dce4dd;border-radius:8px}.ops-panel table{width:100%;border-collapse:collapse;table-layout:fixed;overflow-wrap:anywhere;font-size:13px}.ops-panel .hint-list{margin:8px 0;padding-left:18px}.ops-panel .hint-list li{margin:4px 0;display:flex;gap:8px;align-items:baseline;flex-wrap:wrap}.ops-panel th,.ops-panel td{padding:9px;text-align:left;vertical-align:top;border-bottom:1px solid #e7ece8}.ops-panel small{display:block;color:#708075}.notice,.error{padding:12px;border-radius:7px}.notice{background:#edf3ee}.error{background:#fcecea;color:#8b3025}.ops-panel :focus-visible{outline:2px solid #467158;outline-offset:3px}@media(max-width:640px){.ops-panel{padding:14px}.ops-panel table{font-size:11px}.ops-panel td,.ops-panel th{padding:6px}}
</style>
