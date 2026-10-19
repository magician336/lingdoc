<template>
  <section class="ops-panel" aria-label="变更与运行质量">
    <header><div><h2>变更与运行质量</h2><p>{{ project.name }} · 基线 {{ project.current_context_revision }}</p></div><button @click="$emit('close')">关闭</button></header>
    <p v-if="readonly" class="notice">当前仅可查看。业务操作仍由服务端校验当前权限、来源与版本。</p>
    <nav><button v-for="entry in tabs" :key="entry.id" :aria-pressed="tab === entry.id" @click="tab = entry.id">{{ entry.label }}</button><button :disabled="loading" @click="refresh">{{ loading ? '读取中…' : '重新读取' }}</button></nav>
    <p v-if="error" role="alert" class="error">{{ error }}</p><p v-if="message" role="status" class="notice">{{ message }}</p>
    <label v-if="canWrite">本次操作说明<textarea :disabled="busy" v-model="reason" maxlength="2000" placeholder="变更、依赖决定、任务处置、暂停或试用决策的原因" /></label>
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
          <button v-if="changeActionAllowed(change, 'apply')" :disabled="busy || loading" @click="changeAction(change, 'apply')">Owner 应用</button>
          <button v-if="changeActionAllowed(change, 'reject')" :disabled="busy || loading" @click="changeAction(change, 'reject')">Owner 驳回</button>
          <button v-if="changeActionAllowed(change, 'stabilize')" :disabled="busy || loading" @click="changeAction(change, 'stabilize')">检查稳定化</button>
          <button v-if="changeActionAllowed(change, 'reverse')" :disabled="busy || loading" @click="changeAction(change, 'reverse')">起草逆向变更</button>
        </div>
      </article>
      <article v-if="assessment"><h3>影响预览 · 基线 {{ assessment.context_revision }}</h3><p>评估指纹 {{ assessment.input_hash }}</p><ul><li v-for="(impact, index) in assessment.impacts" :key="index"><strong>{{ impact.certainty === 'definite' ? '确定影响' : '可能影响，等待人工判断' }}</strong>：{{ kindLabel(impact.target.kind) }}/{{ impact.target.id }} @ {{ impact.target.version }} · {{ actionLabel(impact.action) }}<span v-if="impact.action === 'NO_ACTION'">（不创建影响任务）</span><ul><li v-for="cause in impact.causes" :key="cause.edge_id">{{ cause.reason }} · {{ cause.edge_id }}</li></ul></li></ul></article>
      <article v-if="stabilization"><h3>{{ stabilization.status === 'STABILIZED' ? '当前变更已稳定' : '仍有稳定化阻断' }}</h3><p>稳定后仍需冻结快照与交付 Preflight。</p><p>判定时间 {{ stabilization.checked_at }}</p><ul><li v-for="(blocker, index) in stabilization.blockers" :key="index">{{ blocker.reason }} · {{ blocker.target_id }}</li></ul><p v-if="stabilization.stale_task_ids.length">已失效任务 {{ stabilization.stale_task_ids.join('、') }}：依据已消失，既不算闭环也不算阻断。</p></article>
      <details><summary>正式基线历史</summary><p v-for="revision in revisions" :key="revision.revision">基线 {{ revision.revision }} · 条件 {{ revision.spec_revision }} · 模板副本 {{ revision.template_copy_version }} · {{ revision.created_at }}</p></details>
    </template>
    <template v-if="tab === 'impacts'">
      <form v-if="canWrite" @submit.prevent="addDependency"><h3>登记版本依赖</h3><div class="grid"><fieldset v-for="(object, index) in [sourceRef, targetRef]" :key="index"><legend>{{ index === 0 ? '被依赖对象' : '使用它的对象' }}</legend><label>类型<select :disabled="busy" v-model="object.kind"><option v-for="kind in objectKinds" :key="kind" :value="kind">{{ kindLabel(kind) }}</option></select></label><label>对象编号<input :disabled="busy" v-model="object.id" required /></label><label>当前版本<input :disabled="busy" v-model="object.version" required /></label></fieldset></div><label>影响动作<select :disabled="busy" v-model="edgeAction"><option v-for="action in impactActions" :key="action" :value="action">{{ actionLabel(action) }}</option></select></label><button :disabled="busy || !reason.trim()">登记人工依赖</button></form>
      <article v-for="edge in edges" :key="edge.id"><h3>{{ kindLabel(edge.source.kind) }}/{{ edge.source.id }} → {{ kindLabel(edge.target.kind) }}/{{ edge.target.id }}</h3><p>{{ edge.source.version }} → {{ edge.target.version }} · {{ actionLabel(edge.action) }} · {{ label(edge.status) }}</p><p>{{ edge.reason }}</p><div v-if="canWrite && edge.status === 'suggested'" class="actions"><button :disabled="busy" @click="decideEdge(edge, 'confirm')">确认依赖</button><button :disabled="busy" @click="openEdgeModify(edge)">修正候选</button><button :disabled="busy" @click="decideEdge(edge, 'reject')">拒绝建议</button></div><form v-if="canWrite && modifyOpen && modifyingEdgeId === edge.id" @submit.prevent="decideEdge(edge, 'modify')"><h3>人工修正候选</h3><label>关系<select :disabled="busy" v-model="modifyRelation"><option v-for="relation in edgeRelations" :key="relation" :value="relation">{{ relation }}</option></select></label><label>影响动作<select :disabled="busy" v-model="modifyAction"><option v-for="action in impactActions" :key="action" :value="action">{{ actionLabel(action) }}</option></select></label><label>置信度<input :disabled="busy" v-model="modifyConfidence" type="number" min="0" max="1" step="0.05" /></label><button :disabled="busy || !reason.trim()">保存修正并确认</button><button type="button" :disabled="busy" @click="closeEdgeModify">取消</button></form></article>
      <h3>影响任务</h3><p v-if="!tasks.length && !loading">没有当前可见任务。</p><article v-for="task in tasks" :key="task.id"><h3>{{ actionLabel(task.action) }} · {{ label(task.status) }}</h3><p><strong>{{ severityLabel(task.severity) }}</strong> · {{ kindLabel(task.target.kind) }}/{{ task.target.id }} @ {{ task.target.version }} · 基线 {{ task.target_revision }} · 责任人 {{ task.assignee_id || '未分配' }}</p><ul><li v-for="(cause, index) in task.causes" :key="index">{{ cause.reason }} · {{ cause.edge_id }}</li></ul><div v-if="canManage" class="actions"><select :disabled="busy" v-model="assignees[task.id]" aria-label="任务责任人"><option value="">选择责任人</option><option v-for="member in activeMembers" :key="member.user_id">{{ member.user_id }}</option></select><button :disabled="busy || !assignees[task.id]" @click="taskAction(task, 'assign')">分配</button></div><div v-if="canWrite && task.assignee_id === currentUserId && !['DONE','STALE','DISMISSED'].includes(task.status)" class="actions"><button :disabled="busy" @click="taskAction(task, 'start')">开始处理</button><button :disabled="busy" @click="taskAction(task, 'wait')">等待人工</button><button v-if="task.action !== 'BLOCK_RELEASE'" :disabled="busy" @click="openTaskClosure(task)">提交闭环证据</button><button v-if="task.severity !== 'blocking'" :disabled="busy" @click="taskAction(task, 'dismiss')">接受本次不处理</button></div><p v-if="task.severity === 'blocking' && !['DONE','STALE','DISMISSED'].includes(task.status)">此影响阻断交付，只能通过闭环证据完成，不能接受不处理。</p><p v-if="task.status === 'STALE'">任务依据已消失，不算闭环也不再阻断：{{ staleReasonLabel(task.stale_reason) }}</p><p v-if="task.status === 'DISMISSED'">已由责任人接受本次不处理：{{ task.disposition }}</p><p v-if="task.action === 'BLOCK_RELEASE'">必须由当前交付 Preflight 解除。</p></article>
      <form v-if="closingTask" @submit.prevent="closeTask"><h3>当前证据 · {{ actionLabel(closingTask.action) }}</h3><label v-if="closingTask.target.kind === 'ResearchClaim'">人工复核处置<select :disabled="busy" v-model="closureDecision" required><option value="">选择本次处置</option><option value="reviewed">已人工复核当前主张版本</option></select></label><template v-if="closingTask.action==='RETRIEVE_CANDIDATES'"><p>请先按范围 impact:{{ closingTask.id }} 建立并批准真实调研任务，至少完成两轮检索。</p><label>完整 ResearchRun 回执编号<input :disabled="busy" v-model="closureReceipt" required /></label></template><template v-if="closingTask.action==='RECOMPUTE'"><p>先运行当前交付检查，使用实际规则计算回执。未完成或 UNKNOWN 不能关闭任务。</p><label>实际检查回执编号<input :disabled="busy || receiptLoading" v-model="closureReceipt" required @input="computationReceipt=null" /></label><button type="button" :disabled="busy || receiptLoading || !closureReceipt" @click="loadComputationReceipt">读取当前回执</button><p v-if="receiptLoading">正在复核当前回执…</p><dl v-if="computationReceipt"><dt>真实版本 / 基线</dt><dd>{{ computationReceipt.version }} / {{ computationReceipt.context_revision }}</dd><dt>当前来源基线</dt><dd>{{ computationReceipt.source_basis_hash }}</dd><dt>规则 / 检查状态</dt><dd>{{ computationReceipt.ruleset_hash }} / {{ computationReceipt.status }}</dd></dl></template><label v-if="closingTask.action!=='RECOMPUTE'">{{ closingTask.action==='RETRIEVE_CANDIDATES' ? '回执真实版本' : '当前目标版本' }}<input :disabled="busy" v-model="closureVersion" required /></label><button :disabled="busy || receiptLoading || !reason.trim()">按当前证据尝试完成</button><button type="button" @click="closingTask = null">取消</button></form>
      <form v-if="canManage" @submit.prevent="preflightClosure"><h3>关联已有交付检查</h3><p>先执行交付检查，凭检查回执更新影响任务；稳定化通过后，再冻结快照并导出。</p><label>当前正式变更<select :disabled="busy" v-model="preflightChange" required><option value="">选择已应用的当前变更</option><option v-for="change in preflightChanges" :key="change.id" :value="change.id">{{ change.reason }} · {{ change.id }}</option></select></label><label>已有检查回执编号<input :disabled="busy" v-model="preflightSnapshot" required /></label><button :disabled="busy">服务端复验 Preflight 并闭环</button></form>
    </template>
    <template v-if="tab === 'runtime'">
      <div v-if="metrics" class="grid metrics"><article><strong>{{ metrics.event_count }}</strong>已记录事件</article><article><strong>{{ metrics.p95_ms ?? '缺样本' }}</strong>P95 毫秒</article><article><strong>{{ metrics.p99_ms ?? '缺样本' }}</strong>P99 毫秒</article><article><strong>{{ metrics.queue_depth }}</strong>观测队列深度</article></div>
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
import type { Chapter, Project } from '@/api/lingdoc/workspace'
import { listChapters, applyChangeSet, rejectChangeSet } from '@/api/lingdoc/workspace'
import * as api from '@/api/lingdoc/mainlineChanges'
import { listResearchClaims, listEvidenceCards, type ResearchClaim, type EvidenceCard } from '@/api/lingdoc/g2'
import { listAssets, type Asset } from '@/api/lingdoc/workspace'
import { useAuthStore } from '@/stores/auth'
const props = withDefaults(defineProps<{ project: Project; readonly?: boolean; currentUserId?: string; preflightReceiptId?: string }>(), { readonly: true, currentUserId: '', preflightReceiptId: '' })
const emit = defineEmits<{ close: []; changed: []; dirty: [boolean] }>()
const auth = useAuthStore()
const currentIdentity = computed(() => `${auth.effectiveTenantId}:${props.currentUserId}`)
const tabs = [{ id: 'changes', label: '正式变更' }, { id: 'impacts', label: '依赖与影响' }, { id: 'runtime', label: '运行与恢复' }, { id: 'trial', label: '真实试用' }]
const tab = ref('changes'), error = ref(''), message = ref(''), busy = ref(false), loading = ref(false), reason = ref('')
const changes = ref<api.MainlineChangeSet[]>([]), chapters = ref<Chapter[]>([]), revisions = ref<api.ContextRevisionRecord[]>([])
const assessment = ref<api.ImpactAssessment | null>(null), stabilization = ref<api.StabilizationResult | null>(null)
const edges = ref<api.DependencyEdge[]>([]), tasks = ref<api.ImpactTask[]>([]), closingTask = ref<api.ImpactTask | null>(null), closureVersion = ref(''), closureReceipt = ref(''), closureDecision = ref(''), assignees = ref<Record<string, string>>({})
const controls = ref<api.RuntimeControl[]>([]), metrics = ref<api.RuntimeMetrics | null>(null), events = ref<api.RuntimeEvent[]>([]), alerts = ref<api.RuntimeAlert[]>([])
const windows = ref<api.TrialWindow[]>([]), selectedWindow = ref<api.TrialWindow | null>(null), samples = ref<api.TrialSample[]>([]), trialReport = ref<api.TrialReport | null>(null), decisions = ref<api.TrialDecision[]>([]), reviewingSample = ref<api.TrialSample | null>(null)
const activeMembers = computed(() => props.project.members.filter(member => !member.status || member.status === 'active'))
const ownMember = computed(() => activeMembers.value.find(member => member.user_id === props.currentUserId))
const canWrite = computed(() => !props.readonly)
const canOwner = computed(() => canWrite.value && ownMember.value?.role === 'owner')
const canManage = computed(() => canOwner.value || canWrite.value && ownMember.value?.governance_role === 'admin')
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
const preflightChange = ref(''), preflightSnapshot = ref(props.preflightReceiptId || ''), recoverySnapshot = ref(''), recoveryExport = ref(''), correlation = ref('')
const alertSignal = ref(''), alertResponsible = ref(''), alertAction = ref<api.RuntimeAlertRule['action']>('notify')
const projectControl = computed(() => controls.value.find(control => control.scope.kind === 'project' && control.scope.value === props.project.id))
const runtimeScopeKind = ref<api.RuntimeScope['kind']>('project'), runtimeModeScope = ref('')
const runtimeReadReady = ref(false), evidenceLoading = ref(false), evidenceReadError = ref('')
const evidenceOptions = ref<api.TrialEvidenceOption[]>([]), releaseOptions = ref<api.TrialRelease[]>([]), exportOptions = ref<api.TrialExport[]>([])
const selectedEvidenceKey = ref('')
let evidenceRequest = 0
const runtimeScope = computed<api.RuntimeScope>(() => ({ kind: runtimeScopeKind.value, value: ({ project: props.project.id, tenant: String(auth.effectiveTenantId || ''), template_version: props.project.template_version || '', ruleset_hash: props.project.template_copy?.ruleset_hash || '', runtime_mode: runtimeModeScope.value } as Record<api.RuntimeScope['kind'], string>)[runtimeScopeKind.value] }))
const selectedRuntimeControl = computed(() => controls.value.find(control => control.scope.kind === runtimeScope.value.kind && control.scope.value === runtimeScope.value.value))
const recoveryExports = computed(() => exportOptions.value.filter(item => item.status === 'verified' && (!recoverySnapshot.value || item.snapshot_id === recoverySnapshot.value)))
const alertSeverity = ref<api.RuntimeAlertRule['severity'] | ''>('')
const metricNames = [{name:'manual_effort_minutes',label:'人工投入分钟 / 实际样本数'}, {name:'source_support_rate',label:'有来源支持项 / 实际核查项'}, {name:'missed_edits',label:'漏改项 / 实际核查范围'}, {name:'rule_false_positive_rate',label:'规则误报项 / 实际规则发现'}, {name:'model_false_positive_rate',label:'模型误报项 / 实际模型发现'}, {name:'unconfirmed_items',label:'未确认项 / 实际核查范围'}, {name:'export_loss_items',label:'导出丢失项 / 实际核查范围'}]
const processNames = [{name:'authorization',label:'实际授权检查'}, {name:'main_path',label:'实际主流程'}, {name:'failure_path',label:'失败路径'}, {name:'manual_review',label:'人工复盘'}, {name:'repair_or_rollback',label:'修复或已执行回退'}, {name:'key_scenario_rerun',label:'关键场景复跑'}]
const freshSampleForm = () => ({ sampleVersion:'', fixtureDigest:'', permissionSnapshot:'', inputHash:'', outputHash:'', executedAt:'', runtimeMode:'' as '' | api.TrialSample['runtime_mode'], contextRevision:null as number | null, templateVersion:'', rulesetHash:'', correlationId:'', severity:'' as api.TrialSample['severity'], runtimeEventIds:[] as string[], dependencies:{} as Record<string,string>, process:{} as Record<string,string>, metrics:metricNames.map(metric=>({...metric, measured:false, numerator:'', denominator:'', version:''})) })
const sampleForm = ref(freshSampleForm())
const sampleFacts = ref<TrialEvidenceFacts | null>(null)
const sampleOwnershipConfirmed = ref(false), sampleFactsIdentity = ref('')
const sampleEventOptions = computed(() => events.value.filter(event => event.origin === 'runtime' && event.correlation_id === sampleForm.value.correlationId))
interface TrialEvidenceFacts { reference:string; fingerprint:string; projectId:string; contextRevision:number|null; templateVersion:string; rulesetHash:string; runtimeMode:''|api.TrialSample['runtime_mode']; executedAt:string; correlationId:string; fixtureDigest:string; inputHash:string; outputHash:string; dependencies:Record<string,string> }
const decisionRoles = ['product', 'qa', 'safety'] as const
const decisionOwners = ref<Record<typeof decisionRoles[number], string>>({ product: '', qa: '', safety: '' }), includedUsers = ref<string[]>([])
const trialStart = ref(''), trialEnd = ref(''), targetSamples = ref(10), minimumSamples = ref(10), trialSupport = ref(''), trialRedaction = ref(''), trialExit = ref('')
const reviewResult = ref<api.TrialSample['quality_result'] | ''>(''), humanDecision = ref<api.TrialReviewInput['human_decision'] | ''>(''), wordEvidence = ref(''), repairOwner = ref(''), repairVersion = ref(''), rerunEvidence = ref(''), trialDecision = ref<api.TrialDecision['decision'] | ''>('')
const operationKeys = new Map<string, { payload: string; key: string }>()
let epoch = 0
function label(state: string) { return ({ draft: '草稿', assessed: '已评估', applied: '已应用', stabilized: '已稳定', satisfied: '目标已满足', rejected: '已驳回', stale: '已陈旧', confirmed: '已确认', suggested: '建议，等待人工判断', DRAFT: '草稿', LOCKED: '已锁定', PAUSED: '已暂停', CLOSED: '已关闭', OPEN: '待处理', IN_PROGRESS: '处理中', WAITING_USER: '等待人工', DONE: '已完成', WAITING_HUMAN: '等待责任签署', DECIDED: '已共同决定', DISMISSED: '已接受不处理', STALE: '已失效' } as Record<string, string>)[state] || state }
// Severity is shown rather than hidden: whether an impact blocks the release
// gate is the first thing a responsible person needs to know, and it is also
// what decides whether accepting it is even offered.
function severityLabel(severity: string) { return ({ blocking: '阻断交付', warning: '需要处理', info: '知会' } as Record<string, string>)[severity] || severity }
function staleReasonLabel(reason?: string) { return ({ target_version_replaced: '任务所针对的版本已被替换', assignee_revoked: '责任人已失去处理该任务的权限' } as Record<string, string>)[reason || ''] || reason || '' }
function kindLabel(kind: string) { return ({ ProjectSpec: '研究条件', ProjectAsset: '项目资料', SourceAnchor: '资料锚点', EvidenceCard: '证据卡', ResearchClaim: '研究主张', ChapterVersion: '章节版本', TemplateCopy: '模板副本', RuleSet: '规则版本', RuleBinding: '独立规则绑定', ProjectAssetScope: '资料使用范围', ResearchRun: '完整调研回执', ComputationReceipt: '实际重算回执', RouteNode: '技术路线节点' } as Record<string, string>)[kind] || kind }
function actionLabel(action: string) { return ({ REVALIDATE_EVIDENCE: '重新核验证据', RETRIEVE_CANDIDATES: '重新调研候选', REVIEW_REQUIRED: '人工复核', REGENERATE: '重新生成', RECOMPUTE: '重新计算', INVALIDATE_CONFIRMATION: '重新确认', BLOCK_RELEASE: '阻止发布', NO_ACTION: '本次不产生影响' } as Record<string, string>)[action] || action }
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
    if (tab.value === 'changes') { const result = await Promise.all([api.listMainlineChanges(id), listChapters(id), api.listContextRevisions(id), listResearchClaims(id), listAssets(id), api.listRouteNodes(id), listEvidenceCards(id)]); if (epoch === stamp && identity===currentIdentity.value) { changes.value = result[0].data || []; chapters.value = result[1].data || []; revisions.value = result[2].data || []; claims.value = result[3].data || []; assets.value = result[4].data || []; routes.value = result[5].data || []; cards.value = result[6].data || [] } }
    if (tab.value === 'impacts') { const result = await Promise.all([api.listDependencies(id), api.listImpactTasks(id)]); if (epoch === stamp && identity===currentIdentity.value) { edges.value = result[0].data || []; tasks.value = result[1].data || [] } }
    if (tab.value === 'runtime') { const result = await Promise.all([api.listRuntimeControls(id), api.getRuntimeMetrics(id), api.listRuntimeEvents(id, correlation.value), api.listRuntimeAlerts(id)]); if (epoch === stamp && identity===currentIdentity.value) { controls.value = result[0].data || []; metrics.value = result[1].data; events.value = result[2].data || []; alerts.value = result[3].data || [];runtimeReadReady.value=true;await loadEvidenceCatalog() } }
    if (tab.value === 'trial') { const result = await api.listTrialWindows(id); if (epoch === stamp && identity===currentIdentity.value) { windows.value = result.data || []; const next = windows.value.find(window => window.id === selectedWindow.value?.id); selectedWindow.value = next || null; if (next) await selectWindow(next);await loadEvidenceCatalog() } }
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
async function pauseRuntime() {
 if(!canManage.value||!runtimeReadReady.value||!runtimeScope.value.value||runtimeScopeKind.value==='runtime_mode'&&!['mock','real_api_fake_model','real'].includes(runtimeScope.value.value)||!reason.value.trim()){error.value='请先读取当前运行状态、选择范围并填写原因。';return}
 const input:api.RuntimeControlInput={expected_version:selectedRuntimeControl.value?.version||0,scope:{...runtimeScope.value},action:'pause',reason:reason.value}
 await mutate(`pause-${input.scope.kind}-${input.scope.value}`,input,key=>api.controlRuntime(props.project.id,input,key),'服务端暂停结果已记录；权限以当前服务端结果为准。')
}
async function resumeRuntime(control:api.RuntimeControl){
 if(!canManage.value||!runtimeReadReady.value||!reason.value.trim())return
 const projectId=props.project.id,identity=currentIdentity.value,stamp=epoch,snapshotId=recoverySnapshot.value,exportId=recoveryExport.value
 if(!releaseOptions.value.some(item=>item.id===snapshotId)||!exportOptions.value.some(item=>item.id===exportId&&item.snapshot_id===snapshotId)){error.value='请选择此课题已有的快照和对应导出。';return}
 try{
  const [snapshot,artifact]=await Promise.all([api.getTrialRelease(projectId,snapshotId),api.getTrialExport(projectId,exportId)])
  if(projectId!==props.project.id||identity!==currentIdentity.value||stamp!==epoch)return
  if(!snapshot.data||snapshot.data.id!==snapshotId||snapshot.data.project_id!==projectId||snapshot.data.is_current!==true||snapshot.data.check.status!=='passed'||!artifact.data||artifact.data.id!==exportId||artifact.data.project_id!==projectId||artifact.data.snapshot_id!==snapshotId||artifact.data.status!=='verified'||!artifact.data.file_sha256)throw new Error('复验引用已陈旧、跨课题或尚未验证；不能恢复。')
  const current=controls.value.find(item=>item.id===control.id)
  if(!current||current.version!==control.version||current.status!=='PAUSED')throw new Error('运行控制版本已改变，请重新读取。')
  const input:api.RuntimeControlInput={expected_version:control.version,scope:{...control.scope},action:'resume',reason:reason.value,snapshot_id:snapshotId,export_id:exportId}
  const result=await mutate(`resume-${control.id}`,input,key=>api.controlRuntime(projectId,input,key),'服务端已完成权限、当前性与下载复验后恢复。')
  if(result){recoverySnapshot.value='';recoveryExport.value=''}
 }catch(err){if(projectId===props.project.id&&identity===currentIdentity.value&&stamp===epoch)error.value=errorText(err)}
}
async function addAlert(){
 if(!canManage.value||!alertSeverity.value||!safeTrialRef(alertSignal.value)||!activeMembers.value.some(member=>member.user_id===alertResponsible.value)||!reason.value.trim()){error.value='请选择当前课题故障级别、责任人和有效信号，并填写原因。';return}
 const input:api.RuntimeAlertRule={signal:alertSignal.value,action:alertAction.value,responsible_id:alertResponsible.value,severity:alertSeverity.value,scope:{kind:'project',value:props.project.id}}
 const result=await mutate('alert',input,key=>api.saveRuntimeAlertRule(props.project.id,input,key),input.action==='rollback'||input.action==='retry'?'处置申请规则已保存；尚未执行回退或重试。':'故障信号已绑定处置与责任人。')
 if(result){alertSignal.value='';alertResponsible.value='';alertAction.value='notify';alertSeverity.value=''}
}
async function loadEvents() { const projectId=props.project.id,identity=currentIdentity.value,stamp=epoch,requestedCorrelation=correlation.value;try { const result=await api.listRuntimeEvents(projectId,requestedCorrelation);if(props.project.id===projectId&&currentIdentity.value===identity&&epoch===stamp&&correlation.value===requestedCorrelation)events.value=result.data||[] } catch(err){if(props.project.id===projectId&&currentIdentity.value===identity&&epoch===stamp)error.value=errorText(err)} }
async function addWindow() { const start = new Date(trialStart.value), end = new Date(trialEnd.value); if (!Number.isFinite(start.getTime()) || !Number.isFinite(end.getTime()) || end <= start) { error.value = '请填写有效的试用起止时间。'; return }; const input: api.TrialWindowInput = { starts_at: start.toISOString(), ends_at: end.toISOString(), included_project_ids: [props.project.id], included_user_ids: [...includedUsers.value], decision_owners: { ...decisionOwners.value }, target_samples: targetSamples.value, minimum_samples: minimumSamples.value, template_version: props.project.template_version, ruleset_hash: props.project.template_copy?.ruleset_hash || '', runtime_modes: ['real'], human_support: trialSupport.value, redaction: trialRedaction.value, exit_conditions: trialExit.value.split(',').map(value => value.trim()).filter(Boolean) }; const result=await mutate('window', input, key => api.createTrialWindow(props.project.id, input, key), '窗口草稿已保存，锁定后才能纳入样本。');if(result)resetWindowForm() }
async function windowAction(window: api.TrialWindow, action: 'lock' | 'pause' | 'close') { const input = { expected_version: window.version, action, reason: reason.value }; await mutate(`window-${window.id}`, input, key => api.actOnTrialWindow(props.project.id, window.id, input, key), '窗口状态已更新，历史仍保留。') }
async function selectWindow(window: api.TrialWindow) { const projectId = props.project.id, requestEpoch=epoch,identity=currentIdentity.value; if(selectedWindow.value?.id!==window.id){sampleFacts.value=null;sampleFactsIdentity.value='';sampleOwnershipConfirmed.value=false;reviewingSample.value=null}selectedWindow.value = window; try { const result = await Promise.all([api.listTrialSamples(projectId, window.id), api.getTrialReport(projectId, window.id), api.listTrialDecisions(projectId, window.id)]); if (props.project.id === projectId && identity===currentIdentity.value && epoch===requestEpoch && selectedWindow.value?.id === window.id) { samples.value = result[0].data || []; trialReport.value = result[1].data; decisions.value = result[2].data || [] } } catch (err) { if (props.project.id === projectId && identity===currentIdentity.value && epoch===requestEpoch) error.value = errorText(err) } }
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
  const metrics:api.MetricObservation[]=form.metrics.filter(metric=>metric.measured).map(metric=>{
   const numerator=Number(metric.numerator),denominator=Number(metric.denominator)
   if(!metric.numerator.trim()||!metric.denominator.trim()||!Number.isFinite(numerator)||numerator<0||!Number.isFinite(denominator)||denominator<=0||!safeTrialRef(metric.version))throw new Error('已测量指标需要实际分子、正分母和测量版本。')
   return {name:metric.name,numerator,denominator,missing:false,version:metric.version}
  })
  if(form.runtimeEventIds.length){
   const response=await api.listRuntimeEvents(projectId,form.correlationId)
   if(projectId!==props.project.id||identity!==currentIdentity.value||stamp!==epoch||formStamp!==JSON.stringify(sampleForm.value))return
   const rows=response.data||[]
   for(const id of form.runtimeEventIds){const event=rows.find(row=>row.id===id);if(!event||event.origin!=='runtime'||event.correlation_id!==form.correlationId||event.runtime_mode!==form.runtimeMode||event.context_revision!==form.contextRevision||event.template_version!==form.templateVersion||event.ruleset_hash!==form.rulesetHash)throw new Error('实际运行事件缺失或与样本模式、版本和基线不一致。')}
  }
  const input:api.TrialSampleInput={sample_version:form.sampleVersion,fixture_digest:form.fixtureDigest,project_id:projectId,template_version:form.templateVersion,ruleset_hash:form.rulesetHash,context_revision:form.contextRevision,runtime_mode:form.runtimeMode as api.TrialSample['runtime_mode'],dependency_versions:{...actual.dependencies},permission_snapshot:form.permissionSnapshot,input_hash:form.inputHash,output_hash:form.outputHash,executed_at:executed.toISOString(),correlation_id:form.correlationId,runtime_event_ids:[...form.runtimeEventIds],process_evidence:process,severity:form.severity,metrics}
  const result=await mutate(`sample-${window.id}`,input,key=>api.recordTrialSample(projectId,window.id,input,key),'样本已保存为 NOT RUN，补录信息与质量尚未人工审核。')
  if(result&&formStamp===JSON.stringify(sampleForm.value))resetSampleForm()
 }catch(err){if(projectId===props.project.id&&identity===currentIdentity.value&&stamp===epoch)error.value=errorText(err)}
}
function openReview(sample: api.TrialSample) { reviewingSample.value = sample; reviewResult.value = ''; humanDecision.value = ''; wordEvidence.value = ''; repairOwner.value = ''; repairVersion.value = ''; rerunEvidence.value = '' }
async function submitReview() { if (!selectedWindow.value || !reviewingSample.value || !reviewResult.value || !humanDecision.value) return; const windowId = selectedWindow.value.id, sampleId = reviewingSample.value.id; const input: api.TrialReviewInput = { expected_version: reviewingSample.value.version, quality_result: reviewResult.value, human_decision: humanDecision.value, reason: reason.value, word_roundtrip_evidence: wordEvidence.value, repair_owner_id: repairOwner.value, repair_version: repairVersion.value, rerun_evidence: rerunEvidence.value }; const result = await mutate('review', input, key => api.reviewTrialSample(props.project.id, windowId, sampleId, input, key), '真实人工裁定已记录。'); if (result) resetReviewForm() }
async function proposeDecision() { if (!selectedWindow.value || !trialReport.value || !trialDecision.value) return; const windowId = selectedWindow.value.id, input = { decision: trialDecision.value, reason: reason.value, report_hash: trialReport.value.input_hash }; const result=await mutate('decision', input, key => api.createTrialDecision(props.project.id, windowId, input, key), '决策提案等待三责任人共同签署。');if(result)trialDecision.value='' }
async function signDecision(decision: api.TrialDecision, responsibility: typeof decisionRoles[number]) { const input = { expected_version: decision.version, responsibility, reason: reason.value }; await mutate(`sign-${decision.id}-${responsibility}`, input, key => api.signTrialDecision(props.project.id, decision.id, input, key), '本责任项签署已记录。') }

function safeTrialRef(value:string){return /^[a-zA-Z0-9_.:/-]{1,128}$/.test(value)&&!value.toLowerCase().includes('bearer')}
function evidenceKey(item:api.TrialEvidenceOption){return `${item.kind}/${item.id}`}
function localDateTime(value:string){const date=new Date(value);if(!Number.isFinite(date.getTime()))return '';const local=new Date(date.getTime()-date.getTimezoneOffset()*60000);return local.toISOString().slice(0,19)}
async function loadEvidenceCatalog(){
 const projectId=props.project.id,identity=currentIdentity.value,stamp=epoch,request=++evidenceRequest
 evidenceLoading.value=true;evidenceReadError.value=''
 try{
  const results=await Promise.allSettled([api.listTrialResearchRuns(projectId),api.listTrialReleases(projectId),api.listTrialExports(projectId),listChapters(projectId)])
  if(projectId!==props.project.id||identity!==currentIdentity.value||stamp!==epoch||request!==evidenceRequest)return
  const options:api.TrialEvidenceOption[]=[],failures:string[]=[]
  const runs=results[0],releases=results[1],exports=results[2],chapterRows=results[3]
  if(runs.status==='fulfilled'&&Array.isArray(runs.value.data)){for(const run of runs.value.data)if(run.project_id===projectId)options.push({kind:'research_run',id:run.id,label:`调研 ${run.id} · ${run.status} · ${run.run_mode}`})}
  else failures.push(runs.status==='rejected'?`调研记录：${errorText(runs.reason)}`:'调研返回格式未知，未列为证据')
  if(releases.status==='fulfilled'&&Array.isArray(releases.value.data?.items)){releaseOptions.value=releases.value.data.items.filter(item=>item.project_id===projectId);for(const item of releaseOptions.value)options.push({kind:'snapshot',id:item.id,label:`快照 ${item.id} · ${item.check.status}`});if(releases.value.data.truncated)failures.push('快照历史已截断，仅显示实际返回部分')}
  else {releaseOptions.value=[];failures.push(releases.status==='rejected'?`快照：${errorText(releases.reason)}`:'快照返回格式未知，未列为证据')}
  if(exports.status==='fulfilled'&&Array.isArray(exports.value.data?.items)){exportOptions.value=exports.value.data.items.filter(item=>item.project_id===projectId);for(const item of exportOptions.value)options.push({kind:'export',id:item.id,label:`导出 ${item.id} · ${item.status}`});if(exports.value.data.truncated)failures.push('导出历史已截断，仅显示实际返回部分')}
  else {exportOptions.value=[];failures.push(exports.status==='rejected'?`导出：${errorText(exports.reason)}`:'导出返回格式未知，未列为证据')}
  if(chapterRows.status==='fulfilled'&&Array.isArray(chapterRows.value.data)){
   const generation=await Promise.allSettled(chapterRows.value.data.map(chapter=>api.listTrialGenerationCandidates(projectId,chapter.id)))
   if(projectId!==props.project.id||identity!==currentIdentity.value||stamp!==epoch||request!==evidenceRequest)return
   const seen=new Set<string>()
   generation.forEach((result,index)=>{if(result.status==='fulfilled'&&Array.isArray(result.value.data)){for(const candidate of result.value.data)if(candidate.run_id&&!seen.has(candidate.run_id)){seen.add(candidate.run_id);options.push({kind:'generation_run',id:candidate.run_id,chapter_id:chapterRows.value.data[index]!.id,label:`生成 ${candidate.run_id} · ${candidate.validity}`})}}else failures.push('部分生成记录读取失败，未列为证据')})
  }else failures.push('章节或生成记录未能读取')
  evidenceOptions.value=options;evidenceReadError.value=failures.join('；')
 }catch(err){if(projectId===props.project.id&&identity===currentIdentity.value&&request===evidenceRequest)evidenceReadError.value=errorText(err)}
 finally{if(request===evidenceRequest)evidenceLoading.value=false}
}
async function readTrialRelease(projectId:string,id:string){
 const response=await api.getTrialRelease(projectId,id),snapshot=response.data
 if(!snapshot||snapshot.id!==id||snapshot.project_id!==projectId||snapshot.frozen_input?.project_id!==projectId||snapshot.is_current!==true)throw new Error('快照未知、跨课题或已是历史版本，请保留输入并重新选择。')
 const frozen=snapshot.frozen_input
 if(!frozen.template||frozen.template.version!==props.project.template_version||(frozen.template_copy_ruleset_hash||frozen.template.ruleset_hash)!==props.project.template_copy?.ruleset_hash||frozen.spec_revision!==props.project.spec_revision)throw new Error('快照模板、规则或研究条件与当前版本不一致。')
 return snapshot
}
async function readTrialEvidence(item:api.TrialEvidenceOption):Promise<TrialEvidenceFacts>{
 const projectId=props.project.id,empty:TrialEvidenceFacts={reference:evidenceKey(item),fingerprint:'',projectId,contextRevision:null,templateVersion:'',rulesetHash:'',runtimeMode:'',executedAt:'',correlationId:'',fixtureDigest:'',inputHash:'',outputHash:'',dependencies:{}}
 if(item.kind==='research_run'){
  const run=(await api.getTrialResearchRun(projectId,item.id)).data
  if(!run||run.id!==item.id||run.project_id!==projectId||!Number.isInteger(run.version)||run.version<1||!['completed','failed','cancelled','paused','stale'].includes(run.status)||!['mock','real_api_fake_model','real'].includes(run.run_mode))throw new Error('调研回执未知、尚未结束或模式未声明。')
  if(run.created_by!==props.currentUserId||run.basis.context_revision!==props.project.current_context_revision||run.basis.spec_revision!==props.project.spec_revision)throw new Error('调研执行者或版本与当前身份、基线不一致。')
  empty.fingerprint=JSON.stringify(run);empty.contextRevision=run.basis.context_revision;empty.templateVersion=props.project.template_version;empty.rulesetHash=props.project.template_copy?.ruleset_hash||'';empty.runtimeMode=run.run_mode;empty.executedAt=run.finished_at||run.started_at;empty.correlationId=run.correlation_id
  empty.dependencies[`research-run/${run.id}`]=String(run.version);empty.dependencies[`research-task/${run.task_id}`]=String(run.task_version)
  for(const asset of run.basis.asset_versions)empty.dependencies[`asset/${asset.asset_id}`]=String(asset.asset_revision)
 }else if(item.kind==='generation_run'){
  const [response,candidateResponse]=await Promise.all([api.getTrialGenerationRun(projectId,item.id),api.getTrialGenerationCandidate(projectId,item.id)])
  const run=response.data,candidate=candidateResponse.data
  if(!run||run.id!==item.id||run.project_id!==projectId||run.status!=='succeeded'||run.chapter_id!==item.chapter_id||!candidate||candidate.run_id!==item.id||candidate.id!==run.candidate_id||candidate.project_id!==projectId||candidate.chapter_id!==item.chapter_id||candidate.validity!=='fresh')throw new Error('生成回执或候选未知、尚未成功或已陈旧。')
  if(candidate.basis.spec_revision!==props.project.spec_revision||candidate.basis.template_version!==props.project.template_version||candidate.basis.ruleset_hash!==props.project.template_copy?.ruleset_hash||candidate.basis.context_revision!==undefined&&candidate.basis.context_revision!==props.project.current_context_revision)throw new Error('生成候选的模板、规则或基线已改变。')
  empty.fingerprint=JSON.stringify([run,candidate]);empty.contextRevision=candidate.basis.context_revision??null;empty.templateVersion=candidate.basis.template_version;empty.rulesetHash=candidate.basis.ruleset_hash;empty.executedAt=candidate.created_at||''
  empty.dependencies['project-spec']=String(candidate.basis.spec_revision)
  if(candidate.basis.chapter_version_id)empty.dependencies[`chapter/${candidate.chapter_id}`]=candidate.basis.chapter_version_id
  for(const asset of candidate.basis.asset_versions)empty.dependencies[`asset/${asset.asset_id}`]=String(asset.asset_revision)
 }else{
  const artifact=item.kind==='export'?(await api.getTrialExport(projectId,item.id)).data:null
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
 try{const response=await api.listRuntimeEvents(projectId,correlationId);if(projectId===props.project.id&&identity===currentIdentity.value&&stamp===epoch&&correlationId===sampleForm.value.correlationId)events.value=response.data||[]}
 catch(err){if(projectId===props.project.id&&identity===currentIdentity.value&&stamp===epoch)error.value=errorText(err)}
}
function resetSampleForm(){selectedEvidenceKey.value='';sampleFacts.value=null;sampleFactsIdentity.value='';sampleOwnershipConfirmed.value=false;sampleForm.value=freshSampleForm()}
function resetWindowForm(){trialStart.value='';trialEnd.value='';targetSamples.value=10;minimumSamples.value=10;includedUsers.value=[];decisionOwners.value={product:'',qa:'',safety:''};trialSupport.value='';trialRedaction.value='';trialExit.value=''}
function resetReviewForm(){reviewingSample.value=null;reviewResult.value='';humanDecision.value='';wordEvidence.value='';repairOwner.value='';repairVersion.value='';rerunEvidence.value=''}
function resetAllForms(){editId.value='';draftField.value='';draftValue.value='';affectedIds.value=[];semanticDraft.value=[];resetSemanticEditor();targetRef.value={kind:'ChapterVersion',id:'',version:''};sourceRef.value={kind:'ProjectSpec',id:props.project.id,version:String(props.project.spec_revision)};edgeAction.value='REVIEW_REQUIRED';assignees.value={};closeEdgeModify();closingTask.value=null;closureVersion.value='';closureReceipt.value='';closureDecision.value='';computationReceipt.value=null;receiptRequest++;receiptLoading.value=false;preflightChange.value='';preflightReceiptBaseline.value=props.preflightReceiptId || '';preflightSnapshot.value=preflightReceiptBaseline.value;recoverySnapshot.value='';recoveryExport.value='';alertSignal.value='';alertResponsible.value='';alertAction.value='notify';resetWindowForm();resetSampleForm();runtimeScopeKind.value='project';runtimeModeScope.value='';alertSeverity.value='';resetReviewForm();trialDecision.value='';reason.value=''}
const hasUnsavedChanges=computed(()=>Boolean(
 reason.value.trim()||editId.value||draftField.value||draftValue.value||affectedIds.value.length||semanticDraft.value.length||semanticTarget.value||cardConsent.value||(cardEditor.value.trim()!==''&&cardEditor.value.trim()!=='{}')||claimText.value||claimScope.value||claimExplanation.value||claimConsent.value||assetPurpose.value||assetUsers.value.length||assetModelConsent.value||bindingEnabled.value||bindingSeverity.value||bindingTargetKind.value||bindingTargetId.value||(bindingParameters.value.trim()!==''&&bindingParameters.value.trim()!=='{}')||bindingRemoveKeys.value||routeName.value||routeInputs.value.length||routeInput.value.id||routeInput.value.version||routeRequired.value||routeStatus.value!=='planned'||targetRef.value.id||targetRef.value.version||targetRef.value.kind!=='ChapterVersion'||sourceRef.value.kind!=='ProjectSpec'||sourceRef.value.id!==props.project.id||sourceRef.value.version!==String(props.project.spec_revision)||edgeAction.value!=='REVIEW_REQUIRED'||modifyOpen.value||Object.values(assignees.value).some(Boolean)||closingTask.value||closureVersion.value||closureReceipt.value||closureDecision.value||preflightChange.value||preflightSnapshot.value!==preflightReceiptBaseline.value||recoverySnapshot.value||recoveryExport.value||alertSignal.value||alertResponsible.value||alertAction.value!=='notify'||trialStart.value||trialEnd.value||targetSamples.value!==10||minimumSamples.value!==10||includedUsers.value.length||Object.values(decisionOwners.value).some(Boolean)||trialSupport.value||trialRedaction.value||trialExit.value||selectedEvidenceKey.value||sampleOwnershipConfirmed.value||JSON.stringify(sampleForm.value)!==JSON.stringify(freshSampleForm())||runtimeScopeKind.value!=='project'||runtimeModeScope.value||alertSeverity.value||reviewingSample.value||reviewResult.value||humanDecision.value||wordEvidence.value||repairOwner.value||repairVersion.value||rerunEvidence.value||trialDecision.value
))
const preflightChanges=computed(()=>changes.value.filter(change=>['applied','stabilized'].includes(change.status)&&change.target_context_revision===props.project.current_context_revision))
function syncPreflightReceipt(next:string){const untouched=preflightSnapshot.value===preflightReceiptBaseline.value;if(!hasUnsavedChanges.value&&!busy.value&&untouched){preflightSnapshot.value=next}preflightReceiptBaseline.value=next}
watch(()=>props.preflightReceiptId,next=>syncPreflightReceipt(next || ''))
watch(hasUnsavedChanges,value=>emit('dirty',value),{immediate:true})
watch(()=>props.project.spec_revision,(next,previous)=>{if(sourceRef.value.kind==='ProjectSpec'&&sourceRef.value.id===props.project.id&&sourceRef.value.version===String(previous)){sourceRef.value={...sourceRef.value,version:String(next)}}})
defineExpose({hasUnsavedChanges,busy})

watch(() => props.project.id, () => { epoch++; evidenceRequest++; runtimeReadReady.value=false; evidenceOptions.value=[];releaseOptions.value=[];exportOptions.value=[];evidenceReadError.value='';evidenceLoading.value=false;resetAllForms(); operationKeys.clear(); changes.value = []; edges.value = []; tasks.value = []; windows.value = []; assessment.value = null; stabilization.value = null; selectedWindow.value = null; trialReport.value = null; samples.value = []; decisions.value = []; controls.value = []; events.value = []; metrics.value = null; editId.value = ''; semanticDraft.value=[];claims.value=[];cards.value=[];assets.value=[];routes.value=[];semanticTarget.value='';claimConsent.value=false;routeInputs.value=[];reviewingSample.value = null; closingTask.value = null; reason.value = ''; sourceRef.value = { kind: 'ProjectSpec', id: props.project.id, version: String(props.project.spec_revision) }; void refresh() }, { immediate: true })
watch(currentIdentity,()=>{cards.value=[];epoch++;evidenceRequest++;runtimeReadReady.value=false;controls.value=[];events.value=[];alerts.value=[];metrics.value=null;windows.value=[];selectedWindow.value=null;samples.value=[];trialReport.value=null;decisions.value=[];reviewingSample.value=null;evidenceOptions.value=[];releaseOptions.value=[];exportOptions.value=[];sampleFacts.value=null;sampleFactsIdentity.value='';evidenceLoading.value=false;operationKeys.clear();error.value='当前身份已改变，已保留未提交输入；请重新读取并核对当前身份的证据。'})
watch(()=>props.project.current_context_revision,()=>{sampleFacts.value=null;sampleFactsIdentity.value='';runtimeReadReady.value=false})
watch(tab, () => { void refresh() })
</script>

<style scoped>
.ops-panel{max-width:1100px;margin:auto;padding:24px;background:#fbfcfa;color:#20352c;line-height:1.6}.ops-panel header,.actions,nav{display:flex;gap:10px;align-items:center;flex-wrap:wrap}.ops-panel header{justify-content:space-between;border-bottom:1px solid #dce4dd}.ops-panel h2{font-size:24px;margin:0}.ops-panel h3{font-size:17px;margin:0 0 10px}.ops-panel p{margin:8px 0}.ops-panel nav{margin:20px 0}.ops-panel button{border:1px solid #bfcec3;border-radius:7px;padding:8px 12px;background:#fff;color:inherit;cursor:pointer}.ops-panel button[aria-pressed=true]{background:#e8f0ea;border-color:#477058}.ops-panel button:disabled{opacity:.45;cursor:not-allowed}.ops-panel article,.ops-panel form,.ops-panel details{padding:18px;margin:16px 0;background:white;border:1px solid #dce4dd;border-radius:10px}.ops-panel label{display:flex;flex-direction:column;gap:5px;margin:0 0 12px;font-size:13px;color:#52645a}.ops-panel input,.ops-panel select,.ops-panel textarea{min-width:0;padding:9px;border:1px solid #cbd6ce;border-radius:6px;background:white;color:#20352c}.ops-panel textarea{min-height:80px;resize:vertical;font-family:inherit}.ops-panel pre{white-space:pre-wrap;overflow-wrap:anywhere;font-size:12px;background:#f4f7f3;padding:10px;border-radius:6px}.ops-panel input[type=checkbox]{align-self:flex-start}.ops-panel output{padding:10px;background:#f4f7f3;white-space:pre-wrap}.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(200px,1fr));gap:16px}.ops-panel fieldset{border:1px solid #dce4dd;border-radius:8px}.ops-panel table{width:100%;border-collapse:collapse;table-layout:fixed;overflow-wrap:anywhere;font-size:13px}.ops-panel th,.ops-panel td{padding:9px;text-align:left;vertical-align:top;border-bottom:1px solid #e7ece8}.ops-panel small{display:block;color:#708075}.notice,.error{padding:12px;border-radius:7px}.notice{background:#edf3ee}.error{background:#fcecea;color:#8b3025}.metrics article{display:flex;flex-direction:column;margin:0}.metrics strong{font-size:24px}.ops-panel :focus-visible{outline:2px solid #467158;outline-offset:3px}@media(max-width:640px){.ops-panel{padding:14px}.ops-panel table{font-size:11px}.ops-panel td,.ops-panel th{padding:6px}}
</style>
