<template>
<section class="g2-panel" aria-label="调研与证据">
 <div :inert="confirming !== null">
 <header><div><h2>调研与证据</h2><p>{{ project.name }} · 基线 {{ project.current_context_revision }}</p></div><button @click="emit('close')">关闭</button></header>
 <nav><button v-for="entry in tabs" :key="entry.id" :aria-pressed="tab === entry.id" @click="tab=entry.id">{{ entry.label }}</button><button :disabled="loading" @click="refresh">重新读取</button></nav>
 <p v-if="readonly" class="notice">当前仅可查看，实际操作仍由服务端核对权限和版本。</p><p v-if="error" role="alert" class="error">{{ error }}</p><p v-if="message" role="status" class="notice">{{ message }}</p>
 <p v-if="unknown.size" class="notice">有请求尚未取得确定回执。请读取已有结果，暂勿再次执行。</p>
 <template v-if="tab==='tasks'">
 <form v-if="canResearch" @submit.prevent="saveTask"><h3>{{ taskForm.id?'调整调研任务':'先写一个调研问题' }}</h3><label>要弄清什么<textarea v-model="taskForm.goal" required maxlength="6000" /></label><label>至少两条查询<textarea v-model="taskForm.queries" required placeholder="每行一条；分别查询定义、反例或边界" /></label>
 <div class="grid"><label>项目资料<select v-model="taskForm.assets" multiple><option v-for="asset in assets" :key="asset.id" :value="asset.id" :disabled="asset.processing_state!=='ready'">{{ asset.title }} · {{ label(asset.processing_state) }}</option></select></label><label>联网搜索<select v-model="taskForm.provider"><option value="">只查项目资料</option><option v-for="item in providers" :key="item.id" :value="item.id">{{ item.name }}</option></select></label></div>
 <details><summary>范围与预期产物</summary><label>章节<select v-model="taskForm.chapter"><option value="">项目范围</option><option v-for="chapter in chapters" :key="chapter.id" :value="chapter.id">{{ chapter.title }}</option></select></label><label>问题类型<input v-model="taskForm.type" /></label><label>调研范围<input v-model="taskForm.scope" required /></label><label>排除项<textarea v-model="taskForm.exclusions" placeholder="每行一项" /></label><label>预期产物<input v-model="taskForm.output" /></label></details>
 <button :disabled="busy('task-save')">保存草稿</button><button type="button" @click="resetTask">新建另一任务</button></form>
 <p v-if="!tasks.length&&!loading">还没有调研任务。草稿课题也可调研。</p>
 <article v-for="task in tasks" :key="task.id"><h3>{{ task.goal }}</h3><p>{{ label(task.status) }}{{ task.paused?' · 已暂停':'' }} · 基线 {{ task.basis.context_revision }}</p><details><summary>范围与查询</summary><p>{{ task.scope }}</p><ol><li v-for="query in task.queries" :key="query">{{ query }}</li></ol><p>排除 {{ task.exclusions.join('；')||'未设置' }}</p></details><div class="actions">
 <button v-if="canResearch&&task.status!=='running'" @click="editTask(task)">编辑为草稿</button><button v-if="canResearch&&task.status!=='running'&&task.status!=='approved'" @click="approveTask(task)">批准本次范围</button><button v-if="canResearch&&task.status==='approved'" :disabled="busy('run-'+task.id)||unknown.has('run-'+task.id)" @click="runTask(task)">运行已批准任务</button>
 <button v-if="canResearch&&task.status==='running'&&!task.paused" @click="controlTask(task,'pause')">暂停下一轮</button><button v-if="canResearch&&task.paused" @click="controlTask(task,'resume')">继续剩余查询</button><button v-if="canResearch&&['running','approved'].includes(task.status)" @click="controlTask(task,'cancel')">终止并保留结果</button><button v-if="task.latest_run_id" @click="openRun(task.latest_run_id)">查看结果</button></div></article>
 <section v-if="selectedRun"><h3>本次结果 · {{ label(selectedRun.status) }}</h3><p>基线 {{ selectedRun.basis.context_revision }} · {{ selectedRun.run_mode }} · {{ selectedRun.correlation_id }}</p><p v-if="selectedRun.failure_code" class="error">{{ selectedRun.failure_code }}；已有结果保留。</p><details><summary>查询过程</summary><ol><li v-for="round in selectedRun.rounds" :key="round.number">{{ round.query }} · {{ label(round.status) }} {{ round.failure_code }}</li></ol></details>
 <article v-for="result in selectedRun.results" :key="result.id"><h4>{{ result.title }}</h4><p>{{ levelLabel(result.level) }} · {{ label(result.status) }}</p><p class="reply">{{ result.summary }}</p><small>作者 {{ result.author||'未核实' }} · 日期 {{ result.published_at||'未知' }} · {{ result.uncertainty }}</small><p>{{ result.quality_note }}</p><div class="actions"><a v-if="safeResearchUrl(result.url)" :href="safeResearchUrl(result.url)||undefined" target="_blank" rel="noopener noreferrer">打开网页</a><button v-if="result.url" @click="inspectResult(result)">读取网页原文</button><button v-if="result.source_id" @click="openSource(result.source_id)">打开当前片段</button><button v-if="canResearch" @click="prepareImport(result)">保存资料候选</button></div></article></section>
 </template>
 <template v-if="tab==='sources'">
 <form v-if="importResult&&canResearch" @submit.prevent="saveImport"><h3>资料候选 · {{ importResult.title }}</h3><p>{{ levelLabel(importResult.level) }}；全文导入需先读原文并许可模型处理。</p><label>使用目的<input v-model="importForm.purpose" required /></label><div class="grid"><label>保存方式<select v-model="importForm.mode"><option value="metadata_only">只存来源信息</option><option value="link_excerpt">链接与摘录</option><option value="full_text" :disabled="!!importResult.url&&importResult.status!=='inspected_original'">原文入库</option></select></label><label>目标知识库<select v-model="importForm.kb"><option value="">选择已有可写知识库</option><option v-for="kb in knowledgeBases" :key="kb.id" :value="kb.id">{{ kb.name }}</option></select></label></div>
 <details open><summary>保存范围与许可</summary><label>保存范围<select v-model="importForm.scope"><option value="personal">仅本人</option><option value="project">当前项目</option><option value="team">团队范围（仍须现有授权）</option></select></label><label>版权或使用限制<input v-model="importForm.license" required /></label><label class="check"><input v-model="importForm.model" type="checkbox" />允许当前资料版本用于解析与模型输入（可能使用知识库的远程模型）</label></details><button :disabled="busy('import-create')">保存待确认候选</button><button type="button" @click="importResult=null">取消</button></form>
 <article v-for="candidate in imports" :key="candidate.id"><h3>{{ candidate.title }}</h3><p>{{ label(candidate.status) }} · {{ levelLabel(candidate.level) }} · {{ candidate.scope }} · {{ candidate.save_mode }}</p><p>{{ candidate.purpose }} · 模型使用 {{ candidate.model_use_allowed?'已许可':'未许可' }}</p><p v-if="candidate.failure_code" class="error">{{ candidate.failure_code }}</p><div class="actions"><button v-if="canResearch&&candidate.status==='awaiting_consent'" :disabled="unknown.has('import-'+candidate.id)" @click="decideImport(candidate,true)">明确确认保存</button><button v-if="canResearch&&candidate.status==='awaiting_consent'" @click="decideImport(candidate,false)">拒绝</button><button v-if="candidate.knowledge_id&&candidate.status!=='imported'" @click="refreshImport(candidate)">读取处理进度</button><button v-if="candidate.source_id" @click="openSource(candidate.source_id)">查看片段</button></div></article>
 <form @submit.prevent="openSource(sourceId)"><label>打开已有来源编号<input v-model="sourceId" required /></label><button>读取当前片段</button></form>
 <section v-if="source"><h3>当前资料片段</h3><p>{{ source.locator }} · 资料版本 {{ source.asset_revision }} · {{ label(source.status) }}</p><p>选中需要引用的文字。是否能回指原文，由服务端保存时检查。</p><pre ref="sourceText" class="source-text" @mouseup="captureSelection" @keyup="captureSelection">{{ source.quoted_text }}</pre>
 <form v-if="canEvidence&&source.status==='available'" @submit.prevent="saveAnchor"><label>已选摘录<output>{{ selection?.text||'请选中文字' }}</output></label><details><summary>定位与识别方式</summary><div class="grid"><label>页码<input v-model.number="anchorForm.page" type="number" min="1" /></label><label>节标题<input v-model="anchorForm.section" /></label><label>段落<input v-model.number="anchorForm.paragraph" type="number" min="1" /></label><label>识别方式<select v-model="anchorForm.extraction"><option value="native_text">原生文本</option><option value="ocr">OCR</option><option value="manual_correction">人工校对 OCR</option></select></label></div><label v-if="anchorForm.extraction!=='native_text'">OCR 置信度<input v-model.number="anchorForm.confidence" type="number" min="0" max="1" step="0.01" /></label><template v-if="anchorForm.extraction==='manual_correction'"><label>校对文字<textarea v-model="anchorForm.corrected" required /></label><label>校对原因<input v-model="anchorForm.correction" required /></label></template></details>
 <label class="check"><input v-model="anchorForm.checked" type="checkbox" />我已打开原文，核对定位与摘录</label><button :disabled="!selection||!anchorForm.checked||busy('anchor-save')">保存人工核对定位</button></form></section>
 <details v-if="!readonly && owner"><summary>调整资料使用范围</summary>
<form @submit.prevent="proposePolicy"><label>当前资料<select v-model="policyAsset" required @change="loadPolicy"><option value="">选择已有资料</option><option v-for="asset in assets" :key="asset.id" :value="asset.id">{{ asset.title }}</option></select></label>
<template v-if="assetPolicy"><p>资料版本 {{ assetPolicy.asset_revision }} · 范围策略版本 {{ assetPolicy.policy_version }}</p><label>使用目的<input v-model="policyForm.purpose" :required="policyForm.model" /></label><label>范围<select v-model="policyForm.scope"><option value="personal">仅本人</option><option value="project">当前项目</option><option value="team">团队范围（仍须既有知识库授权）</option></select></label><label>额外限定人员<select v-model="policyForm.users" multiple><option v-for="member in project.members.filter(item=>!item.status||item.status==='active')" :key="member.user_id" :value="member.user_id">{{ member.user_id }}</option></select></label><label class="check"><input v-model="policyForm.model" type="checkbox" />明确许可当前资料版本用于模型输入</label><p>将创建拟议变更，之后由 Owner 评估并应用。不会新增知识库授权；应用后原定位与卡片需要复核。</p><button :disabled="busy('policy-propose')">预览并起草范围变更</button></template></form></details>

<h3>已保存定位</h3><p v-if="!anchors.length&&!loading">搜索摘要不能直接变成正式证据。</p><article v-for="anchor in anchors" :key="anchor.id"><h4>{{ anchor.locator }}</h4><p>{{ label(anchor.status) }} · v{{ anchor.version }} · 资料版本 {{ anchor.asset_revision }} · {{ anchor.extraction_type }}</p><button @click="openSource(anchor.source_id)">核对当前片段</button><button v-if="canEvidence" @click="reviseAnchor(anchor)">重新选文并修订</button></article>
 </template>

 <template v-if="tab==='evidence'">
 <p v-if="project.status!=='active'" class="notice">先确认课题研究条件，再创建正式主张与证据卡。草稿阶段可继续调研和摘录。</p>
 <details v-if="canEvidence&&project.status==='active'" :open="!!claimForm.id"><summary>{{ claimForm.id?'修订主张':'写下要检验的主张' }}</summary><form @submit.prevent="saveClaim"><label>主张<textarea v-model="claimForm.text" required /></label><label>适用范围<input v-model="claimForm.scope" required /></label><label>状态<select v-model="claimForm.status"><option value="active">当前主张</option><option value="disputed">存在争议</option><option value="superseded">已替代</option></select></label><label>解释与边界<textarea v-model="claimForm.explanation" /></label><button>预览并保存主张</button><button type="button" @click="resetClaim">新写主张</button></form></details>
 <article v-for="claim in claims" :key="claim.id"><h3>{{ claim.text }}</h3><p>{{ label(claim.status) }} · v{{ claim.version }} · {{ claim.scope }}</p><p>{{ claim.explanation }}</p><button v-if="canEvidence" @click="editClaim(claim)">修订</button></article>
 <form v-if="canEvidence&&project.status==='active'" @submit.prevent="saveCard"><h3>{{ cardForm.id?'修订证据卡':'将定位关联到主张' }}</h3><label>原文定位<select v-model="cardForm.anchors" multiple required><option v-for="anchor in anchors" :key="anchor.id" :value="anchor.id" :disabled="anchor.status!=='CURRENT'">{{ anchor.locator }} · v{{ anchor.version }}</option></select></label><label>主张<select v-model="cardForm.claim"><option value="">先保存未关联的候选卡</option><option v-for="claim in claims" :key="claim.id" :value="claim.id" :disabled="claim.status!=='active'">{{ claim.text }}</option></select></label>
 <div class="grid"><label>关系<select v-model="cardForm.support"><option value="supports">支持</option><option value="refutes">反驳</option><option value="qualifies">限定条件</option><option value="background">背景资料</option></select></label><label>适用章节<select v-model="cardForm.chapters" multiple required><option v-for="chapter in chapters" :key="chapter.id" :value="chapter.id">{{ chapter.title }}</option></select></label></div><label>适用范围<input v-model="cardForm.scope" required /></label><details open><summary>局限与缺口</summary><label>资料局限<textarea v-model="cardForm.limits" required /></label><label>尚缺证据<textarea v-model="cardForm.gaps" /></label><label>替代解释<textarea v-model="cardForm.alternative" /></label></details><button>预览并保存候选卡</button><button type="button" @click="resetCard">新建另一卡</button></form>
 <label>显示卡片<select v-model="cardFilter"><option value="">全部状态</option><option value="CANDIDATE">待确认</option><option value="CONFIRMED">已人工确认</option><option value="NEEDS_REVIEW">需复核</option><option value="INSUFFICIENT">证据不足</option></select></label>
 <article v-for="card in visibleCards" :key="card.id" :data-card-id="card.id"><h3>{{ supportLabel(card.support_type) }} · {{ claimText(card) }}</h3><p>{{ label(card.status) }} · v{{ card.version }} · 基线 {{ card.basis.context_revision }}</p><p>{{ card.scope }} · {{ card.limitations }}</p><p v-if="card.gaps">缺口：{{ card.gaps }}</p><p v-if="card.invalid_reason" class="error">{{ card.invalid_reason }}</p><p v-if="card.confirmation">{{ card.confirmation.meaning }} · {{ card.confirmation.confirmed_by }}</p><details><summary>定位与版本依据</summary><ul><li v-for="ref in card.anchor_refs" :key="ref.id">{{ anchorName(ref.id) }} · v{{ ref.version }} <button @click="openAnchor(ref.id)">打开片段</button></li></ul></details><div class="actions"><button v-if="canEvidence" @click="editCard(card)">修订候选</button><button v-if="canConfirmCard(card)&&card.status!=='CONFIRMED'" @click="confirmCard(card)">人工确认适用范围</button><button v-if="canEvidence" @click="reviewCard(card,'INSUFFICIENT')">证据不足</button><button v-if="canEvidence" @click="reviewCard(card,'NEEDS_REVIEW')">要求复核</button><button :disabled="card.status!=='CONFIRMED'" :aria-pressed="evidenceSelected(card)" @click="emit('useEvidence',[{id:card.id,version:card.version}])">{{evidenceSelected(card)?'取消正文选择':'选择用于正文'}}</button></div></article>
 </template>
 <template v-if="tab==='agents'">
 <label>助手<select v-model="agent"><option value="task_assistant">任务助手</option><option value="research_agent">调研助手</option></select></label><div class="actions"><select v-model="sessionId" aria-label="独立会话"><option value="">选择当前助手的会话</option><option v-for="session in sessions" :key="session.id" :value="session.id">{{ session.created_at }}</option></select><button v-if="canAgent" @click="newSession">新建独立会话</button><button v-if="sessionId" @click="loadTurns">读取会话回执</button></div>
 <form v-if="canAgent&&sessionId" @submit.prevent="sendTurn"><h3>本轮可用材料</h3><div class="grid"><label>章节<select v-model="contextForm.chapter"><option value="">项目范围</option><option v-for="chapter in chapters" :key="chapter.id" :value="chapter.id">{{ chapter.title }}</option></select></label><label>调研任务<select v-model="contextForm.task"><option value="">不关联</option><option v-for="task in tasks" :key="task.id" :value="task.id">{{ task.goal }}</option></select></label></div>
 <label>原文定位<select v-model="contextForm.anchors" multiple><option v-for="anchor in anchors" :key="anchor.id" :value="anchor.id" :disabled="anchor.status!=='CURRENT'">{{ anchor.locator }}</option></select></label><label>证据卡<select v-model="contextForm.cards" multiple><option v-for="card in cards" :key="card.id" :value="card.id" :disabled="card.status!=='CONFIRMED'">{{ claimText(card) }}</option></select></label><button type="button" @click="previewContext">预览当前上下文</button>
 <section v-if="contextPreview" class="notice"><p>{{ contextPreview.authorization_state }} · 基线 {{ contextPreview.context_revision }}</p><ul><li v-for="ref in contextPreview.source_refs" :key="ref.source_id">{{ ref.locator }} · v{{ ref.asset_revision }} · 模型使用 {{ ref.model_use_allowed?'已许可':'未许可' }}</li><li v-for="constraint in contextPreview.output_constraints" :key="constraint">{{ constraint }}</li></ul></section>
 <label class="check"><input v-model="contextForm.consent" type="checkbox" />我已核对本轮敏感内容，确认发送选定上下文</label><label>本轮问题<textarea v-model="userInput" required /></label><button :disabled="busy('turn-'+sessionId)||unknown.has('turn-'+sessionId)||!contextForm.consent">发送一次</button></form>
 <article v-for="turn in turns" :key="turn.id"><p>{{ label(turn.status) }} · {{ turn.run_mode }} · 基线 {{ turn.context.context_revision }}</p><h4>{{ turn.user_input||'当前来源权限受限，原内容已隐藏' }}</h4><p class="reply">{{ turn.reply }}</p><p v-if="turn.failure_code" class="error">{{ turn.failure_code }}</p><button v-if="canAgent&&project.status==='active'&&turn.suggestions?.change_draft" @click="prepareAgentChangeDraft(turn)">编辑为变更草稿提案</button><button v-if="canResearch&&turn.suggestions?.task_card" @click="adoptTaskSuggestion(turn)">放入可编辑任务草稿</button><button v-if="canResearch&&turn.status==='completed'" @click="saveConversationCandidate(turn)">另存为 AI 候选</button><button v-if="canUseSpecCandidate&&eligibleSpecTurn(turn)" :disabled="!!specCandidate || busy('agent-spec-save')" @click="prepareSpecCandidate(turn)">用于研究条件候选</button></article>
 <section v-if="specCandidate" class="agent-spec-candidate" aria-labelledby="agent-spec-candidate-title">
  <h3 id="agent-spec-candidate-title">会话回复 → 待确认研究条件</h3>
  <p>真实会话 {{specCandidate.turn.session_id}} · 轮次 {{specCandidate.turn.id}} / v{{specCandidate.turn.version}} · {{agentSpecRunModeLabel(specCandidate.turn.run_mode)}}<br>本轮来源 {{(specCandidate.turn.context.source_refs||[]).length}} 份；服务端保存时重新核对当前来源许可。</p>
  <p class="notice">这里只保存待人工确认的研究条件，不运行模型、不自动立项，也不证明研究内容正确。原样回复显示“AI 生成”；编辑或提取后显示“AI 辅助人工整理”，最终来源由服务端核对。</p>
  <ul v-if="specCandidate.turn.context.source_refs?.length"><li v-for="ref in specCandidate.turn.context.source_refs" :key="ref.source_id">{{ref.locator || ref.source_id}} · 资料版本 {{ref.asset_revision}}</li></ul>
  <p v-if="!canUseSpecCandidate" class="notice">当前身份尚未取得这个草稿的管理许可。保存入口暂不可用，输入保留。</p>
  <p v-if="specCandidateUnknown" role="alert" class="notice">保存结果尚未确定，输入和原请求已保留。不会自动再次保存；只能人工重试同一请求，不能改字段或关联新版本。</p>
  <section v-if="specCandidateStale" class="notice" role="alert"><h4>条件或课题基线已变化，候选输入保留</h4><p>原条件版本 {{specCandidate.basis.specRevision}} / 课题版本 {{specCandidate.basis.projectVersion}}；当前条件 {{project.spec_revision}} / 课题 {{project.project_version}}。不会自动覆盖或绑定新版本。</p></section>
  <label>当前模板字段<select ref="specCandidateFieldInput" v-model="specCandidate.field" :disabled="busy('agent-spec-save') || specCandidateUnknown" required><option value="">请选择一个字段</option><option v-for="field in specCandidateFields" :key="field.field_id" :value="field.field_id">{{field.label}}（{{field.field_id}}）</option></select></label>
  <p>当前保存值：<output>{{project.spec[specCandidate.field] || '（空）'}}</output></p>
  <label>候选值<textarea v-model="specCandidate.value" :disabled="busy('agent-spec-save') || specCandidateUnknown" maxlength="10000" required /></label>
  <p>候选来源：{{specCandidate.value.trim()===specCandidate.turn.reply.trim() ? 'AI 生成（原样回复）' : 'AI 辅助人工整理（已编辑或提取）'}} · 待人工确认</p>
  <div class="actions"><button :disabled="busy('agent-spec-read') || busy('agent-spec-save') || specCandidateUnknown" @click="readSpecCandidateBasis">读取当前条件与会话</button><button v-if="specCandidateStale" :disabled="!specCandidateFresh || busy('agent-spec-save') || specCandidateUnknown || !canUseSpecCandidate" @click="reassociateSpecCandidate">已比较，保留输入并关联当前版本</button><button :disabled="!canSaveSpecCandidate" @click="confirmSpecCandidate">{{specCandidateUnknown ? '人工重试同一保存请求' : '明确确认并保存候选字段'}}</button><button :disabled="busy('agent-spec-save')" @click="discardSpecCandidate">放弃此候选输入</button></div>
 </section>
 <details v-if="canAgent" :open="actionForm.action==='create_change_set'"><summary>提出需要人工确认的动作</summary><form @submit.prevent="proposeAction"><label>动作<select v-model="actionForm.action"><option v-for="action in actionKinds" :key="action" :value="action">{{ actionLabel(action) }}</option></select></label>
 <template v-if="actionForm.action==='save_spec'"><label>研究条件<select v-model="actionForm.field" required><option value="">选择条件</option><option v-for="(_,field) in project.spec" :key="field" :value="field">{{ field }}</option></select></label><label>拟议值<textarea v-model="actionForm.value" required /></label></template><template v-if="actionForm.action==='create_change_set'"><p>仅在明确确认动作后创建待评估草稿，正式条件与版本保持不变；之后另行评估，并由课题所有者正式应用。AI 会话来源随新值保存。</p><label>拟议字段变更（可编辑 fields 和 reason）<textarea v-model="actionForm.changeDraft" required /></label>
  <fieldset><legend>建议复核章节（至少一章，实际影响以评估为准）</legend><p>请核对模型预选或自行勾选。发送时以这里的勾选列表为准，JSON 中的 affected_chapter_ids 不会覆盖勾选结果；这不授予章节写入权限。</p><label v-for="chapter in changeDraftChapters" :key="chapter.id" class="check"><input v-model="changeDraftChapterIDs" type="checkbox" :value="chapter.id" :disabled="loading" />{{ chapter.title }}</label><p v-if="!changeDraftChapters.length">当前没有可选章节，请重新读取当前课题与授权。</p></fieldset></template><label v-if="actionForm.action==='bind_asset'">已有知识编号<input v-model="actionForm.knowledge" required /></label>
 <label v-if="actionForm.action==='confirm_evidence_card'">证据卡<select v-model="actionForm.target" required><option value="">选择卡片</option><option v-for="card in cards" :key="card.id" :value="card.id">{{ claimText(card) }}</option></select></label><label v-if="actionForm.action==='apply_change_set'">已评估变更<select v-model="actionForm.target" required><option value="">选择变更</option><option v-for="change in changes.filter(item=>item.status==='assessed')" :key="change.id" :value="change.id">{{ change.reason }}</option></select></label><label v-if="actionForm.action==='commit_working_copy'">章节<select v-model="actionForm.target" required><option value="">选择章节</option><option v-for="chapter in chapters" :key="chapter.id" :value="chapter.id">{{ chapter.title }}</option></select></label><label v-if="actionForm.action==='export'">冻结快照<select v-model="actionForm.target" required><option value="">提案时重新检查当前性</option><option v-for="release in releases" :key="release.id" :value="release.id">{{ release.created_at }} · {{ release.id }}</option></select></label><p v-if="actionForm.action==='create_evidence_card'">使用证据页当前填写的定位、主张、章节与局限。</p><label>预期影响<textarea v-model="actionForm.effect" required /></label><button>预览并保存提案</button></form></details>
 <article v-for="proposal in proposals" :key="proposal.id"><h3>{{ actionLabel(proposal.action) }}</h3><p>{{ label(proposal.status) }} · 基线 {{ proposal.basis.context_revision }}</p><p>{{ proposal.expected_effect }} · {{ proposal.reversibility }}</p><details><summary>范围与版本预览</summary><p>对象 {{ proposal.target_id }} · 来源 {{ proposal.source_ids.join('、')||'无直接来源' }}</p><pre>{{ JSON.stringify(proposal.input,null,2) }}</pre></details><p v-if="proposal.failure_code" class="error">{{ proposal.failure_code }}</p><div v-if="proposal.status==='pending'&&canAgent" class="actions"><button :disabled="busy('action-'+proposal.id)||unknown.has('action-'+proposal.id)" @click="decideAction(proposal,true)">明确确认并执行</button><button @click="decideAction(proposal,false)">拒绝提案</button></div></article>
 </template></div>
 <dialog ref="confirmDialog" @cancel.prevent="cancelConfirmation"><form v-if="confirming" @submit.prevent="executeConfirmation"><h3>{{ confirming.title }}</h3><p class="reply">{{ confirming.summary }}</p><label>本次决定原因<textarea v-model="confirmReason" required /></label><label class="check"><input v-model="confirmChecked" type="checkbox" />我已核对以上范围、版本和影响</label><button :disabled="!confirmChecked||!confirmReason.trim()||confirmingBusy">明确确认</button><button type="button" :disabled="confirmingBusy" @click="cancelConfirmation">返回修改</button></form></dialog>
</section>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, reactive, ref, watch } from 'vue'
import * as api from '@/api/lingdoc/g2'
import { getSource, getWorkingCopy, listAssets, listChapters, listChangeSets } from '@/api/lingdoc/workspace'
import { useAuthStore } from '@/stores/auth'
import { getEffectivePermission } from '@/api/lingdoc/permissions'
import type { EffectivePermission } from '@/api/lingdoc/permissions'
import type { Asset, Chapter, ChangeSet, Project, Source } from '@/api/lingdoc/workspace'
import { getRelease, listReleases } from '@/api/lingdoc/delivery'
import type { ReleaseSnapshot } from '@/api/lingdoc/delivery'
import { listWebSearchProviders } from '@/api/web-search-provider'
import { listKnowledgeBases } from '@/api/knowledge-base'
import { G2OperationKeys, G2ResponseEpoch, acceptedAgentSpecCandidate, agentSpecCandidateBasisCurrent, buildAgentSpecCandidateInput, agentSpecRunModeLabel, eligibleAgentSpecTurn, pinAgentSpecCandidateBasis, nonemptyResearchQueries, safeResearchUrl, selectedRuneRange } from './g2UiState'
import type { AgentSpecCandidateBasis } from './g2UiState'
const auth=useAuthStore()
const props=withDefaults(defineProps<{project:Project;readonly?:boolean;currentUserId?:string;selectedEvidence?:api.G2VersionRef[]}>(),{readonly:true,currentUserId:'',selectedEvidence:()=>[]})
const emit=defineEmits<{close:[];changed:[];dirty:[value:boolean];useEvidence:[refs:api.G2VersionRef[]]}>()
const tabs=[{id:'tasks',label:'1 调研'},{id:'sources',label:'2 摘录'},{id:'evidence',label:'3 证据'},{id:'agents',label:'助手与动作'}]
const tab=ref('tasks'),error=ref(''),message=ref(''),loading=ref(false)
const tasks=ref<api.ResearchTask[]>([]),runs=ref<api.ResearchRun[]>([]),anchors=ref<api.SourceAnchor[]>([]),claims=ref<api.ResearchClaim[]>([]),cards=ref<api.EvidenceCard[]>([]),imports=ref<api.ImportCandidate[]>([]),proposals=ref<api.ActionProposal[]>([])
const chapters=ref<Chapter[]>([]),assets=ref<Asset[]>([]),changes=ref<ChangeSet[]>([]),releases=ref<ReleaseSnapshot[]>([])
const providers=ref<Array<{id:string;name:string}>>([]),knowledgeBases=ref<Array<{id:string;name:string}>>([])
const selectedRun=ref<api.ResearchRun|null>(null),source=ref<Source|null>(null),sourceId=ref(''),sourceText=ref<HTMLElement|null>(null),selection=ref<{start:number;end:number;text:string}|null>(null)
const epoch=new G2ResponseEpoch(),keys=new G2OperationKeys(),pending=reactive(new Set<string>()),unknown=reactive(new Set<string>())
const unknownTurnIDs=new Map<string,Set<string>>()
const ownMember=computed(()=>props.project.members.find(member=>member.user_id===props.currentUserId&&(!member.status||member.status==='active')))
const owner=computed(()=>ownMember.value?.role==='owner')
const functional=(role:'author'|'researcher'|'reviewer')=>ownMember.value?.function_roles?.includes(role)||false
const canResearch=computed(()=>!props.readonly&&(owner.value||functional('researcher')||functional('author')))
const canEvidence=computed(()=>!props.readonly&&(owner.value||functional('researcher')))
const canAgent=computed(()=>!props.readonly&&(owner.value||functional('researcher')||functional('author')||functional('reviewer')))
const busy=(slot:string)=>pending.has(slot)
const expectation=(version=0):api.G2Expectation=>({expected_project_version:props.project.project_version,expected_context_revision:props.project.current_context_revision,expected_version:version})
const taskForm=reactive({id:'',version:0,goal:'',queries:'',assets:[] as string[],provider:'',chapter:'',type:'',scope:'当前课题研究范围',exclusions:'',output:'候选资料、反例与证据缺口'})
const anchorForm=reactive({id:'',version:0,page:'' as number|'',section:'',paragraph:'' as number|'',extraction:'native_text' as api.SourceAnchor['extraction_type'],confidence:0,checked:false,corrected:'',correction:''})
const claimForm=reactive({id:'',version:0,text:'',scope:'',status:'active' as api.SaveResearchClaimInput['status'],explanation:''})
const cardForm=reactive({id:'',version:0,anchors:[] as string[],claim:'',support:'supports',chapters:[] as string[],scope:'',limits:'',gaps:'',alternative:''})
const taskSaved=ref(JSON.stringify(taskForm)),claimSaved=ref(JSON.stringify(claimForm)),cardSaved=ref(JSON.stringify(cardForm))
const cardFilter=ref(''),visibleCards=computed(()=>cards.value.filter(card=>!cardFilter.value||card.status===cardFilter.value))
const importResult=ref<api.ResearchResult|null>(null),importForm=reactive({purpose:'',mode:'metadata_only' as api.ImportCandidate['save_mode'],scope:'personal' as api.ImportCandidate['scope'],license:'',model:false,kb:''})
type TurnCopySource=Pick<api.AgentTurn,'id'|'project_id'|'session_id'|'version'>
const taskDraftSource=ref<TurnCopySource|null>(null),turnReadSequences=new Map<string,number>()
const agent=ref<api.LingDocAgent>('task_assistant'),sessions=ref<api.AgentSession[]>([]),sessionId=ref(''),turns=ref<api.AgentTurn[]>([]),userInput=ref('')
const contextForm=reactive({chapter:'',task:'',anchors:[] as string[],cards:[] as string[],consent:false}),contextPreview=ref<api.AgentContextPackage|null>(null)
type SpecCandidate={field:string;value:string;turn:api.AgentTurn;basis:AgentSpecCandidateBasis}
const specCandidate=ref<SpecCandidate|null>(null),specCandidateFresh=ref<{project:Project;turn:api.AgentTurn}|null>(null),specCandidateUnknown=ref(false)
const specCandidateFieldInput=ref<HTMLSelectElement|null>(null)
const specCandidateAttempt=ref<{input:api.SaveAgentSpecCandidateInput;key:string}|null>(null)
const specManagePermission=ref<EffectivePermission|null>(null)
let specPermissionSequence=0,specReadSequence=0
const candidateTenant=()=>String(auth.effectiveTenantId ?? '')
const canUseSpecCandidate=computed(()=>props.project.status==='draft'&&!props.readonly&&!!props.currentUserId&&auth.currentUserId===props.currentUserId&&specManagePermission.value?.allowed===true&&specManagePermission.value.capability==='manage'&&specManagePermission.value.project_version===props.project.project_version)
const specCandidateFields=computed(()=>props.project.template_copy?.definition.fields || [])
const specCandidateStale=computed(()=>!!specCandidate.value&&!agentSpecCandidateBasisCurrent(specCandidate.value.basis,props.project,props.currentUserId,candidateTenant()))
const canSaveSpecCandidate=computed(()=>canUseSpecCandidate.value&&!!specCandidate.value&&!busy('agent-spec-save')&&!busy('agent-spec-read')&&(specCandidateUnknown.value ? !!specCandidateAttempt.value : !specCandidateStale.value&&!!specCandidate.value.field&&!!specCandidate.value.value.trim()&&eligibleSpecTurn(specCandidate.value.turn)))
const actionKinds:api.ActionProposal['action'][]=['save_spec','create_change_set','bind_asset','create_evidence_card','confirm_evidence_card','apply_change_set','commit_working_copy','export']
const actionForm=reactive({action:'save_spec' as api.ActionProposal['action'],field:'',value:'',target:'',knowledge:'',effect:'',changeDraft:''})
const policyAsset=ref(''),assetPolicy=ref<api.ProjectAssetPolicy|null>(null)
const policyForm=reactive({scope:'personal' as api.ProjectAssetPolicy['scope'],purpose:'',users:[] as string[],model:false})
type Confirmation={title:string;summary:string;execute:(reason:string)=>Promise<void>;sourceTurn?:TurnCopySource}
const confirming=ref<Confirmation|null>(null),confirmDialog=ref<HTMLDialogElement|null>(null),confirmReason=ref(''),confirmChecked=ref(false),confirmingBusy=ref(false)
let oldFocus:HTMLElement|null=null,poll:ReturnType<typeof setTimeout>|undefined
const labels:Record<string,string>={draft:'草稿',approved:'已批准',running:'运行中',completed:'已完成',failed:'失败',cancelled:'已终止',stale:'历史结果，基线已变',ready:'已就绪',pending:'等待处理',awaiting_consent:'等待明确确认',importing:'处理中',imported:'已保存',processing:'处理中',available:'可读',unavailable:'不可读',inspected_original:'已读取网页原文',CURRENT:'当前定位',CANDIDATE:'候选证据',CONFIRMED:'已人工确认适用范围',NEEDS_REVIEW:'需复核',INSUFFICIENT:'证据不足',SUPERSEDED:'已替代',active:'当前',disputed:'有争议',superseded:'已替代',proposed:'待确认提案',confirmed:'已执行',rejected:'已拒绝',outcome_unknown:'结果待核对'}
const levels:Record<string,string>={candidate_evidence:'待核证据候选',background_lead:'背景线索',discovery_entry:'发现入口',unread_search_summary:'未读原文的搜索摘要',AI_GENERATED_CANDIDATE:'AI 生成候选，不能直接确认'}
const supports:Record<string,string>={supports:'支持',refutes:'反驳',qualifies:'限定条件',background:'背景'}
const actions:Record<string,string>={create_change_set:'创建待评估变更草稿',save_spec:'保存研究条件',bind_asset:'绑定已有资料',create_evidence_card:'保存候选证据卡',confirm_evidence_card:'人工确认证据卡',apply_change_set:'应用已评估变更',commit_working_copy:'提交工作副本',export:'导出冻结快照'}
const label=(value:string)=>labels[value]||value,levelLabel=(value:string)=>levels[value]||value,supportLabel=(value:string)=>supports[value]||value,actionLabel=(value:string)=>actions[value]||value
const evidenceSelected=(card:api.EvidenceCard)=>props.selectedEvidence.some(ref=>ref.id===card.id&&ref.version===card.version)
const claimText=(card:api.EvidenceCard)=>claims.value.find(claim=>claim.id===card.claim_ref?.id)?.text||'尚未关联主张'
const anchorName=(id:string)=>anchors.value.find(anchor=>anchor.id===id)?.locator||'原文定位'
function canConfirmCard(card:api.EvidenceCard){return !props.readonly&&(owner.value||functional('reviewer')&&card.scope_chapter_ids.length>0&&card.scope_chapter_ids.every(id=>ownMember.value?.function_scopes?.reviewer?.includes(id)))}
function responseRows(value:unknown):Array<{id:string;name:string}>{
 const result=value as {data?:unknown},list=Array.isArray(result?.data)?result.data:[]
 return list.filter((item):item is {id:string;name?:string}=>!!item&&typeof item==='object'&&typeof item.id==='string').map(item=>({id:item.id,name:item.name||item.id}))
}
function resetTask(){taskDraftSource.value=null;Object.assign(taskForm,{id:'',version:0,goal:'',queries:'',assets:[],provider:'',chapter:'',type:'',scope:'当前课题研究范围',exclusions:'',output:'候选资料、反例与证据缺口'});taskSaved.value=JSON.stringify(taskForm)}
function resetClaim(){Object.assign(claimForm,{id:'',version:0,text:'',scope:'',status:'active',explanation:''});claimSaved.value=JSON.stringify(claimForm)}
function resetCard(){Object.assign(cardForm,{id:'',version:0,anchors:[],claim:'',support:'supports',chapters:[],scope:'',limits:'',gaps:'',alternative:''});cardSaved.value=JSON.stringify(cardForm)}
function clearSensitive(){
 taskDraftSource.value=null;turnReadSequences.clear()
 tasks.value=[];runs.value=[];anchors.value=[];claims.value=[];cards.value=[];imports.value=[];proposals.value=[];assets.value=[];chapters.value=[];changes.value=[];releases.value=[];providers.value=[];knowledgeBases.value=[]
 source.value=null;selectedRun.value=null;selection.value=null;turns.value=[];sessions.value=[];sessionId.value='';userInput.value='';sourceId.value='';contextPreview.value=null;contextForm.consent=false;contextForm.anchors=[];contextForm.cards=[];contextForm.task='';contextForm.chapter='';importResult.value=null
 changeDraftTurn.value=null;changeDraftChapterIDs.value=[];actionForm.changeDraft='';specCandidate.value=null;specCandidateFresh.value=null;specCandidateAttempt.value=null;specCandidateUnknown.value=false;specManagePermission.value=null
 policyAsset.value='';assetPolicy.value=null;Object.assign(policyForm,{scope:'personal',purpose:'',users:[],model:false});loading.value=false;Object.assign(importForm,{purpose:'',license:'',model:false,kb:''});Object.assign(anchorForm,{id:'',version:0,checked:false,corrected:'',correction:''});Object.assign(actionForm,{target:'',field:'',value:'',knowledge:'',effect:''});resetTask();resetClaim();resetCard()
}
function report(cause:unknown){
 const item=cause as {message?:string;status?:number;$httpStatus?:number}
 if([401,403,404].includes(item?.$httpStatus||item?.status||0)){epoch.next();clearTimeout(poll);clearSensitive();keys.clear();confirmingBusy.value=false;cancelConfirmation()}
 error.value=item?.message||'操作未取得成功回执，请重新读取当前状态。'
}
async function refresh(){
 const token=epoch.capture(),id=props.project.id;loading.value=true;error.value=''
 const results=await Promise.allSettled([api.listResearchTasks(id),api.listResearchRuns(id),api.listSourceAnchors(id),api.listResearchClaims(id),api.listEvidenceCards(id),api.listImportCandidates(id),api.listActionProposals(id),listChapters(id),listAssets(id),listChangeSets(id),listReleases(id),listWebSearchProviders(),listKnowledgeBases()])
 if(!epoch.accepts(token))return
 const setters:Array<(data:any)=>void>=[value=>{tasks.value=value},value=>{runs.value=value},value=>{anchors.value=value},value=>{claims.value=value},value=>{cards.value=value},value=>{imports.value=value},value=>{proposals.value=value},value=>{chapters.value=value},value=>{assets.value=value},value=>{changes.value=value},value=>{releases.value=value.items||[]}]
 for(let index=0;index<results.length;index++){const result=results[index]!;if(result.status==='rejected'){report(result.reason);if(!epoch.accepts(token))break;continue}if(index<setters.length)setters[index]!((result.value as {data:unknown}).data);if(index===11)providers.value=responseRows(result.value);if(index===12)knowledgeBases.value=responseRows(result.value)}
 if(epoch.accepts(token)){if(selectedRun.value)selectedRun.value=runs.value.find(run=>run.id===selectedRun.value?.id)||null;for(const proposal of proposals.value)if(['completed','rejected','failed'].includes(proposal.status))unknown.delete('action-'+proposal.id);for(const item of imports.value)if(['imported','rejected','failed','processing'].includes(item.status))unknown.delete('import-'+item.id);for(const task of tasks.value)if(!['approved','running'].includes(task.status))unknown.delete('run-'+task.id);await refreshTurnCopies(token);if(epoch.accepts(token)){loading.value=false;schedulePoll()}}
}
async function write<T>(slot:string,input:unknown,call:(key:string)=>Promise<T>,long=false):Promise<T|undefined>{
 if(props.readonly||pending.has(slot)||unknown.has(slot))return
 const token=epoch.capture(),beforeTurns=new Set(turns.value.map(turn=>turn.id));pending.add(slot);error.value='';message.value=''
 try{const result=await call(keys.forRequest(slot,input));if(!epoch.accepts(token))return;const receipt=(result as {data?:{status?:string;failure_code?:string}}).data;if(long&&(receipt?.status==='outcome_unknown'||receipt?.failure_code==='outcome_unknown')){unknown.add(slot);if(slot.startsWith('turn-'))unknownTurnIDs.set(slot,beforeTurns)}else keys.accepted(slot);message.value='已收到服务端回执。';emit('changed');await refresh();return result}
 catch(cause){if(epoch.accepts(token)){const problem=cause as {status?:number;$httpStatus?:number;code?:string;error?:{code?:string}};const status=problem.$httpStatus||problem.status;if(long&&(!status||[408,502,503,504].includes(status)||(problem.code||problem.error?.code)==='outcome_unknown')){unknown.add(slot);if(slot.startsWith('turn-'))unknownTurnIDs.set(slot,beforeTurns)}report(cause)}}
 finally{pending.delete(slot)}
}
function requestConfirmation(title:string,summary:string,execute:Confirmation['execute'],sourceTurn?:TurnCopySource){if(confirmingBusy.value)return;confirming.value={title,summary,execute,sourceTurn:sourceTurn?{id:sourceTurn.id,project_id:sourceTurn.project_id,session_id:sourceTurn.session_id,version:sourceTurn.version}:undefined};confirmChecked.value=false;confirmReason.value='';oldFocus=document.activeElement as HTMLElement|null;void nextTick(()=>{confirmDialog.value?.showModal();confirmDialog.value?.querySelector('textarea')?.focus()})}
function cancelConfirmation(){if(confirmingBusy.value)return;confirmDialog.value?.close();confirming.value=null;oldFocus?.focus()}
async function executeConfirmation(){if(!confirming.value||!confirmChecked.value||!confirmReason.value.trim())return;const request=confirming.value;confirmingBusy.value=true;try{await request.execute(confirmReason.value.trim())}catch(cause){report(cause)}finally{confirmingBusy.value=false;cancelConfirmation()}}
function editTask(task:api.ResearchTask){taskDraftSource.value=null;Object.assign(taskForm,{id:task.id,version:task.version,goal:task.goal,queries:task.queries.join('\n'),assets:[...task.asset_ids],provider:task.provider_id||'',chapter:task.chapter_id||'',type:task.question_type,scope:task.scope,exclusions:task.exclusions.join('\n'),output:task.expected_output});tab.value='tasks';taskSaved.value=JSON.stringify(taskForm)}
async function saveTask(){
 const queries=nonemptyResearchQueries(taskForm.queries);if(queries.length<2||!taskForm.assets.length&&!taskForm.provider){error.value='请填写至少两条查询，并选择项目资料或联网搜索。';return}
 const input:api.SaveResearchTaskInput={...expectation(taskForm.version),task_id:taskForm.id||undefined,chapter_id:taskForm.chapter||undefined,goal:taskForm.goal,question_type:taskForm.type,scope:taskForm.scope,exclusions:nonemptyResearchQueries(taskForm.exclusions),queries,asset_ids:[...taskForm.assets],provider_id:taskForm.provider||undefined,expected_output:taskForm.output}
 const result=await write('task-save',input,key=>api.saveResearchTask(props.project.id,input,key));if(result){editTask(result.data);emit('dirty',false)}
}
function approveTask(task:api.ResearchTask){requestConfirmation('批准调研范围',task.goal+'\n查询：'+task.queries.join('；')+'\n范围：'+task.scope+'\n排除：'+task.exclusions.join('；'),async reason=>{const input={...expectation(task.version),confirmed:true,reason};await write('approve-'+task.id,input,key=>api.approveResearchTask(props.project.id,task.id,input,key))})}
async function runTask(task:api.ResearchTask,resumeRunID?:string){const input={...expectation(task.version),resume_run_id:resumeRunID},token=epoch.capture();setTimeout(()=>{if(epoch.accepts(token))void pollTasks()},800);const result=await write('run-'+task.id,input,key=>api.runResearchTask(props.project.id,task.id,input,key),true);if(result)selectedRun.value=result.data}
function controlTask(task:api.ResearchTask,action:'pause'|'resume'|'cancel'){requestConfirmation(action==='pause'?'暂停下一轮':action==='resume'?'继续剩余查询':'终止调研',task.goal+'\n保留已有结果和运行历史。',async reason=>{const fresh=(await api.listResearchTasks(props.project.id)).data.find(item=>item.id===task.id);if(!fresh)throw new Error('任务已不可见。');const input={...expectation(fresh.version),action,reason};const result=await write('control-'+task.id,input,key=>api.controlResearchTask(props.project.id,task.id,input,key));if(result&&action==='resume'&&result.data.latest_run_id)await runTask(result.data,result.data.latest_run_id)})}
function schedulePoll(){clearTimeout(poll);if(tasks.value.some(task=>task.status==='running'&&!task.paused))poll=setTimeout(()=>{void pollTasks()},3000)}
async function pollTasks(){const token=epoch.capture();try{const result=await api.listResearchTasks(props.project.id);if(epoch.accepts(token)){tasks.value=result.data;schedulePoll()}}catch(cause){if(epoch.accepts(token))report(cause)}}
async function openRun(id:string){const token=epoch.capture();try{const result=await api.getResearchRun(props.project.id,id);if(epoch.accepts(token)){selectedRun.value=result.data;tab.value='tasks';taskSaved.value=JSON.stringify(taskForm)}}catch(cause){if(epoch.accepts(token))report(cause)}}
async function inspectResult(result:api.ResearchResult){if(!selectedRun.value)return;const token=epoch.capture();try{const current=await api.inspectResearchResult(props.project.id,selectedRun.value.id,result.id);if(epoch.accepts(token)&&selectedRun.value)selectedRun.value.results=selectedRun.value.results.map(item=>item.id===result.id?current.data:item)}catch(cause){if(epoch.accepts(token))report(cause)}}
function prepareImport(result:api.ResearchResult){importResult.value=result;Object.assign(importForm,{purpose:'',mode:'metadata_only',scope:'personal',license:'',model:false,kb:''});tab.value='sources'}
async function saveImport(){if(!importResult.value)return;const result=importResult.value;if(importForm.mode==='full_text'&&result.url&&!importForm.model){error.value='全文处理可能调用现有知识库模型，请明确许可或只保存来源信息。';return}const input:api.CreateImportCandidateInput={...expectation(),run_id:selectedRun.value?.id,result_id:result.id,source_id:result.source_id,url:result.url,title:result.title,knowledge_base_id:importForm.kb||undefined,purpose:importForm.purpose,save_mode:importForm.mode,scope:importForm.scope,model_use_allowed:importForm.model,license:importForm.license};if(await write('import-create',input,key=>api.createImportCandidate(props.project.id,input,key)))importResult.value=null}
function decideImport(candidate:api.ImportCandidate,accept:boolean){requestConfirmation(accept?'确认保存资料':'拒绝保存资料',candidate.title+'\n目的：'+candidate.purpose+'\n范围：'+candidate.scope+' · '+candidate.save_mode+'\n模型使用：'+(candidate.model_use_allowed?'许可':'未许可')+'\n许可：'+candidate.license,async reason=>{const input={...expectation(candidate.version),confirmed:true,reason};await write('import-'+candidate.id,input,key=>accept?api.confirmImportCandidate(props.project.id,candidate.id,input,key):api.rejectImportCandidate(props.project.id,candidate.id,input,key),accept)})}
async function refreshImport(candidate:api.ImportCandidate){const token=epoch.capture();try{const result=await api.refreshImportCandidate(props.project.id,candidate.id);if(epoch.accepts(token)){imports.value=imports.value.map(item=>item.id===candidate.id?result.data:item);emit('changed')}}catch(cause){if(epoch.accepts(token))report(cause)}}

async function openSource(id:string){
 const token=epoch.capture();selection.value=null;anchorForm.checked=false;anchorForm.id='';anchorForm.version=0;anchorForm.corrected='';anchorForm.correction='';source.value=null
 try{const result=await getSource(props.project.id,id);if(epoch.accepts(token)){source.value=result.data;sourceId.value=id;tab.value='sources'}}catch(cause){if(epoch.accepts(token))report(cause)}
}
function captureSelection(){
 const element=sourceText.value,picked=window.getSelection();if(!source.value||!element||!picked||picked.rangeCount!==1)return
 const range=picked.getRangeAt(0);if(!element.contains(range.commonAncestorContainer))return
 const before=range.cloneRange();before.selectNodeContents(element);before.setEnd(range.startContainer,range.startOffset)
 selection.value=selectedRuneRange(source.value.quoted_text,before.toString(),range.toString());anchorForm.checked=false
}
async function reviseAnchor(anchor:api.SourceAnchor){await openSource(anchor.source_id);if(source.value?.id===anchor.source_id)Object.assign(anchorForm,{id:anchor.id,version:anchor.version,extraction:anchor.extraction_type,page:anchor.page||'',section:anchor.section||'',paragraph:anchor.paragraph||'',checked:false,corrected:anchor.corrected_text||'',correction:'',confidence:anchor.ocr_confidence})}
function saveAnchor(){
 if(!source.value||!selection.value||!anchorForm.checked)return
 const input:api.SaveSourceAnchorInput={...expectation(anchorForm.version),anchor_id:anchorForm.id||undefined,source_id:source.value.id,selection_start:selection.value.start,selection_end:selection.value.end,page:anchorForm.page||undefined,section:anchorForm.section||undefined,paragraph:anchorForm.paragraph||undefined,extraction_type:anchorForm.extraction,ocr_confidence:anchorForm.confidence,human_verified:anchorForm.checked,corrected_text:anchorForm.corrected||undefined,correction_reason:anchorForm.correction||undefined,confirmed:true}
 requestConfirmation('保存原文定位',source.value.locator+'\n资料版本 '+source.value.asset_revision+'\n选文：'+selection.value.text+'\n识别方式：'+anchorForm.extraction,async()=>{if(await write('anchor-save',input,key=>api.saveSourceAnchor(props.project.id,input,key))){anchorForm.id='';anchorForm.version=0;selection.value=null;anchorForm.checked=false}})
}
function editClaim(claim:api.ResearchClaim){Object.assign(claimForm,{id:claim.id,version:claim.version,text:claim.text,scope:claim.scope,status:claim.status,explanation:claim.explanation||''});claimSaved.value=JSON.stringify(claimForm)}
function saveClaim(){const draft={...claimForm};requestConfirmation('保存待检验主张',draft.text+'\n范围：'+draft.scope+'\n修订会使关联卡片需要复核。',async reason=>{const input:api.SaveResearchClaimInput={...expectation(draft.version),claim_id:draft.id||undefined,text:draft.text,scope:draft.scope,status:draft.status,explanation:draft.explanation,reason,confirmed:true};if(await write('claim-save',input,key=>api.saveResearchClaim(props.project.id,input,key)))resetClaim()})}
function editCard(card:api.EvidenceCard){Object.assign(cardForm,{id:card.id,version:card.version,anchors:card.anchor_refs.map(ref=>ref.id),claim:card.claim_ref?.id||'',support:card.support_type,chapters:[...card.scope_chapter_ids],scope:card.scope,limits:card.limitations,gaps:card.gaps,alternative:card.alternative_explanation});cardSaved.value=JSON.stringify(cardForm)}
function cardInput():api.SaveEvidenceCardInput{
 const refs=cardForm.anchors.map(id=>anchors.value.find(anchor=>anchor.id===id)).filter((anchor):anchor is api.SourceAnchor=>!!anchor),claim=claims.value.find(item=>item.id===cardForm.claim)
 if(!refs.length||refs.some(anchor=>anchor.status!=='CURRENT')||refs.length!==cardForm.anchors.length||!cardForm.chapters.length||!cardForm.scope.trim())throw new Error('请选择当前定位、适用章节并填写范围。')
 return {...expectation(cardForm.version),card_id:cardForm.id||undefined,anchor_refs:refs.map(anchor=>({id:anchor.id,version:anchor.version})),claim_ref:claim?{id:claim.id,version:claim.version}:undefined,support_type:cardForm.support,scope:cardForm.scope,scope_chapter_ids:[...cardForm.chapters],limitations:cardForm.limits,gaps:cardForm.gaps,alternative_explanation:cardForm.alternative,confirmed:true}
}
function saveCard(){try{const input=cardInput();requestConfirmation('保存候选证据卡',supportLabel(input.support_type)+'\n范围：'+input.scope+'\n定位：'+input.anchor_refs.map(ref=>anchorName(ref.id)).join('；')+'\n局限：'+input.limitations+'\n保存后仍需人工确认。',async()=>{if(await write('card-save',input,key=>api.saveEvidenceCard(props.project.id,input,key)))resetCard()})}catch(cause){report(cause)}}
function confirmCard(card:api.EvidenceCard){requestConfirmation('人工确认证据适用范围',claimText(card)+'\n'+card.scope+'\n局限：'+card.limitations+'\n缺口：'+card.gaps+'\n只确认已核对来源、定位与范围，不证明主张科学正确。',async reason=>{const input={...expectation(card.version),confirmed:true,reason};await write('confirm-'+card.id,input,key=>api.confirmEvidenceCard(props.project.id,card.id,input,key))})}
function reviewCard(card:api.EvidenceCard,status:'INSUFFICIENT'|'NEEDS_REVIEW'){requestConfirmation(label(status),claimText(card)+'\n会取消已有确认。',async reason=>{const input={...expectation(card.version),confirmed:true,status,reason};await write('review-'+card.id,input,key=>api.reviewEvidenceCard(props.project.id,card.id,input,key))})}
function openAnchor(id:string){const anchor=anchors.value.find(item=>item.id===id);if(anchor)void openSource(anchor.source_id)}
function contextInput():api.AgentContextInput{return {...expectation(),chapter_id:contextForm.chapter||undefined,task_id:contextForm.task||undefined,source_ids:[...new Set(contextForm.anchors.map(id=>anchors.value.find(anchor=>anchor.id===id)?.source_id).filter((id):id is string=>!!id))],card_refs:contextForm.cards.map(id=>cards.value.find(card=>card.id===id)).filter((card):card is api.EvidenceCard=>!!card).map(card=>({id:card.id,version:card.version})),sensitive_context_confirmed:contextForm.consent}}
async function previewContext(){const token=epoch.capture();try{const result=await api.previewAgentContext(props.project.id,contextInput());if(epoch.accepts(token))contextPreview.value=result.data}catch(cause){if(epoch.accepts(token))report(cause)}}
async function loadSessions(){const token=epoch.capture(),chosen=agent.value;try{const result=await api.listAgentSessions(props.project.id,chosen);if(epoch.accepts(token)&&agent.value===chosen)sessions.value=result.data}catch(cause){if(epoch.accepts(token))report(cause)}}
async function newSession(){const chosen=agent.value,input=expectation();const result=await write('session-create',input,key=>api.createAgentSession(props.project.id,chosen,input,key));if(result&&chosen===agent.value){await loadSessions();sessionId.value=result.data.id}}
function sameTurnCopy(turn:TurnCopySource,copy:TurnCopySource|null|undefined){return !!copy&&turn.id===copy.id&&turn.project_id===copy.project_id&&turn.session_id===copy.session_id}
function invalidateRestrictedCopies(rows:readonly api.AgentTurn[],session:string){
 const restricted=rows.filter(turn=>turn.status==='restricted'&&turn.project_id===props.project.id&&turn.session_id===session)
 if(!restricted.length)return
 let cleared=false
 for(const turn of restricted){
  if(sameTurnCopy(turn,taskDraftSource.value)){
   // Manual channel/chapter choices and unrelated forms are not derived text.
   Object.assign(taskForm,{goal:'',queries:'',scope:'',type:'',exclusions:'',output:''});taskDraftSource.value=null;cleared=true
  }
  if(sameTurnCopy(turn,changeDraftTurn.value)){
   changeDraftTurn.value=null;changeDraftChapterIDs.value=[];actionForm.changeDraft='';if(actionForm.action==='create_change_set')actionForm.effect='';keys.accepted('action-create');cleared=true
  }
  if(sameTurnCopy(turn,specCandidate.value?.turn)){
   specCandidate.value=null;specCandidateFresh.value=null;specCandidateAttempt.value=null;specCandidateUnknown.value=false;keys.accepted('agent-spec-save');cleared=true
  }
  if(sameTurnCopy(turn,confirming.value?.sourceTurn)){
   confirmDialog.value?.close();confirming.value=null;confirmChecked.value=false;confirmReason.value='';cleared=true
  }
 }
 if(cleared)error.value='该会话来源已不可用，相关 AI 派生输入已清除；独立人工输入保留。保存结果未知时请读取服务端状态，不会自动重试。'
}
async function readCurrentTurns(session:string,token:ReturnType<G2ResponseEpoch['capture']>){
 const id=props.project.id,sequence=(turnReadSequences.get(session)||0)+1;turnReadSequences.set(session,sequence)
 const result=await api.getAgentTurns(id,session)
 if(!epoch.accepts(token)||props.project.id!==id||turnReadSequences.get(session)!==sequence)return null
 invalidateRestrictedCopies(result.data,session)
 return result.data
}
async function refreshTurnCopies(token:ReturnType<G2ResponseEpoch['capture']>){
 const sessionsToRead=new Set([sessionId.value,taskDraftSource.value?.session_id,changeDraftTurn.value?.session_id,specCandidate.value?.turn.session_id].filter((id):id is string=>!!id))
 for(const selected of sessionsToRead){
  if(!epoch.accepts(token))return
  try{const rows=await readCurrentTurns(selected,token);if(rows&&epoch.accepts(token)&&sessionId.value===selected)turns.value=rows}
  catch(cause){if(epoch.accepts(token))report(cause)}
 }
}
async function loadTurns(){
 turns.value=[];if(!sessionId.value)return;const token=epoch.capture(),selected=sessionId.value
 try{const rows=await readCurrentTurns(selected,token);if(rows&&epoch.accepts(token)&&sessionId.value===selected){turns.value=rows;const oldIDs=unknownTurnIDs.get('turn-'+selected);if(oldIDs&&rows.some(turn=>!oldIDs.has(turn.id)&&['completed','stale'].includes(turn.status))){unknown.delete('turn-'+selected);unknownTurnIDs.delete('turn-'+selected)}}}catch(cause){if(epoch.accepts(token))report(cause)}
}
async function sendTurn(){
 const selected=sessionId.value,input={...contextInput(),user_input:userInput.value}
 const result=await write('turn-'+selected,input,key=>api.sendAgentTurn(props.project.id,selected,input,key),true)
 if(result&&sessionId.value===selected){if(result.data.failure_code==='outcome_unknown'){unknown.add('turn-'+selected)}else if(result.data.status==='completed'){userInput.value='';contextForm.consent=false}await loadTurns()}
}
const changeDraftTurn=ref<api.AgentTurn|null>(null),changeDraftChapterIDs=ref<string[]>([])
const changeDraftChapters=computed(()=>chapters.value.filter(chapter=>chapter.project_id===props.project.id&&!!chapter.id))
function checkedChangeDraftChapters(ids:readonly string[]){
 if(!canAgent.value||props.project.status!=='active')throw new Error('当前身份无权提出变更草稿，请重新读取课题与授权。')
 const selected=[...new Set(ids)]
 if(!selected.length)throw new Error('请至少勾选一章作为建议复核范围，实际影响以评估为准。')
 if(selected.some(id=>!changeDraftChapters.value.some(chapter=>chapter.id===id)))throw new Error('所选章节已陈旧、不可见或不属于当前课题，请重新读取并选择。')
 return selected
}
function prepareAgentChangeDraft(turn:api.AgentTurn){
 const draft=turn.suggestions?.change_draft
 if(!draft||!canAgent.value||props.project.status!=='active'||turn.status!=='completed'||turn.agent!=='task_assistant'||turn.created_by!==props.currentUserId||turn.project_id!==props.project.id||turn.context.project_version!==props.project.project_version||turn.context.context_revision!==props.project.current_context_revision||draft.expected_context_revision!==props.project.current_context_revision){error.value='会话或课题基线已变化，请重新读取并生成候选。';return}
 const fields:Record<string,{old_value:string;new_value:string}>={}
 for(const [key,value] of Object.entries(draft.fields)){
  if(!Object.prototype.hasOwnProperty.call(props.project.spec,key)||typeof value!=='string'){error.value='候选包含当前不可见或未知条件，请先澄清。';return}
  fields[key]={old_value:props.project.spec[key]||'',new_value:value}
 }
 if(!Object.keys(fields).length)return
 if((draft.affected_chapter_ids||[]).some(id=>!changeDraftChapters.value.some(chapter=>chapter.id===id))){error.value='候选章节已不可见，请重新读取。';return}
 changeDraftTurn.value=JSON.parse(JSON.stringify(turn));changeDraftChapterIDs.value=[...new Set(draft.affected_chapter_ids||[])]
 Object.assign(actionForm,{action:'create_change_set',target:props.project.id,effect:'仅创建待评估草稿：'+draft.reason,changeDraft:JSON.stringify({expected_context_revision:draft.expected_context_revision,reason:draft.reason,fields},null,2)})
 tab.value='agents';message.value='候选已放入下方动作提案表单。请核对并勾选至少一章作为建议复核范围，再编辑新值和原因；登记不执行，仍需单独明确确认。'
}
function adoptTaskSuggestion(turn:api.AgentTurn){const draft=turn.suggestions?.task_card;if(!draft)return;resetTask();taskDraftSource.value={id:turn.id,project_id:turn.project_id,session_id:turn.session_id,version:turn.version};Object.assign(taskForm,{goal:draft.goal||'',queries:(draft.queries||[]).join('\n'),scope:draft.scope||'',type:draft.question_type||'',exclusions:(draft.exclusions||[]).join('\n'),output:draft.expected_output||''});tab.value='tasks';message.value='建议已放入可编辑草稿，请自行选择资料与搜索渠道后保存。'}
function saveConversationCandidate(turn:api.AgentTurn){requestConfirmation('保存 AI 生成候选','只保存本轮对话候选，不产生正式定位或证据卡。',async reason=>{const input:api.CreateImportCandidateInput={...expectation(),conversation_session_id:turn.session_id,conversation_turn_ids:[turn.id],title:'助手对话候选',purpose:reason,save_mode:'metadata_only',scope:'personal',model_use_allowed:false,license:'AI 生成内容，来源与正确性尚待核实'};await write('conversation-'+turn.id,input,key=>api.createImportCandidate(props.project.id,input,key))})}
function eligibleSpecTurn(turn:api.AgentTurn){
 const current=turns.value.find(item=>item.id===turn.id&&item.version===turn.version&&item.reply===turn.reply&&item.status==='completed')
 return !!current&&eligibleAgentSpecTurn(current,sessions.value.find(session=>session.id===current.session_id),props.project,props.currentUserId)
}
async function readSpecManagePermission(){
 const sequence=++specPermissionSequence,token=epoch.capture(),id=props.project.id,pv=props.project.project_version,user=props.currentUserId,tenant=candidateTenant()
 specManagePermission.value=null
 if(props.readonly||props.project.status!=='draft'||!user||auth.currentUserId!==user)return
 try{const result=await getEffectivePermission(id,'manage');if(sequence===specPermissionSequence&&epoch.accepts(token)&&props.project.id===id&&props.project.project_version===pv&&props.currentUserId===user&&candidateTenant()===tenant)specManagePermission.value=result.data}
 catch(cause){if(sequence===specPermissionSequence&&epoch.accepts(token)){specManagePermission.value=null;report(cause)}}
}
function prepareSpecCandidate(turn:api.AgentTurn){
 if(!canUseSpecCandidate.value||!eligibleSpecTurn(turn)||specCandidate.value)return
 specCandidate.value={field:'',value:turn.reply,turn:JSON.parse(JSON.stringify(turn)),basis:pinAgentSpecCandidateBasis(props.project,props.currentUserId,candidateTenant())}
 specCandidateFresh.value=null;specCandidateAttempt.value=null;specCandidateUnknown.value=false;error.value='';void nextTick(()=>specCandidateFieldInput.value?.focus())
}
async function readSpecCandidateBasis(){
 const draft=specCandidate.value;if(!draft||pending.has('agent-spec-read')||pending.has('agent-spec-save')||specCandidateUnknown.value)return
 const token=epoch.capture(),sequence=++specReadSequence,id=props.project.id,user=props.currentUserId,tenant=candidateTenant();pending.add('agent-spec-read');specCandidateFresh.value=null
 try{
  const [p,t,permission]=await Promise.all([api.getAgentSpecCandidateProject(id),readCurrentTurns(draft.turn.session_id,token),getEffectivePermission(id,'manage')])
  if(!t||!epoch.accepts(token)||sequence!==specReadSequence||props.project.id!==id||props.currentUserId!==user||candidateTenant()!==tenant||specCandidate.value!==draft)return
  const turn=t.find(item=>item.id===draft.turn.id)
  if(!turn||!eligibleAgentSpecTurn(turn,sessions.value.find(session=>session.id===turn.session_id),p.data,user)||p.data.id!==id||permission.data.allowed!==true||permission.data.capability!=='manage'||permission.data.project_version!==p.data.project_version)throw new Error('当前会话、草稿或管理许可已不可用；不能关联旧来源。')
  if(sessionId.value===draft.turn.session_id)turns.value=t;specCandidateFresh.value={project:p.data,turn};specManagePermission.value=permission.data;emit('changed');message.value='已读取当前条件与真实会话。比较当前值和候选值后，再明确关联；输入没有改变。'
 }catch(cause){if(epoch.accepts(token)&&sequence===specReadSequence)report(cause)}
 finally{if(sequence===specReadSequence)pending.delete('agent-spec-read')}
}
function reassociateSpecCandidate(){
 const draft=specCandidate.value,fresh=specCandidateFresh.value;if(!draft||!fresh||specCandidateUnknown.value||!canUseSpecCandidate.value)return
 if(!agentSpecCandidateBasisCurrent(pinAgentSpecCandidateBasis(fresh.project,props.currentUserId,candidateTenant()),props.project,props.currentUserId,candidateTenant())){error.value='父页正在读取或课题再次变化，请重新读取并比较后再关联。';return}
 if(!fresh.project.template_copy?.definition.fields.some(field=>field.field_id===draft.field)){error.value='所选字段已不在当前模板，请明确选择一个当前字段。';return}
 draft.basis=pinAgentSpecCandidateBasis(fresh.project,props.currentUserId,candidateTenant());draft.turn=JSON.parse(JSON.stringify(fresh.turn));specCandidateAttempt.value=null;specCandidateFresh.value=null;error.value='';message.value='候选输入已由你关联当前版本，请再次明确确认后保存。'
}
function confirmSpecCandidate(){
 const draft=specCandidate.value;if(!draft||!canSaveSpecCandidate.value)return
 try{
  const input=specCandidateUnknown.value ? specCandidateAttempt.value!.input : buildAgentSpecCandidateInput(props.project,draft.turn,draft.field,draft.value)
  const field=Object.keys(input.field_metadata)[0]!
  requestConfirmation(specCandidateUnknown.value?'人工重试同一候选保存':'保存待确认研究条件',field+'\n当前值：'+(props.project.spec[field]||'（空）')+'\n候选值：'+input.fields[field]+'\n条件版本 '+input.expected_spec_revision+' · 轮次 '+draft.turn.id+' / '+draft.turn.version+'\n保存仍待人工确认，不自动立项。',async()=>{await submitSpecCandidate(draft,input)},draft.turn)
 }catch(cause){report(cause)}
}
async function submitSpecCandidate(draft:SpecCandidate,input:api.SaveAgentSpecCandidateInput){
 if(!canUseSpecCandidate.value||specCandidate.value!==draft||pending.has('agent-spec-save'))return
 if(!specCandidateUnknown.value&&(!agentSpecCandidateBasisCurrent(draft.basis,props.project,props.currentUserId,candidateTenant())||!eligibleSpecTurn(draft.turn)||JSON.stringify(buildAgentSpecCandidateInput(props.project,draft.turn,draft.field,draft.value))!==JSON.stringify(input))){error.value='确认期间候选输入或课题基线已变化，请保留输入并重新核对。';return}
 const token=epoch.capture(),user=props.currentUserId,tenant=candidateTenant(),id=props.project.id
 const attempt=specCandidateUnknown.value ? specCandidateAttempt.value! : {input:JSON.parse(JSON.stringify(input)),key:keys.forRequest('agent-spec-save',input)}
 specCandidateAttempt.value=attempt;pending.add('agent-spec-save');error.value=''
 try{
  const result=await api.saveAgentSpecCandidate(id,attempt.input,attempt.key)
  if(!epoch.accepts(token)||props.currentUserId!==user||candidateTenant()!==tenant||props.project.id!==id||specCandidate.value!==draft)return
  if(!acceptedAgentSpecCandidate(result.data,id,attempt.input)){specCandidateUnknown.value=true;error.value='未取得匹配真实来源与候选字段的完整回执。请保留原请求，人工核对后重试同一保存。';return}
  keys.accepted('agent-spec-save');specCandidateUnknown.value=false;specCandidateAttempt.value=null
  if(agentSpecCandidateBasisCurrent(draft.basis,props.project,user,tenant)){specCandidate.value=null;specCandidateFresh.value=null;message.value='已保存为待人工确认的研究条件；服务端已核对真实会话来源。'}
  else{message.value='此请求已保存，但当前课题基线已变化。候选输入保留，请读取当前条件并比较后决定。'}
  emit('changed');await refresh()
 }catch(cause){
  if(epoch.accepts(token)&&props.currentUserId===user&&candidateTenant()===tenant&&specCandidate.value===draft){
   const problem=cause as {status?:number;$httpStatus?:number;code?:string;error?:{code?:string}},status=problem.$httpStatus||problem.status
   specCandidateUnknown.value=!status||[408,502,503,504].includes(status)||(problem.code||problem.error?.code)==='outcome_unknown'
   report(cause)
  }
 }finally{if(epoch.accepts(token))pending.delete('agent-spec-save')}
}
function discardSpecCandidate(){
 if(!specCandidate.value||pending.has('agent-spec-save'))return
 requestConfirmation('放弃此候选输入',specCandidateUnknown.value?'原请求结果仍未知。放弃输入不会撤销可能已保存的条件，请后续读取服务端状态。':'只放弃本页候选输入，不修改已保存条件。',async()=>{specCandidate.value=null;specCandidateFresh.value=null;specCandidateAttempt.value=null;specCandidateUnknown.value=false})
}
async function proposeAction(){
 const token=epoch.capture(),action=actionForm.action,confirmationSource=actionForm.action==='create_change_set'?changeDraftTurn.value:undefined;let target=actionForm.target,input:Record<string,unknown>={},sourceIDs:string[]=[]
 try{
  if(action==='create_change_set'){
   const source=changeDraftTurn.value
   if(!source||source.created_by!==props.currentUserId||source.project_id!==props.project.id||source.context.project_version!==props.project.project_version||source.context.context_revision!==props.project.current_context_revision)throw new Error('草稿候选已陈旧，请重新生成；不会静默重绑定。')
   const parsed=JSON.parse(actionForm.changeDraft) as {expected_context_revision:number;fields:Record<string,{old_value:string;new_value:string}>;[key:string]:unknown}
   if(parsed.expected_context_revision!==source.context.context_revision||!parsed.fields||!Object.keys(parsed.fields).length)throw new Error('草稿输入缺少当前基线和字段变化。')
   const selectedChapters=checkedChangeDraftChapters(changeDraftChapterIDs.value),metadata:Record<string,unknown>={}
   for(const [field,delta] of Object.entries(parsed.fields)){
    if(!Object.prototype.hasOwnProperty.call(props.project.spec,field)||delta.old_value!==props.project.spec[field]||typeof delta.new_value!=='string')throw new Error('字段或旧值已变化，请重新读取。')
    metadata[field]={origin:'ai_assisted_human',status:'pending_confirmation',provenance:{source_type:'agent_turn',source_id:source.id,source_version:String(source.version),purpose:'project_spec',locator:'reply'}}
   }
   target=props.project.id;input={...parsed,affected_chapter_ids:selectedChapters,field_metadata:metadata};sourceIDs=source.context.source_refs.map(ref=>ref.source_id)
  }
  if(action==='save_spec'){target=props.project.id;input={expected_spec_revision:props.project.spec_revision,fields:{...props.project.spec,[actionForm.field]:actionForm.value}}}
  if(action==='bind_asset'){target=props.project.id;input={knowledge_id:actionForm.knowledge}}
  if(action==='create_evidence_card'){target=cardForm.id||props.project.id;input={...cardInput()};sourceIDs=cardForm.anchors.map(id=>anchors.value.find(anchor=>anchor.id===id)?.source_id).filter((id):id is string=>!!id)}
  if(action==='confirm_evidence_card'){const card=cards.value.find(item=>item.id===target);if(!card)throw new Error('证据卡已不可见。');input={...expectation(card.version),confirmed:false,reason:actionForm.effect};sourceIDs=[...card.source_ids]}
  if(action==='apply_change_set'&&!changes.value.some(change=>change.id===target&&change.status==='assessed'))throw new Error('请选择当前已评估的变更。')
  if(action==='commit_working_copy'){const copy=(await getWorkingCopy(props.project.id,target)).data;input={expected_spec_revision:copy.spec_revision,expected_working_copy_revision:copy.working_copy_revision,expected_chapter_version_id:copy.base_chapter_version_id};sourceIDs=[...copy.source_ids]}
  if(action==='export'){const release=(await getRelease(props.project.id,target)).data;if(!release.is_current||release.check.status!=='passed')throw new Error('快照此刻不能导出，请回交付页重新检查。')}
  if(!epoch.accepts(token))return
   error.value=''
  const payload:api.CreateActionProposalInput={...expectation(),action,target_id:target,input,source_ids:sourceIDs,expected_effect:actionForm.effect}
  requestConfirmation('保存待人工确认的动作',actionLabel(action)+'\n'+actionForm.effect+'\n对象：'+target+'\n输入：'+JSON.stringify(input,null,2)+'\n此步骤只登记提案。',async()=>{
    if(action==='create_change_set'){
     if(!epoch.accepts(token)||props.project.id!==target||props.project.project_version!==payload.expected_project_version||props.project.current_context_revision!==payload.expected_context_revision)throw new Error('课题或身份已变化，请重新核对草稿与建议复核章节。')
     checkedChangeDraftChapters(input.affected_chapter_ids as string[])
    }
    await write('action-create',payload,key=>api.createActionProposal(props.project.id,payload,key))
   },confirmationSource||undefined)
 }catch(cause){if(epoch.accepts(token))report(cause)}
}
function decideAction(proposal:api.ActionProposal,accept:boolean){requestConfirmation(accept?'明确确认并执行动作':'拒绝动作提案',actionLabel(proposal.action)+'\n'+proposal.expected_effect+'\n'+proposal.reversibility+'\n基线 '+proposal.basis.context_revision+'\n输入：'+JSON.stringify(proposal.input,null,2),async reason=>{const input={...expectation(proposal.version),confirmed:true,reason};await write('action-'+proposal.id,input,key=>accept?api.confirmActionProposal(props.project.id,proposal.id,input,key):api.rejectActionProposal(props.project.id,proposal.id,input,key),accept)})}
watch([()=>props.project.id,()=>props.currentUserId],()=>{epoch.next();clearTimeout(poll);keys.clear();pending.clear();unknown.clear();unknownTurnIDs.clear();clearSensitive();confirmingBusy.value=false;cancelConfirmation();void refresh();void loadSessions()},{immediate:true})
watch(()=>JSON.stringify([props.project.current_context_revision,contextForm.anchors.map(id=>{const value=anchors.value.find(item=>item.id===id);return [value?.version,value?.asset_revision,value?.status]}),contextForm.cards.map(id=>{const value=cards.value.find(item=>item.id===id);return [value?.version,value?.status]}),tasks.value.find(item=>item.id===contextForm.task)?.version]),()=>{contextForm.consent=false;contextPreview.value=null})
watch(agent,()=>{changeDraftTurn.value=null;changeDraftChapterIDs.value=[];actionForm.changeDraft='';sessionId.value='';turns.value=[];userInput.value='';contextPreview.value=null;contextForm.consent=false;void loadSessions()})
watch(sessionId,()=>{changeDraftTurn.value=null;changeDraftChapterIDs.value=[];actionForm.changeDraft='';contextForm.consent=false;contextPreview.value=null;void loadTurns()})
watch(()=>[contextForm.chapter,contextForm.task,[...contextForm.anchors],[...contextForm.cards]],()=>{contextForm.consent=false;contextPreview.value=null})
watch([()=>props.project.id,()=>props.currentUserId,()=>auth.effectiveTenantId,()=>auth.currentUserId,()=>props.project.project_version,()=>props.readonly],()=>{void readSpecManagePermission()},{immediate:true})
watch([()=>auth.effectiveTenantId,()=>auth.currentUserId],()=>{epoch.next();clearTimeout(poll);specPermissionSequence++;specReadSequence++;clearSensitive();keys.clear();pending.clear();unknown.clear();unknownTurnIDs.clear();confirmingBusy.value=false;cancelConfirmation();error.value='';message.value='';void refresh();void loadSessions();void readSpecManagePermission()})
watch(()=>[!!specCandidate.value,!!actionForm.changeDraft,changeDraftChapterIDs.value.length>0,JSON.stringify(taskForm)!==taskSaved.value,JSON.stringify(claimForm)!==claimSaved.value,JSON.stringify(cardForm)!==cardSaved.value,!!userInput.value,!!selection.value,!!importResult.value&&!!importForm.purpose],values=>emit('dirty',values.some(Boolean)))
// 只读定位：切到“3 证据”、等当前读取完成后按精确 id+version 找当前卡并滚动到条目。
// 不修改任何表单、不确认、不选择；找不到时明确提示，不回退旧卡。
async function showEvidenceCard(ref:api.G2VersionRef){
  tab.value='evidence'
  await refresh()
  const card=cards.value.find(item=>item.id===ref.id&&item.version===ref.version)
  if(!card){error.value='当前证据列表中没有这张卡的所选版本；不会自动改用其他版本，请重新读取或重新选择。';return}
  await nextTick()
  const target=typeof document==='undefined'?null:document.querySelector(`[data-card-id="${card.id}"]`)
  if(!target){error.value='已找到该证据卡，但当前筛选下没有对应条目；请调整卡片筛选后重试。';return}
  target.scrollIntoView?.({behavior:'smooth',block:'center'})
}
defineExpose({operationBusy:computed(()=>pending.size>0||confirmingBusy.value),showEvidenceCard})
onBeforeUnmount(()=>{epoch.next();clearTimeout(poll);keys.clear();clearSensitive();confirmDialog.value?.close()})

async function loadPolicy(){assetPolicy.value=null;const token=epoch.capture(),id=policyAsset.value;if(!id)return;try{const result=await api.getProjectAssetPolicy(props.project.id,id);if(epoch.accepts(token)&&policyAsset.value===id){assetPolicy.value=result.data;Object.assign(policyForm,{scope:result.data.scope,purpose:result.data.purpose,users:[...result.data.allowed_user_ids],model:result.data.model_use_allowed})}}catch(cause){if(epoch.accepts(token))report(cause)}}
function proposePolicy(){if(!assetPolicy.value)return;const id=assetPolicy.value.asset_id,version=assetPolicy.value.version;const policy={scope:policyForm.scope,purpose:policyForm.purpose,allowed_user_ids:[...policyForm.users],model_use_allowed:policyForm.model};requestConfirmation('起草资料使用变更','资料版本 '+version+'\n范围 '+policy.scope+'\n目的 '+(policy.purpose||'历史未记录')+'\n模型使用 '+(policy.model_use_allowed?'许可':'未许可')+'\n应用后所有定位和关联卡片需要复核。',async reason=>{const input:api.ProjectAssetPolicyProposalInput={...expectation(version),confirmed:true,reason,policy};await write('policy-propose',input,key=>api.proposeProjectAssetPolicy(props.project.id,id,input,key))})}
watch([()=>props.project.id,()=>props.currentUserId],()=>{policyAsset.value='';assetPolicy.value=null;Object.assign(policyForm,{scope:'personal',purpose:'',users:[],model:false})})

</script>
<style scoped>
.g2-panel{padding:22px;max-width:1050px;margin:auto;color:var(--td-text-color-primary,#172235);background:var(--td-bg-color-container,#fff)}
header{display:flex;justify-content:space-between;gap:16px}header h2{margin:0}header p,small{color:var(--td-text-color-secondary,#667085)}
nav,.actions{display:flex;gap:8px;flex-wrap:wrap;margin:14px 0}nav button[aria-pressed=true]{background:#e8f0ff;border-color:#8fb4ec;color:#1755ad}
button,select,input,textarea{font:inherit;border:1px solid var(--td-component-border,#c9d3df);border-radius:6px;padding:8px;background:var(--td-bg-color-container,#fff);color:inherit}button{cursor:pointer;margin-right:6px}button:disabled{opacity:.5;cursor:default}
button:focus-visible,a:focus-visible,input:focus-visible,textarea:focus-visible,select:focus-visible{outline:3px solid #679ce5;outline-offset:2px}label{display:flex;flex-direction:column;gap:6px;margin:12px 0}textarea{min-height:65px;resize:vertical}select[multiple]{min-height:100px}.check{flex-direction:row;align-items:start}.check input{margin-top:4px}
article,form,section>section{border:1px solid var(--td-component-border,#dce2eb);border-radius:10px;padding:16px;margin:16px 0}article h3,article h4{margin-top:0}details{margin:12px 0}summary{cursor:pointer;font-weight:600}.grid{display:grid;grid-template-columns:1fr 1fr;gap:18px}.notice{background:var(--td-brand-color-light,#eef5ff);padding:12px;border-radius:6px;line-height:1.6}.error{color:var(--td-error-color,#a82d26);white-space:pre-wrap}
.source-text,.reply,pre,output{white-space:pre-wrap;overflow-wrap:anywhere;line-height:1.7}.source-text{user-select:text;background:var(--td-bg-color-secondarycontainer,#f4f7fa);padding:16px;max-height:420px;overflow:auto}pre{font-size:13px}dialog{border:1px solid #aebbd0;border-radius:12px;max-width:650px;width:calc(100% - 48px);padding:0;color:inherit;background:var(--td-bg-color-container,#fff)}dialog::backdrop{background:rgba(10,20,35,.5)}dialog form{border:0;margin:0}
@media(max-width:680px){.g2-panel{padding:12px}.grid{grid-template-columns:1fr;gap:0}article,form{padding:12px}}
</style>
