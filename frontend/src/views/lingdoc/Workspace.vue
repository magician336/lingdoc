<template>
  <main ref="wbRoot" class="lingdoc-workspace wb" :class="mainClasses" :inert="projectSwitchCommitting">
    <header class="workspace-head wb-top">
      <div class="wb-brand">
        <h1>灵档文稿</h1>
        <p class="wb-brand-note">写作 · 来源 · 核查 · 交付</p>
      </div>
      <div class="wb-top-actions">
        <span class="wb-cluster wb-cluster-view">
          <button type="button" class="wb-toggle" :aria-pressed="railOpen" @click="toggleRail">{{ railOpen ? '收起目录' : '显示目录' }}</button>
          <button v-if="project?.status === 'active'" type="button" ref="inspectorToggle" class="wb-toggle" :aria-pressed="inspOpen" @click="toggleInsp">{{ inspOpen ? '收起辅助' : '显示辅助' }}</button>
          <div v-if="project?.status === 'active'" class="wb-mode" role="group" aria-label="工作状态">
            <button type="button" :aria-pressed="mode === 'research'" @click="mode = 'research'">研究</button>
            <button type="button" :aria-pressed="mode === 'focus'" @click="mode = 'focus'">专注</button>
          </div>
        </span>
        <span v-if="project?.status === 'active'" class="wb-cluster wb-cluster-status">
          <span class="wb-save" role="status">
            <span class="save-dot" :class="workingCopyStatus" aria-hidden="true"></span>{{ workingCopyStatusLabel }}
          </span>
          <span v-if="chapter" class="badge warn">本次待核 <span id="wb-review-count">{{ pendingReviewCount }}</span></span>
          <span v-if="chapter && editorReady" class="document-chapter-context">当前章：{{ chapter.title }}</span>
        </span>
        <template v-if="project?.status === 'active' && chapter && editorReady">
          <span class="wb-cluster wb-cluster-write">
            <button type="button" @click="saveAllDocument" :disabled="busy || workingCopyLoading">保存文稿</button>
            <button type="button" @click="commitDraft" :disabled="busy || workingCopyLoading || chapterChanged || !workingCopyDiffersFromFormal">提交为正式版本</button>
            <button v-if="chapter.current_version_id" type="button" @click="confirmCurrentChapter" :disabled="confirmationReasons.length>0" :title="confirmationReasons.join('；')">{{ chapter.confirmation_valid && !workingCopyDiffersFromFormal ? '重新确认当前版本' : '确认当前章节版本' }}</button>
            <button v-if="confirmationReasons.length" type="button" class="confirmation-help" @click="openInspector('checks')">确认前还需处理 {{ confirmationReasons.length }} 项</button>
          </span>
          <span class="wb-cluster wb-cluster-aux">
            <button type="button" @click="openInspector('ai')">选段改写</button>
            <button type="button" @click="openInspector('checks')">核查</button>
            <button type="button" @click="openInspector('history')">版本</button>
          </span>
        </template>
        <button v-if="project?.status === 'active'" type="button" class="primary" @click="deliveryOpen = true">交付</button>
        <button v-if="project" type="button" class="ghost" @click="openFullPanel('members')">成员与协作权限 →</button>
        <div ref="devMenuRef" class="wb-devmenu">
          <button type="button" class="wb-devmenu-toggle" :aria-expanded="devMenuOpen" aria-haspopup="menu" @click="toggleDevMenu">开发与验收</button>
          <div v-if="devMenuOpen" class="wb-devmenu-body" role="menu">
            <RouterLink class="evidence-link" role="menuitem" to="/platform/lingdoc/permissions" @click="devMenuOpen = false">权限验收</RouterLink>
          </div>
        </div>
      </div>
    </header>

    <p v-if="errorMessage" role="alert" class="alert">{{ errorMessage }}</p>
    <OwnerUnavailableNotice v-if="project" :reason="ownerNoticeReason" :recovery="ownerNoticeRecovery" />
    <section class="wb-notify" aria-label="通知">
      <button type="button" class="wb-notify-toggle" :aria-expanded="notifyOpen" @click="notifyOpen = !notifyOpen">
        <span class="wb-notify-title">通知</span>
        <span v-if="activeInvitationCount" class="wb-notify-count">{{ activeInvitationCount }} 条邀请待处理</span>
        <span v-else-if="myInvitations.length" class="wb-notify-hist">{{ myInvitations.length }} 条历史邀请</span>
        <span class="wb-notify-caret">{{ notifyOpen ? '收起' : '展开' }}</span>
      </button>
      <div v-if="notifyOpen" class="wb-notify-body">
        <div v-if="myInvitations.length || invitationLoading || invitationError" class="wb-notify-group" aria-label="课题邀请">
          <p class="wb-notify-hint">接受后才会获得课题访问权。请先核对角色和分配范围。</p>
          <p v-if="invitationLoading" role="status">正在读取当前账号的邀请。</p>
          <p v-if="invitationError" role="alert">{{invitationError}}</p>
          <button type="button" :disabled="invitationLoading || invitationBusy" @click="loadInvitationInbox">重新读取邀请</button>
          <ul class="wb-invite-list">
            <li v-for="invite in myInvitations" :key="invite.id" class="wb-invite" :data-state="invitationViewState(invite)">
              <div class="wb-invite-main">
                <span class="wb-invite-badge" :data-state="invitationViewState(invite)">{{ invitationStatusLabel(invite) }}</span>
                <span class="wb-invite-meta">邀请人 {{invite.inviter}} · {{invite.reason}} · 有效至 {{new Date(invite.expires_at).toLocaleString()}}</span>
                <span class="wb-invite-scope">{{ invitationAssignment(invite) }}</span>
                <details v-if="invitationScopeDetail(invite).length" class="wb-invite-ids"><summary>原始范围 ID（供核对）</summary><p v-for="line in invitationScopeDetail(invite)" :key="line">{{ line }}</p></details>
              </div>
              <button v-if="invitationViewState(invite)==='active'" type="button" class="wb-invite-accept" :disabled="invitationBusy" @click="acceptMyInvitation(invite)">接受并进入课题</button>
            </li>
          </ul>
        </div>
        <details class="discarded-drafts" @toggle="!discardedOpen && ($event.target as HTMLDetailsElement).open && showDiscarded()"><summary>恢复已丢弃的课题草稿</summary><p class="muted">仅列出当前账号仍有权恢复的草稿，不展示研究正文。</p><p v-if="discardedOpen&&!discardedDrafts.length" class="muted">没有可恢复的草稿。</p><div v-for="p in discardedDrafts" :key="p.id"><span>{{p.name}}</span><button type="button" :disabled="busy" @click="restoreDiscarded(p)">恢复草稿</button></div></details>
      </div>
    </section>
    <div class="workspace-grid" :inert="!!fullPanel || activationOpen">
      <aside class="panel">
        <div class="wb-project">
          <h2 class="wb-rail-heading">当前课题 <button type="button" class="wb-rail-close" aria-label="关闭目录" @click="closeRail">×</button></h2>
          <div v-if="project" class="wb-project-chip">
            <strong>{{ project.name }}</strong>
            <span class="badge">{{ project.status === 'draft' ? '草稿' : '已立项' }}</span>
          </div>
          <p v-else class="muted">未选择课题。</p>
          <button type="button" class="small" :aria-expanded="projectPickerOpen" @click="toggleProjectPicker">切换课题</button>
          <div v-if="projectPickerOpen" class="wb-picker" role="dialog" aria-label="切换课题">
            <div class="wb-picker-head">
              <strong>切换课题</strong>
              <span class="wb-picker-head-actions">
                <button type="button" class="small ghost" :disabled="loading" @click="loadProjects">{{ loading ? '正在刷新…' : '刷新列表' }}</button>
                <button type="button" class="wb-picker-close" aria-label="关闭课题切换" @click="closeProjectPicker">×</button>
              </span>
            </div>
            <input id="project-search" v-model="projectQuery" placeholder="搜索课题" aria-label="搜索课题" maxlength="120">
            <p v-if="loading && projects.length === 0" class="muted">正在加载…</p>
            <p v-else-if="projects.length === 0" class="muted">还没有课题。</p>
            <p v-else-if="filteredProjects.length === 0" class="muted">没有匹配的课题。</p>
            <ul v-else class="wb-picker-list">
              <li v-for="item in filteredProjects" :key="item.id">
                <button type="button" :title="item.name" :class="{ selected: project?.id === item.id }" @click="pickProject(item.id)">
                  <strong>{{ item.name }}</strong><small>{{ item.status === 'draft' ? '草稿' : '已立项' }}</small>
                </button>
              </li>
            </ul>
            <form class="create-form" @submit.prevent="create">
              <input id="project-name" v-model="newName" maxlength="120" required placeholder="新课题名称" aria-label="新课题名称">
              <label for="new-project-template">课题模板</label>
              <select id="new-project-template" v-model="newTemplateKey" :disabled="busy || catalogLoading"><option value="template-demo@1">两章演示模板 · 1 · 内部演示</option><option v-for="v in publishedTemplates" :key="v.template_id+'@'+v.version" :value="v.template_id+'@'+v.version">{{templateChoiceLabel(v)}}</option></select>
              <p v-if="selectedPublishedTemplate" class="muted">{{templateChoiceDescription(selectedPublishedTemplate)}}</p>
              <p v-else-if="newTemplateKey === 'template-demo@1'" class="muted">当前选择的是内部两章演示模板，不代表正式申报要求。</p>
              <p v-else class="warning" role="alert">先前选择的模板已不在当前可用目录中，请重新选择；不会自动改用演示模板。</p>
              <p class="muted">创建时固定这个版本；目录更新不会静默改变课题副本。</p><p v-if="templateCatalogNotice" role="status" class="muted">{{templateCatalogNotice}}</p>
              <button type="submit" :disabled="busy || !newName.trim() || catalogLoading || templateChoiceUnavailable">创建课题</button>
            </form>
          </div>
        </div>
        <nav v-if="project?.status === 'active' && chapters.length" class="wb-chapters" aria-label="章节目录">
          <p class="wb-chapters-title">章节目录</p>
          <button v-for="item in orderedChapters" :key="item.id" type="button"
            :class="{ selected: chapter?.id === item.id }" @click="selectChapter(item)">
            <strong>{{ item.title }}</strong><small>{{ item.current_version_id ? '已编辑' : '未填写' }}</small>
          </button>
        </nav>
        <div class="wb-rail-entries">
          <button type="button" class="ghost" :disabled="!project" @click="specOpen = true">研究条件 →</button>
          <button type="button" class="ghost" :disabled="!project" @click="templateOpen = true">当前模板副本 →</button>
          <button type="button" class="ghost" :disabled="!project" @click="openFullPanel('research')">调研与证据 →</button>
          <button type="button" class="ghost" :disabled="!project" @click="openInspector('sources')">上传与项目资料 →</button>
          <button type="button" class="ghost" :disabled="!project || project.status !== 'active'" @click="openFullPanel('changes')">正式变更与运行 →</button>
          <button type="button" class="ghost" :disabled="!project || project.status !== 'active'" @click="openFullPanel('operations')">运行观测与试用 →</button>
          <button type="button" class="ghost" :disabled="!project" @click="openFullPanel('governance')">模板目录与规则 →</button>
          <button type="button" class="ghost" :disabled="!project || !chapter" @click="openFullPanel('review')">章节评审与批注 →</button>
        </div>
      </aside>

      <section class="panel work-area" v-if="project">
        <div class="section-head">
          <div><h2>{{ project.name }}</h2><p class="muted">研究条件版本 {{ project.spec_revision }} · 课题版本 {{ project.project_version }}</p>
          <p class="muted">课题成员 {{ project.members.length }} 人。现阶段由课题成员协作编辑。</p></div>
          <button type="button" @click="selectProject(project.id, { force: true }).then(loadProjectTemplateStatus)">重新读取</button>
        </div>

        <section v-if="templateRecall" class="warning" role="alert" aria-label="来源模板召回提示">
          <h3>当前课题使用的来源模板已召回</h3>
          <p>原因：{{templateRecall.reason}}</p><p>处置建议：{{templateRecall.migration_advice}}</p>
          <p>当前课题副本和历史交付不会自动改写；请由负责人复核后决定是否走正式模板升级。</p>
        </section>
        <p v-if="templateStatusError" class="warning" role="status">{{templateStatusError}} <button type="button" @click="loadProjectTemplateStatus">重试</button></p>


        <section class="template-copy-panel" aria-label="课题模板副本">
          <h3>课题模板副本 <button type="button" class="ghost wb-drawer-close" aria-label="关闭模板与规则" @click="templateOpen = false">×</button></h3>
          <template v-if="project.template_copy">
            <p class="muted">来源 {{ project.template_copy.source_template_id }} · 原始版本 {{ project.template_copy.source_template_version }} · 课题副本第 {{ project.template_copy.version }} 版（{{ templateCopyStatusLabel(project.template_copy.status) }}）</p>
            <p class="template-hash">内容摘要：{{ project.template_copy.content_hash }}</p>
            <p class="template-hash">规则集摘要：{{ project.template_copy.ruleset_hash }}</p>
            <p v-if="project.status === 'active'" class="muted">课题已立项；普通保存不可用。模板改动会创建 ChangeSet，列出受影响章节并由 Owner 确认应用。</p>
            <template v-if="templateCopyDraft">
              <p class="muted">可编辑字段、章节结构与顺序、术语和声明式规则。字段 ID 与类型保持稳定；{{ project.status === 'draft' ? '改动先预览，再保存为新的不可变草稿版本。' : '改动需经过模板升级 ChangeSet，应用后旧版本和章节确认历史仍保留。' }}</p>
              <fieldset class="template-editor" :disabled="busy || templateCopyBusy">
                <legend>字段名称</legend>
                <label v-for="field in templateCopyDraft.fields" :key="field.field_id" class="template-editor__item">
                  <span>{{ field.field_id }} <small>· {{ field.type }}{{ field.required ? ' · 必填' : '' }}</small></span>
                  <input v-model="field.label" :aria-label="`${field.field_id} 显示名称`" maxlength="120" />
                </label>
              </fieldset>
                <fieldset class="template-editor" :disabled="busy || templateCopyBusy">
                  <legend>章节名称</legend>
                  <label v-for="section in templateCopyDraft.sections" :key="section.section_id" class="template-editor__item">
                    <span>{{ section.section_id }} <small>· {{ section.required ? '必需章节' : '可选章节' }}</small></span>
                    <input v-model="section.title" :aria-label="`${section.section_id} 章节名称`" maxlength="120" />
                    <input v-model.number="section.order" type="number" min="1" :aria-label="`${section.section_id} 章节顺序`" />
                    <button type="button" @click="removeTemplateSection(section.section_id)">移除章节 {{ section.title }}</button>
                  </label>
                  <button type="button" @click="addTemplateSection">添加章节</button>
                </fieldset>
                <fieldset class="template-editor" :disabled="busy || templateCopyBusy">
                  <legend>术语表</legend>
                  <label v-for="term in templateCopyDraft.terms" :key="term.term_id" class="template-editor__item">
                    <span>{{ term.term_id }}</span>
                    <input v-model="term.preferred" :aria-label="`${term.term_id} 首选术语`" maxlength="120" />
                    <input :value="term.variants.join('、')" :aria-label="`${term.term_id} 术语变体`" placeholder="变体用逗号分隔" @input="updateTermVariants(term, $event)" />
                    <button type="button" @click="templateCopyDraft.terms = templateCopyDraft.terms.filter(item => item.term_id !== term.term_id)">移除术语 {{ term.preferred }}</button>
                  </label>
                  <button type="button" @click="addTemplateTerm">添加术语</button>
                </fieldset>
                <fieldset v-if="templateCopyDraft.rules.length" class="template-editor" :disabled="busy || templateCopyBusy">
                  <legend>声明式规则</legend>
                  <label v-for="rule in templateCopyDraft.rules" :key="rule.rule_id" class="template-editor__item">
                    <span>{{ rule.rule_id }} <small>· {{ rule.kind }}</small></span>
                    <p class="muted">{{ rule.severity }} · {{ rule.evaluator }}（由服务端校验）</p>
                    <input v-model="rule.message" :aria-label="`${rule.rule_id} 规则说明`" maxlength="500" />
                    <template v-if="rule.kind !== 'computed'">
                      <select v-model="rule.parameters.target_kind" :aria-label="`${rule.rule_id} 检查对象`"><option value="field">研究条件字段</option><option value="chapter">章节</option></select>
                      <input v-model="rule.parameters.target_id" :aria-label="`${rule.rule_id} 目标标识`" placeholder="字段或章节标识" />
                      <input v-if="rule.kind === 'pattern'" v-model="rule.parameters.pattern" :aria-label="`${rule.rule_id} 匹配表达式`" placeholder="匹配表达式" />
                      <template v-if="rule.kind === 'consistency'">
                        <input v-model="rule.parameters.preferred" :aria-label="`${rule.rule_id} 标准用词`" />
                        <input :value="(rule.parameters.variants as string[] ?? []).join('、')" :aria-label="`${rule.rule_id} 非标准用词`" @input="updateRuleVariants(rule, $event)" />
                      </template>
                    </template>
                  </label>
                  <button type="button" @click="addTemplateRule('presence')">添加必填检查</button>
                  <button type="button" @click="addTemplateRule('pattern')">添加格式检查</button>
                  <button type="button" @click="addTemplateRule('consistency')">添加术语检查</button>
                </fieldset>
              <label v-if="project.status === 'active'" for="template-upgrade-reason">升级理由</label>
              <input v-if="project.status === 'active'" id="template-upgrade-reason" v-model="templateUpgradeReason" :disabled="busy || templateCopyBusy" maxlength="2000" placeholder="说明模板调整原因" />
              <div class="actions">
                <template v-if="project.status === 'draft'">
                  <button type="button" :disabled="busy || templateCopyBusy || !templateCopyChanged" @click="previewTemplateCopy">{{ templateCopyBusy ? '处理中…' : '预览改动' }}</button>
                  <button type="button" :disabled="busy || templateCopyBusy || !templateCopyChanged" @click="discardTemplateCopyDraft">放弃未保存改动</button>
                  <button type="button" :disabled="busy || templateCopyBusy || !templateCopyChanged || !templateCopyPreviewCurrent" @click="saveTemplateCopy">保存为新版本</button>
                </template>
                <template v-else>
                  <button type="button" :disabled="busy || templateCopyBusy || !templateCopyChanged || !templateUpgradeReason.trim() || pendingChangeSet?.status === 'assessed'" @click="createTemplateUpgradeChangeSet">{{ templateCopyBusy ? '处理中…' : '创建模板升级评估' }}</button>
                  <button type="button" :disabled="busy || templateCopyBusy || !templateCopyChanged" @click="discardTemplateCopyDraft">放弃未提交改动</button>
                </template>
              </div>
              <p v-if="templateCopyNotice" class="binding-notice" role="status">{{ templateCopyNotice }}</p>
              <section v-if="templateCopyPreview" class="template-preview" aria-label="模板改动预览" aria-live="polite">
                <h4>版本 {{ templateCopyPreview.source_copy_version }} → {{ templateCopyPreview.target_copy_version }} 预览</h4>
                <p v-if="!templateCopyPreviewCurrent" class="warning" role="status">草稿在预览后又有改动；当前预览已失效，请重新预览后保存。</p>
                <ul v-if="templateCopyPreview.section_changes.length">
                  <li v-for="change in templateCopyPreview.section_changes" :key="change.section_id">
                    章节 {{ change.section_id }}：{{ change.before || '（无）' }} → {{ change.after || '（无）' }}
                  </li>
                </ul>
                <p v-else class="muted">章节标题没有变化。</p>
                <p>字段：{{ templateCopyPreview.fields.length }} 项；规则集{{ templateCopyPreview.ruleset_changed ? '发生变化' : '保持不变' }}。</p>
                <p v-if="templateCopyPreview.missing_required.length" class="warning">缺少必填项：{{ templateCopyPreview.missing_required.join('、') }}</p>
                <p v-if="templateCopyPreview.orphaned.length" class="warning">保留的孤立字段：{{ templateCopyPreview.orphaned.join('、') }}</p>
                <p v-if="templateCopyPreview.incompatible.length" class="warning">类型不兼容：{{ templateCopyPreview.incompatible.join('、') }}</p>
                <p class="template-hash">新内容摘要：{{ templateCopyPreview.target_content_hash }}</p>
                <p class="template-hash">新规则集摘要：{{ templateCopyPreview.target_ruleset_hash }}</p>
              </section>
              <section v-if="project.status === 'active' && pendingChangeSet?.template_upgrade" class="change-set-preview" aria-label="待应用的模板升级">
                <p><strong>模板升级评估 {{ pendingChangeSet.id.slice(0, 8) }}</strong></p>
                <p>将影响 {{ pendingChangeSet.impacts.length }} 个章节，并使这些章节的旧确认失效。</p>
                <template v-if="pendingChangeSet.template_upgrade.impact">
                  <p>字段 {{ pendingChangeSet.template_upgrade.impact.affected_field_ids.length }} 项 · 模板章节 {{ pendingChangeSet.template_upgrade.impact.affected_section_ids.length }} 项 · 规则 {{ pendingChangeSet.template_upgrade.impact.changed_rule_ids.length }} 项有变化。</p>
                  <p>将失效的旧确认：{{ pendingChangeSet.template_upgrade.impact.invalidated_confirmation_chapter_ids.length }} 章；问题处置：{{ pendingChangeSet.template_upgrade.impact.validation_issue_effect }}</p>
                  <p>{{ pendingChangeSet.template_upgrade.impact.delivery_snapshot_effect }}</p>
                  <p v-if="pendingChangeSet.template_upgrade.preview.missing_required.length || pendingChangeSet.template_upgrade.preview.incompatible.length" class="warning">
                    当前迁移失败：缺少必填 {{ pendingChangeSet.template_upgrade.preview.missing_required.join('、') || '无' }}；类型不兼容 {{ pendingChangeSet.template_upgrade.preview.incompatible.join('、') || '无' }}。
                  </p>
                </template>
                <ul>
                  <li v-for="impact in pendingChangeSet.impacts" :key="impact.chapter_id">{{ impact.title }}：{{ impact.reason }}</li>
                </ul>
                <div class="change-set-actions">
                  <button type="button" :disabled="busy || !pendingTemplateUpgradeMatches || ownerActionsFrozen" :title="ownerActionsFrozen ? ownerUnavailableReason : ''" @click="applyTemplateUpgrade">由 Owner 应用并开始复核</button>
                  <button type="button" :disabled="busy || ownerActionsFrozen" :title="ownerActionsFrozen ? ownerUnavailableReason : ''" @click="rejectTemplateUpgrade">驳回评估</button>
                </div>
                <OwnerUnavailableNotice :reason="ownerUnavailableReason" :recovery="ownerRecoveryGuidance" />
                <p v-if="!pendingTemplateUpgradeMatches" class="muted">模板草稿或理由已变化；请重新创建评估后再应用。</p>
              </section>
            </template>
          </template>
          <p v-else class="muted">此课题没有可读取的模板副本；请重新读取课题状态。</p>
        </section>

        <nav class="wb-tabs" role="tablist" aria-label="辅助面板" v-if="project?.status === 'active'">
          <button type="button" role="tab" :aria-selected="inspTab === 'sources'" @click="inspTab = 'sources'; inspOpen = true">来源</button>
          <button type="button" role="tab" :aria-selected="inspTab === 'ai'" @click="inspTab = 'ai'; inspOpen = true">AI</button>
          <button type="button" role="tab" :aria-selected="inspTab === 'history'" @click="inspTab='history';inspOpen=true">版本</button>
          <button type="button" role="tab" :aria-selected="inspTab === 'checks'" @click="inspTab = 'checks'; inspOpen = true">待核 <span class="wb-tab-count">{{ pendingReviewCount }}</span></button>
          <button type="button" class="wb-tabs-close" aria-label="收起辅助面板" @click="closeInsp">×</button>
        </nav>

        <div class="wb-pane wb-pane-checks">
        <div v-if="project?.status === 'active' && displayedReviewItems.length" class="rv">
          <div class="rv-head">
            <h4>待核事项</h4>
            <span class="muted" id="rv-progress">本次核查 {{ reviewProgressState.done }}/{{ reviewProgressState.total }}</span>
            <div class="rv-track" aria-hidden="true"><span :style="{ width: reviewProgressState.total ? (reviewProgressState.done / reviewProgressState.total * 100) + '%' : '0%' }"></span></div>
          </div>
          <p v-if="chapter?.confirmation_valid && !currentConfirmation" class="rv-server-note" role="note">当前版本已确认，处置明细读取中或不可用；下方是新一轮核查草稿；需再次确认后才提交本次处理。</p>
          <div v-if="currentConfirmation" class="rv-confirm-detail" aria-label="已确认处置明细">
            <p class="rv-server-note" role="note">当前版本已于服务器确认。以下为服务端记录的处置明细（只读）：</p>
            <ul>
              <li v-for="d in currentConfirmation.review_decisions" :key="d.review_item_id">
                <span class="badge" :class="d.disposition === 'resolved' ? 'ok' : 'pend'">{{ d.disposition === 'resolved' ? '已解决' : '保留提醒' }}</span>
                事项 {{ d.review_item_id }}：{{ d.reason }}
              </li>
            </ul>
          </div>
          <p class="warning review-lead">本章有 {{ displayedReviewItems.length }} 条待核事项，保存修改会保留这些事项。以下为人工审阅：选择处理方式并填写理由后才记为已处理；最终生效仍随章节确认。</p>
          <div class="rv-list">
            <article v-for="item in displayedReviewItems" :key="item.id" :data-review-id="item.id" class="rv-card" :data-status="statusOf(item)">
              <button type="button" class="rv-card-head" :aria-expanded="expandedReviewId === item.id" @click="expandedReviewId = expandedReviewId === item.id ? '' : item.id">
                <span class="rv-badge" :data-status="statusOf(item)">{{ statusLabelOf(item) }}</span>
                <span class="rv-stmt">{{ item.statement }}</span>
                <small class="rv-anchor" :data-state="anchorStateOf(item)">{{ anchorHintOf(item) }}</small>
              </button>
              <div v-if="expandedReviewId === item.id" class="rv-card-body">
                <p class="muted">核查问题：{{ item.statement }}</p>
                <div class="review-location">
                  <template v-if="item.anchor?.status==='located'"><p class="review-quote">{{ reviewQuotePreview(item.anchor.quote_markdown) }}</p><button type="button" @click="locateReviewItem(item)">定位对应原文</button><button type="button" @click="clearReviewAnchor(item)" :disabled="busy">清除关联</button></template>
                  <p v-else class="muted">{{ item.anchor?.status==='stale' ? '原片段已改变，需重新关联。' : '尚未关联具体片段；此项仍属于当前章节。' }}</p>
                  <button v-if="item.anchor?.status!=='located'" type="button" @click="startRelink(item.id)">关联当前选段</button>
                </div>
                <div class="rv-options" role="group" aria-label="处理方式">
                  <button type="button" class="rv-option" :disabled="busy" :aria-pressed="reviewDraft(item.id).explicitlyChosen === true && reviewDraft(item.id).disposition === 'resolved'" @click="chooseReviewDisposition(item.id, 'resolved')">
                    <strong>我已核查并解决</strong>
                    <small>人工确认该问题不再影响交付；这不是模型或程序对事实真伪的判定。</small>
                  </button>
                  <button type="button" class="rv-option" :disabled="busy" :aria-pressed="reviewDraft(item.id).explicitlyChosen === true && reviewDraft(item.id).disposition === 'retained_warning'" @click="chooseReviewDisposition(item.id, 'retained_warning')">
                    <strong>保留提醒</strong>
                    <small>承认问题存在、本次接受为提示，随版本保留，供后续复核。</small>
                  </button>
                </div>
                <label :for="`reason-${item.id}`">处理理由（必填）</label>
                <textarea :id="`reason-${item.id}`" v-model="reviewDraft(item.id).reason" :disabled="busy" rows="3" maxlength="2000" placeholder="记录核查依据或保留原因"></textarea>
                <p v-if="!reviewDraft(item.id).explicitlyChosen" class="muted">尚未选择处理方式：此项仍为未处理，章节确认会被阻断。</p>
                <p v-else-if="!reviewDraft(item.id).reason.trim()" class="muted">理由为空：此项仍为未处理，章节确认会被阻断。</p>
                <p v-else class="rv-recorded" role="status">本次处理尚未提交；确认章节后生效。</p>
              </div>
            </article>
          </div>
          <div class="rv-nav">
            <button type="button" class="small" :disabled="currentReviewIndex <= 0" @click="expandedReviewId = displayedReviewItems[currentReviewIndex - 1].id">← 上一项</button>
            <span class="muted">第 {{ currentReviewIndex + 1 }}/{{ displayedReviewItems.length }} 项</span>
            <button type="button" class="small" :disabled="currentReviewIndex >= displayedReviewItems.length - 1" @click="expandedReviewId = displayedReviewItems[currentReviewIndex + 1].id">下一项 →</button>
          </div>
        </div>
        <div class="relink-box" aria-label="待核重新关联">
              <label for="relink-item">待核重新关联（无锚点/已失效项）</label>
              <div class="asset-bind-row">
                <select id="relink-item" v-model="relinkItemId" :disabled="busy">
                  <option value="" disabled>选择待核事项</option>
                  <option v-for="it in relinkCandidates" :key="it.id" :value="it.id">{{ it.statement.slice(0, 24) }}</option>
                </select>
                <button type="button" :disabled="busy || !relinkItemId" @click="relinkReviewItem">用当前选段重新关联</button>
              </div>
              <p v-if="relinkNotice" class="binding-notice" role="status">{{ relinkNotice }}</p>
            </div>
</div>

        <div class="wb-pane wb-pane-sources">
        <button v-if="project.status === 'draft'" type="button" class="ghost" @click="closeInsp">收起项目资料</button>
        <section class="assets">
          <ProjectMaterialIntakePanel :key="JSON.stringify([auth.currentUserId,auth.effectiveTenantId,project.id])" :project-id="project.id" :project-name="project.name" :identity="JSON.stringify([auth.currentUserId,auth.effectiveTenantId])" @bound="refreshMaterialAssets" />
          <h3>课题绑定资料</h3>
          <a href="/platform/knowledge-bases" target="_blank" rel="noopener" class="native-bank-link">管理资料库（WeKnora，新窗口）</a><p class="muted">资料库用于保管和索引资料；本窗口保留当前文稿。绑定和正文引用仍在此处分别完成。</p>
          <form class="asset-bind-form" @submit.prevent="bindProjectAsset">
            <label for="knowledge-id">绑定知识资料</label>
            <div class="asset-bind-row">
              <input id="knowledge-id" v-model="knowledgeId" :disabled="busy" placeholder="输入 knowledge_id" />
              <button type="submit" :disabled="busy || !knowledgeId.trim()">绑定</button>
            </div>
          </form>
          <div class="kb-picker" aria-label="从知识库按名称选择资料">
            <p class="muted">按名称选择（不需输入内部 ID）：</p>
            <div class="asset-bind-row">
              <select v-model="kbSelectedId" :disabled="busy || kbLoading" aria-label="知识库" @change="onKbChange">
                <option value="" disabled>{{ kbList.length ? '选择知识库' : '加载知识库…' }}</option>
                <option v-for="kb in kbList" :key="kb.kb_id" :value="kb.kb_id">{{ kb.kb_name }}</option>
              </select>
              <select v-model="kbFileId" :disabled="busy || !kbFiles.length" aria-label="资料文件">
                <option value="" disabled>{{ kbFiles.length ? '选择资料' : '无可用资料' }}</option>
                <option v-for="f in kbFiles" :key="f.id" :value="f.id">{{ f.file_name || f.id }}</option>
              </select>
              <button type="button" :disabled="busy || !kbFileId" @click="bindKbFile">绑定选中资料</button>
            </div>
            <p v-if="kbNotice" class="binding-notice" role="status">{{ kbNotice }}</p>
          </div>
          <p class="muted para-citations">当前段落实际引用：{{ paraCitationLabel || '（本段暂无引用）' }}</p>
          <!-- 绑定成功但资料还没就绪时，它随后会从下面的列表里消失（listAssets 只回允许集合）。
               不说这一句，用户看到的是「绑定成功了但什么都没发生，再绑一次还是这样」。 -->
          <p v-if="bindingNoticeText" class="binding-notice" role="status">{{ bindingNoticeText }}</p>
          <p v-if="assets.length === 0" class="muted">当前课题没有可用的已就绪资料。</p>
          <ul v-else class="asset-list">
            <li v-for="asset in assets" :key="asset.id">
              <span><strong>{{ asset.title || asset.knowledge_id }}</strong><small>版本 {{ asset.asset_revision }}</small></span>
              <em>{{ asset.processing_state === 'ready' ? '已就绪' : asset.processing_state }}</em>
            </li>
          </ul>
        </section>

        <section class="sources">
          <h3>资料检索</h3>
          <form class="source-search-form" @submit.prevent="searchSources">
            <label for="source-query">检索问题</label>
            <div class="asset-bind-row">
              <input id="source-query" v-model="sourceQuery" :disabled="busy || assets.length === 0" placeholder="输入要定位的内容" />
              <button type="submit" :disabled="busy || !sourceQuery.trim() || assets.length === 0">检索</button>
            </div>
          </form>
          <!-- 被拒明细（F07）：服务端对有未授权项的检索整批答 422、一条都不处理，而那句概括
               里没说**是哪一份、为什么**。明细比概括具体，所以它替掉顶部的通用错误提示。 -->
          <section v-if="deniedRows.length" class="denied-sources" role="alert" aria-label="未获授权的资料">
            <p>{{ DENIED_NOTICE }}</p>
            <ul>
              <li v-for="row in deniedRows" :key="row.assetId">
                <span><strong>{{ row.assetId }}</strong> —— {{ row.label }}</span>
                <small>{{ row.next }}</small>
              </li>
            </ul>
            <p class="muted">这里只给得出资料 ID：项目资料列表只列已就绪且已授权的资料，
              被拒的那一份在里面没有痕迹，界面上也就没有它的标题可显示。</p>
          </section>
          <p v-if="searchedNothing" class="muted">暂无可定位来源。</p>
          <ul v-else-if="sources.length" ref="sourceListElement" class="source-list">
            <li v-for="source in sources" :key="source.id">
              <div class="source-head">
                <span><strong>{{ source.locator }}</strong><small>{{ sourceStatusLabel(source.status) }}</small></span>
                <div class="source-actions">
                  <button type="button" :disabled="busy" @click="refreshSource(source.id)">重新定位</button>
                  <!-- 同页展开，不跳知识库页：那条路要 kbId，而 Asset 与 Source 都带不了它。 -->
                  <button type="button" :disabled="contextBusy" :aria-expanded="contextOpenFor === source.id"
                    @click="toggleSourceContext(source.id)">查看原文</button>
                </div>
              </div>
              <p class="source-quote">{{ quotedTextOf(source) }}</p>
              <section v-if="contextOpenFor === source.id" class="source-context" aria-label="引用处的原文上下文">
                <h4>{{ contextHeading }}</h4>
                <p v-if="contextReason" class="muted">{{ contextReason }}</p>
                <!-- 窗口开得出来但一段都没有：引用就在文档的两头。空列表配着「前后各 1 段」
                     的抬头，看上去像加载失败了，所以这一句要说出来。 -->
                <p v-else-if="!contextRows.length" class="muted">这一段前后都没有同族的邻居段，它可能就在文档的两头。</p>
                <ol v-else class="source-context__segments">
                  <li v-for="line in contextRows" :key="line.id" :class="{ 'is-verbatim': line.verbatim }">
                    <small>{{ line.label }}</small>
                    <p v-if="line.text">{{ line.text }}</p>
                    <p v-else class="muted">这一段没有正文可摆。</p>
                    <small v-if="line.note">{{ line.note }}</small>
                  </li>
                </ol>
                <p class="muted">展开的是分块记录里的文本，不是重新解析原文件得到的逐字原文。</p>
              </section>
            </li>
          </ul>
        </section>
        <section v-if="draftCitations.sourceIds.length" class="citation-usages" aria-label="引用用途与限制">
              <h4>引用用途与限制</h4>
              <article v-for="sourceId in draftCitations.sourceIds" :key="sourceId">
                <strong>{{ sourceId }}</strong>
                <p v-if="citationStatus(sourceId)">{{ citationStatus(sourceId)?.detail }}</p>
                <label :for="`citation-purpose-${sourceId}`">用途</label>
                <p v-if="!citationUsageDraft(sourceId).purpose" class="muted">尚未补充用途</p>
                <textarea :id="`citation-purpose-${sourceId}`" v-model="citationUsageDraft(sourceId).purpose" :disabled="busy || workingCopySaving" @input="scheduleWorkingCopySave" maxlength="2000" />
                <label :for="`citation-limitation-${sourceId}`">限制</label>
                <p v-if="!citationUsageDraft(sourceId).limitation" class="muted">尚未补充限制</p>
                <textarea :id="`citation-limitation-${sourceId}`" v-model="citationUsageDraft(sourceId).limitation" :disabled="busy || workingCopySaving" @input="scheduleWorkingCopySave" maxlength="2000" />
              </article>
            </section>
</div>

        <section v-if="project.status === 'active' && chapter" class="generation">
          <SelectedEvidenceContext v-if="project && generationEvidenceRefs.length" :project="project" :current-user-id="auth.currentUserId ?? ''" :selected="generationEvidenceRefs" :readonly="busy || generationPending || rewriteBusy" @remove="removeGenerationEvidence" @open="openSelectedEvidence" />
<section class="selected-rewrite" aria-label="选段改写">
              <h4>选段 AI 改写</h4>
              <p class="muted">先在正文中选中一段，再选择动作与产出参数。生成只会产生候选，不会直接改正文。</p>
              <GenerationActionPanel v-if="project" :project-id="project.id" scope="section" action="rewrite" @submit="onRewriteActionSubmit" />
              <label for="rewrite-instruction">改写要求（不按动作发起的自由文本路径）</label>
              <textarea id="rewrite-instruction" v-model="rewriteInstruction" :disabled="busy || rewriteBusy" rows="2" maxlength="2000" />
              <label v-if="generationEvidenceRefs.length" class="rewrite-evidence-choice"><input v-model="rewriteUseEvidence" type="checkbox" :disabled="rewriteBusy || busy">本次选段改写使用上述 {{generationEvidenceRefs.length}} 张明确选定证据卡（不混入额外来源）</label>
              <fieldset class="rewrite-sources" :disabled="rewriteUseEvidence">
                <legend>额外允许使用的来源</legend>
                <p class="muted">选中原文已有的引用会自动带入并重新核验权限，无需重复勾选；下方可补充其他参考来源。</p>
                <label v-for="source in sources" :key="source.id" class="rewrite-source">
                  <input type="checkbox" :checked="rewriteSourceIds.includes(source.id)" :disabled="source.status !== 'available' || rewriteBusy"
                    @change="toggleRewriteSource(source.id, $event)" />
                  <span>{{ source.locator }}<small>{{ source.quoted_text.slice(0, 120) }}</small></span>
                </label>
                <p v-if="!sources.length" class="muted">如需补充其他参考资料，请先检索。原文已有引用不受此列表是否为空影响。</p>
              </fieldset>
              <button type="button" @click="requestLegacyRewrite" :disabled="busy || rewriteBusy || workingCopySaving || workingCopyLoading || !workingCopy">
                {{ rewriteBusy ? '正在生成候选…' : '按自由文本要求生成候选' }}
              </button>
              <section v-if="rewriteCandidate" class="rewrite-preview" aria-live="polite">
                <p class="muted">候选状态：{{ rewriteCandidateStageLabel }} · 运行模式：{{ rewriteCandidate.run_mode }}</p>
                <p v-if="rewriteCandidateStage === 'failed'" class="warning">
                  <template v-if="rewriteCandidate.error_code === 'source_access_denied'">本次改写未通过引用来源核验。请重新读取项目并检查引用资料是否仍可访问；修改改写要求不能解决来源权限问题。原文未被替换。</template>
                  <template v-else-if="rewriteCandidate.error_code === 'unsupported_format'">模型输出了当前 DOCX 不支持的格式，候选不能采纳。请使用纯文本段落和原文引用；原文未被替换，系统不会自动重试。</template>
                  <template v-else>候选生成未完成（{{ rewriteCandidate.error_code || 'unknown' }}）。原文未被替换，请检查错误原因后重试。</template>
                </p>
                <p v-else-if="rewriteCandidateStage === 'stale' || rewriteCandidateStage === 'not_authorized'" class="warning">{{ rewriteCandidateStageNotice }}</p>
                <template v-else-if="rewriteCandidateStage === 'ready' || rewriteCandidateStage === 'applying'">
                  <h5>原文选段</h5>
                  <pre>{{ rewriteCandidate.selection.selected_text }}</pre>
                  <h5>改写候选</h5>
                  <pre>{{ rewriteCandidate.replacement_markdown }}</pre>
                  <button v-if="rewriteCandidateStage === 'ready'" type="button" class="ghost" @click="openRewriteCompare">展开宽对照</button>
                  <p v-if="rewriteCandidate.replacement_markdown.trim() === rewriteCandidate.selection.selected_text.trim()" class="muted" role="status">本次候选与原文一致，未产生文字改动。可以保留原文，或明确需要改善的地方后再生成。</p>
                  <p class="muted">候选来源：{{ rewriteCandidate.source_ids.length ? rewriteCandidate.source_ids.join('、') : '无' }}</p>
                  <ul v-if="rewriteCandidate.review_items.length" class="rewrite-review-list">
                    <li v-for="item in rewriteCandidate.review_items" :key="item.id">待核：{{ item.statement }}</li>
                  </ul>
                  <button type="button" @click="applyRewriteCandidate" :disabled="rewriteBusy || busy || chapterChanged || !workingCopy || (rewriteCandidateStage !== 'applying' && workingCopy.working_copy_revision !== rewriteCandidate.working_copy_revision)">
                    {{ rewriteCandidateStage === 'applying' ? '重试确认采纳结果' : '接受并应用到工作副本' }}
                  </button>
                  <button type="button" @click="rejectRewriteCandidate" :disabled="rewriteBusy || busy || rewriteCandidateStage === 'applying'">拒绝候选</button>
                </template>
              </section>
            </section>
            <LingDocReviewReportPanel
              v-if="project && chapter && workingCopy"
              :project-id="project.id" :chapter-id="chapter.id"
              scope="section" :resolve-selection="resolveRewriteSelection"
              :expected-spec-revision="project.spec_revision"
              :expected-working-copy-revision="workingCopy.working_copy_revision"
              :base-chapter-version-id="workingCopy.base_chapter_version_id"
              @applied="onReviewReportApplied" />
          <h3>生成候选稿</h3>
          <fieldset class="generation-assets" :disabled="generationPending || busy">
            <legend>本次允许使用的资料</legend>
            <label v-for="asset in readyAssets" :key="asset.id" class="asset-choice">
              <input v-model="selectedAssetIds" type="checkbox" :value="asset.id" />
              {{ asset.title || asset.knowledge_id }}（版本 {{ asset.asset_revision }}）
            </label>
            <p v-if="readyAssets.length === 0" class="muted">请先绑定已就绪的项目资料。</p>
          </fieldset>
          <form class="generation-form" @submit.prevent="startDraft">
            <label for="generation-instruction">写作要求</label>
            <textarea id="generation-instruction" v-model="generationInstruction" rows="3"
              :disabled="busy || generationPending" placeholder="说明本章要回答的问题和需要关注的重点" />
            <div class="actions">
              <p v-if="generationEvidenceRefs.length" class="binding-notice">本次使用 {{generationEvidenceRefs.length}} 张已选择证据卡的原文；额外资料不会混入。<button type="button" @click="generationEvidenceRefs = []">取消卡片选择</button></p>
              <button type="submit" :disabled="busy || generationPending || (!selectedAssetIds.length && !generationEvidenceRefs.length) || !generationInstruction.trim()">生成候选稿</button>
              <button v-if="generationRun" type="button" :disabled="generationBusy" @click="refreshGeneration()">刷新任务状态</button>
              <button v-if="generationRun?.status === 'queued' || generationRun?.status === 'running'" type="button"
                :disabled="generationBusy" @click="cancelGenerationRun()">取消生成</button>
            </div>
          </form>
          <p v-if="generationRun" class="muted">任务 {{ generationRun.id }} · 状态：{{ generationRun.status }}</p>
          <p v-if="generationRun?.error" role="alert" class="warning">{{ generationRun.error.message }}</p>
          <section v-if="generationCandidates.length" class="candidate-list" aria-label="本章候选历史">
            <h4>本章候选历史</h4>
            <ul>
              <li v-for="candidate in generationCandidates" :key="candidate.id">
                <button type="button" :aria-pressed="generationCandidate?.id === candidate.id"
                  @click="showGenerationCandidate(candidate.run_id)">
                  候选 {{ candidate.id.slice(0, 8) }} · {{ candidate.validity }} · {{ new Date(candidate.created_at).toLocaleString() }}
                </button>
              </li>
            </ul>
          </section>
          <article v-if="generationCandidate" class="candidate-preview">
            <h4>候选稿预览（不会自动覆盖章节）</h4>
            <pre>{{ generationCandidate.body_markdown }}</pre>
            <p>引用 {{ generationCandidate.source_ids.length }} 条来源 · 待核事项 {{ generationCandidate.review_items.length }} 条 · 生成时状态：{{ generationCandidate.validity }}（采纳时仍会复核当前版本）</p>
            <div class="actions">
              <button v-for="(sourceId, index) in generationCandidate.source_ids" :key="sourceId" type="button"
                :disabled="busy || contextBusy" @click="showCandidateSource(sourceId)">查看引用 {{ index + 1 }} 原文</button>
              <button type="button" :disabled="busy" @click="openAdoption">采纳到本章</button>
            </div>
          </article>
        </section>

        <!-- v-if 而不是 :open 传布尔：对话框自带本地状态（幂等键、错误文案、提交中），
             v-if 让每次打开都是干净的一份；用 :open 隐藏再显示，会把上一次的失败文案和
             用过的键一起带回来——那个键对应的是上一次的请求体。 -->
        <LingDocCandidateAdoptionDialog
          v-if="adoptionOpen && project && chapter && generationCandidate"
          :project-id="project.id" :chapter="chapter" :candidate="generationCandidate"
          :expected-spec-revision="project.spec_revision"
          @adopted="onAdopted" @cancel="adoptionOpen = false" />

        <form class="spec-form" @submit.prevent="saveConditions">
          <button v-if="project.status==='draft' && project.members.some(m=>m.user_id===auth.currentUserId && m.role==='owner')" type="button" :disabled="busy" @click="discardCurrentDraft">丢弃此草稿</button><h3>研究条件 <button type="button" class="ghost wb-drawer-close" aria-label="关闭研究条件" @click="specOpen = false">×</button></h3>
          <label v-if="project.template_copy?.definition.fields.some(x=>x.field_id==='research_subject')" for="subject">研究主题</label>
          <input v-if="project.template_copy?.definition.fields.some(x=>x.field_id==='research_subject')" id="subject" v-model="subject" :disabled="busy" placeholder="写清项目研究什么" />
          <label v-if="project.template_copy?.definition.fields.some(x=>x.field_id==='research_goal')" for="goal">研究目标</label>
          <textarea v-if="project.template_copy?.definition.fields.some(x=>x.field_id==='research_goal')" id="goal" v-model="goal" :disabled="busy" rows="3" placeholder="写清本轮要验证什么" />
          <template v-for="field in additionalSpecFields" :key="field.field_id"><label :for="'spec-'+field.field_id">{{field.label}}{{field.required?'（必填）':''}}</label><select v-if="field.type==='boolean'" :id="'spec-'+field.field_id" v-model="extraSpecDraft[field.field_id]" :disabled="busy"><option value="">未填写</option><option value="true">是</option><option value="false">否</option></select><input v-else :id="'spec-'+field.field_id" v-model="extraSpecDraft[field.field_id]" :disabled="busy" :type="field.type==='number'||field.type==='integer'?'number':'text'" :step="field.type==='integer'?'1':'any'" :placeholder="field.description || field.label"><p v-if="field.description" class="muted">{{field.description}}</p></template>
          <section v-if="specBasisChanged" class="warning" role="alert"><h4>服务器研究条件已变化，当前输入保留</h4><p>本页输入基于条件版本 {{specEditingRevision}}，服务器当前为 {{project.spec_revision}}。普通课题刷新不会把你的输入自动绑定到新版本。</p><dl><template v-for="field in project.template_copy?.definition.fields" :key="field.field_id"><dt>{{field.label}}</dt><dd>服务器：{{project.spec[field.field_id]||'（空）'}}<br>你的输入：{{specDraftFields[field.field_id]||'（空）'}}</dd></template></dl><button type="button" :disabled="busy" @click="acknowledgeCurrentSpecBasis">已比较，保留输入并关联当前条件版本</button></section>
          <div class="actions">
            <button type="submit" :disabled="busy || !specChanged || specBasisChanged">保存研究条件</button>
            <button v-if="project.status === 'draft'" type="button" @click="activate" :disabled="busy || specChanged || !requiredSpecComplete || ownerActionsFrozen" :title="ownerActionsFrozen ? ownerUnavailableReason : ''">立项并创建章节</button>
          </div>
          <p v-if="specChanged && project.status === 'draft'" class="muted">立项前请先保存研究条件。</p>
          <OwnerUnavailableNotice :reason="ownerUnavailableReason" :recovery="ownerRecoveryGuidance" />
        </form>

        <DraftCandidatePanel ref="draftCandidatePanelRef" v-if="project.status==='draft'" :project="project" :readonly="!canManageProjectMembers" :current-user-id="auth.currentUserId??''" @dirty="draftCandidateDirty=$event" @use-field="useManualDraftField" />

        <!-- 归档入口是本面新建的组件；这里是集成步骤，不独占这个文件。 -->
        <ProjectArchivePanel v-if="project.status === 'active'" :project="project" @archived="refreshProjectVersion()" />

        <section v-if="project.status === 'active' && (specChanged || lastChangeSet)" class="change-set-panel" aria-label="研究条件变更复核">
          <div v-if="specChanged">
            <h3>提交研究条件变更</h3>
            <p class="muted">研究条件变更会使选中章节的旧确认失效，完成重新确认后才能继续冻结和导出。</p>
            <label for="change-reason">变更理由</label>
            <input id="change-reason" v-model="changeReason" :disabled="busy" maxlength="2000" placeholder="说明为什么需要变更" />
            <fieldset :disabled="busy">
              <legend>需要重新复核的章节</legend>
              <label v-for="item in orderedChapters" :key="item.id" class="chapter-impact-choice">
                <input v-model="selectedImpactChapterIds" type="checkbox" :value="item.id" />
                {{ item.title }}
              </label>
            </fieldset>
            <button type="button" :disabled="busy || !changeReason.trim() || selectedImpactChapterIds.length === 0" @click="createConditionsChangePreview">
              生成变更预览
            </button>
            <div v-if="pendingChangeSet" class="change-set-preview">
              <p><strong>语义变更</strong></p>
              <ul>
                <li v-for="field in pendingChangeSet.fields" :key="field.key">
                  {{ field.key }}：{{ field.old_value }} → {{ field.new_value }}
                </li>
              </ul>
              <p><strong>已登记受影响章节</strong></p>
              <ul>
                <li v-for="impact in pendingChangeSet.impacts" :key="impact.chapter_id">{{ impact.title }}：{{ impact.reason }}</li>
              </ul>
              <p class="muted">尚未核对范围：{{ uncheckedChapterTitles.length > 0 ? uncheckedChapterTitles.join('、') : '无（已登记章节覆盖当前章节列表）' }}</p>
              <div class="change-set-actions">
                <button type="button" :disabled="busy || !pendingDraftMatches || ownerActionsFrozen" @click="applyConditionsChange">
                  应用预览并开始复核
                </button>
                <button type="button" :disabled="busy || ownerActionsFrozen" @click="rejectConditionsChange">
                  驳回预览
                </button>
              </div>
              <OwnerUnavailableNotice :reason="ownerUnavailableReason" :recovery="ownerRecoveryGuidance" />
              <p v-if="!pendingDraftMatches" class="muted">研究条件已改变，请重新生成预览。</p>
            </div>
          </div>
          <p v-if="lastChangeSet" class="muted" role="status">
            变更 {{ lastChangeSet.id.slice(0, 8) }} 已{{ changeSetStatusLabel(lastChangeSet.status) }}<template v-if="lastChangeSet.status === 'applied'">；请重新确认受影响章节。</template><template v-else-if="lastChangeSet.status === 'stale'">；基线已过期，请重新生成预览。</template><template v-else-if="lastChangeSet.status === 'satisfied'">；当前项目已包含拟议值，无待应用内容。</template><template v-else>。</template>
          </p>
        </section>

        <!-- 撤权提示（§8）。只在服务端判 restricted 时出现，且它是一条**提示**而不是拦截：
             本轮只有 access-status 这一条读路径带资料层判定，别的端点仍会照常返回内容
             （差额记在 02-接口与Mock约定 §8）。所以这里给的是恢复入口，不是封锁——写成
             封锁，界面就在声称一个它并没有执行的限制。 -->
        <section v-if="restricted" class="access-warning" role="status" aria-label="资料访问状态">
          <h3>{{ RESTRICTED_NOTICE }}</h3>
          <ul v-if="recoveryActions.length" class="access-warning__actions">
            <li v-for="action in recoveryActions" :key="action.code">
              <strong>{{ action.label }}</strong>
              <span v-if="action.detail">{{ action.detail }}</span>
            </li>
          </ul>
          <button type="button" :disabled="accessBusy" @click="recheckAccess">
            {{ accessBusy ? '检查中…' : '重新检查' }}
          </button>
        </section>

        <div v-if="project.status === 'active'" class="chapters">
          <h3>章节</h3>
          <p v-if="chapters.length === 0" class="muted">章节尚未加载，点击“重新读取”重试。</p>
          <div class="chapter-tabs">
            <button v-for="item in orderedChapters" :key="item.id" type="button"
              :class="{ selected: chapter?.id === item.id }" @click="selectChapter(item)">
              {{ item.title }} <small>{{ item.current_version_id ? '已编辑' : '未填写' }}</small>
            </button>
          </div>
          <div v-if="project.status === 'active' && chapters.length && !editorReady" class="editor-loading" role="status">
            <p class="muted">正在读取全部章节工作副本…</p>
            <p v-if="editorLoadError" class="warning">{{ editorLoadError }}</p>
            <button v-if="editorLoadError" type="button" @click="loadAllWorkingCopies">重试</button>
          </div>
          <LingDocEditor v-if="project.status === 'active' && chapters.length && editorReady" ref="editorRef"
            :key="project.id" :chapters="editorChapters" :project-id="project.id" :source-labels="sourceLabels" :review-anchors="editorReviewAnchors"
            :baselines="editorBaselines" :committed-revisions="editorCommitted"
            :save-chapter="saveChapterViaApi" :chapter-write-permissions="editorWritePermissions" :chapter-write-permission-reasons="chapterPermissionReasons"
            @chapter-dirty="onEditorChapterDirty" @saved-all="onEditorSavedAll"
            @autosaved-clean="onEditorSavedAll(true)" @active-chapter="onEditorActiveChapter" @selection-context="onSelectionContext" @source-open="openNativeSource" @review-open="openNativeReview" />

        </div>

        <div v-if="project.status === 'active'" class="wb-delivery" role="dialog" aria-label="交付">
          <header class="wb-drawer-head">
            <h3>交付</h3>
            <button type="button" class="ghost wb-drawer-close" aria-label="关闭交付面板" @click="deliveryOpen = false">×</button>
          </header>
          <div class="wb-delivery-body">
            <DeliveryPanel :project="project" :chapters="chapters"
              @refresh-project="refreshProjectVersion" @computed-receipt="handleComputedReceipt" @review-change-receipt="handleReviewChangeReceipt" />
          </div>
        </div>

        <div v-if="rewriteCompareOpen && rewriteCandidate" class="wb-compare" role="dialog" aria-label="原文与候选对照">
          <div class="wb-compare-pane">
            <p class="wb-eyebrow">CURRENT DOCUMENT</p>
            <h3>原文选段</h3>
            <pre>{{ rewriteCandidate.selection.selected_text }}</pre>
          </div>
          <div class="wb-compare-pane">
            <p class="wb-eyebrow">CANDIDATE · {{ rewriteCandidate.run_mode }}</p>
            <h3>改写候选</h3>
            <pre>{{ rewriteCandidate.replacement_markdown }}</pre>
            <p v-if="rewriteCandidate.review_items.length" class="muted">待核 {{ rewriteCandidate.review_items.length }} 条</p>
          </div>
          <footer class="wb-compare-bar">
            <span class="muted">候选基于工作副本修订 {{ rewriteCandidate.working_copy_revision }}；正文尚未改变。</span>
            <span class="grow" />
            <button type="button" class="primary" @click="applyFromCompare"
              :disabled="rewriteBusy || busy || chapterChanged || (rewriteCandidateStage !== 'applying' && workingCopy?.working_copy_revision !== rewriteCandidate.working_copy_revision)">
              {{ rewriteCandidateStage === 'applying' ? '重试确认采纳结果' : '采纳选段改写' }}
            </button>
            <button type="button" @click="rejectFromCompare" :disabled="rewriteBusy || busy || rewriteCandidateStage === 'applying'">放弃候选</button>
            <button type="button" class="ghost" @click="closeRewriteCompare">返回编辑位置</button>
          </footer>
        </div>

        <div class="wb-pane wb-pane-history" aria-label="当前章节版本与确认"><form v-if="chapter" @submit.prevent="saveAllDocument" class="chapter-form">



            <p v-if="!chapter.current_version_id" class="muted">空章节尚无可确认版本；仍可继续交付检查，检查结果会指出内容缺失。</p>
            <p v-else-if="chapter.confirmation_valid" class="confirmed">当前章节版本已确认。保存新版本后需要重新确认。</p>
            <p v-else-if="workingCopyDiffersFromFormal" class="muted">工作副本与正式版本不同；保存工作副本不会创建正式版本。</p>
            <button v-if="chapter.current_version_id" type="button" @click="confirmCurrentChapter"
              :disabled="confirmationReasons.length > 0" :aria-describedby="confirmationReasons.length ? 'chapter-confirm-help' : undefined">
              {{ chapter.confirmation_valid ? '重新确认当前版本' : '确认当前章节版本' }}
            </button>
            <p v-if="chapter.current_version_id && confirmationReasons.length" id="chapter-confirm-help" class="muted" role="status">当前无法确认：{{ confirmationReasons.join('；') }}。</p>
            <p v-if="draftCitations.malformed" class="warning">{{ MALFORMED_CITATION_MESSAGE }}</p>
            <p v-else-if="draftCitations.sourceIds.length" class="muted">
              本章引用 {{ draftCitations.sourceIds.length }} 条来源。保存时会逐条复核；删掉正文里的标记就等于放弃那一条。
            </p>
            <p v-if="workingCopy" class="muted" role="status">
              工作副本修订 {{ workingCopy.working_copy_revision }} · {{ workingCopyStatusLabel }}
              <span v-if="workingCopy.updated_at">· {{ new Date(workingCopy.updated_at).toLocaleString() }}</span>
            </p>

            <button type="submit" :disabled="busy || workingCopySaving || !chapterChanged">保存工作副本</button>
            <button type="button" @click="commitDraft" :disabled="busy || workingCopySaving || chapterChanged || !workingCopyDiffersFromFormal">
              提交为正式版本
            </button>
            <section v-if="chapterVersions.length" class="chapter-history" aria-label="章节版本历史">
              <h4>正式版本历史</h4>
              <ul>
                <li v-for="version in chapterVersions" :key="version.id">
                  <span>{{ new Date(version.created_at).toLocaleString() }} · {{ version.id.slice(0, 8) }}<small v-if="version.id === chapter.current_version_id"> · 当前版本</small></span>
                  <button type="button" :disabled="busy || workingCopySaving" @click="restoreVersion(version)">恢复到工作副本</button>
                </li>
              </ul>
            </section>
          </form></div>
        <div v-if="anyOverlay" class="wb-scrim" @click="closeOverlays" />
      </section>
      <section v-else class="panel empty-work">
        <div class="document-welcome"><span class="document-eyebrow">研究文稿</span><h2>从一个课题开始写作</h2><p>设定课题 → 连续写作与资料引用 → 核查后交付。</p>
          <form @submit.prevent="create" class="create-form"><label for="welcome-template">课题模板</label><select id="welcome-template" v-model="newTemplateKey" :disabled="busy||catalogLoading"><option value="template-demo@1">两章演示模板 · 1 · 内部演示</option><option v-for="v in publishedTemplates" :key="v.template_id+'@'+v.version" :value="v.template_id+'@'+v.version">{{templateChoiceLabel(v)}}</option></select><p v-if="selectedPublishedTemplate" class="muted">{{templateChoiceDescription(selectedPublishedTemplate)}}</p><p v-else-if="newTemplateKey === 'template-demo@1'" class="muted">当前选择的是内部两章演示模板，不代表正式申报要求。</p><p v-else class="warning" role="alert">先前选择的模板已不在当前可用目录中，请重新选择；不会自动改用演示模板。</p><p v-if="templateCatalogNotice" role="status" class="muted">{{templateCatalogNotice}}</p><input id="welcome-project-name" v-model="newName" required maxlength="120" placeholder="课题名称" aria-label="课题名称"><button type="submit" :disabled="busy || catalogLoading || !newName.trim() || templateChoiceUnavailable">创建课题</button></form>
          <button type="button" class="ghost" @click="railOpen=true; projectPickerOpen=true">打开已有课题</button>
        </div>
      </section>
    </div>

    <div v-if="project && fullPanel" ref="fullDrawerRef" class="full-workflow-backdrop" @mousedown.self="closeFullPanel">
      <section class="full-workflow-dialog" role="dialog" aria-modal="true" :aria-label="fullPanelLabel" tabindex="-1">
        <header class="full-workflow-head"><strong>{{ fullPanelLabel }}</strong><button type="button" :disabled="fullPanelBusy" @click="closeFullPanel">返回文稿 ×</button></header>
        <ResearchPanel v-if="fullPanel === 'research'" ref="researchPanelRef" :project="project" :current-user-id="auth.currentUserId ?? ''" :readonly="fullReadonly" :selected-evidence="generationEvidenceRefs" @close="closeFullPanel" @changed="refreshFullWorkflowProject" @dirty="fullPanelDirty = $event" @use-evidence="useGenerationEvidence" />
        <ImpactOpsPanel v-if="fullPanel === 'changes'" ref="opsPanelRef" :project="project" :current-user-id="auth.currentUserId ?? ''" :readonly="fullReadonly" :preflight-receipt-id="currentCheckReceipt" @close="closeFullPanel" @changed="refreshFullWorkflowProject" @dirty="fullPanelDirty = $event" />
        <RuntimeOpsPanel v-if="fullPanel === 'operations'" ref="runtimeOpsPanelRef" :project="project" :current-user-id="auth.currentUserId ?? ''" :readonly="fullReadonly" @close="closeFullPanel" @changed="refreshFullWorkflowProject" @dirty="fullPanelDirty = $event" />
        <TemplateGovernancePanel v-if="fullPanel === 'governance'" ref="governancePanelRef" :project="project" :chapters="chapters" :sources="sources" :can-manage-catalog="auth.hasRole('admin')" @close="closeFullPanel" @refresh="refreshFullWorkflowProject" @error="errorMessage = $event" @data-dirty="fullPanelDirty = $event" />
        <ProjectCollaborationPanel v-if="fullPanel === 'members'" ref="collaborationPanelRef" :project="project" :chapters="chapters" :assets="assets" :current-user-id="auth.currentUserId ?? ''" :readonly="restricted" @close="closeFullPanel" @changed="refreshFullWorkflowProject" @dirty="fullPanelDirty = $event" />
        <ChapterReviewPanel v-if="fullPanel === 'review' && chapter" ref="chapterReviewPanelRef" :project="project" :chapter="chapter" :current-user-id="auth.currentUserId ?? ''" :selection="commentSelection" :readonly="fullReadonly" :formal-current="!workingCopyDiffersFromFormal && !chapterChanged" @close="closeFullPanel" @changed="refreshFullWorkflowProject" @dirty="fullPanelDirty = $event" @locate="locateComment" />
      </section>
    </div>
    <ChapterCommitReview v-if="pendingChapterCommit && chapter" :chapter-title="chapter.title"
      @wording="commitWordingDraft" @semantic="routeSemanticDraft" @cancel="pendingChapterCommit = null" />
    <p v-if="commitIntentNotice" class="muted" role="status">{{ commitIntentNotice }}</p>
    <div v-if="activationOpen && activationDiff && project" ref="activationDialogRef" class="full-workflow-backdrop" @mousedown.self="closeActivationReview">
      <section class="full-workflow-dialog activation-review" role="dialog" aria-modal="true" aria-labelledby="activation-title" tabindex="-1">
        <header class="full-workflow-head"><strong id="activation-title">立项前核对研究条件</strong><button type="button" :disabled="busy" @click="closeActivationReview">返回修改 ×</button></header>
        <p>将以课题版本 {{activationDiff.current_project_version}}、条件版本 {{activationDiff.current_spec_revision}} 和当前模板副本建立正式基线。请核对字段和 AI 来源；这不会替你判断研究结论。</p>
        <h3>本次研究条件</h3><dl class="activation-fields"><template v-for="field in activationDiff.changed_fields" :key="field.key"><dt>{{conditionLabel(field.key)}} <small>{{field.origin === 'human' ? '人工填写' : 'AI 候选 / 辅助填写'}} · {{field.status}} · 修改者 {{field.modified_by}}</small></dt><dd>{{field.value || '（空）'}}<p v-if="project.spec_fields?.[field.key]?.provenance" class="muted">来源：{{project.spec_fields[field.key]!.provenance!.source_type}} · {{project.spec_fields[field.key]!.provenance!.locator || project.spec_fields[field.key]!.provenance!.source_id || '来源标识待补充'}} · {{project.spec_fields[field.key]!.provenance!.needs_review?'需重新核对':'仅为记录，不证明研究结论'}}</p></dd></template></dl><section v-if="activationDiff.changed_fields.some(x=>x.modified_by!==auth.currentUserId)"><h3>其他成员的修改</h3><ul><li v-for="field in activationDiff.changed_fields.filter(x=>x.modified_by!==auth.currentUserId)" :key="field.key">{{conditionLabel(field.key)}} · {{field.modified_by}} · {{field.modified_at}}</li></ul></section>
        <p v-if="activationDiff.pending_ai_fields.length" class="warning">有 {{activationDiff.pending_ai_fields.length}} 项 AI 辅助字段待你核对。阅读回执绑定当前服务器版本；条件变动后必须重新查看。</p>
        <p v-if="project.project_version !== activationDiff.current_project_version" role="alert" class="warning">课题已变化；请返回并重新查看。</p>
        <label class="activation-consent"><input v-model="activationAcknowledged" type="checkbox" :disabled="busy">我已查看本次研究条件和变更，并确认据此立项。</label>
        <button type="button" class="primary" :disabled="busy || !activationAcknowledged || project.project_version !== activationDiff.current_project_version" @click="confirmActivationReview">{{busy ? '正在立项…' : '确认并立项'}}</button>
      </section>
    </div>

  </main>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import { canLeaveWorkspace, conditionsChangeInputsAreDirty, conditionsChangeSnapshot, confirmationBlockReasons, unsavedInputsAreDirty, installUnloadGuard, reviewDraftsAreDirty, type ReviewDraft, type UnsavedInputState } from './uiSafety'
import type { Candidate } from '@/api/lingdoc/candidateAdoption'
import {
  cancelGeneration, getGeneratedCandidate, getGeneration, listGenerationCandidates, startGeneration,
  type GenerationCandidateSummary, type GenerationRun,
} from '@/api/lingdoc/generation'
import DeliveryPanel from './DeliveryPanel.vue'
import ProjectMembers from './ProjectMembers.vue'
import ProjectArchivePanel from './ProjectArchivePanel.vue'
import ResearchPanel from './ResearchPanel.vue'
import DraftCandidatePanel from './DraftCandidatePanel.vue'
import LingDocReviewReportPanel from './LingDocReviewReportPanel.vue'
import ImpactOpsPanel from './ImpactOpsPanel.vue'
import RuntimeOpsPanel from './RuntimeOpsPanel.vue'
import ChapterCommitReview from './ChapterCommitReview.vue'
import GenerationActionPanel from './GenerationActionPanel.vue'
import { sameChapterCommitBasis, type ChapterCommitBasis } from './chapterCommitReview'
import TemplateGovernancePanel from '@/components/TemplateGovernancePanel.vue'
import ProjectCollaborationPanel from './ProjectCollaborationPanel.vue'
import { listMyProjectInvitations, acceptProjectInvitation, type ProjectInvitation } from '@/api/lingdoc/collaboration'
import { getEffectivePermission } from '@/api/lingdoc/permissions'
import { CatalogIntegrityError, catalogNoticeForView, invitationAssignment, invitationScopeDetail, invitationStatusLabel, invitationViewState, permissionIdentity, publishedChoices, recallNoticeForProject, resolveTemplateChoice, templateChoiceDescription, templateChoiceLabel, templateStatusErrorFor, toggleEvidenceSelection } from './firstPassEntryState'
import { getTemplateVersion, listTemplateProfiles, listTemplateVersions, type TemplateCatalogVersion } from '@/api/lingdoc/templateGovernance'
import ChapterReviewPanel from './ChapterReviewPanel.vue'
import type { CommentSelection } from '@/api/lingdoc/chapterReview'
import type { VersionRef } from '@/api/lingdoc/agentApi'
import { getActivationDiff, recordActivationRead, listDiscardedProjects, discardDraftProject, restoreDraftProject, type ActivationDiff, type ActivationReadReceipt } from '@/api/lingdoc/projectInitialization'
import LingDocCandidateAdoptionDialog from '@/components/LingDocCandidateAdoptionDialog.vue'
import SelectedEvidenceContext from './SelectedEvidenceContext.vue'
import { RESTRICTED_NOTICE, isRestricted, recoveryActionsOf } from './accessStatus'
import { chapterCitations, MALFORMED_CITATION_MESSAGE } from './chapterCitations'
import { clearGenerationAttempt, generationIdempotencyKey } from './generationAttempt'
import {
  CONTEXT_UNAVAILABLE_NOTICE, contextLines, contextNotice, quotedTextOf, sourceStatusLabel, windowLabel,
} from './sourceContext'
import { DENIED_NOTICE, bindingNotice, denyReasonOf, deniedSourcesOf } from './sourceNotices'
import { useAuthStore } from '@/stores/auth'
import type { WritingActionOutputParams, WritingActionScope } from '@/api/lingdoc/writingAction'
import { reviewAnchorHint, reviewAnchorState, reviewItemStatus, reviewProgress, reviewStatusLabel, type ReviewDisposition } from './reviewPanel'
import { rewriteStage, rewriteStageAdoptable, rewriteStageLabel, rewriteStageNotice } from './rewriteStage'
import { inspectorOnNarrowTransition, mergeWbLayoutPref, readWbLayoutPref, wbLayoutStorageKey, type WbLayoutPref } from './layoutPref'
import { filterProjects } from './projectPicker'
import { rewriteAdoptionConfirmation } from './candidateAdoption'
import { OWNER_DECISION_UNAVAILABLE_TEXT, useOwnerAvailability } from '@/composables/useOwnerAvailability'
import OwnerUnavailableNotice from './OwnerUnavailableNotice.vue'
import { listKnowledgeBases, listKnowledgeFiles } from '@/api/knowledge-base'
import ProjectMaterialIntakePanel from './ProjectMaterialIntakePanel.vue'
import LingDocEditor from './editor/LingDocEditor.vue'
import { copyMatchesFormal, committedRevision, currentAnchorRange, sortChaptersByTemplate } from './editor/documentState'
import { plainOf, parseInlineText } from './editor/markdown'
import {
  activateProject, applyChangeSet, bindAsset, commitWorkingCopy, confirmChapter, createChangeSet, createProject, getAccessStatus, getProject,
  getSource, getSourceContext, getWorkingCopy, getChangeSet, listAssets, listChangeSets, listChapterVersions, listChapters, listProjects,
  applySelectedRewrite, createSelectedRewrite, getSelectedRewrite, getCurrentConfirmation, putReviewItemAnchor,
  previewTemplateCopyEdit, rejectChangeSet, restoreWorkingCopy, retrieveSources, saveProjectMembers, saveSpec, saveTemplateCopyEdit, saveWorkingCopy,
  type AccessStatus, type Asset, type Chapter, type ChapterVersion, type ChangeSet, type CitationUsage, type Confirmation, type Project, type ProjectMemberInput, type ReviewDecision,
  type TemplateCopyDefinition, type TemplateCopyEditInput, type TemplateMigrationPreview,
  type SelectedRewriteCandidate, type Source, type SourceContext, type WorkingCopy,
} from '@/api/lingdoc/workspace'

const projects = ref<Project[]>([])
const auth = useAuthStore()
const truncated = ref(false)
const project = ref<Project | null>(null)
// Owner 可用性事实与恢复指引文案都在这一处持有：按钮的置灰条件与提示文案
// 从这里取，不在本页另写一遍。
const {
  frozen: ownerActionsFrozen,
  reason: ownerUnavailableReason,
  recovery: ownerRecoveryGuidance,
  noticeReason: ownerNoticeReason,
  noticeRecovery: ownerNoticeRecovery,
  refresh: refreshOwnerAvailability,
} = useOwnerAvailability(() => project.value?.id, { reasonText: OWNER_DECISION_UNAVAILABLE_TEXT })
const memberSaving = ref(false)
const memberDraftDirty = ref(false)

type FullPanel = 'research' | 'changes' | 'operations' | 'governance' | 'members' | 'review'
const fullPanel=ref<FullPanel|null>(null),fullPanelDirty=ref(false),fullDrawerRef=ref<HTMLElement|null>(null)
const researchPanelRef=ref<InstanceType<typeof ResearchPanel>|null>(null)
const opsPanelRef=ref<InstanceType<typeof ImpactOpsPanel>|null>(null),runtimeOpsPanelRef=ref<InstanceType<typeof RuntimeOpsPanel>|null>(null),governancePanelRef=ref<InstanceType<typeof TemplateGovernancePanel>|null>(null),collaborationPanelRef=ref<InstanceType<typeof ProjectCollaborationPanel>|null>(null),chapterReviewPanelRef=ref<InstanceType<typeof ChapterReviewPanel>|null>(null)
const fullPanelBusy=computed(()=>!!researchPanelRef.value?.operationBusy||!!opsPanelRef.value?.busy||!!runtimeOpsPanelRef.value?.busy||!!governancePanelRef.value?.busy||!!collaborationPanelRef.value?.busy||!!chapterReviewPanelRef.value?.busy)
const fullPanelLabel=computed(()=>({research:'调研与证据',changes:'正式变更与运行',operations:'运行观测与真实试用',governance:'模板目录与规则',members:'成员协作',review:'章节评审与批注'}[fullPanel.value??'research']))
const fullReadonly=computed(()=>{const m=project.value?.members.find(x=>x.user_id===auth.currentUserId);return restricted.value||!m||m.status!==undefined&&m.status!==''&&m.status!=='active'||m.role!=='owner'&&m.governance_role!=='admin'&&!(m.function_roles??[]).some(x=>x!=='observer')})
const commentSelection=ref<CommentSelection|undefined>(undefined)
const generationEvidenceRefs=ref<VersionRef[]>([])
const chapterWritePermissions=ref<Record<string,boolean>>({}),chapterConfirmPermissions=ref<Record<string,boolean>>({}),chapterPermissionReasons=ref<Record<string,string>>({})
const editorWritePermissions=computed(()=>restricted.value?{}:chapterWritePermissions.value)
let permissionEpoch=0
let lastPermissionIdentity=''
async function loadChapterPermissions(id:string,version:number,list:Chapter[],session:number){const epoch=++permissionEpoch;const p=project.value;const identity=p?permissionIdentity(p,auth.currentUserId,session,auth.effectiveTenantId):'';if(!identity||identity!==lastPermissionIdentity){chapterWritePermissions.value={};chapterConfirmPermissions.value={};chapterPermissionReasons.value={}}lastPermissionIdentity=identity;const writes:Record<string,boolean>={},confirms:Record<string,boolean>={},reasons:Record<string,string>={};await Promise.all(list.map(async c=>{try{const [w,f]=await Promise.all([getEffectivePermission(id,`write:${c.id}`),getEffectivePermission(id,`confirm:${c.id}`)]);if(w.data.project_version!==version||f.data.project_version!==version){reasons[c.id]='课题权限版本已变化，请重新读取。';return}writes[c.id]=w.data.allowed;confirms[c.id]=f.data.allowed;reasons[c.id]=w.data.allowed?'':`当前章只读：${w.data.reason_code||'未授予写作权限'}`}catch{reasons[c.id]='无法确认当前章写作权限，请重新读取。'}}));if(epoch!==permissionEpoch||project.value?.id!==id||editorSessionEpoch!==session||project.value.project_version!==version)return;chapterWritePermissions.value=writes;chapterConfirmPermissions.value=confirms;chapterPermissionReasons.value=reasons}
const invitationLoading=ref(false),invitationError=ref('')
const notifyOpen=ref(true)
let invitationEpoch=0
const myInvitations=ref<ProjectInvitation[]>([]),invitationBusy=ref(false),newTemplateKey=ref('template-demo@1'),publishedTemplates=ref<TemplateCatalogVersion[]>([]),catalogLoading=ref(false),catalogError=ref('')
const selectedPublishedTemplate=computed(()=>publishedTemplates.value.find(v=>`${v.template_id}@${v.version}`===newTemplateKey.value))
const templateChoiceUnavailable=computed(()=>newTemplateKey.value!=='template-demo@1'&&!selectedPublishedTemplate.value)
const templateCatalogNotice=computed(()=>catalogNoticeForView(catalogLoading.value,catalogError.value,publishedTemplates.value.length))
const templateRecall=ref<NonNullable<TemplateCatalogVersion['recall']>|null>(null),templateStatusError=ref('')
const activeInvitationCount=computed(()=>myInvitations.value.filter(i=>invitationViewState(i)==='active').length)
async function loadInvitationInbox(){const epoch=++invitationEpoch,user=auth.currentUserId,tenant=auth.effectiveTenantId;invitationLoading.value=true;invitationError.value='';myInvitations.value=[];try{const result=await listMyProjectInvitations();if(epoch===invitationEpoch&&auth.currentUserId===user&&auth.effectiveTenantId===tenant)myInvitations.value=result.data}catch{if(epoch===invitationEpoch&&auth.currentUserId===user&&auth.effectiveTenantId===tenant)invitationError.value='邀请暂不可读，不能据此判断没有邀请。请重新读取。'}finally{if(epoch===invitationEpoch)invitationLoading.value=false}}
async function acceptMyInvitation(invite:ProjectInvitation){if(invitationBusy.value)return;invitationBusy.value=true;const body={expected_invitation_version:invite.version},name=`invite:${invite.id}`;try{await acceptProjectInvitation(invite.project_id,invite.id,invite.version,operationKey(name,body));attempts.delete(name);await loadInvitationInbox();await loadProjects();await selectProject(invite.project_id)}catch(e){failure(e)}finally{invitationBusy.value=false}}
let catalogEpoch=0
async function loadPublishedTemplates(){const epoch=++catalogEpoch,user=auth.currentUserId,tenant=auth.effectiveTenantId;catalogLoading.value=true;catalogError.value='';publishedTemplates.value=[];const current=()=>epoch===catalogEpoch&&auth.currentUserId===user&&auth.effectiveTenantId===tenant;try{const profiles=(await listTemplateProfiles({state:'published'})).data;const versions=await Promise.all(profiles.map(x=>listTemplateVersions(x.template_id)));if(current())publishedTemplates.value=publishedChoices(versions.flatMap(x=>x.data))}catch{if(current())catalogError.value='模板目录暂不可读；可以使用明确标为演示的两章模板。'}finally{if(current())catalogLoading.value=false}}
let templateStatusEpoch=0
async function loadProjectTemplateStatus(){const epoch=++templateStatusEpoch,p=project.value,user=auth.currentUserId,tenant=auth.effectiveTenantId;templateRecall.value=null;templateStatusError.value='';if(!p)return;const current=()=>epoch===templateStatusEpoch&&project.value?.id===p.id&&project.value.template_id===p.template_id&&project.value.template_version===p.template_version&&auth.currentUserId===user&&auth.effectiveTenantId===tenant;try{const result=await getTemplateVersion(p.template_id,p.template_version);if(current())templateRecall.value=recallNoticeForProject(p,result.data)}catch(error:unknown){if(current()){if(error instanceof CatalogIntegrityError)console.error('[LingDoc] template catalog integrity failure',{project_id:p.id,template_id:p.template_id,template_version:p.template_version,error:error.message});templateStatusError.value=templateStatusErrorFor(error,p.template_id==='template-demo'&&p.template_version==='1')}}}
watch(()=>[project.value?.id,project.value?.template_id,project.value?.template_version,auth.currentUserId,auth.effectiveTenantId],()=>{void loadProjectTemplateStatus()})
watch(()=>[project.value?.id,project.value?.project_version,auth.currentUserId,auth.effectiveTenantId],()=>{const p=project.value;if(p)void loadChapterPermissions(p.id,p.project_version,chapters.value,editorSessionEpoch);else{permissionEpoch++;lastPermissionIdentity='';chapterWritePermissions.value={};chapterConfirmPermissions.value={}}})
const activationOpen=ref(false),activationDialogRef=ref<HTMLElement|null>(null),activationAcknowledged=ref(false),activationDiff=ref<ActivationDiff|null>(null),activationReceipt=ref<ActivationReadReceipt|null>(null)
let fullReturnFocus:HTMLElement|null=null
function closeFullPanel(){if(fullPanelBusy.value)return;if(fullPanelDirty.value&&!window.confirm('此面板有未提交输入。返回文稿会放弃这些输入，是否继续？'))return;fullPanel.value=null;fullPanelDirty.value=false;nextTick(()=>fullReturnFocus?.isConnected&&fullReturnFocus.focus())}
function openFullPanel(panel:FullPanel){if(!project.value)return;if(fullPanel.value&&fullPanel.value!==panel){closeFullPanel();if(fullPanel.value)return}fullReturnFocus=document.activeElement instanceof HTMLElement?document.activeElement:null;if(panel==='review'){const selected=editorRef.value?.selectionToUtf16();commentSelection.value=selected?.ok&&selected.chapterId===chapter.value?.id&&!workingCopyDiffersFromFormal.value&&!chapterChanged.value?{start_utf16:selected.start_utf16,end_utf16:selected.end_utf16,quote_markdown:selected.quote}:undefined}fullPanel.value=panel}
async function refreshFullWorkflowProject(){const id=project.value?.id,session=editorSessionEpoch;if(!id)return;try{const [result,formal]=await Promise.all([getProject(id),project.value?.status==='active'?listChapters(id):Promise.resolve(null)]);if(project.value?.id!==id||editorSessionEpoch!==session)return;if(result.data.project_version<project.value.project_version)return;project.value=result.data;if(formal){chapters.value=formal.data;const active=formal.data.find(x=>x.id===chapter.value?.id);if(active)chapter.value=active}void loadAccessStatus(id);void loadChapterPermissions(id,result.data.project_version,chapters.value,session)}catch(e){if(project.value?.id===id&&editorSessionEpoch===session)failure(e)}}
function removeGenerationEvidence(ref:VersionRef) {
 if (busy.value || generationPending.value || rewriteBusy.value) return
 generationEvidenceRefs.value = generationEvidenceRefs.value.filter(item=>item.id!==ref.id || item.version!==ref.version)
 if (!generationEvidenceRefs.value.length) rewriteUseEvidence.value=false
}
async function openSelectedEvidence(ref:VersionRef) {
 if (!project.value || busy.value || fullPanelBusy.value || !generationEvidenceRefs.value.some(item=>item.id===ref.id&&item.version===ref.version)) return
 const projectId=project.value.id, userId=auth.currentUserId
 openFullPanel('research')
 await nextTick()
 if (project.value?.id!==projectId || auth.currentUserId!==userId || fullPanel.value!=='research' || !generationEvidenceRefs.value.some(item=>item.id===ref.id&&item.version===ref.version)) return
 await researchPanelRef.value?.showEvidenceCard(ref)
}
function useGenerationEvidence(refs:VersionRef[]){generationEvidenceRefs.value=toggleEvidenceSelection(generationEvidenceRefs.value,refs);bindingNoticeText.value=`已选择 ${generationEvidenceRefs.value.length} 张证据卡；目标章为 ${chapter.value?.title??'尚未选择'}。整章生成使用此集合，选段改写须另行明确勾选。服务端会核对版本、适用章节与当前授权。`}
function locateComment(selection:CommentSelection){const id=chapter.value?.id;if(!id)return;const ok=editorRef.value?.locateMarkdownRange(id,selection.start_utf16,selection.end_utf16,selection.quote_markdown);if(!ok){errorMessage.value='正文已变化，当前无法安全定位该历史批注；没有猜测关联。';return}closeFullPanel()}
function closeActivationReview(){if(busy.value)return;activationOpen.value=false;activationAcknowledged.value=false;activationReceipt.value=null;nextTick(()=>fullReturnFocus?.isConnected&&fullReturnFocus.focus())}
async function confirmActivationReview(){if(!project.value||!activationDiff.value||!activationAcknowledged.value||busy.value)return;const p=project.value,id=p.id,session=editorSessionEpoch,review=activationDiff.value;if(p.project_version!==review.current_project_version){errorMessage.value='课题已变化，请重新查看立项条件。';return}busy.value=true;errorMessage.value='';try{if(!activationReceipt.value)activationReceipt.value=(await recordActivationRead(id,review.current_project_version)).data;if(project.value?.id!==id||session!==editorSessionEpoch)return;const receipt=activationReceipt.value,input={expected_spec_revision:review.current_spec_revision,expected_project_version:review.current_project_version,activation_read_id:receipt.id};await activateProject(id,review.current_spec_revision,operationKey(`activate:${id}`,input),review.current_project_version,receipt.id);if(project.value?.id!==id||session!==editorSessionEpoch)return;attempts.delete(`activate:${id}`);activationOpen.value=false;activationAcknowledged.value=false;activationReceipt.value=null;await loadProjects();await selectProject(id)}catch(e){if(project.value?.id===id&&session===editorSessionEpoch)failure(e)}finally{if(session===editorSessionEpoch)busy.value=false}}
function onFullDialogKey(e:KeyboardEvent){if(!fullPanel.value&&!activationOpen.value)return;const root=fullPanel.value?fullDrawerRef.value:activationDialogRef.value;if(!root||root.querySelector('dialog[open]'))return;if(e.key==='Escape'){e.preventDefault();e.stopImmediatePropagation();if(fullPanel.value)closeFullPanel();else closeActivationReview();return}if(e.key!=='Tab')return;const elements=Array.from(root.querySelectorAll<HTMLElement>('button:not(:disabled),input:not(:disabled),textarea:not(:disabled),select:not(:disabled),a[href],[tabindex="0"]')).filter(x=>x.getClientRects().length>0);if(!elements.length){e.preventDefault();root.querySelector<HTMLElement>('[role="dialog"]')?.focus();return}const first=elements[0],last=elements[elements.length-1];if(e.shiftKey&&(document.activeElement===first||!root.contains(document.activeElement))){e.preventDefault();last.focus()}else if(!e.shiftKey&&(document.activeElement===last||!root.contains(document.activeElement))){e.preventDefault();first.focus()}}
watch(()=>[fullPanel.value,activationOpen.value],async()=>{if(!fullPanel.value&&!activationOpen.value)return;await nextTick();const root=fullPanel.value?fullDrawerRef.value:activationDialogRef.value;root?.querySelector<HTMLElement>('button:not(:disabled),[tabindex="-1"]')?.focus()})
watch(()=>project.value?.id,()=>{fullPanel.value=null;fullPanelDirty.value=false;generationEvidenceRefs.value=[];rewriteUseEvidence.value=false;activationOpen.value=false;activationDiff.value=null;activationReceipt.value=null})
onMounted(()=>document.addEventListener('keydown',onFullDialogKey,true))
onUnmounted(()=>document.removeEventListener('keydown',onFullDialogKey,true))


const templateCopyDraft = ref<TemplateCopyDefinition | null>(null)
const templateCopyPreview = ref<TemplateMigrationPreview | null>(null)
const templateCopyPreviewFingerprint = ref('')
const templateCopyBusy = ref(false)
const templateCopyNotice = ref('')
const templateUpgradeReason = ref('')
const pendingTemplateUpgrade = ref<{ fingerprint: string; reason: string } | null>(null)
const chapters = ref<Chapter[]>([])
const assets = ref<Asset[]>([])
const selectedAssetIds = ref<string[]>([])
const knowledgeId = ref('')
async function refreshMaterialAssets() {
  const id = project.value?.id, session = editorSessionEpoch
  if (!id) return
  try { const result = await listAssets(id); if (project.value?.id === id && session === editorSessionEpoch) assets.value = result.data ?? [] }
  catch { if (project.value?.id === id && session === editorSessionEpoch) bindingNoticeText.value = '材料操作后资料列表暂不可读，请重新读取。' }
}
/* 按名称绑定：知识库可用资料 / 课题绑定资料（assets 列表）/ 本段实际引用（paraCitationLabel）三层分开 */
const kbList = ref<Array<{ kb_id: string; kb_name: string }>>([])
const kbFiles = ref<Array<{ id: string; file_name: string }>>([])
const kbSelectedId = ref('')
const kbFileId = ref('')
const kbLoading = ref(false)
const kbNotice = ref('')
async function loadKbList() {
  if (kbList.value.length) return
  kbLoading.value = true
  try {
    const r = await listKnowledgeBases()
    const items = (r.data?.items ?? r.data ?? []) as Array<{ kb_id?: string; kb_name?: string; id?: string; name?: string }>
    kbList.value = items.map(k => ({ kb_id: String(k.kb_id ?? k.id ?? ''), kb_name: String(k.kb_name ?? k.name ?? k.kb_id ?? k.id) })).filter(k => k.kb_id)
  } catch { kbNotice.value = '知识库列表加载失败，可仍用上方手动方式绑定。' }
  finally { kbLoading.value = false }
}
void loadKbList()
async function onKbChange() {
  kbFiles.value = []
  kbFileId.value = ''
  if (!kbSelectedId.value) return
  try {
    const r = await listKnowledgeFiles(kbSelectedId.value, { page: 1, page_size: 100, parse_status: 'completed' })
    const items = (r.data?.items ?? r.data ?? []) as Array<{ id?: string; file_name?: string }>
    kbFiles.value = items.map(f => ({ id: String(f.id ?? ''), file_name: String(f.file_name ?? f.id ?? '') })).filter(f => f.id)
  } catch { kbNotice.value = '资料列表加载失败。' }
}
async function bindKbFile() {
  if (!project.value || busy.value || !kbFileId.value) return
  busy.value = true
  kbNotice.value = ''
  const projectId = project.value.id
  try {
    await bindAsset(projectId, kbFileId.value, operationKey('asset-kb', { kb: kbSelectedId.value, id: kbFileId.value }))
    kbNotice.value = '绑定请求已提交；就绪后出现在下方「课题绑定资料」列表。'
    const refreshed = await listAssets(projectId)
    assets.value = refreshed.data ?? []
  } catch (e) { failure(e) }
  finally { busy.value = false }
}
const paraCitationLabel = computed(() => {
  void selectionContextVersion.value
  const ids=editorRef.value?.currentParagraphSources() ?? []
  return ids.map(id=>sourceLabels.value[id] || '来源（名称待加载）').join('、')
})

const sourceQuery = ref('')
const sources = ref<Source[]>([])
// 上一次**真的问过**的那个问题。空结果与「还没检索」在界面上必须分开：只看 sources.length
// 的话，刚打开项目就会显示一句「暂无可定位来源」。
const searchedQuery = ref('')
const deniedRows = ref<Array<{ assetId: string; label: string; next: string }>>([])
const bindingNoticeText = ref('')
const sourceListElement = ref<HTMLElement | null>(null)
const contextSourceId = ref('')
const sourceContext = ref<SourceContext | null>(null)
const contextBusy = ref(false)
const chapter = ref<Chapter | null>(null)
const workingCopy = ref<WorkingCopy | null>(null)
const chapterVersions = ref<ChapterVersion[]>([])
const workingCopySaving = ref(false)
const workingCopyStatus = ref<'saved' | 'unsaved' | 'saving' | 'error'>('saved')
const workingCopyLoading = ref(false)
const rewriteCandidate = ref<SelectedRewriteCandidate | null>(null)
// 候选走到了哪一步：失效判据归 validity 一个拥有者，「正在应用」归
// apply_pending 一个拥有者，模板只读这一个计算属性。
const rewriteCandidateStage = computed(() => rewriteStage(rewriteCandidate.value))
const rewriteCandidateStageLabel = computed(() => rewriteStageLabel(rewriteCandidateStage.value))
const rewriteCandidateStageNotice = computed(() => rewriteStageNotice(rewriteCandidateStage.value))
const rewriteInstruction = ref('保留原意、事实、数字、引用和不确定性说明。梳理句子的主次关系，精简重复或冗长表达，让并列内容和前后衔接更清楚；不要仅替换序号或同义词，也不要为变化而变化。原文无需修改时可保持原文。使用纯文本段落，保留引用标记，不使用加粗等 Markdown 格式。')
const rewriteSourceIds = ref<string[]>([])
const rewriteUseEvidence=ref(false)
const rewriteBusy = ref(false)
const rewriteOpen = ref(false)
// 这一次按动作发起的改写计划：动作、目标范围、产出参数与补充说明。
//
// 它是**一次**计划，不是页面的常驻设置：动作面板每次提交都覆盖它，而走自由
// 文本路径时它被清空——否则「按自由文本要求生成候选」会悄悄带上上一次的动作。
const rewriteActionPlan = ref<{
  action: string
  scope: WritingActionScope
  output_params: WritingActionOutputParams
  instruction: string
} | null>(null)
/* 待核重新关联：选段经三重验证映射后真实 PUT anchor；无锚点/失效项才可关联 */
const relinkItemId = ref('')
const relinkNotice = ref('')
const relinkCandidates = computed(() => (workingCopy.value?.review_items ?? chapter.value?.review_items ?? [])
  .filter(it => {
    const a = (it as { anchor?: { status?: string } | null }).anchor
    return !a || a.status !== 'located'
  }))
async function relinkReviewItem() {
  const proj = project.value
  const ch = chapter.value
  const itemId = relinkItemId.value
  if (!proj || !ch || !itemId || busy.value) return
  relinkNotice.value = ''
  // 先冲刷编辑器脏稿，保证 working_copy_revision 与服务端一致
  if (editorRef.value?.hasUnsaved && !(await editorRef.value.flushSave()).ok) { relinkNotice.value='保存未完成，请先处理错误，当前输入保留。';return }
  const doc = editorDocs.value[ch.id]
  const mapped = editorRef.value?.selectionToUtf16()
  if (!mapped || !mapped.ok || mapped.chapterId !== ch.id) {
    relinkNotice.value = '请先在当前章正文选中要关联的段落（映射失败或跨章时不发送请求）。'
    return
  }
  busy.value = true
  try {
    await putReviewItemAnchor(proj.id, ch.id, itemId, {
      expected_chapter_version_id: ch.current_version_id,
      expected_spec_revision: proj.spec_revision,
      expected_working_copy_revision: doc?.revision ?? workingCopy.value?.working_copy_revision ?? 0,
      anchor: { start_utf16: mapped.start_utf16, end_utf16: mapped.end_utf16, quote_markdown: mapped.quote },
    }, operationKey(`anchor:${itemId}`, mapped))
    relinkNotice.value = '已关联并保存。锚点版本基线：当前工作副本。'
    relinkItemId.value = ''
    await loadAllWorkingCopies()
    await loadChapterDraft(proj.id, ch.id)
  } catch (e) {
    const err = e as { status?: number }
    relinkNotice.value = err?.status === 409
      ? '关联失败：版本已变化（409）。当前输入已保留，请重新读取后重试。'
      : '关联失败，请重试。'
  } finally { busy.value = false }
}
function lastProjectStorageKey() {
  return 'lingdoc:last-project:'+String(auth.currentUserId??'anonymous')+':'+String(auth.effectiveTenantId??'none')
}
function reviewQuotePreview(markdown: string) {
  const text=plainOf(parseInlineText(markdown))
  return text.replace(/\[\[source:([^\]]+)\]\]/g,(_match,id)=>`[${sourceLabels.value[id] || '来源'}]`)
}
function openInspector(tab: 'sources' | 'ai' | 'checks' | 'history') { inspTab.value=tab;inspOpen.value=true;saveWbLayoutPref({insp:true}) }
function startRelink(itemId: string) { relinkItemId.value=itemId;expandedReviewId.value=itemId;relinkNotice.value='在正文中选择对应片段，再点击“用当前选段重新关联”。'; }
async function locateReviewItem(item: Chapter['review_items'][number]) {
  if(!editorRef.value) return
  if(editorRef.value.hasUnsaved && !(await editorRef.value.flushSave()).ok) { relinkNotice.value='请先处理保存错误；原文输入保留。';return }
  const current=workingCopy.value?.review_items.find(i=>i.id===item.id) ?? item
  const range=currentAnchorRange(currentBodyMarkdown(),current.anchor)
  if(!range || !chapter.value || !editorRef.value.locateMarkdownRange(chapter.value.id,range.from,range.to,range.quote)) { relinkNotice.value='这条待核项没有可核对的当前定位，请用当前选段重新关联。';return }
  expandedReviewId.value=item.id
  if(wbWidth.value<INSPECTOR_DOCK_MIN_WIDTH) inspOpen.value=false
}
async function clearReviewAnchor(item: Chapter['review_items'][number]) {
  if(!project.value || !chapter.value || !workingCopy.value || busy.value) return
  if(editorRef.value?.hasUnsaved && !(await editorRef.value.flushSave()).ok) return
  const pid=project.value.id,chid=chapter.value.id,copy=workingCopy.value
  busy.value=true
  try {
    const result=await putReviewItemAnchor(pid,chid,item.id,{expected_chapter_version_id:copy.base_chapter_version_id,expected_spec_revision:copy.spec_revision,expected_working_copy_revision:copy.working_copy_revision,anchor:null},operationKey('clear-anchor:'+item.id,{revision:copy.working_copy_revision}))
    if(project.value?.id!==pid || chapter.value?.id!==chid) return
    workingCopy.value=result.data;editorDocs.value[chid]=snapshotOf(result.data);await synchronizeCleanDocument()
  } catch(e) {failure(e)} finally {busy.value=false}
}
const newName = ref('')
const extraSpecDraft=ref<Record<string,string>>({})
const draftCandidateDirty=ref(false),draftCandidatePanelRef=ref<InstanceType<typeof DraftCandidatePanel>|null>(null)
const specEditingRevision=ref<number|null>(null)
const specBasisChanged=computed(()=>!!project.value&&specEditingRevision.value!==null&&specEditingRevision.value!==project.value.spec_revision)
function acknowledgeCurrentSpecBasis(){const p=project.value;if(!p||!specBasisChanged.value||busy.value)return;if(!window.confirm("已比较服务器当前条件与自己的输入，保留输入并基于当前版本继续编辑？下一步仍需要保存或正式变更复核。"))return;specEditingRevision.value=p.spec_revision;pendingChangeSet.value=null;pendingDraft.value=null}
function useManualDraftField(fieldId:string,value:string){if(!project.value||project.value.status!=='draft'||!canManageProjectMembers.value)return;const field=project.value.template_copy?.definition.fields.find(x=>x.field_id===fieldId);if(!field)return;const current=specDraftFields.value[fieldId]||'';if(current&&current!==value&&!window.confirm('用此人工备选替换该字段的可编辑输入？其余输入保留，尚未保存研究条件。'))return;if(fieldId==='research_subject')subject.value=value;else if(fieldId==='research_goal')goal.value=value;else extraSpecDraft.value={...extraSpecDraft.value,[fieldId]:value};specOpen.value=true}
const additionalSpecFields=computed(()=>project.value?.template_copy?.definition.fields.filter(x=>!['research_subject','research_goal'].includes(x.field_id))??[])
const specDraftFields=computed(()=>{const p=project.value;if(!p)return {};const ids=new Set(p.template_copy?.definition.fields.map(x=>x.field_id)??['research_subject','research_goal']);const fields={...p.spec,...extraSpecDraft.value};if(ids.has('research_subject'))fields.research_subject=subject.value;if(ids.has('research_goal'))fields.research_goal=goal.value;return fields})
const requiredSpecComplete=computed(()=>!!project.value&&(project.value.template_copy?.definition.required_fields??['research_subject','research_goal']).every(id=>!!specDraftFields.value[id]?.trim()))
function conditionLabel(id:string){return project.value?.template_copy?.definition.fields.find(x=>x.field_id===id)?.label??id}
const currentCheckReceipt=ref('')
const discardedDrafts=ref<Project[]>([]),discardedOpen=ref(false)
async function showDiscarded(){discardedOpen.value=!discardedOpen.value;if(discardedOpen.value){try{discardedDrafts.value=(await listDiscardedProjects()).data}catch(e){failure(e)}}}
async function discardCurrentDraft(){const p=project.value;if(!p||p.status!=='draft'||busy.value)return;if(!window.confirm('丢弃当前草稿？资料不会删除，可从已丢弃草稿中恢复。未保存的输入不会保存。'))return;busy.value=true;try{const body={expected_project_version:p.project_version},name=`discard:${p.id}`;await discardDraftProject(p.id,p.project_version,operationKey(name,body));attempts.delete(name);project.value=null;extraSpecDraft.value={};await loadProjects()}catch(e){failure(e)}finally{busy.value=false}}
async function restoreDiscarded(p:Project){if(busy.value)return;busy.value=true;try{const body={expected_project_version:p.project_version},name=`restore:${p.id}`;await restoreDraftProject(p.id,p.project_version,operationKey(name,body));attempts.delete(name);discardedDrafts.value=discardedDrafts.value.filter(x=>x.id!==p.id);await loadProjects();await selectProject(p.id)}catch(e){failure(e)}finally{busy.value=false}}

function handleComputedReceipt(id:string){currentCheckReceipt.value=id}
function handleReviewChangeReceipt(id:string){currentCheckReceipt.value=id;deliveryOpen.value=false;openFullPanel('changes')}

const subject = ref('')
const goal = ref('')
const changeReason = ref('')
const selectedImpactChapterIds = ref<string[]>([])
const acknowledgedConditionsInputs = ref(conditionsChangeSnapshot('', '', []))
type ConditionsChangeDraft = { subject: string; goal: string; reason: string; chapterIds: string[]; fieldsFingerprint?:string }
const pendingChangeSet = ref<ChangeSet | null>(null)
const pendingDraft = ref<ConditionsChangeDraft | null>(null)
const lastChangeSet = ref<ChangeSet | null>(null)
const bodyDraft = ref('')
const busy = ref(false)
const loading = ref(false)
const errorMessage = ref('')
const reviewDrafts = ref<Record<string, ReviewDraft>>({})
const savedReviewDrafts = ref<Record<string, ReviewDraft>>({})
const citationUsageDrafts = ref<Record<string, CitationUsage>>({})
const generationInstruction = ref('根据已允许的项目资料起草本章，引用来源并列出所有待核事项。')
const generationRun = ref<GenerationRun | null>(null)
const generationCandidate = ref<Candidate | null>(null)
const generationCandidates = ref<GenerationCandidateSummary[]>([])
const generationBusy = ref(false)
const accessStatus = ref<AccessStatus | null>(null)
const accessBusy = ref(false)
const adoptionOpen = ref(false)

/* ---- 方案一工作台外壳状态（gong 2026-10-12 选型接线；只改排布与入口，不动业务语义） ---- */
const mode = ref<'research' | 'focus'>('research')
// R2 退修：右栏停靠改按「工作台容器真实可用宽度」决定，而不是视口宽度。
// 停靠还需计入正文内边距：232目录 + 344辅助 + 78内容边距 + 640实际输入区，向上取1296。
const INSPECTOR_DOCK_MIN_WIDTH = 1296
// 低于阈值时辅助以浮层出现（不挤正文），"显示辅助"仍可随时打开。
const wbRoot = ref<HTMLElement | null>(null)
const wbWidth = ref(0)
let wbResizeObserver: ResizeObserver | undefined
/* 布局偏好（kimi-ui-20261013）：目录/辅助开收按用户记忆；窄屏自动收起不写偏好，回宽按偏好恢复。 */
function loadWbLayoutPref(): WbLayoutPref {
  try { return readWbLayoutPref(localStorage.getItem(wbLayoutStorageKey(localStorage.getItem('weknora_user')))) } catch { return {} }
}
const wbLayoutPref = ref<WbLayoutPref>(loadWbLayoutPref())
function saveWbLayoutPref(patch: WbLayoutPref) {
  wbLayoutPref.value = mergeWbLayoutPref(wbLayoutPref.value, patch)
  try { localStorage.setItem(wbLayoutStorageKey(localStorage.getItem('weknora_user')), JSON.stringify(wbLayoutPref.value)) } catch { /* 写入失败仅退化为不记忆 */ }
}
// 宽屏默认展开目录；≤899px 默认收起，经顶栏"目录"显式打开；跨入窄屏自动收起不遮正文
const WB_NARROW_MQ = '(max-width: 899px)'
const initialNarrow = typeof window !== 'undefined' && window.matchMedia(WB_NARROW_MQ).matches
const railOpen = ref(initialNarrow ? false : (wbLayoutPref.value.rail ?? false))
let wbWasNarrow = initialNarrow
// mq change 在真实窗口缩放下可靠；CDP 模拟/嵌入宿主下可能不触发，由 ResizeObserver 兜底对账
function syncNarrowRail() {
  const narrow = window.matchMedia(WB_NARROW_MQ).matches
  if (narrow === wbWasNarrow) return
  inspOpen.value = inspectorOnNarrowTransition(inspOpen.value, wbWasNarrow, narrow, wbLayoutPref.value, wbWidth.value >= INSPECTOR_DOCK_MIN_WIDTH)
  wbWasNarrow = narrow
  if (narrow) railOpen.value = false
  else railOpen.value = wbLayoutPref.value.rail ?? false // 回宽恢复偏好，不强制展开
}
let wbNarrowMediaQuery: MediaQueryList | undefined
if (typeof window !== 'undefined') {
  wbNarrowMediaQuery = window.matchMedia(WB_NARROW_MQ)
  wbNarrowMediaQuery.addEventListener?.('change', syncNarrowRail)
}
const inspOpen = ref(false)
const inspTab = ref<'sources' | 'ai' | 'checks' | 'history'>('sources')
const specOpen = ref(false)
const templateOpen = ref(false)
const deliveryOpen = ref(false)
const rewriteCompareOpen = ref(false)
const rewriteOpSeq = ref(0)
const anyOverlay = computed(() => !!fullPanel.value || activationOpen.value || specOpen.value || templateOpen.value || deliveryOpen.value || rewriteCompareOpen.value)
const mainClasses = computed(() => ({
  'wb--focus': mode.value === 'focus',
  'wb--rail': railOpen.value,
  'wb--insp': inspOpen.value,
  'wb--dock': inspOpen.value && wbWidth.value >= INSPECTOR_DOCK_MIN_WIDTH,
  'wb--spec': specOpen.value,
  'wb--template': templateOpen.value,
  'wb--delivery': deliveryOpen.value,
  [`wb--tab-${inspTab.value}`]: true,
}))
// 待核徽标 = 本页草稿里仍未完成（未显式选择或理由为空）的条数；与进度/确认守卫同一判定。
const pendingReviewCount = computed(() => (chapter.value
  ? displayedReviewItems.value.filter(item => reviewItemStatus(reviewDrafts.value[`${chapter.value!.id}:${item.id}`]) === 'pending').length
  : 0))

/* ---- 下一轮交互优化（workbench-ux-refinement-20261012-01）：项目切换 + 待核审阅卡片 ---- */
/* ---- 连续文稿编辑器接线（document-editor-20261013）：真实按章 CAS，移除旧 autosave 竞争写 ---- */
interface EditorDocSnapshot { copy: WorkingCopy; markdown: string; revision: number; committed: number; source_ids: string[]; citation_usages: CitationUsage[]; base_chapter_version_id: string | null }
const editorRef = ref<InstanceType<typeof LingDocEditor> | null>(null)
const editorDocs = ref<Record<string, EditorDocSnapshot>>({})
const orderedChapters = computed(()=>sortChaptersByTemplate(chapters.value,project.value?.template_copy?.definition.sections ?? []))
const editorChapters = computed(() => orderedChapters.value.map(c => ({ id: c.id, title: c.title, body_markdown: editorDocs.value[c.id]?.markdown ?? c.body_markdown })))
const editorReviewAnchors = computed(()=>Object.fromEntries(chapters.value.map(c=>[c.id,editorDocs.value[c.id]?.copy.review_items ?? c.review_items])))
async function openNativeReview(chapterId:string,itemId:string) {
  const doc=editorDocs.value[chapterId]
  const item=doc?.copy.review_items.find(i=>i.id===itemId)
  const body=editorRef.value?.currentMarkdown(chapterId) ?? doc?.markdown ?? ''
  const range=currentAnchorRange(body,item?.anchor)
  const located=range && editorRef.value?.locateMarkdownRange(chapterId,range.from,range.to,range.quote)
  if(!located) editorRef.value?.focusChapter(chapterId)
  onEditorActiveChapter(chapterId)
  expandedReviewId.value=itemId;inspTab.value='checks';inspOpen.value=true
  await nextTick()
  const card=[...document.querySelectorAll<HTMLElement>('.rv-card')].find(e=>e.dataset.reviewId===itemId)
  card?.scrollIntoView({block:'nearest',behavior:'smooth'})
}
const editorBaselines = computed(() => Object.fromEntries(Object.entries(editorDocs.value).map(([k, v]) => [k, v.revision])))
const editorCommitted = computed(() => Object.fromEntries(Object.entries(editorDocs.value).map(([k, v]) => [k, v.committed])))
const editorDirtyAny = ref(false)
const pendingCitationUsages = ref<Record<string,CitationUsage[]>>({})
const editorReady = ref(false)
const editorLoadError = ref('')
let editorLoadSequence = 0
let editorSessionEpoch = 0
let chapterReadSequence = 0
const selectionContextVersion = ref(0)
function snapshotOf(copy: WorkingCopy): EditorDocSnapshot {
  const formal = chapters.value.find(c => c.id === copy.chapter_id)
  return {
    copy, markdown: copy.body_markdown, revision: copy.working_copy_revision,
    committed: formal ? committedRevision(copy, formal) : 0,
    source_ids: [...copy.source_ids], citation_usages: (copy.citation_usages ?? []).map(u => ({...u})),
    base_chapter_version_id: copy.base_chapter_version_id,
  }
}
async function synchronizeCleanDocument() {
  await nextTick()
  if (!editorRef.value?.hasUnsaved) editorRef.value?.synchronizeServer(editorChapters.value,editorBaselines.value,editorCommitted.value)
}
const sourceLabels = computed(() => {
  const ids = [...new Set(Object.values(editorDocs.value).flatMap(d => d.source_ids))].sort()
  return Object.fromEntries(ids.map((id,i) => {
    const label = sources.value.find(src => src.id === id)?.locator
    return [id, `来源 ${i+1}${label ? ' · '+label.slice(0,18) : ''}`]
  }))
})
function onSelectionContext() { selectionContextVersion.value++ }
// 当前选区（UTF-16）。它是一个**取数函数**，不是一个渲染期算好的值：
// 选区的映射要经编辑器的 UTF-16 映射与三重验证，渲染页面上顺手算一次太贵
// （每次击键都要重新序列化整篇正文）。页面只在用户点「发起语言评审」的那一刻
// 问一次。
function resolveRewriteSelection() {
  const editor = editorRef.value
  if (!editor || typeof editor.selectionToUtf16 !== 'function') return undefined
  const mapped = editor.selectionToUtf16()
  if (!mapped || !mapped.ok || mapped.chapterId !== chapter.value?.id) return undefined
  return { start_utf16: mapped.start_utf16, end_utf16: mapped.end_utf16, selected_text: mapped.quote }
}
async function openNativeSource(id: string) {
  inspOpen.value = true; inspTab.value = 'sources'
  const pid = project.value?.id
  if (!pid) return
  try {
    let source = sources.value.find(src => src.id === id)
    if (!source) { const r = await getSource(pid,id); if (project.value?.id !== pid) return; source=r.data; sources.value.push(source) }
    await toggleSourceContext(id)
  } catch(e) { failure(e) }
}


async function loadAllWorkingCopies() {
  const proj=project.value
  if (!proj) return
  if (editorRef.value?.hasUnsaved) { editorLoadError.value='当前输入仍保留，请先保存或明确重新读取后再载入服务器文稿。'; return }
  const sequence=++editorLoadSequence, projectId=proj.id
  editorLoadError.value=''
  const next: Record<string,EditorDocSnapshot>={}
  const requested=[...chapters.value]
  try {
    for (const ch of requested) {
      const r=await getWorkingCopy(projectId,ch.id)
      if(project.value?.id!==projectId || sequence!==editorLoadSequence) return
      if(r.data.working_copy_revision<1) throw new Error('服务端未提供有效工作副本基线')
      next[ch.id]=snapshotOf(r.data)
    }
    if(editorRef.value?.hasUnsaved) { editorLoadError.value='读取期间有新输入，原稿已保留，请保存后再重读。'; return }
    editorDocs.value=next;editorReady.value=true
    if(chapter.value) { workingCopy.value=next[chapter.value.id]?.copy ?? null;bodyDraft.value=next[chapter.value.id]?.markdown ?? '' }
    await synchronizeCleanDocument()
  } catch(e) { if(sequence===editorLoadSequence) editorLoadError.value='工作副本读取失败，尚未载入猜测的版本；请重试。' }
}

async function saveChapterViaApi(chapterId: string, markdown: string, expectedWorkingCopyRevision: number) {
  const proj = project.value
  const doc = editorDocs.value[chapterId]
  const session=editorSessionEpoch
  if (!proj) return { ok: false as const, error: 'no-project' }
  const citations = chapterCitations(markdown)
  if (citations.kind === 'malformed') return { ok: false as const, error: 'malformed-citation' }
  const input = {
    base_chapter_version_id: doc?.base_chapter_version_id ?? null,
    expected_spec_revision: proj.spec_revision,
    expected_working_copy_revision: expectedWorkingCopyRevision,
    body_markdown: markdown,
    source_ids: citations.sourceIds,
    citation_usages: citations.sourceIds.map(sid => ({ ...(pendingCitationUsages.value[chapterId]?.find(u=>u.source_id===sid) ?? (chapter.value?.id === chapterId ? citationUsageDraft(sid) : doc?.citation_usages.find(u => u.source_id === sid) ?? { source_id: sid, purpose: '', limitation: '' })) })),
  }
  try {
    const result = await saveWorkingCopy(proj.id, chapterId, input, operationKey(String('ed-wc:' + chapterId), input))
    if (project.value?.id !== proj.id || session !== editorSessionEpoch) return { ok: false as const, error: 'project-changed' }
    editorDocs.value[chapterId] = snapshotOf(result.data)
    if(JSON.stringify(pendingCitationUsages.value[chapterId])===JSON.stringify(input.citation_usages)) delete pendingCitationUsages.value[chapterId]
    if (chapter.value?.id === chapterId) {
      const currentUsages=JSON.stringify(citations.sourceIds.map(sid=>citationUsageDraft(sid)))
      workingCopy.value=result.data;bodyDraft.value=markdown
      if(currentUsages===JSON.stringify(input.citation_usages)) hydrateCitationUsages(result.data)
    }
    return { ok: true as const, newWorkingCopyRevision: result.data.working_copy_revision }
  } catch (e) {
    const err = e as { status?: number }
    if(project.value?.id===proj.id && session===editorSessionEpoch) failure(e)
    return { ok: false as const, conflict: err?.status === 409, error: 'save-failed' }
  }
}
function onEditorChapterDirty() {
  editorDirtyAny.value = true
  workingCopyStatus.value = 'unsaved'
}
function onEditorSavedAll(ok: boolean) {
  if (ok) editorDirtyAny.value = false
  workingCopyStatus.value = ok ? 'saved' : 'error'
  if (chapter.value) bodyDraft.value = editorDocs.value[chapter.value.id]?.markdown ?? bodyDraft.value
}
function currentBodyMarkdown(): string {
  const active = chapter.value?.id
  if (!active) return bodyDraft.value
  const fromEditor = editorRef.value?.currentMarkdown(active)
  // 空串是合法正文（清空），不能当 falsy 回退旧草稿
  return fromEditor !== null && fromEditor !== undefined ? fromEditor : bodyDraft.value
}
function onEditorActiveChapter(chapterId: string) {
  editorActiveChapterId.value=chapterId
  if(chapter.value?.id===chapterId) return
  const next=chapters.value.find(c=>c.id===chapterId)
  if(!next || !project.value) return
  chapter.value=next;currentConfirmation.value=null
  workingCopy.value=editorDocs.value[chapterId]?.copy ?? null
  bodyDraft.value=editorRef.value?.currentMarkdown(chapterId) ?? workingCopy.value?.body_markdown ?? next.body_markdown
  if(workingCopy.value) hydrateCitationUsages(pendingCitationUsages.value[chapterId] ? {...workingCopy.value,citation_usages:pendingCitationUsages.value[chapterId]} : workingCopy.value)
  rewriteCandidate.value=null;rewriteSourceIds.value=[];relinkItemId.value='';generationCandidate.value=null;generationCandidates.value=[]
  void loadChapterDraft(project.value.id,chapterId)
  void loadGenerationCandidates(chapterId)
  void loadSavedRewriteCandidate(project.value.id,chapterId)
}
const editorActiveChapterId = ref('')

// 实读当前有效确认的完整处置记录（GET confirmations/current；只读展示，不假填）
const currentConfirmation = ref<Confirmation | null>(null)
let confirmationReadSequence=0
async function loadCurrentConfirmation() {
  const proj = project.value
  const ch = chapter.value
  const sequence=++confirmationReadSequence
  currentConfirmation.value=null
  if (!proj || !ch || !ch.confirmation_valid) { currentConfirmation.value = null; return }
  try {
    const r = await getCurrentConfirmation(proj.id, ch.id)
    if (project.value?.id === proj.id && chapter.value?.id === ch.id && chapter.value.current_version_id===ch.current_version_id && sequence===confirmationReadSequence) {
      currentConfirmation.value = r.data ?? null
    }
  } catch { if(sequence===confirmationReadSequence) currentConfirmation.value=null }
}
watch(() => [project.value?.id,chapter.value?.id,chapter.value?.current_version_id,chapter.value?.confirmation_valid], () => { void loadCurrentConfirmation() }, { flush: 'post' })

const projectPickerOpen = ref(false)
const projectQuery = ref('')
const filteredProjects = computed(() => filterProjects(projects.value, projectQuery.value))
// R3 退修：记录触发按钮，弹层关闭后把焦点还回去；打开时焦点进入搜索框。
const projectPickerTrigger = ref<HTMLElement | null>(null)
function toggleProjectPicker(event: MouseEvent) {
  if (projectPickerOpen.value) { closeProjectPicker(); return }
  projectPickerTrigger.value = (event.currentTarget as HTMLElement | null) ?? projectPickerTrigger.value
  projectPickerOpen.value = true
  void nextTick(() => document.getElementById('project-search')?.focus())
}
function closeProjectPicker() {
  if (!projectPickerOpen.value) return
  projectPickerOpen.value = false
  projectPickerTrigger.value?.focus()
}
// 目录/辅助显式开关写入偏好；自动收起（窄屏/定位）不写（kimi-ui-20261013）
function toggleRail() { railOpen.value = !railOpen.value; saveWbLayoutPref({ rail: railOpen.value }) }
function closeRail() { railOpen.value = false; saveWbLayoutPref({ rail: false }) }
const inspectorToggle = ref<HTMLButtonElement | null>(null)
function closeInsp() {
  inspOpen.value = false
  saveWbLayoutPref({ insp: false })
  void nextTick(() => inspectorToggle.value?.focus())
}
function toggleInsp() {
  if (inspOpen.value) closeInsp()
  else { inspOpen.value = true; saveWbLayoutPref({ insp: true }) }
}
// “开发与验收”次级菜单：诊断入口不占顶栏主操作区；Esc/点外部关闭并还原焦点
const devMenuOpen = ref(false)
const devMenuRef = ref<HTMLElement | null>(null)
function toggleDevMenu(event: MouseEvent) {
  devMenuOpen.value = !devMenuOpen.value
  if (devMenuOpen.value) return
  (event.currentTarget as HTMLElement | null)?.focus()
}
function closeDevMenu(restoreFocus = true) {
  if (!devMenuOpen.value) return
  devMenuOpen.value = false
  if (restoreFocus) devMenuRef.value?.querySelector<HTMLElement>('.wb-devmenu-toggle')?.focus()
}
function onWorkbenchClick(e: MouseEvent) {
  if (!devMenuOpen.value) return
  if (devMenuRef.value && e.target instanceof Node && !devMenuRef.value.contains(e.target)) closeDevMenu(false)
}
function pickProject(id: string) {
  closeProjectPicker()
  void selectProject(id)
}

// R1 退修：只有显式点选才置 explicitlyChosen；创建草稿默认 false，默认值不再算"已处理"。
function chooseReviewDisposition(itemId: string, disposition: ReviewDisposition) {
  if (busy.value) return
  const draft = reviewDraft(itemId)
  draft.disposition = disposition
  draft.explicitlyChosen = true
}

// 待核审阅：一次展开一项；换章时回到第一项；状态与进度全部由既有 reviewDrafts 推导（键 = 章 id + 项 id）。
const expandedReviewId = ref('')
watch(() => chapter.value?.id, () => {
  expandedReviewId.value = displayedReviewItems.value[0]?.id ?? ''
}, { flush: 'post' })
function reviewDraftOf(itemId: string) {
  return reviewDrafts.value[`${chapter.value?.id ?? ''}:${itemId}`]
}
function statusOf(item: { id: string }) {
  return reviewItemStatus(reviewDraftOf(item.id))
}
function statusLabelOf(item: { id: string }) {
  return reviewStatusLabel(statusOf(item))
}
function anchorStateOf(item: Chapter['review_items'][number]) {
  return reviewAnchorState(item.anchor, currentBodyMarkdown())
}
function anchorHintOf(item: Chapter['review_items'][number]) {
  return reviewAnchorHint(item.anchor, currentBodyMarkdown())
}
const reviewProgressState = computed(() => reviewProgress(
  displayedReviewItems.value,
  item => `${chapter.value?.id ?? ''}:${item.id}`,
  reviewDrafts.value,
))
const currentReviewIndex = computed(() => {
  const idx = displayedReviewItems.value.findIndex(item => item.id === expandedReviewId.value)
  return idx < 0 ? 0 : idx
})
function closeOverlays() {
  specOpen.value = false
  templateOpen.value = false
  deliveryOpen.value = false
  if (rewriteCompareOpen.value) closeRewriteCompare()
}
function openRewriteCompare() {
  if (!rewriteCandidate.value || rewriteCandidateStage.value !== 'ready') return
  rewriteCompareOpen.value = true
}
function closeRewriteCompare() {
  rewriteCompareOpen.value = false
  rewriteOpSeq.value++
}
async function applyFromCompare() {
  const seq = rewriteOpSeq.value
  await applyRewriteCandidate()
  if (seq === rewriteOpSeq.value && !rewriteCandidate.value) rewriteCompareOpen.value = false
}
function rejectFromCompare() {
  rejectRewriteCandidate()
  closeRewriteCompare()
}
function onWorkbenchKeydown(e: KeyboardEvent) {
  if (e.key !== 'Escape') return
  if (devMenuOpen.value) {
    closeDevMenu()
    e.stopPropagation()
    return
  }
  // R3 退修：项目弹层也纳入 Esc 关闭（非模态语义，不做焦点囚禁）
  if (projectPickerOpen.value) {
    closeProjectPicker()
    e.stopPropagation()
    return
  }
  if (!anyOverlay.value) { if(inspOpen.value){closeInsp()}else if(railOpen.value){railOpen.value=false;saveWbLayoutPref({rail:false})}return }
  if (rewriteCompareOpen.value) closeRewriteCompare()
  else closeOverlays()
  e.stopPropagation()
}
onMounted(() => {
  document.addEventListener('keydown', onWorkbenchKeydown)
  document.addEventListener('click', onWorkbenchClick)
  // R2 退修：用 ResizeObserver 实测工作台容器宽度（含宿主导航占位变化），决定右栏停靠/浮层。
  // 模板 ref 优先；合成桩/异常场景退化为 DOM 查询，保证宽度始终可测。
  const el = (wbRoot.value ?? document.querySelector('main.lingdoc-workspace')) as HTMLElement | null
  if (el) {
    wbWidth.value = el.clientWidth || window.innerWidth
    inspOpen.value = window.matchMedia(WB_NARROW_MQ).matches ? false : (wbLayoutPref.value.insp ?? (wbWidth.value >= INSPECTOR_DOCK_MIN_WIDTH))
    const syncHeaderPosition = () => {
      const header = el.querySelector('.wb-top') as HTMLElement | null
      if (header) el.style.setProperty('--wb-head-bottom', `${Math.ceil(header.getBoundingClientRect().bottom)}px`)
    }
    syncHeaderPosition()
    wbResizeObserver = new ResizeObserver(() => {
      // 同一观察器监听工作台与换行后的顶栏；宽度始终从工作台读取。
      const width = el.clientWidth
      if (width > 0) {
        // 容器宽度只决定停靠/浮层；自动开收由真实窄屏边界处理。
        // 原生导航 60→260px 的动画不能撤销用户已恢复的打开偏好。
        wbWidth.value = width
        syncNarrowRail()
      }
      syncHeaderPosition()
    })
    wbResizeObserver.observe(el)
    const header = el.querySelector('.wb-top')
    if (header) wbResizeObserver.observe(header)
  }
})
onUnmounted(() => {
  document.removeEventListener('keydown', onWorkbenchKeydown)
  document.removeEventListener('click', onWorkbenchClick)
  wbNarrowMediaQuery?.removeEventListener?.('change', syncNarrowRail)
  wbResizeObserver?.disconnect()
})
const restricted = computed(() => isRestricted(accessStatus.value))
const recoveryActions = computed(() => recoveryActionsOf(accessStatus.value))
const readyAssets = computed(() => assets.value.filter(item => item.processing_state === 'ready'))
const canManageProjectMembers = computed(() => {
  const current = project.value?.members.find(member => member.user_id === auth.currentUserId)
  const governanceRole = current?.governance_role ?? (current?.role === 'owner' ? 'owner' : 'member')
  return current?.status !== 'suspended' && (governanceRole === 'owner' || governanceRole === 'admin')
})
const generationPending = computed(() => generationRun.value?.status === 'queued' || generationRun.value?.status === 'running')

// 「上一次检索确实什么都没找到」——而且提问没被改过。改了提问而没重新检索，
// 屏幕上那句话说的已经不是现在这个问题了。
const searchedNothing = computed(() => !!searchedQuery.value && searchedQuery.value === sourceQuery.value.trim()
  && sources.value.length === 0)

// 面板真的开着 = 记住了是哪一条 **且** 拿到了内容。只看 id 的话，一次失败的展开会让
// 再点一次变成「收起」：什么都不会发生，而用户以为按钮坏了。
const contextOpenFor = computed(() => (sourceContext.value ? contextSourceId.value : ''))
const contextHeading = computed(() => (sourceContext.value?.context_available
  ? windowLabel(sourceContext.value) : CONTEXT_UNAVAILABLE_NOTICE))
const contextReason = computed(() => contextNotice(sourceContext.value))
// 这一层只是把判定搬到渲染旁边：窗口里最多三段，且同时只有一条来源的面板开着。
const contextRows = computed(() => contextLines(sourceContext.value))

let generationTimer: ReturnType<typeof setTimeout> | undefined
let workingCopyTimer: ReturnType<typeof setTimeout> | undefined

// Keep one key for a retry of the exact same operation and body.
const attempts = new Map<string, { body: string; key: string }>()
function operationKey(name: string, body: unknown): string {
  const serialized = JSON.stringify(body)
  const old = attempts.get(name)
  if (old?.body === serialized) return old.key
  const key = crypto.randomUUID()
  attempts.set(name, { body: serialized, key })
  return key
}

const specChanged = computed(() => !!project.value && Object.keys(specDraftFields.value).some(id=>(specDraftFields.value[id]??'')!==(project.value!.spec[id]??'')))
const templateCopyFingerprint = computed(() => JSON.stringify(templateCopyDraft.value))
const templateCopyChanged = computed(() => {
  const definition = project.value?.template_copy?.definition
  if (!definition || !templateCopyDraft.value) return false
  return JSON.stringify(templateCopyDraft.value) !== JSON.stringify(editableTemplateCopy(definition))
})
const templateCopyPreviewCurrent = computed(() => !!templateCopyPreview.value &&
  templateCopyPreviewFingerprint.value === templateCopyFingerprint.value)
const pendingTemplateUpgradeMatches = computed(() => !!pendingChangeSet.value?.template_upgrade && !!pendingTemplateUpgrade.value &&
  pendingTemplateUpgrade.value.fingerprint === templateCopyFingerprint.value &&
  pendingTemplateUpgrade.value.reason === templateUpgradeReason.value.trim())
const bodyChanged = computed(() => !!workingCopy.value && bodyDraft.value !== workingCopy.value.body_markdown)
const pendingDraftMatches = computed(() => {
  if (!pendingDraft.value) return false
  return pendingDraft.value.subject === subject.value && pendingDraft.value.goal === goal.value &&
    (!pendingDraft.value.fieldsFingerprint||pendingDraft.value.fieldsFingerprint===JSON.stringify(specDraftFields.value)) &&
    pendingDraft.value.reason === changeReason.value.trim() &&
    JSON.stringify(pendingDraft.value.chapterIds) === JSON.stringify([...selectedImpactChapterIds.value].sort())
})
const conditionsInputsChanged = computed(() => !!project.value && conditionsChangeInputsAreDirty(
  conditionsChangeSnapshot(project.value.id, changeReason.value, selectedImpactChapterIds.value),
  acknowledgedConditionsInputs.value,
))
function resetConditionsInputs(projectId: string) {
  changeReason.value = ''
  selectedImpactChapterIds.value = []
  acknowledgedConditionsInputs.value = conditionsChangeSnapshot(projectId, '', [])
}
function acknowledgeHandledConditions(projectId: string, draft: ConditionsChangeDraft | null) {
  if (!draft) return
  const handled = conditionsChangeSnapshot(projectId, draft.reason, draft.chapterIds)
  const current = conditionsChangeSnapshot(projectId, changeReason.value, selectedImpactChapterIds.value)
  if (!conditionsChangeInputsAreDirty(current, handled)) resetConditionsInputs(projectId)
  else acknowledgedConditionsInputs.value = conditionsChangeSnapshot(projectId, '', [])
}
const uncheckedChapterTitles = computed(() => chapters.value.filter(item => !selectedImpactChapterIds.value.includes(item.id)).map(item => item.title))
const citationUsagesChanged = computed(() => {
  const parsed = chapterCitations(currentBodyMarkdown())
  if (parsed.kind === 'malformed') return false
  const next = parsed.sourceIds.map(sourceId => citationUsageDrafts.value[sourceId] ?? { source_id: sourceId, purpose: '', limitation: '' })
  return JSON.stringify(next) !== JSON.stringify(workingCopy.value?.citation_usages ?? [])
})
const chapterChanged = computed(() => bodyChanged.value || citationUsagesChanged.value || editorDirtyAny.value || (editorRef.value?.hasUnsaved ?? false))
const unsavedInputState = computed<UnsavedInputState>(() => ({
  chapterChanged: chapterChanged.value, specChanged: specChanged.value,
  templateChanged: templateCopyChanged.value, saving: workingCopySaving.value,
  reviewChanged: reviewDraftsAreDirty(reviewDrafts.value, savedReviewDrafts.value),
  editorUnsaved: editorDirtyAny.value || (editorRef.value?.hasUnsaved ?? false) || draftCandidateDirty.value,
  conditionsChanged: conditionsInputsChanged.value,
}))
const unsavedInputs = computed(() => fullPanelDirty.value || memberDraftDirty.value || unsavedInputsAreDirty(unsavedInputState.value))
function changeSetStatusLabel(status: ChangeSet['status']): string {
  return ({ draft: '保存为草稿', assessed: '生成预览', applied: '应用', rejected: '驳回', stale: '标记为过期', stabilized: '完成稳定化', satisfied: '满足' })[status]
}
function hydrateCitationUsages(item: { citation_usages?: CitationUsage[] } | null) {
  const next: Record<string, CitationUsage> = {}
  for (const usage of item?.citation_usages ?? []) next[usage.source_id] = { ...usage }
  citationUsageDrafts.value = next
}
function editableTemplateCopy(definition: NonNullable<Project['template_copy']>['definition']): TemplateCopyDefinition {
  return JSON.parse(JSON.stringify({
    fields: definition.fields ?? [], sections: definition.sections ?? [], terms: definition.terms ?? [],
    required_fields: definition.required_fields ?? [], rules: definition.rules ?? [],
  })) as TemplateCopyDefinition
}
function updateTermVariants(term: TemplateCopyDefinition['terms'][number], event: Event) {
  const value = (event.target as HTMLInputElement | null)?.value ?? ''
  term.variants = value.split(/[、,，]/).map(item => item.trim()).filter(Boolean)
}
function addTemplateSection() {
  const draft = templateCopyDraft.value
  if (!draft) return
  draft.sections.push({ section_id: `section-${crypto.randomUUID()}`, title: '新章节', order: draft.sections.length + 1, required: false })
}
function removeTemplateSection(id: string) {
  if (templateCopyDraft.value) templateCopyDraft.value.sections = templateCopyDraft.value.sections.filter(item => item.section_id !== id)
}
function addTemplateTerm() {
  templateCopyDraft.value?.terms.push({ term_id: `term-${crypto.randomUUID()}`, preferred: '新术语', variants: [] })
}
function updateRuleVariants(rule: TemplateCopyDefinition['rules'][number], event: Event) {
  rule.parameters.variants = ((event.target as HTMLInputElement)?.value ?? '').split(/[、,，]/).map(item => item.trim()).filter(Boolean)
}
function addTemplateRule(kind: 'presence' | 'pattern' | 'consistency') {
  const parameters: Record<string, unknown> = { target_kind: 'chapter', target_id: 'question' }
  if (kind === 'pattern') parameters.pattern = '.+'
  if (kind === 'consistency') { parameters.preferred = '标准术语'; parameters.variants = ['非标准术语'] }
  templateCopyDraft.value?.rules.push({ rule_id: `rule-${crypto.randomUUID()}`, kind, severity: 'warning', evaluator: '', message: '', parameters })
}
function normalizeChangeSet(change: ChangeSet): ChangeSet {
  if (!change.template_upgrade) return { ...change, fields: change.fields ?? [], impacts: change.impacts ?? [] }
  const upgrade = change.template_upgrade
  const preview = upgrade.preview ?? {} as TemplateMigrationPreview
  const impact = upgrade.impact
  return {
    ...change,
    fields: change.fields ?? [],
    impacts: change.impacts ?? [],
    template_upgrade: {
      ...upgrade,
      preview: {
        ...preview,
        fields: preview.fields ?? [], section_changes: preview.section_changes ?? [],
        missing_required: preview.missing_required ?? [], orphaned: preview.orphaned ?? [], incompatible: preview.incompatible ?? [],
      },
      impact: impact ? {
        ...impact,
        affected_field_ids: impact.affected_field_ids ?? [], affected_section_ids: impact.affected_section_ids ?? [],
        affected_chapter_ids: impact.affected_chapter_ids ?? [], invalidated_confirmation_chapter_ids: impact.invalidated_confirmation_chapter_ids ?? [],
        changed_rule_ids: impact.changed_rule_ids ?? [],
      } : undefined,
    },
  }
}
function templateCopyStatusLabel(status: NonNullable<Project['template_copy']>['status']): string {
  return ({ draft: '草稿', bound: '已绑定', superseded: '已被新版本替代', discarded: '已放弃' })[status]
}
function hydrateTemplateCopyDraft(item: Project | null) {
  templateCopyDraft.value = item?.template_copy ? editableTemplateCopy(item.template_copy.definition) : null
  templateCopyPreview.value = null
  templateCopyPreviewFingerprint.value = ''
  templateCopyNotice.value = ''
}
function templateCopyInput(): TemplateCopyEditInput | null {
  if (!project.value || !project.value.template_copy || !templateCopyDraft.value) return null
  return {
    expected_project_version: project.value.project_version,
    expected_template_copy_version: project.value.template_copy.version,
    ...JSON.parse(JSON.stringify(templateCopyDraft.value)) as TemplateCopyDefinition,
  }
}
async function previewTemplateCopy() {
  if (!project.value || !templateCopyChanged.value || templateCopyBusy.value) return
  const input = templateCopyInput()
  if (!input) return
  templateCopyBusy.value = true
  templateCopyNotice.value = ''
  errorMessage.value = ''
  try {
    const result = await previewTemplateCopyEdit(project.value.id, input)
    templateCopyPreview.value = result.data
    templateCopyPreviewFingerprint.value = templateCopyFingerprint.value
  } catch (error) { failure(error) }
  finally { templateCopyBusy.value = false }
}
function discardTemplateCopyDraft() {
  hydrateTemplateCopyDraft(project.value)
}
async function saveTemplateCopy() {
  if (!project.value || !templateCopyChanged.value || !templateCopyPreviewCurrent.value || templateCopyBusy.value) return
  const input = templateCopyInput()
  if (!input) return
  templateCopyBusy.value = true
  templateCopyNotice.value = ''
  errorMessage.value = ''
  try {
    const result = await saveTemplateCopyEdit(project.value.id, input, operationKey('template-copy', input))
    attempts.delete('template-copy')
    project.value = result.data
    projects.value = projects.value.map(item => item.id === result.data.id ? result.data : item)
    hydrateTemplateCopyDraft(result.data)
    templateCopyNotice.value = `已保存为第 ${result.data.template_copy_version} 版；旧版本仍保留。`
  } catch (error) { failure(error) }
  finally { templateCopyBusy.value = false }
}
async function createTemplateUpgradeChangeSet() {
  if (!project.value || project.value.status !== 'active' || !templateCopyChanged.value || !templateUpgradeReason.value.trim() || templateCopyBusy.value || pendingChangeSet.value?.status === 'assessed') return
  const definition = templateCopyInput()
  if (!definition) return
  templateCopyBusy.value = true
  templateCopyNotice.value = ''
  errorMessage.value = ''
  const input = {
    expected_context_revision: project.value.current_context_revision,
    fields: {},
    affected_chapter_ids: [],
    reason: templateUpgradeReason.value.trim(),
    template_upgrade: { ...definition, field_values: {} },
  }
  try {
    const result = await createChangeSet(project.value.id, input, operationKey(`template-upgrade:${project.value.id}`, input))
    pendingChangeSet.value = normalizeChangeSet(result.data)
    pendingTemplateUpgrade.value = { fingerprint: templateCopyFingerprint.value, reason: templateUpgradeReason.value.trim() }
    templateCopyPreview.value = result.data.template_upgrade?.preview ?? null
    templateCopyPreviewFingerprint.value = templateCopyFingerprint.value
    templateCopyNotice.value = '已生成升级评估；当前模板未变更，需由 Owner 应用后才会生效。'
  } catch (error) { failure(error) }
  finally { templateCopyBusy.value = false }
}
async function applyTemplateUpgrade() {
  if (!project.value || busy.value || !pendingChangeSet.value?.template_upgrade || !pendingTemplateUpgradeMatches.value) return
  busy.value = true
  errorMessage.value = ''
  const id = project.value.id
  const changeSetId = pendingChangeSet.value.id
  const applyKey = operationKey(`change-set-apply:${id}:${changeSetId}`, { change_set_id: changeSetId })
  try {
    const applied = await applyChangeSet(id, changeSetId, applyKey)
    attempts.delete(`change-set-apply:${id}:${changeSetId}`)
    pendingChangeSet.value = null
    pendingTemplateUpgrade.value = null
    await selectProject(id, { force: true, skipConfirm: true })
    lastChangeSet.value = applied.data
    templateCopyNotice.value = `模板升级已应用为第 ${project.value?.template_copy_version ?? ''} 版；受影响章节需重新确认。`
  } catch (error) { failure(error) }
  finally { busy.value = false }
}
async function rejectTemplateUpgrade() {
  if (!project.value || busy.value || !pendingChangeSet.value?.template_upgrade) return
  busy.value = true
  errorMessage.value = ''
  const id = project.value.id
  const changeSetId = pendingChangeSet.value.id
  const rejectKey = operationKey(`change-set-reject:${id}:${changeSetId}`, { change_set_id: changeSetId })
  try {
    const rejected = await rejectChangeSet(id, changeSetId, rejectKey)
    attempts.delete(`change-set-reject:${id}:${changeSetId}`)
    pendingChangeSet.value = null
    pendingTemplateUpgrade.value = null
    templateUpgradeReason.value = ''
    await selectProject(id, { force: true, skipConfirm: true })
    lastChangeSet.value = rejected.data
  } catch (error) { failure(error) }
  finally { busy.value = false }
}
function citationUsageDraft(sourceId: string): CitationUsage {
  return citationUsageDrafts.value[sourceId] ??= { source_id: sourceId, purpose: '', limitation: '' }
}
function citationStatus(sourceId: string) {
  return chapter.value?.citation_statuses?.find(item => item.source_id === sourceId)
}
const workingCopyDiffersFromFormal = computed(() => !!workingCopy.value && !!chapter.value && !copyMatchesFormal(workingCopy.value,chapter.value))

const displayedReviewItems = computed(() => workingCopy.value?.review_items ?? chapter.value?.review_items ?? [])
const workingCopyStatusLabel = computed(() => ({
  saved: '已保存', unsaved: '有待保存修改', saving: '保存中…', error: '保存失败，输入仍保留',
}[workingCopyStatus.value]))

// 正文里的来源标记：提取与体检一次算完。模板因此不必自己去拆这个联合类型。
const draftCitations = computed(() => {
  const parsed = chapterCitations(currentBodyMarkdown())
  return parsed.kind === 'ok'
    ? { sourceIds: parsed.sourceIds, malformed: false }
    : { sourceIds: [] as string[], malformed: true }
})
const confirmationReasons = computed(() => [...(chapter.value && !chapterConfirmPermissions.value[chapter.value.id] ? ['当前身份尚未获得此章节的确认许可'] : []),...confirmationBlockReasons({
  busy: busy.value, saving: workingCopySaving.value, unsaved: chapterChanged.value,
  needsCommit: workingCopyDiffersFromFormal.value,
  // R1 退修：与进度/卡片同一判定——未显式选择或理由为空都计入未完成
  missingReasons: chapter.value?.review_items.filter(item => reviewItemStatus(reviewDrafts.value[`${chapter.value!.id}:${item.id}`]) === 'pending').length ?? 0,
})])

function reviewDraft(itemId: string) {
  const key = `${chapter.value?.id ?? ''}:${itemId}`
  // R1 退修：草稿默认"未显式选择"。默认 disposition 只是提交契约的占位，不代表用户已处理。
  return reviewDrafts.value[key] ??= { disposition: 'resolved', reason: '', explicitlyChosen: false }
}

function failure(error: unknown) {
  const item = error as { status?: number; message?: string; error?: { code?: string } }
  // 被拒明细比服务端那句概括更具体（逐条说了是哪一份、为什么），所以它**替掉**概括：
  // 两句话说的是同一件事，并排显示只会让人读两遍再去找哪一句带细节。
  deniedRows.value = deniedSourcesOf(error).map(entry => ({ assetId: entry.assetId, ...denyReasonOf(entry.reason) }))
  if (deniedRows.value.length) {
    errorMessage.value = ''
    return
  }
  if (item?.status === 409 && item?.error?.code === 'version_conflict') {
    errorMessage.value = '内容已被修改。当前输入已保留；请先重新读取，再决定如何处理。'
  } else {
    errorMessage.value = item?.message || '操作失败，请重试。'
  }
}

async function loadProjects() {
  loading.value = true
  errorMessage.value = ''
  try {
    const result = await listProjects()
    projects.value = result.data.items
    truncated.value = result.data.truncated
  } catch (error) { failure(error) }
  finally { loading.value = false }
}

async function saveMembers(members: ProjectMemberInput[]) {
  if (!project.value || memberSaving.value || !canManageProjectMembers.value) return
  const current = project.value
  const input = { expected_project_version: current.project_version, members }
  const name = `members:${current.id}`
  memberSaving.value = true
  errorMessage.value = ''
  try {
    const result = await saveProjectMembers(current.id, current.project_version, members, operationKey(name, input))
    attempts.delete(name)
    if (project.value?.id !== current.id) return
    const acknowledged = result.meta?.refresh_required ? (await getProject(current.id)).data : result.data
    if (project.value?.id !== current.id || acknowledged.project_version < project.value.project_version) return
    project.value = acknowledged
    projects.value = projects.value.map(item => item.id === acknowledged.id ? acknowledged : item)
  } catch (error) { failure(error) }
  finally { memberSaving.value = false }
}

async function loadChapterDraft(projectId: string,chapterId: string) {
  const sequence=++chapterReadSequence
  workingCopyLoading.value=true
  try {
    const [r,v]=await Promise.all([getWorkingCopy(projectId,chapterId),listChapterVersions(projectId,chapterId)])
    if(project.value?.id!==projectId || chapter.value?.id!==chapterId || sequence!==chapterReadSequence) return
    workingCopy.value=r.data;chapterVersions.value=v.data.items
    if(!editorRef.value?.hasUnsaved) {
      editorDocs.value[chapterId]=snapshotOf(r.data)
      bodyDraft.value=r.data.body_markdown;hydrateCitationUsages(r.data)
      await synchronizeCleanDocument()
    }
  } catch(e) {if(sequence===chapterReadSequence) failure(e)}
  finally {if(sequence===chapterReadSequence) workingCopyLoading.value=false}
}

function scheduleWorkingCopySave() {
  if(!chapter.value || !editorRef.value || !workingCopy.value) return
  pendingCitationUsages.value[chapter.value.id]=draftCitations.value.sourceIds.map(sid=>({...citationUsageDraft(sid)}))
  editorDirtyAny.value=true;workingCopyStatus.value='unsaved'
  editorRef.value.queue.markDirty(chapter.value.id,currentBodyMarkdown())
}
async function saveAllDocument() {
  if(!editorRef.value) return
  const r=await editorRef.value.flushSave()
  if(!r.ok) errorMessage.value='保存尚未完成，当前输入保留。请处理错误后重试。'
}

async function create() {
  if (busy.value) return
  busy.value = true
  errorMessage.value = ''
  const name = newName.value.trim()
  try {
    const selected=resolveTemplateChoice(newTemplateKey.value,publishedTemplates.value)
    const templateId=selected.template_id,templateVersion=selected.template_version
    const key = operationKey('create', { name, template_id: templateId,template_version:templateVersion })
    const result = await createProject(name, key, templateId, templateVersion)
    attempts.delete('create')
    newName.value = ''
    await loadProjects()
    await selectProject(result.data.id)
    closeProjectPicker()
    specOpen.value=true
  } catch (error) { failure(error) }
  finally { busy.value = false }
}

let projectReadSequence = 0
const projectSwitchCommitting = ref(false)
function navigationInputsSnapshot(): string {
  return JSON.stringify({
    projectId: project.value?.id, subject: subject.value, goal: goal.value,extraSpec:extraSpecDraft.value,draftCandidateInputs:draftCandidatePanelRef.value?.inputsSnapshot??'',
    conditions: conditionsChangeSnapshot(project.value?.id ?? '', changeReason.value, selectedImpactChapterIds.value),
    template: templateCopyDraft.value, templateReason: templateUpgradeReason.value,
    reviews: reviewDrafts.value, citations: citationUsageDrafts.value, pendingCitations: pendingCitationUsages.value,
    chapters: chapters.value.map(item => [item.id, editorRef.value?.currentMarkdown(item.id) ?? editorDocs.value[item.id]?.markdown ?? item.body_markdown]),
  })
}
function navigationInputsAreDirty(handled: ConditionsChangeDraft | null): boolean {
  const specHandled = !!handled && handled.subject === subject.value && handled.goal === goal.value &&
    (!handled.fieldsFingerprint || handled.fieldsFingerprint === JSON.stringify(specDraftFields.value))
  // A handled request cannot acknowledge edits made while that request was in flight.
  const conditionsHandled = specHandled && !conditionsInputsChanged.value
  return memberDraftDirty.value || unsavedInputsAreDirty({
    ...unsavedInputState.value, specChanged: unsavedInputState.value.specChanged && !specHandled,
  }, { conditionsHandled })
}
interface ProjectSelectionOptions {
  force?: boolean
  skipConfirm?: boolean
  handledConditions?: ConditionsChangeDraft | null
}
async function selectProject(id: string, { force = false, skipConfirm = false, handledConditions = null }: ProjectSelectionOptions = {}) {
  if (projectSwitchCommitting.value) return
  const message = force || project.value?.id === id
    ? '重新读取会丢弃当前未保存的输入，确定继续吗？'
    : '当前编辑尚未保存，确定切换课题吗？'
  if(fullPanelBusy.value)return
  if(fullPanelDirty.value && !skipConfirm && !window.confirm('切换课题将丢弃面板中未提交的输入，是否继续？'))return
  if (!skipConfirm && navigationInputsAreDirty(handledConditions) && !window.confirm(message)) return
  const sequence = ++projectReadSequence
  const beforeRead = navigationInputsSnapshot()
  const oldProjectId = project.value?.id
  const preferredChapterId = oldProjectId === id ? chapter.value?.id : undefined
  const handledReceipt = handledConditions ? lastChangeSet.value : null
  let ownsCommit = false
  try {
    // Stage every initial read while the old editor, selection and save queue stay usable.
    const result = await getProject(id)
    const chapterResult = result.data.status === 'active' ? await listChapters(id) : null
    const assetResult = await listAssets(id)
    const changeSetsResult = await listChangeSets(id)
    const nextChapters = chapterResult?.data ?? []
    const ordered = sortChaptersByTemplate(nextChapters, result.data.template_copy?.definition.sections ?? [])
    const nextChapter = ordered.find(item => item.id === preferredChapterId) ?? ordered[0] ?? null
    const copies: Record<string, WorkingCopy> = {}
    for (const item of nextChapters) {
      const copy = (await getWorkingCopy(id, item.id)).data
      if (copy.working_copy_revision < 1) throw new Error('服务端未提供有效工作副本基线')
      copies[item.id] = copy
    }
    const versions = nextChapter ? (await listChapterVersions(id, nextChapter.id)).data.items : []
    if (sequence !== projectReadSequence) return
    if (navigationInputsSnapshot() !== beforeRead && navigationInputsAreDirty(handledConditions) &&
        !window.confirm('读取期间有新的输入，继续会丢弃这些未保存内容。确定继续吗？')) return
    // A save from an older spec/base cannot safely be installed with this target snapshot.
    if (oldProjectId === id && nextChapters.some(item => {
      const old = editorDocs.value[item.id]?.copy, staged = copies[item.id]!
      return old && editorRef.value?.queue.state(item.id) === 'saving' &&
        (old.spec_revision !== staged.spec_revision || old.base_chapter_version_id !== staged.base_chapter_version_id)
    })) throw new Error('读取期间仍有旧版本保存请求，当前输入已保留；请等待保存完成后重新读取。')
    const discardReviewDrafts = reviewDraftsAreDirty(reviewDrafts.value, savedReviewDrafts.value)
    projectSwitchCommitting.value = true
    ownsCommit = true
    busy.value = true
    templateCopyBusy.value = true
    const selection = editorRef.value?.editor?.state.selection
    const previousDocument = editorRef.value?.editor?.getJSON()
    await editorRef.value?.pauseForNavigation()
    // Drain can acknowledge a save newer than the staged GET. Preserve its monotonic CAS baseline.
    if (oldProjectId === id) for (const item of nextChapters) {
      const received = editorDocs.value[item.id]?.copy, staged = copies[item.id]!
      if (received && received.spec_revision === staged.spec_revision &&
          received.base_chapter_version_id === staged.base_chapter_version_id &&
          received.working_copy_revision > staged.working_copy_revision) copies[item.id] = received
    }
    editorReady.value=false;editorDirtyAny.value=false;editorDocs.value={};pendingCitationUsages.value={};editorSessionEpoch++;editorLoadSequence++
    currentConfirmation.value=null;chapterReadSequence++
    if (generationTimer) clearTimeout(generationTimer)
    if (workingCopyTimer) clearTimeout(workingCopyTimer)
    workingCopyTimer = undefined
    generationRun.value = null
    generationCandidate.value = null
    generationCandidates.value = []
    adoptionOpen.value = false
    errorMessage.value = ''
    busy.value = false
    templateCopyBusy.value = false
    lastChangeSet.value = handledReceipt?.project_id === id ? handledReceipt : null
    pendingChangeSet.value = null
    pendingDraft.value = null
    pendingTemplateUpgrade.value = null
    templateUpgradeReason.value = ''
    chapterWritePermissions.value={};chapterConfirmPermissions.value={};permissionEpoch++
    if (discardReviewDrafts) {
      reviewDrafts.value = {}
      savedReviewDrafts.value = {}
    }
    project.value = result.data
    void refreshOwnerAvailability()
    hydrateTemplateCopyDraft(result.data)
    specEditingRevision.value=result.data.spec_revision
    subject.value = result.data.spec.research_subject ?? ''
    goal.value = result.data.spec.research_goal ?? ''
    extraSpecDraft.value=Object.fromEntries(Object.entries(result.data.spec).filter(([id])=>!['research_subject','research_goal'].includes(id)))
    currentCheckReceipt.value=''
    resetConditionsInputs(id)
    chapters.value = nextChapters
    void loadChapterPermissions(id,result.data.project_version,nextChapters,editorSessionEpoch)
    const changeSets = (changeSetsResult.data ?? []).map(normalizeChangeSet)
    const assessedTemplateUpgrade = [...changeSets].reverse().find(item => item.status === 'assessed' && item.template_upgrade)
    if (assessedTemplateUpgrade?.template_upgrade) {
      templateCopyDraft.value = editableTemplateCopy(assessedTemplateUpgrade.template_upgrade.definition)
      templateCopyPreview.value = assessedTemplateUpgrade.template_upgrade.preview
      templateCopyPreviewFingerprint.value = templateCopyFingerprint.value
      templateUpgradeReason.value = assessedTemplateUpgrade.reason
      pendingChangeSet.value = assessedTemplateUpgrade
      pendingTemplateUpgrade.value = { fingerprint: templateCopyFingerprint.value, reason: assessedTemplateUpgrade.reason }
    }
    assets.value = assetResult.data ?? []
    selectedAssetIds.value = readyAssets.value.map(item => item.id)
    sources.value = []
    searchedQuery.value = ''
    deniedRows.value = []
    bindingNoticeText.value = ''
    closeSourceContext()
    chapter.value = nextChapter
    editorDocs.value = Object.fromEntries(Object.entries(copies).map(([key, copy]) => [key, snapshotOf(copy)]))
    workingCopy.value = nextChapter ? copies[nextChapter.id] ?? null : null
    chapterVersions.value = versions
    rewriteCandidate.value = null
    rewriteSourceIds.value = []
    bodyDraft.value = workingCopy.value?.body_markdown ?? nextChapter?.body_markdown ?? ''
    hydrateCitationUsages(workingCopy.value)
    // Let Vue destroy the paused old editor before mounting the staged document.
    await nextTick()
    editorReady.value = true
    editorLoadError.value = ''
    await nextTick()
    if (oldProjectId === id && selection && previousDocument &&
        JSON.stringify(previousDocument) === JSON.stringify(editorRef.value?.editor?.getJSON())) {
      editorRef.value?.editor?.commands.setTextSelection({ from: selection.from, to: selection.to })
    }
    projectSwitchCommitting.value = false
    ownsCommit = false
    sessionStorage.setItem(lastProjectStorageKey(),id)
    if (chapter.value) await loadSavedRewriteCandidate(id, chapter.value.id)
    if (sequence !== projectReadSequence) return
    await loadGenerationCandidates(chapter.value?.id)
    if (sequence !== projectReadSequence) return
    await resumeGeneration()
  } catch (error) { if (sequence === projectReadSequence) failure(error) }
  finally { if (ownsCommit) { projectSwitchCommitting.value = false; busy.value = false; templateCopyBusy.value = false } }
}

// 交付面板的写入都要交 expected_project_version，过期时它请这里重读一次。
//
// 只读项目、不动别的：走 selectProject 会重选章节、清掉未保存的正文草稿，而用户要的只是
// 把版本号更新到当前。协作编辑是这条路的现实来源——别人改了项目，版本就旧了。
async function refreshProjectVersion(done?: (ok: boolean) => void) {
  const session=editorSessionEpoch
  const projectId = project.value?.id
  if (!projectId) { done?.(false); return }
  let refreshed = false
  try {
    const result = await getProject(projectId)
    if (project.value?.id !== projectId || editorSessionEpoch!==session || result.data.project_version<project.value.project_version) return
    project.value = result.data
    refreshed = true
  } catch (error) { if (!done) failure(error) }
  finally { done?.(refreshed) }
}

// 打开项目时问一次访问状态，决定要不要给出撤权恢复入口（§8）。
//
// 只跟项目 ID 走，不跟 project_version：一条资料被撤权是资料层的事，本地保存一次正文
// 不会改这个答案。反过来，每存一次正文就重问一遍，会让提示条随着无关操作闪来闪去。
watch(() => project.value?.id, (projectId) => {
  // 换项目就把上一条结论丢掉：它是**别的项目**的答案。这里清掉之后如果读取失败，
  // 界面不显示提示——不知道就别说，与三态里 unknown 不显示是同一条规矩。
  accessStatus.value = null
  if (projectId) void loadAccessStatus(projectId)
}, { immediate: true })

async function loadAccessStatus(projectId: string) {
  try {
    const result = await getAccessStatus(projectId)
    // 慢响应回来时可能已经换了项目：别人的状态不能贴到当前这个项目上。
    if (project.value?.id === projectId) accessStatus.value = result.data
  } catch {
    // 「重新检查」读失败时**不**清掉上一次的结论：网络失败不是关于资料授权的证据，
    // 而悄悄撤掉一条撤权提示会变成一次假的「一切正常」，用户就没得可点了。
    // （换项目那一路已经清过了，见上面的 watch。）
  }
}

async function recheckAccess() {
  if (!project.value || accessBusy.value) return
  accessBusy.value = true
  try { await loadAccessStatus(project.value.id) }
  finally { accessBusy.value = false }
}

async function searchSources() {
  if (!project.value || busy.value || !sourceQuery.value.trim() || assets.value.length === 0) return
  busy.value = true
  errorMessage.value = ''
  // 上一次的结论先撤掉：留着它，一次正在飞行的检索会顶着一句「有资料未获授权」，
  // 而那句话说的是上一次的问题、上一次的资料范围。
  deniedRows.value = []
  const query = sourceQuery.value.trim()
  try {
    const result = await retrieveSources(project.value.id, query, assets.value.map(item => item.id), chapter.value?.id)
    sources.value = result.data
    searchedQuery.value = query
    closeSourceContext()
  } catch (error) { failure(error) }
  finally { busy.value = false }
}

// Saved citations are read directly, with current source authorization checks.
async function showCandidateSource(sourceId: string) {
  if (!project.value || busy.value || contextBusy.value) return
  const projectId = project.value.id
  busy.value = true
  errorMessage.value = ''
  try {
    const result = await getSource(projectId, sourceId)
    if (project.value?.id !== projectId) return
    const index = sources.value.findIndex(item => item.id === sourceId)
    if (index < 0) sources.value.push(result.data)
    else sources.value[index] = result.data
    await loadSourceContext(sourceId)
    if (project.value?.id !== projectId) return
    // The source section lives in the inspector's sources pane. A successful
    // protected read must reveal that pane before attempting to scroll it.
    inspTab.value = 'sources'
    inspOpen.value = true
    await nextTick()
    sourceListElement.value?.scrollIntoView({ behavior: 'smooth', block: 'center' })
  } catch (error) { failure(error) }
  finally { busy.value = false }
}

async function refreshSource(sourceId: string) {
  if (!project.value || busy.value) return
  busy.value = true
  errorMessage.value = ''
  try {
    const result = await getSource(project.value.id, sourceId)
    sources.value = sources.value.map(item => item.id === sourceId ? result.data : item)
    // 面板里那一份是重新定位**之前**的结论。留着不重读，标题说着「可以指回原文」
    // 而列表那行已经变成了「坐标已不可信」——同一屏上两个相反的结论。
    if (contextOpenFor.value === sourceId) await loadSourceContext(sourceId)
  } catch (error) { failure(error) }
  finally { busy.value = false }
}

// 「查看原文」：同页展开这一条引用所在分块的前后邻居，不跳转到知识库页。
//
// 展开得了与展开不了都要开面板——展不开的那两种原因（这一段是派生块 / 这条引用此刻失效）
// 本身就是用户点这一下想知道的事，闷着不显示等于让按钮看起来坏了。
async function toggleSourceContext(sourceId: string) {
  if (contextOpenFor.value === sourceId) {
    closeSourceContext()
    return
  }
  await loadSourceContext(sourceId)
}

async function loadSourceContext(sourceId: string) {
  if (!project.value || contextBusy.value) return
  const projectId = project.value.id
  contextBusy.value = true
  contextSourceId.value = sourceId
  sourceContext.value = null
  errorMessage.value = ''
  try {
    const result = await getSourceContext(projectId, sourceId)
    // 慢响应回来时可能已经换了项目、或点了另一条来源：别人的上下文不能贴到这一条上。
    if (project.value?.id === projectId && contextSourceId.value === sourceId) sourceContext.value = result.data
  } catch (error) { failure(error) }
  finally { contextBusy.value = false }
}

function closeSourceContext() {
  contextSourceId.value = ''
  sourceContext.value = null
}

async function bindProjectAsset() {
  if (!project.value || busy.value || !knowledgeId.value.trim()) return
  busy.value = true
  errorMessage.value = ''
  bindingNoticeText.value = ''
  const projectId = project.value.id
  const input = { knowledge_id: knowledgeId.value.trim() }
  const key = operationKey(`asset:${projectId}`, input)
  try {
    // 响应体就是绑定后的那一份资料（带 processing_state）。此前它被丢掉了，于是
    // 「绑定成功但还没就绪」这件事在界面上完全没有痕迹——只见它 201 之后从列表里消失。
    const bound = await bindAsset(projectId, input.knowledge_id, key)
    attempts.delete(`asset:${projectId}`)
    knowledgeId.value = ''
    bindingNoticeText.value = bindingNotice(bound.data)
    const refreshed = await listAssets(projectId)
    assets.value = refreshed.data ?? []
  } catch (error) { failure(error) }
  finally { busy.value = false }
}

async function saveConditions() {
  if (!project.value || busy.value) return
  if (project.value.status === 'active') {
	    await createConditionsChangePreview()
    return
  }
  busy.value = true
  errorMessage.value = ''
  const id = project.value.id
  const session=editorSessionEpoch
  const expected = specEditingRevision.value??project.value.spec_revision
  const fields = {...specDraftFields.value}
  const key = operationKey(`spec:${id}`, { expected_spec_revision: expected, fields })
  try {
    const result = await saveSpec(id, expected, fields, key)
    if(project.value?.id!==id||editorSessionEpoch!==session)return
    attempts.delete(`spec:${id}`)
    project.value = result.data
    specEditingRevision.value=result.data.spec_revision
    await loadProjects()
    if (result.meta.refresh_required) await selectProject(id)
  } catch (error) { failure(error) }
  finally { busy.value = false }
}

async function createConditionsChangePreview() {
  if (!project.value || busy.value || !specChanged.value || project.value.status !== 'active') return
  if(specBasisChanged.value){errorMessage.value='研究条件基线已变化。请先比较服务器条件与当前输入，再明确关联新版本；没有自动覆盖。';specOpen.value=true;return}
  busy.value = true
  errorMessage.value = ''
  const id = project.value.id
  const session = editorSessionEpoch
  const fields: Record<string, { old_value: string; new_value: string }> = {}
for(const [id,value] of Object.entries(specDraftFields.value)){const old=project.value.spec[id]??'';if(value!==old)fields[id]={old_value:old,new_value:value}}
  const input = {
    expected_context_revision: project.value.current_context_revision,
    fields,
    affected_chapter_ids: [...selectedImpactChapterIds.value],
    reason: changeReason.value.trim(),
  }
  const submittedInputs = conditionsChangeSnapshot(id, input.reason, input.affected_chapter_ids)
  const submittedDraft = { subject: subject.value, goal: goal.value, reason: submittedInputs.reason, chapterIds: submittedInputs.chapterIds,fieldsFingerprint:JSON.stringify(specDraftFields.value) }
  const createKey = operationKey(`change-set-create:${id}`, input)
  try {
    const created = await createChangeSet(id, input, createKey)
    if (project.value?.id !== id || session !== editorSessionEpoch) return
    pendingChangeSet.value = created.data
    pendingDraft.value = submittedDraft
    acknowledgedConditionsInputs.value = submittedInputs
  } catch (error) { if (project.value?.id === id && session === editorSessionEpoch) failure(error) }
  finally { if (session === editorSessionEpoch && !projectSwitchCommitting.value) busy.value = false }
}

async function applyConditionsChange() {
  if (!project.value || busy.value || !pendingChangeSet.value || !pendingDraftMatches.value) return
  busy.value = true
  errorMessage.value = ''
  const id = project.value.id
  const session = editorSessionEpoch
  const handledDraft = pendingDraft.value ? { ...pendingDraft.value, chapterIds: [...pendingDraft.value.chapterIds] } : null
  const changeSetId = pendingChangeSet.value.id
  const applyKey = operationKey(`change-set-apply:${id}:${changeSetId}`, { change_set_id: changeSetId })
  try {
    const applied = await applyChangeSet(id, changeSetId, applyKey)
    if (project.value?.id !== id || session !== editorSessionEpoch) return
    attempts.delete(`change-set-create:${id}`)
    attempts.delete(`change-set-apply:${id}:${changeSetId}`)
    acknowledgeHandledConditions(id, handledDraft)
    pendingChangeSet.value = null
    pendingDraft.value = null
    lastChangeSet.value = applied.data
    await selectProject(id, { handledConditions: handledDraft })
  } catch (error) {
    if (project.value?.id !== id || session !== editorSessionEpoch) return
    const item = error as { status?: number }
    if (item?.status === 409) {
      try {
        const current = await getChangeSet(id, changeSetId)
        if (project.value?.id !== id || session !== editorSessionEpoch) return
        if (current.data.status === 'stale') {
          pendingChangeSet.value = null
          pendingDraft.value = null
          lastChangeSet.value = current.data
        }
      } catch (readError) {
        if (project.value?.id !== id || session !== editorSessionEpoch) return
        failure(readError)
        return
      }
    }
    failure(error)
  }
  finally { if (session === editorSessionEpoch && !projectSwitchCommitting.value) busy.value = false }
}

async function rejectConditionsChange() {
  if (!project.value || busy.value || !pendingChangeSet.value) return
  busy.value = true
  errorMessage.value = ''
  const id = project.value.id
  const session = editorSessionEpoch
  const handledDraft = pendingDraft.value ? { ...pendingDraft.value, chapterIds: [...pendingDraft.value.chapterIds] } : null
  const changeSetId = pendingChangeSet.value.id
  const rejectKey = operationKey(`change-set-reject:${id}:${changeSetId}`, { change_set_id: changeSetId })
  try {
    const rejected = await rejectChangeSet(id, changeSetId, rejectKey)
    if (project.value?.id !== id || session !== editorSessionEpoch) return
    attempts.delete(`change-set-create:${id}`)
    attempts.delete(`change-set-reject:${id}:${changeSetId}`)
    acknowledgeHandledConditions(id, handledDraft)
    pendingChangeSet.value = null
    pendingDraft.value = null
    lastChangeSet.value = rejected.data
    await selectProject(id, { handledConditions: handledDraft })
  } catch (error) { if (project.value?.id === id && session === editorSessionEpoch) failure(error) }
  finally { if (session === editorSessionEpoch && !projectSwitchCommitting.value) busy.value = false }
}

async function activate() {
  if (!project.value || busy.value || specChanged.value) return
  const id=project.value.id,session=editorSessionEpoch
  busy.value=true;errorMessage.value=''
  try{const result=await getActivationDiff(id,0);if(project.value?.id!==id||session!==editorSessionEpoch)return;activationDiff.value=result.data;activationAcknowledged.value=false;activationReceipt.value=null;fullReturnFocus=document.activeElement instanceof HTMLElement?document.activeElement:null;activationOpen.value=true}
  catch(e){if(project.value?.id===id&&session===editorSessionEpoch)failure(e)}finally{if(session===editorSessionEpoch)busy.value=false}
}

function selectChapter(item: Chapter) {
  if(!editorRef.value?.focusChapter(item.id)) onEditorActiveChapter(item.id)
}

async function loadGenerationCandidates(chapterId = chapter.value?.id) {
  if (!project.value || !chapterId) {
    generationCandidates.value = []
    return
  }
  const projectId = project.value.id
  try {
    const result = await listGenerationCandidates(projectId, chapterId)
    if (project.value?.id === projectId && chapter.value?.id === chapterId) generationCandidates.value = result.data
  } catch (error) { failure(error) }
}

async function showGenerationCandidate(runId: string) {
  if (!project.value || generationBusy.value) return
  generationBusy.value = true
  try {
    const result = await getGeneratedCandidate(project.value.id, runId)
    generationCandidate.value = result.data
  } catch (error) { failure(error) }
  finally { generationBusy.value = false }
}

function openAdoption() {
  if (!project.value || !chapter.value || !generationCandidate.value || busy.value) return
  adoptionOpen.value = true
}

// 采纳成功后必须重读**整个**工作区，而不是只换章节。
//
// 采纳会推进项目版本；只更新 chapters/chapter 会让界面拿着旧的 project_version 去发
// 下一个请求——下一个动作必然 409，而用户什么都没做错。
//
// 这里刻意不再顺手调 saveChapter/confirmChapter：采纳已经是整章替换，下一步该由人
// 看过正文再决定（§5 要求采纳后重新确认待核项）。也不要复用 saveText 那把幂等键——
// 对话框有自己的签名，两者的「同一件事」定义不同。
async function onAdopted(adopted: Chapter) {
  adoptionOpen.value = false
  if (!project.value || !chapter.value) return
  const projectId = project.value.id
  const chapterId = chapter.value.id
  const index = chapters.value.findIndex(item => item.id === chapterId)
  if (index >= 0) chapters.value[index] = adopted
  chapter.value = adopted
  // 不同步的话 bodyChanged 为真、保存按钮亮着，用户一点就把刚采纳的正文又存成一版。
  bodyDraft.value = adopted.body_markdown
  workingCopy.value = null
  clearRewriteCandidate(projectId, chapterId)
  // 待核项已经换成候选自带那一份，为本章编的逐项处置不再适用。只清本章的：
  // 别的章节的草稿是用户刚写的理由，采纳这一章不该把它抹掉。
  for (const key of Object.keys(reviewDrafts.value)) {
    if (key.startsWith(`${chapterId}:`)) {
      delete reviewDrafts.value[key]
      delete savedReviewDrafts.value[key]
    }
  }
  generationCandidate.value = null
  try {
    const [refreshed, candidates] = await Promise.all([
      getProject(projectId), listGenerationCandidates(projectId, chapterId),
    ])
    project.value = refreshed.data
    // 旧候选的 validity 是按旧版本算出来的，不重读会继续显示一条已经不成立的 fresh。
    generationCandidates.value = candidates.data
    await loadChapterDraft(projectId, chapterId)
  } catch { errorMessage.value = '候选已采纳，但项目状态刷新失败。请重新读取项目；无需重复采纳。' }
}

function generationStorageKey(projectId: string, chapterId: string) {
  return `lingdoc:generation:${projectId}:${chapterId}`
}

async function resumeGeneration() {
  if (!project.value || !chapter.value) return
  const savedRunId = localStorage.getItem(generationStorageKey(project.value.id, chapter.value.id))
  if (savedRunId) await refreshGeneration(savedRunId)
}

async function refreshGeneration(runId = generationRun.value?.id) {
  if (!project.value || !runId || generationBusy.value) return
  generationBusy.value = true
  const projectId = project.value.id
  try {
    const result = await getGeneration(projectId, runId)
    if (project.value?.id !== projectId || chapter.value?.id !== result.data.chapter_id) return
    generationRun.value = result.data
    generationCandidate.value = null
    if (result.data.status === 'succeeded' && result.data.candidate_id) {
      const candidate = await getGeneratedCandidate(projectId, runId)
      if (project.value?.id !== projectId || chapter.value?.id !== candidate.data.chapter_id) return
      generationCandidate.value = candidate.data
      await loadGenerationCandidates(candidate.data.chapter_id)
    }
    if (result.data.status === 'queued' || result.data.status === 'running') {
      if (generationTimer) clearTimeout(generationTimer)
      generationTimer = setTimeout(() => { void refreshGeneration(runId) }, 2000)
    }
  } catch (error) { failure(error) }
  finally { generationBusy.value = false }
}

async function cancelGenerationRun() {
  if (!project.value || !generationRun.value || generationBusy.value) return
  const runId = generationRun.value.id
  generationBusy.value = true
  try {
    const result = await cancelGeneration(project.value.id, runId)
    generationRun.value = result.data
  } catch (error) { failure(error) }
  finally { generationBusy.value = false }
  if (generationRun.value?.status === 'queued' || generationRun.value?.status === 'running') {
    if (generationTimer) clearTimeout(generationTimer)
    generationTimer = setTimeout(() => { void refreshGeneration(runId) }, 2000)
  }
}

async function confirmCurrentChapter() {
  if (!project.value || !chapter.value?.current_version_id || confirmationReasons.value.length > 0) return
  const current = chapter.value
  const expectedVersion = current.current_version_id
  if (!expectedVersion) return
  const projectId = project.value.id
  const decisions: ReviewDecision[] = current.review_items.map(item => ({
    review_item_id: item.id,
    disposition: reviewDraft(item.id).disposition,
    reason: reviewDraft(item.id).reason.trim(),
  }))
  if (!window.confirm(`将确认“${current.title}”的当前版本。后续保存新版本会使本次确认失效。继续吗？`)) return
  busy.value = true
  errorMessage.value = ''
  const input = {
    expected_chapter_version_id: expectedVersion,
    expected_spec_revision: project.value.spec_revision,
    review_decisions: decisions,
  }
  const key = operationKey(`confirm:${current.id}`, input)
  try {
    await confirmChapter(projectId, current.id, input, key)
    // These reasons were acknowledged by the server; retaining the form is not
    // an unsaved edit. Unconfirmed reasons in other chapters remain protected.
    for (const decision of decisions) {
      const draftKey = `${current.id}:${decision.review_item_id}`
      savedReviewDrafts.value[draftKey] = { ...reviewDrafts.value[draftKey]! }
    }
    attempts.delete(`confirm:${current.id}`)
    void loadCurrentConfirmation()
    const result = await listChapters(projectId)
    chapters.value = result.data
    chapter.value = result.data.find(item => item.id === current.id) ?? null
    if (chapter.value) {
      bodyDraft.value = chapter.value.body_markdown
      hydrateCitationUsages(chapter.value)
    }
    // 确认会推进项目版本（确认记在项目上），所以项目必须跟着重读一次。少了这一步，
    // 紧接着的交付检查与冻结会拿着一个过期的 expected_project_version 去问，换来一个
    // 409——而用户什么都没做错。保存章节那条路径早就在重读，确认这条一直漏着。
    const refreshed = await getProject(projectId)
    project.value = refreshed.data
  } catch (error) { failure(error) }
  finally { busy.value = false }
}

async function startDraft() {
  if (!project.value || !chapter.value || busy.value || (!selectedAssetIds.value.length && !generationEvidenceRefs.value.length) || !generationInstruction.value.trim()) return
  if (workingCopyDiffersFromFormal.value) {
    errorMessage.value = '请先提交或恢复当前工作副本，再基于正式版本生成整章候选。'
    return
  }
  busy.value = true
  errorMessage.value = ''
  const projectId = project.value.id
  const chapterId = chapter.value.id
  const input = {
    chapter_id: chapterId,
    asset_ids: generationEvidenceRefs.value.length ? [] : [...selectedAssetIds.value].sort(),
    evidence_card_refs: generationEvidenceRefs.value.length ? generationEvidenceRefs.value.map(({id,version})=>({id,version})) : undefined,
    instruction: generationInstruction.value.trim(),
    expected_spec_revision: project.value.spec_revision,
    expected_chapter_version_id: chapter.value.current_version_id,
  }
  try {
    const key = await generationIdempotencyKey(localStorage, projectId, chapterId, input)
    const result = await startGeneration(projectId, input, key)
    generationRun.value = result.data
    generationCandidate.value = null
    localStorage.setItem(generationStorageKey(projectId, chapterId), result.data.id)
    clearGenerationAttempt(localStorage, projectId, chapterId)
    await refreshGeneration(result.data.id)
  } catch (error) { failure(error) }
  finally { busy.value = false }
}

async function saveText() {
  if (!project.value || !chapter.value || busy.value || workingCopySaving.value || !workingCopy.value) return
  // 引用从正文里读，与服务端用同一条规则。写坏的标记在本地就拦下：服务端也会判 400，
  // 但「请求字段不符合约定」说不清是哪里坏了，而这一刻我们完全知道。
  const citations = chapterCitations(bodyDraft.value)
  if (citations.kind === 'malformed') {
    errorMessage.value = MALFORMED_CITATION_MESSAGE
    return
  }
  workingCopySaving.value = true
  workingCopyStatus.value = 'saving'
  errorMessage.value = ''
  const projectId = project.value.id
  const chapterId = chapter.value.id
  const input = {
    base_chapter_version_id: workingCopy.value.base_chapter_version_id,
    expected_spec_revision: project.value.spec_revision,
    expected_working_copy_revision: workingCopy.value.working_copy_revision,
    body_markdown: currentBodyMarkdown(),
    source_ids: citations.sourceIds,
    citation_usages: citations.sourceIds.map(sourceId => ({ ...citationUsageDraft(sourceId) })),
  }
  const key = operationKey(`working-copy:${chapterId}`, input)
  try {
    const result = await saveWorkingCopy(projectId, chapterId, input, key)
    attempts.delete(`working-copy:${chapterId}`)
    if (project.value?.id !== projectId || chapter.value?.id !== chapterId) return
    workingCopy.value = result.data
    hydrateCitationUsages(result.data)
    workingCopyStatus.value = 'saved'
    clearRewriteCandidate(projectId, chapterId)
    if (result.meta.refresh_required) {
      errorMessage.value = '工作副本已保存，但服务端要求刷新状态；请重新读取后继续。'
      await loadChapterDraft(projectId, chapterId)
    }
  } catch (error) {
    workingCopyStatus.value = 'error'
    failure(error)
  } finally {
    workingCopySaving.value = false
  }
}

function rewriteCandidateStorageKey(projectId: string, chapterId: string) {
  return `lingdoc:selected-rewrite:${projectId}:${chapterId}`
}

function clearRewriteCandidate(projectId = project.value?.id, chapterId = chapter.value?.id) {
  if (projectId && chapterId) localStorage.removeItem(rewriteCandidateStorageKey(projectId, chapterId))
  rewriteCandidate.value = null
}

async function loadSavedRewriteCandidate(projectId: string, chapterId: string) {
  const storageKey = rewriteCandidateStorageKey(projectId, chapterId)
  const candidateId = localStorage.getItem(storageKey)
  if (!candidateId) return
  try {
    const result = await getSelectedRewrite(projectId, candidateId)
    if (project.value?.id === projectId && chapter.value?.id === chapterId) rewriteCandidate.value = result.data
  } catch {
    localStorage.removeItem(storageKey)
  }
}

function toggleRewriteSource(sourceId: string, event: Event) {
  const checked = (event.target as HTMLInputElement).checked
  const next = new Set(rewriteSourceIds.value)
  if (checked) next.add(sourceId)
  else next.delete(sourceId)
  rewriteSourceIds.value = [...next].sort()
}

// 动作面板提交：这一次按动作发起。
//
// 面板交出来的动作、目标范围、产出参数与补充说明**真的进请求体**——动作路
// 径的候选因此带上动作与来源，采纳时才有东西可确认。
function onRewriteActionSubmit(payload: {
  action: string
  scope: WritingActionScope
  output_params: WritingActionOutputParams
  instruction: string
}) {
  rewriteActionPlan.value = {
    action: payload.action,
    scope: payload.scope,
    output_params: payload.output_params,
    instruction: payload.instruction,
  }
  void requestSelectedRewrite()
}

// 自由文本路径：清掉上一次的动作计划，免得「按自由文本要求」悄悄带上动作。
function requestLegacyRewrite() {
  rewriteActionPlan.value = null
  void requestSelectedRewrite()
}

async function requestSelectedRewrite() {
  if (!project.value || !chapter.value || !workingCopy.value || rewriteBusy.value || busy.value) return
  // 连续文稿编辑器：经"保存正文 + 三重验证"的 UTF-16 映射取选区；无法安全映射明确拒绝（不猜范围）
  const mapped = editorRef.value?.selectionToUtf16()
  if (mapped && mapped.ok && mapped.chapterId !== chapter.value.id) {
    errorMessage.value = '选区所在章节与当前章节不一致，请切换到对应章节后再改写（不能跨章串写）。'
    return
  }
  if (!mapped || !mapped.ok) {
    errorMessage.value = !mapped || mapped.reason === 'cross-chapter-selection'
      ? '请先在同一段落范围内选中要改写的文字（不能跨章选择）。'
      : '当前选区无法安全映射到保存正文，请重新选择后再改写。'
    return
  }
  const selection = { start_utf16: mapped.start_utf16, end_utf16: mapped.end_utf16, selected_text: mapped.quote }
  if (!selection.selected_text.trim()) {
    errorMessage.value = '选中的内容为空白，请选择一段正文。'
    return
  }
  const plan = rewriteActionPlan.value
  // 自由文本路径的说明是必填的；动作路径的说明只是补充说明，必填的是动作与
  // 产出参数（服务端按执行计划校验）。
  if (!plan && !rewriteInstruction.value.trim()) {
    errorMessage.value = '请填写改写要求。'
    return
  }
  const projectId = project.value.id
  const chapterId = chapter.value.id
  if (chapterChanged.value) {
    if (workingCopyTimer) clearTimeout(workingCopyTimer)
    workingCopyTimer = undefined
    // 编辑器脏稿可能分布在任意章：先冲刷编辑器全部脏章（按章 CAS），再落旧路径
    if (editorRef.value?.hasUnsaved) await editorRef.value.flushSave()
    else await saveText()
    if (chapterChanged.value || workingCopyStatus.value === 'error') return
  }
  if (!workingCopy.value || !project.value || project.value.id !== projectId || chapter.value?.id !== chapterId) return
  const input = {
    base_chapter_version_id: workingCopy.value.base_chapter_version_id,
    expected_spec_revision: project.value.spec_revision,
    expected_working_copy_revision: workingCopy.value.working_copy_revision,
    selection,
    instruction: plan ? plan.instruction.trim() : rewriteInstruction.value.trim(),
    source_ids: rewriteUseEvidence.value&&generationEvidenceRefs.value.length?[]:[...rewriteSourceIds.value].sort(),
    evidence_card_refs: rewriteUseEvidence.value&&generationEvidenceRefs.value.length?generationEvidenceRefs.value.map(({id,version})=>({id,version})):undefined,
    // 动作路径三个字段一起给：服务端看到它们就按动作解析执行计划，缺一即拒。
    ...(plan ? { writing_action: plan.action, output_params: plan.output_params, scope: plan.scope } : {}),
  }
  const keyName = `selected-rewrite:${projectId}:${chapterId}`
  const key = operationKey(keyName, input)
  clearRewriteCandidate(projectId, chapterId)
  rewriteBusy.value = true
  errorMessage.value = ''
  try {
    const result = await createSelectedRewrite(projectId, chapterId, input, key)
    attempts.delete(keyName)
    if (project.value?.id !== projectId || chapter.value?.id !== chapterId) return
    rewriteCandidate.value = result.data
    localStorage.setItem(rewriteCandidateStorageKey(projectId, chapterId), result.data.candidate_id)
  } catch (error) { failure(error) }
  finally { rewriteBusy.value = false }
}

async function applyRewriteCandidate() {
  if (!project.value || !chapter.value || !workingCopy.value || !rewriteCandidate.value || rewriteBusy.value || busy.value || chapterChanged.value) return
  // 确认载荷：动作、采纳目标、目标范围、确认时刻、确认人、来源。
  //
  // 动作路径的候选缺它会被服务端 400 拒（ErrRewriteConfirmationRequired）——
  // 采纳不是「点一下就写进去」，用户要确认这次采纳的是哪个动作的结果、写到哪里、
  // 写到哪一段。旧路径的候选没有动作可确认，因此**不带**这个字段。
  const confirmation = rewriteAdoptionConfirmation(rewriteCandidate.value, auth.currentUserId, new Date().toISOString())
  if (confirmation.kind === 'unconfirmable') {
    errorMessage.value = confirmation.reason
    return
  }
  const candidate = rewriteCandidate.value
  const stage = rewriteCandidateStage.value
  if (!rewriteStageAdoptable(stage)) return
  if (stage !== 'applying' && workingCopy.value.working_copy_revision !== candidate.working_copy_revision) return
  if (!window.confirm('只把候选替换到所选文字，并保存为工作副本；不会创建正式版本。确定接受吗？')) return
  const projectId = project.value.id
  const chapterId = chapter.value.id
  const input = {
    expected_spec_revision: candidate.spec_revision,
    expected_working_copy_revision: candidate.working_copy_revision,
    base_chapter_version_id: candidate.base_chapter_version_id,
    ...(confirmation.kind === 'confirmed' ? { confirmation: confirmation.confirmation } : {}),
  }
  const keyName = `apply-selected-rewrite:${candidate.candidate_id}`
  const key = `apply-${candidate.candidate_id}`
  rewriteBusy.value = true
  errorMessage.value = ''
  try {
    const result = await applySelectedRewrite(projectId, chapterId, candidate.candidate_id, input, key)
    attempts.delete(keyName)
    if (project.value?.id !== projectId || chapter.value?.id !== chapterId) return
    workingCopy.value = result.data
    hydrateCitationUsages(result.data)
    bodyDraft.value = result.data.body_markdown
    workingCopyStatus.value = 'saved'
    clearRewriteCandidate(projectId, chapterId)
    editorDocs.value[chapterId]=snapshotOf(result.data)
    await synchronizeCleanDocument()
    if (result.meta.refresh_required) await loadChapterDraft(projectId, chapterId)
  } catch (error) { failure(error) }
  finally { rewriteBusy.value = false }
}

function rejectRewriteCandidate() {
  clearRewriteCandidate()
}

// 评审报告已应用：工作副本多了一份待核事项，正文随之更新。
//
// 报告不是候选，所以这里没有「候选被采纳」那套清场动作：改写候选还在原地，
// 它与这份报告是两条独立的线。
async function onReviewReportApplied(copy: WorkingCopy) {
  if (!project.value || chapter.value?.id !== copy.chapter_id) return
  workingCopy.value = copy
  hydrateCitationUsages(copy)
  bodyDraft.value = copy.body_markdown
  workingCopyStatus.value = 'saved'
  editorDocs.value[copy.chapter_id] = snapshotOf(copy)
  await synchronizeCleanDocument()
}

const pendingChapterCommit = ref<ChapterCommitBasis | null>(null)
const commitIntentNotice = ref('')
function currentChapterCommitBasis(): ChapterCommitBasis | null {
  if (!project.value || !chapter.value || !workingCopy.value) return null
  return {
    projectId: project.value.id, chapterId: chapter.value.id,
    identity: `${auth.effectiveTenantId}:${auth.currentUserId}`, session: editorSessionEpoch,
    specRevision: project.value.spec_revision, workingCopyRevision: workingCopy.value.working_copy_revision,
    chapterVersionId: chapter.value.current_version_id ?? null,
  }
}
function commitDraft() {
  if (!project.value || !chapter.value || !workingCopy.value || busy.value || workingCopySaving.value || chapterChanged.value) return
  if (!workingCopyDiffersFromFormal.value) return
  commitIntentNotice.value = ''
  pendingChapterCommit.value = currentChapterCommitBasis()
}
function currentCommitChoice(): ChapterCommitBasis | null {
  const reviewed = pendingChapterCommit.value
  if (!reviewed || busy.value || workingCopySaving.value || chapterChanged.value || !sameChapterCommitBasis(reviewed, currentChapterCommitBasis())) {
    pendingChapterCommit.value = null
    errorMessage.value = '提交前的章节、工作副本或身份已变化。原稿保留，请重新查看后选择修改性质。'
    return null
  }
  return reviewed
}
function routeSemanticDraft() {
  if (!currentCommitChoice()) return
  pendingChapterCommit.value = null
  commitIntentNotice.value = '正文仍保留在工作副本，尚未提交。请在正式变更中填写实际条件或语义差异并评估；只有当前 Owner 应用后才改变研究基线。'
  openFullPanel('changes')
}
watch([() => project.value?.id, () => chapter.value?.id, () => auth.currentUserId, () => auth.effectiveTenantId], () => {
  pendingChapterCommit.value = null
  commitIntentNotice.value = ''
})
async function commitWordingDraft() {
  const reviewed = currentCommitChoice()
  if (!reviewed || !project.value || !chapter.value || !workingCopy.value || !workingCopyDiffersFromFormal.value) return
  pendingChapterCommit.value = null
  busy.value = true
  errorMessage.value = ''
  const projectId = project.value.id
  const chapterId = chapter.value.id
  const input = {
    expected_spec_revision: project.value.spec_revision,
    expected_working_copy_revision: workingCopy.value.working_copy_revision,
    expected_chapter_version_id: chapter.value.current_version_id,
  }
  const key = operationKey(`commit-working-copy:${chapterId}`, input)
  try {
    await commitWorkingCopy(projectId, chapterId, input, key)
    attempts.delete(`commit-working-copy:${chapterId}`)
    // 真实提交回执 → 已提交基线推进（解除"已保存未提交"阻断；非按保存回执假定）
    const committedNow = (workingCopy.value?.working_copy_revision ?? 0)
    if (editorDocs.value[chapterId]) {
      editorDocs.value[chapterId] = { ...editorDocs.value[chapterId], committed: committedNow }
    }
    const [projectResult, chapterResult] = await Promise.all([getProject(projectId), listChapters(projectId)])
    if (project.value?.id !== projectId || chapter.value?.id !== chapterId) return
    project.value = projectResult.data
    chapters.value = chapterResult.data
    chapter.value = chapterResult.data.find(item => item.id === chapterId) ?? null
    if (chapter.value) await loadChapterDraft(projectId, chapterId)
    clearRewriteCandidate(projectId, chapterId)
  } catch (error) { failure(error) }
  finally { busy.value = false }
}

async function restoreVersion(version: ChapterVersion) {
  if (!project.value || !chapter.value || !workingCopy.value || busy.value || workingCopySaving.value) return
  if (chapterChanged.value && !window.confirm('当前还有未保存输入；继续恢复会覆盖编辑框内容，确定吗？')) return
  if (!window.confirm('将此历史版本恢复到工作副本？历史记录不会被改写；若要生效，还需提交为正式版本。')) return
  busy.value = true
  errorMessage.value = ''
  const projectId = project.value.id
  const chapterId = chapter.value.id
  const input = {
    chapter_version_id: version.id,
    expected_spec_revision: project.value.spec_revision,
    expected_working_copy_revision: workingCopy.value.working_copy_revision,
    expected_chapter_version_id: chapter.value.current_version_id,
  }
  const key = operationKey(`restore-working-copy:${chapterId}`, input)
  try {
    const result = await restoreWorkingCopy(projectId, chapterId, input, key)
    attempts.delete(`restore-working-copy:${chapterId}`)
    if (project.value?.id !== projectId || chapter.value?.id !== chapterId) return
    workingCopy.value = result.data
    hydrateCitationUsages(result.data)
    bodyDraft.value = result.data.body_markdown
    workingCopyStatus.value = 'saved'
    editorDocs.value[chapterId]=snapshotOf(result.data)
    await synchronizeCleanDocument()
    clearRewriteCandidate(projectId, chapterId)
  } catch (error) { failure(error) }
  finally { busy.value = false }
}

let removeUnloadGuard: (() => void) | undefined
onMounted(() => {
  removeUnloadGuard=installUnloadGuard(window,()=>unsavedInputs.value)
  void loadInvitationInbox();void loadPublishedTemplates()
  void (async()=>{await loadProjects();const id=sessionStorage.getItem(lastProjectStorageKey());if(id)await selectProject(id)})()
})
watch([()=>auth.currentUserId,()=>auth.effectiveTenantId],()=>{editorSessionEpoch++;projectReadSequence++;chapterReadSequence++;permissionEpoch++;invitationEpoch++;catalogEpoch++;lastPermissionIdentity='';project.value=null;projects.value=[];chapter.value=null;chapters.value=[];assets.value=[];sources.value=[];editorDocs.value={};editorDirtyAny.value=false;editorReady.value=false;workingCopy.value=null;bodyDraft.value='';subject.value='';goal.value='';extraSpecDraft.value={};specEditingRevision.value=null;reviewDrafts.value={};savedReviewDrafts.value={};citationUsageDrafts.value={};pendingCitationUsages.value={};generationCandidates.value=[];generationCandidate.value=null;generationRun.value=null;rewriteCandidate.value=null;chapterWritePermissions.value={};chapterConfirmPermissions.value={};myInvitations.value=[];publishedTemplates.value=[];sourceContext.value=null;currentConfirmation.value=null;if(generationTimer)clearTimeout(generationTimer);if(workingCopyTimer)clearTimeout(workingCopyTimer);void loadInvitationInbox();void loadPublishedTemplates();void loadProjects()})
onBeforeRouteLeave(() => canLeaveWorkspace(unsavedInputs.value, message => window.confirm(message)))
onUnmounted(() => {
  removeUnloadGuard?.()
  if (generationTimer) clearTimeout(generationTimer)
  if (workingCopyTimer) clearTimeout(workingCopyTimer)
})
</script>

<style scoped>
.workspace-head__actions { display: flex; gap: 10px; align-items: center; }
.evidence-link { display: inline-flex; align-items: center; padding: 8px 12px; border: 1px solid #0b8c91; border-radius: 7px; color: #0b6e71; background: #e5f6f2; text-decoration: none; }
.evidence-link:hover { background: #d5f0ea; }
.lingdoc-workspace { flex: 1; min-height: 0; width: 100%; max-width: 1200px; box-sizing: border-box; overflow-y: auto; scrollbar-gutter: stable; margin: 0 auto; padding: 32px; color: #24342e; }
.workspace-head, .section-head, .actions { display: flex; justify-content: space-between; align-items: center; gap: 16px; }
h1 { margin: 0 0 8px; font-size: 28px; } h2 { margin: 0 0 12px; font-size: 20px; } h3 { margin: 22px 0 14px; font-size: 17px; }
p { margin: 6px 0; } .muted { color: #6b7670; font-size: 13px; } .warning { color: #8b5b10; font-size: 13px; }
.alert { padding: 12px 16px; margin: 20px 0; background: #fff1ee; border: 1px solid #eea99e; border-radius: 8px; }
.workspace-grid { display: grid; grid-template-columns: 280px minmax(0, 1fr); gap: 20px; margin-top: 24px; }
.panel { background: #fff; border: 1px solid #dbe5dd; border-radius: 12px; padding: 22px; min-width: 0; }
.template-copy-panel { margin: 18px 0; padding: 14px 16px; border: 1px solid #dbe5dd; border-radius: 8px; background: #fbfdfb; }
.template-copy-panel h3 { margin-top: 0; }
.template-hash { overflow-wrap: anywhere; color: #6b7670; font-size: 12px; }
.template-editor { display: grid; gap: 8px; margin: 12px 0; padding: 12px; border: 1px solid #dbe5dd; border-radius: 7px; }
.template-editor__item { display: grid; grid-template-columns: minmax(150px, 1fr) minmax(180px, 2fr); align-items: center; gap: 12px; font-weight: 400; }
.template-editor__item small { color: #6b7670; font-weight: 400; }
.template-preview { margin-top: 14px; padding: 12px; border: 1px solid #dbe5dd; border-radius: 7px; background: #fff; }
.template-preview h4 { margin: 0 0 8px; }
.template-preview ul { padding-left: 20px; }
.create-form, .spec-form, .chapter-form, .generation-form { display: flex; flex-direction: column; gap: 10px; }
label { font-weight: 600; font-size: 14px; }
input, textarea { width: 100%; box-sizing: border-box; padding: 10px 12px; border: 1px solid #becdc3; border-radius: 7px; font: inherit; }
button { padding: 8px 12px; border: 1px solid #becdc3; border-radius: 7px; background: #fff; color: #25452f; cursor: pointer; }
button:hover:not(:disabled), button.selected { border-color: #238a52; background: #edf8f0; }
button:disabled { opacity: .55; cursor: not-allowed; }
.project-list { list-style: none; padding: 0; display: grid; gap: 7px; }
.project-list button { width: 100%; display: flex; justify-content: space-between; text-align: left; }
.project-list small, .chapter-tabs small { color: #67746a; margin-left: 8px; }
.asset-list { list-style: none; padding: 0; display: grid; gap: 8px; }
.asset-bind-form { display: flex; flex-direction: column; gap: 8px; margin-bottom: 14px; }
.asset-bind-row { display: flex; gap: 8px; }
.asset-bind-row input { flex: 1; min-width: 0; }
.asset-list li { display: flex; justify-content: space-between; gap: 12px; padding: 10px 12px; border: 1px solid #dbe5dd; border-radius: 7px; }
.asset-list span { display: flex; flex-direction: column; gap: 3px; min-width: 0; }
.asset-list small, .asset-list em { color: #67746a; font-size: 12px; font-style: normal; }
.generation-assets { display: grid; gap: 8px; margin: 0 0 12px; padding: 12px; border: 1px solid #dbe5dd; border-radius: 7px; }
.asset-choice { display: flex; align-items: center; gap: 8px; font-weight: 400; }
.asset-choice input { width: auto; }
.candidate-list { margin-top: 14px; }
.candidate-list ul { display: grid; gap: 6px; padding-left: 20px; }
.candidate-preview { margin-top: 14px; padding: 14px; border: 1px solid #dbe5dd; border-radius: 8px; background: #f7faf8; }
.citation-usages { display: grid; gap: 10px; margin: 14px 0; padding: 14px; border: 1px solid #dbe5dd; border-radius: 8px; background: #f7faf8; }
.citation-usage { display: grid; gap: 6px; padding: 10px; border: 1px solid #e5ece7; border-radius: 6px; background: white; }
.citation-status { margin: 0; font-size: 12px; }
.citation-status--available { color: #238a52; }
.citation-status--needs_review, .citation-status--unavailable { color: #8b5b10; }
.candidate-preview pre, .rewrite-preview pre { white-space: pre-wrap; overflow-wrap: anywhere; font: inherit; max-width: 100%; }
.rewrite-preview pre { margin: 8px 0 14px; line-height: 1.7; }
.rewrite-preview { min-width: 0; margin-top: 14px; padding: 14px; border: 1px solid #dbe5dd; border-radius: 8px; background: #f7faf8; overflow-wrap: anywhere; }
.binding-notice { padding: 10px 12px; background: #fff8ec; border: 1px solid #e6c98a; border-radius: 7px; color: #6b5a2e; font-size: 13px; }
.denied-sources { margin: 12px 0; padding: 12px 16px; background: #fff1ee; border: 1px solid #eea99e; border-radius: 8px; font-size: 13px; }
.denied-sources ul { display: grid; gap: 8px; margin: 8px 0; padding-left: 20px; }
.denied-sources li { display: grid; gap: 2px; }
.denied-sources small { color: #6b7670; font-size: 12px; }
.source-list { list-style: none; padding: 0; display: grid; gap: 10px; }
.source-list > li { display: grid; gap: 8px; padding: 10px 12px; border: 1px solid #dbe5dd; border-radius: 7px; }
.source-head { display: flex; flex-wrap: wrap; justify-content: space-between; align-items: flex-start; gap: 12px; }
.source-head span { display: flex; flex-direction: column; gap: 3px; min-width: 0; }
.source-head small { color: #67746a; font-size: 12px; }
.source-actions { display: flex; flex-wrap: wrap; gap: 8px; }
.source-quote { margin: 0; font-size: 13px; }
.source-context { display: grid; gap: 8px; padding: 12px; background: #f7faf8; border: 1px solid #dbe5dd; border-radius: 7px; }
.source-context h4 { margin: 0; font-size: 13px; }
.source-context p { margin: 0; font-size: 12px; }
.source-context__segments { display: grid; gap: 8px; margin: 0; padding-left: 20px; }
.source-context__segments li { display: grid; gap: 3px; font-size: 13px; }
.source-context__segments p { font-size: 13px; }
.source-context__segments small { color: #6b7670; font-size: 12px; }
.source-context__segments .is-verbatim > small:first-child { color: #238a52; font-weight: 600; }
.chapter-tabs { display: flex; flex-wrap: wrap; gap: 8px; margin-bottom: 14px; }
.access-warning { display: grid; gap: 10px; margin: 22px 0 0; padding: 14px 16px; background: #fff8ec; border: 1px solid #e6c98a; border-radius: 8px; }
.access-warning h3 { margin: 0; font-size: 15px; }
.access-warning__actions { display: grid; gap: 8px; margin: 0; padding-left: 20px; }
.access-warning__actions li { display: grid; gap: 2px; }
.access-warning__actions span { color: #6b7670; font-size: 13px; }
.access-warning button { justify-self: start; }
.empty-work { display: grid; place-items: center; min-height: 300px; color: #6b7670; }
@media (max-width: 760px) { .workspace-grid { grid-template-columns: 1fr; } .lingdoc-workspace { padding: 16px; } .template-editor__item { grid-template-columns: 1fr; gap: 5px; } }

/* ============================================================
   方案一三栏工作台外壳（gong 2026-10-12 选型接线）
   网格：rail 232 | doc 1fr | 右固定检查器 344；抽屉右滑出；宽对照全幅。
   ============================================================ */
.wb {
  display: grid;
  grid-template-columns: 232px minmax(0, 1fr);
  grid-template-rows: auto auto auto auto auto minmax(0, 1fr);
  grid-template-areas:
    "head head"
    "alert alert"
    "notify notify"
    "rail docwarn"
    "rail dochead"
    "rail doc";
  height: 100dvh;
  max-width: none;
  margin: 0;
  padding: 0;
  overflow: hidden;
  background: #f5f5f2;
}
.wb .workspace-grid, .wb .work-area { display: contents; }
/* R2 退修：默认辅助为浮层（不挤正文）；容器 ≥1296 时才停靠（见 mainClasses 的 wb--dock） */
.wb.wb--focus { padding-right: 0; }
.wb-top { grid-area: head; position: relative; z-index: 102; display: flex; align-items: center; gap: 14px; padding: 8px 16px; flex-wrap: wrap; background: #fff; }
.wb-brand h1 { font-size: 17px; }
.wb-brand-note { margin: 0; font-size: 12px; color: #6b7670; }
.wb-crumb { color: #6b7670; font-size: 13px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; min-width: 0; }
.wb-top-actions { display: flex; align-items: center; gap: 10px; margin-left: auto; flex-wrap: wrap; }
.wb-mode { display: flex; gap: 2px; background: #f0f1ec; border-radius: 8px; padding: 2px; }
.wb-mode button { border: 0; background: transparent; border-radius: 6px; padding: 4px 12px; font-size: 12px; }
.wb-mode button[aria-pressed="true"] { background: #fff; box-shadow: 0 1px 2px rgba(32,36,31,.12); font-weight: 600; }
.wb-save { display: inline-flex; align-items: center; gap: 6px; font-size: 12px; color: #4a5148; white-space: nowrap; }
.save-dot { width: 8px; height: 8px; border-radius: 50%; background: #9aa79b; display: inline-block; }
.save-dot.saved { background: #1c6b3f; }
.save-dot.unsaved { background: #8a5a10; }
.save-dot.saving { background: #8a5a10; animation: wb-pulse 1s infinite; }
.save-dot.error { background: #b3261e; }
@keyframes wb-pulse { 50% { opacity: .35; } }

.wb > .alert { grid-area: alert; margin: 0; border-radius: 0; }
/* g5-invitation-workbench-20261014：通知区（邀请/草稿恢复）独占一行，可收起、限高可滚动，不再以无 grid-area 的子元素挤压文稿列 */
.wb-notify {
  grid-area: notify;
  position: relative;
  z-index: 101;
  display: flex;
  flex-direction: column;
  min-width: 0;
  max-height: min(38dvh, 340px);
  overflow-y: auto;
  overscroll-behavior: contain;
  background: #fff;
  border-bottom: 1px solid #dbe5dd;
}
.wb-notify-toggle {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  border: 0;
  border-radius: 0;
  background: #f3faf5;
  padding: 8px 16px;
  text-align: left;
  flex: none;
}
.wb-notify-title { font-weight: 600; }
.wb-notify-count { font-size: 12px; color: #1c6b3f; }
.wb-notify-hist { font-size: 12px; color: #6b7670; }
.wb-notify-caret { margin-left: auto; font-size: 12px; color: #6b7670; flex: none; }
.wb-notify-body { display: grid; gap: 10px; padding: 10px 16px 14px; min-width: 0; }
.wb-notify-hint { margin: 0; font-size: 13px; color: #506259; }
.wb-notify-group { display: grid; gap: 8px; min-width: 0; }
.wb-invite-list { list-style: none; margin: 0; padding: 0; display: grid; gap: 8px; min-width: 0; }
.wb-invite {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  padding: 10px 12px;
  border: 1px solid #b6c9bf;
  border-radius: 10px;
  background: #f3faf5;
  min-width: 0;
}
.wb-invite-main { display: grid; gap: 4px; min-width: 0; overflow-wrap: anywhere; }
.wb-invite-badge { justify-self: start; font-size: 12px; border-radius: 999px; padding: 2px 8px; white-space: nowrap; }
.wb-invite-badge[data-state="active"] { background: #e2f1e7; color: #1c6b3f; }
.wb-invite-badge[data-state="expired"], .wb-invite-badge[data-state="closed"] { background: #eceeeb; color: #6b7670; }
.wb-invite-badge[data-state="accepted"] { background: #eef1f6; color: #4a5a72; }
.wb-invite-meta { font-size: 13px; color: #506259; }
.wb-invite-scope { font-size: 13px; }
.wb-invite-ids summary { cursor: pointer; font-size: 12px; color: #6b7670; }
.wb-invite-ids p { margin: 4px 0 0; font-size: 12px; color: #6b7670; overflow-wrap: anywhere; }
.wb-invite-accept { flex: none; white-space: nowrap; }
@media (max-width: 640px) {
  .wb-invite { flex-direction: column; align-items: stretch; }
  .wb-invite-accept { align-self: flex-start; }
  .wb-notify { max-height: 45dvh; }
}
.wb .access-warning { grid-area: docwarn; margin: 0 16px; align-self: center; }
.wb .workspace-grid > aside.panel {
  grid-area: rail;
  overflow-y: auto;
  border: 0;
  border-right: 1px solid #dbe5dd;
  border-radius: 0;
  background: #fafbf9;
  padding: 14px;
  display: flex;
  flex-direction: column;
  gap: 14px;
}
.wb-rail-heading { display: flex; align-items: center; justify-content: space-between; }
.wb-rail-close { display: none; }
.wb-rail-entries { display: grid; gap: 4px; }
.wb-rail-entries button { text-align: left; }

/* 下一轮：当前项目 + 切换弹层 / 待核审阅卡片（ux-refinement-20261012-01） */
.wb-toggle { white-space: nowrap; }
/* kimi-ui-20261013：顶栏分簇——视图 / 状态 / 写作 / 辅助，簇间分隔，避免 13 枚按钮平铺 */
.wb-cluster { display: inline-flex; align-items: center; gap: 6px; flex-wrap: nowrap; }
.wb-cluster + .wb-cluster { border-left: 1px solid #e2e4dc; padding-left: 10px; }
.wb-cluster-status { color: #4a5148; }
/* “开发与验收”次级菜单：诊断入口不占用写作主操作区 */
.wb-devmenu { position: relative; }
.wb-devmenu-toggle { white-space: nowrap; color: #536459; border-style: dashed; }
.wb-devmenu-body {
  position: absolute; top: calc(100% + 6px); right: 0; z-index: 150;
  display: grid; gap: 6px; min-width: 160px; padding: 10px;
  background: #fff; border: 1px solid #dbe5dd; border-radius: 10px;
  box-shadow: 0 10px 30px rgba(15,23,42,.16);
}
.wb-devmenu-body .evidence-link { justify-content: center; }
.wb-picker-head-actions { display: inline-flex; align-items: center; gap: 6px; }
.wb-picker-head-actions .small { white-space: nowrap; font-size: 12px; padding: 4px 9px; }
/* 状态徽章不随课题名挤压竖排 */
.wb-picker-list button small { white-space: nowrap; flex: none; }
/* 待核卡片头部外显原文关联状态 */
.rv-anchor { flex: none; align-self: center; font-size: 11px; color: #79816f; white-space: nowrap; }
.rv-anchor[data-state="located"] { color: #276555; }
.rv-anchor[data-state="stale"] { color: #8a5a10; }
/* 顶栏"目录/辅助"开关对（全部宽度生效）：收起只改可见性，组件常驻不销毁 */
.wb:not(.wb--rail) { grid-template-columns: 0 minmax(0, 1fr); }
.wb:not(.wb--rail) .workspace-grid > aside.panel { display: none; }
.wb:not(.wb--insp) { padding-right: 0; }
.wb:not(.wb--insp) .wb-tabs { display: none; }
.wb-project { display: grid; gap: 8px; }
.wb-project-chip { display: flex; align-items: center; justify-content: space-between; gap: 8px; padding: 8px 10px; border: 1px solid #dbe5dd; border-radius: 10px; background: #fff; font-size: 13px; min-width: 0; }
.wb-project-chip strong { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.wb-project-chip .badge { white-space: nowrap; flex: none; }
.wb-picker { display: grid; gap: 8px; padding: 10px; border: 1px solid #cfd3c8; border-radius: 10px; background: #fff; }
.wb-picker-head { display: flex; align-items: center; justify-content: space-between; gap: 8px; }
.wb-picker-head strong { font-size: 13px; }
.wb-picker-close { border: 0; background: transparent; font-size: 16px; padding: 0 6px; line-height: 1.2; }
.wb-picker input { border: 1px solid #cfd3c8; border-radius: 8px; padding: 6px 9px; font-size: 13px; min-width: 0; }
.wb-picker-list { list-style: none; margin: 0; padding: 0; display: grid; grid-template-columns: minmax(0, 1fr); min-width: 0; gap: 4px; max-height: 30vh; overflow-y: auto; overflow-x: hidden; }
.wb-picker-list li { min-width: 0; }
.wb-picker-list button { box-sizing: border-box; width: 100%; max-width: 100%; display: flex; justify-content: space-between; align-items: center; gap: 8px; text-align: left; min-width: 0; }
.wb-picker-list button strong { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; min-width: 0; }
.wb-picker-list button.selected { border-color: #276555; }
.rv { display: grid; gap: 12px; }
.rv-head { display: grid; gap: 6px; }
.rv-head h4 { margin: 0; font-size: 15px; }
.rv-track { height: 5px; border-radius: 99px; background: #f0f1ec; overflow: hidden; }
.rv-track > span { display: block; height: 100%; background: #276555; transition: width .2s; }
.rv-list { display: grid; gap: 8px; }
.rv-card { border: 1px solid #dbe5dd; border-radius: 10px; background: #fff; overflow: hidden; }
.rv-card-head { display: flex; gap: 8px; align-items: flex-start; width: 100%; text-align: left; border: 0; background: transparent; padding: 10px 12px; }
.rv-card-head:hover { background: #f0f1ec; }
.rv-badge { flex: none; font-size: 12px; border-radius: 999px; padding: 2px 8px; white-space: nowrap; }
.rv-badge[data-status="pending"] { background: #fdf3e0; color: #8a5a10; }
.rv-badge[data-status="resolved"], .rv-badge[data-status="retained"] { background: #f0f1ec; color: #4a5148; }
.rv-stmt { font-size: 13px; font-weight: 600; line-height: 1.6; }
.rv-card-body { display: grid; gap: 8px; padding: 4px 12px 12px; border-top: 1px dashed #dbe5dd; }
.rv-options { display: grid; gap: 6px; }
.rv-option { display: grid; gap: 3px; text-align: left; border: 1px solid #cfd3c8; border-radius: 8px; padding: 8px 10px; background: #fff; }
.rv-option[aria-pressed="true"] { border-color: #276555; background: #e9f2ee; }
.rv-option small { color: #79816f; font-size: 12px; line-height: 1.6; }
.rv-card-body textarea { width: 100%; border: 1px solid #cfd3c8; border-radius: 8px; padding: 6px 9px; font-size: 13px; }
.rv-recorded { color: #4a5148; font-size: 12px; }
.rv-server-note { margin: 0; font-size: 12px; line-height: 1.7; color: #1d5043; background: #e9f2ee; border: 1px solid #cfe3da; border-radius: 8px; padding: 8px 10px; }
.rv-nav { display: flex; align-items: center; justify-content: space-between; gap: 8px; }
.wb-chapters { display: grid; gap: 4px; }
.wb-chapters-title { margin: 0; font-size: 11px; letter-spacing: 1.4px; text-transform: uppercase; color: #79816f; }
.wb-chapters > button { display: flex; flex-direction: column; align-items: flex-start; gap: 2px; text-align: left; border: 1px solid transparent; background: transparent; padding: 8px 10px; border-radius: 10px; }
.wb-chapters > button:hover { background: #f0f1ec; }
.wb-chapters > button.selected { background: #fff; border-color: #cfd3c8; }
.wb-chapters > button small { color: #79816f; }

.wb .section-head { grid-area: dochead; padding: 12px 24px 6px; }
.wb .chapters { grid-area: doc; overflow-y: auto; min-height: 0; padding: 6px 24px 32px; }
.wb .chapter-tabs { display: none; }
.wb .chapters > h3 { display: none; }
.wb .empty-work { grid-area: doc; }

/* 右栏检查器（浮层为默认；wb--dock 时停靠） */
.wb .wb-tabs {
  position: fixed; top: var(--wb-head-bottom, 53px); right: 0; width: min(360px, 94vw); z-index: 100;
  display: flex; align-items: center; gap: 2px;
  background: #fff; border-bottom: 1px solid #dbe5dd; border-left: 1px solid #dbe5dd;
  box-shadow: -10px 0 30px rgba(15,23,42,.16);
  padding: 4px 6px;
}
.wb .wb-tabs [role="tab"] { flex: 1; border: 0; background: transparent; border-radius: 7px; padding: 7px 4px; font-size: 13px; position: relative; }
.wb .wb-tabs [role="tab"][aria-selected="true"] { background: #e9f2ee; color: #1d5043; font-weight: 600; }
.wb-tab-count { display: inline-grid; place-items: center; min-width: 17px; height: 17px; border-radius: 99px; background: #fdf3e0; color: #8a5a10; font-size: 10.5px; padding: 0 4px; margin-left: 2px; }
.wb-tabs-close { display: block; border: 0; background: transparent; font-size: 16px; padding: 2px 8px; }
/* 检查器主体：单一滚动容器（R1 修复——同页签多面板必须共用一个滚动容器，禁止同位叠加） */
.wb .wb-pane, .wb .generation {
  position: fixed; top: calc(var(--wb-head-bottom, 53px) + 43px); right: 0; bottom: 0; width: min(360px, 94vw); z-index: 99;
  overflow-y: auto; padding: 12px 14px 28px;
  background: #fafbf9; border-left: 1px solid #dbe5dd;
  box-shadow: -10px 0 30px rgba(15,23,42,.16);
  display: none; margin: 0;
}
/* R2 退修：容器 ≥1296 时停靠——正文 = 容器 − 232(目录) − 344(辅助) ≥ 640 */
.wb--dock { padding-right: 344px; }
.wb--dock .wb-tabs { width: 344px; box-shadow: none; }
.wb--dock .wb-pane, .wb--dock .generation { width: 344px; box-shadow: none; }
.wb--dock .wb-tabs-close { display: none; }
.wb--insp.wb--tab-sources .wb-pane-sources,
.wb--insp.wb--tab-ai .generation,
.wb--insp.wb--tab-checks .wb-pane-checks,
.wb--insp.wb--tab-history .wb-pane-history { display: block; }
.wb--focus .wb-tabs, .wb--focus .wb-pane, .wb--focus .generation { display: none !important; }

/* 抽屉：研究条件（含变更复核）/ 模板 / 交付 */
.wb .spec-form, .wb .change-set-panel, .wb .template-copy-panel, .wb .wb-delivery {
  position: fixed; right: 0; z-index: 120;
  background: #fff; border: 1px solid #dbe5dd;
  box-shadow: -12px 0 40px rgba(15,23,42,.18);
  visibility: hidden; opacity: 0; pointer-events: none;
  transition: opacity .15s ease-out;
}
.wb .spec-form { top: 53px; bottom: 45%; width: min(520px, 96vw); border-radius: 14px 0 0 0; overflow-y: auto; padding: 16px 18px; margin: 0; }
.wb .change-set-panel { top: calc(53px + 47%); bottom: 10px; width: min(520px, 96vw); border-radius: 0 0 0 14px; overflow-y: auto; padding: 14px 18px; margin: 0; }
.wb--spec .spec-form { visibility: visible; opacity: 1; pointer-events: auto; }
.wb--spec .change-set-panel { visibility: visible; opacity: 1; pointer-events: auto; }
.wb .template-copy-panel { top: 53px; bottom: 10px; width: min(560px, 96vw); border-radius: 14px 0 0 14px; overflow-y: auto; padding: 16px 18px; margin: 0; }
.wb--template .template-copy-panel { visibility: visible; opacity: 1; pointer-events: auto; }
.wb .wb-delivery { top: 53px; bottom: 10px; width: min(780px, 96vw); border-radius: 14px 0 0 14px; display: flex; flex-direction: column; overflow: hidden; margin: 0; border-top: 1px solid #dbe5dd; }
.wb--delivery .wb-delivery { visibility: visible; opacity: 1; pointer-events: auto; }
.wb-drawer-head { display: flex; align-items: center; justify-content: space-between; padding: 12px 16px; border-bottom: 1px solid #e2e4dc; background: #fff; }
.wb-drawer-head h3 { margin: 0; }
.wb-delivery-body { overflow-y: auto; padding: 8px 16px 20px; }
.wb-drawer-close { float: right; font-size: 16px; padding: 0 8px; }
.wb-scrim { position: fixed; inset: 0; background: rgba(15,23,42,.32); z-index: 110; }

/* 宽对照（临时审阅态） */
.wb .wb-compare {
  position: fixed; top: 53px; left: 0; right: 0; bottom: 0; z-index: 140;
  display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); grid-template-rows: minmax(0, 1fr) auto;
  background: #f5f5f2;
}
.wb-compare-pane { overflow-y: auto; padding: 20px 26px; min-width: 0; }
.wb-compare-pane + .wb-compare-pane { border-left: 1px solid #dbe5dd; }
.wb-compare-pane h3 { margin: 4px 0 12px; }
.wb-compare-pane pre { white-space: pre-wrap; overflow-wrap: anywhere; background: #fff; border: 1px solid #e2e4dc; border-radius: 10px; padding: 14px 16px; font-family: Georgia, "Songti SC", "SimSun", serif; font-size: 15px; line-height: 1.85; }
.wb-compare-bar { grid-column: 1 / -1; display: flex; align-items: center; gap: 10px; flex-wrap: wrap; padding: 10px 20px; background: #fff; border-top: 1px solid #dbe5dd; }
.wb-compare-bar .grow { flex: 1; }
.wb-eyebrow { margin: 0; font-size: 11px; letter-spacing: 1.6px; text-transform: uppercase; color: #79816f; }

/* 响应式：右栏停靠/浮层由容器宽度（wb--dock）决定，见脚本 ResizeObserver；≤899 目录转浮层、对照上下堆叠 */
@media (max-width: 899px) {
  .wb:not(.wb--rail) { grid-template-columns: minmax(0, 1fr); }
  .wb { grid-template-columns: minmax(0, 1fr); grid-template-rows: auto auto auto auto auto minmax(0, 1fr);
    grid-template-areas: "head" "alert" "notify" "docwarn" "dochead" "doc"; padding-right: 0; }
  .wb .workspace-grid > aside.panel {
    position: fixed; top: 0; left: 0; bottom: 0; width: min(280px, 84vw); z-index: 130;
    transform: translateX(-105%); visibility: hidden; transition: transform .18s ease-out;
    box-shadow: 12px 0 40px rgba(15,23,42,.18);
  }
  .wb--rail .workspace-grid > aside.panel { transform: none; visibility: visible; }
  .wb-rail-close { display: inline-flex; align-items: center; justify-content: center; min-width: 36px; min-height: 36px; }
  .wb .section-head { padding: 10px 14px 4px; }
  .wb .chapters { padding: 4px 14px 24px; }
  .wb .wb-compare { grid-template-columns: minmax(0, 1fr); grid-template-rows: minmax(0, 1fr) minmax(0, 1fr) auto; }
  .wb-compare-pane + .wb-compare-pane { border-left: 0; border-top: 1px solid #dbe5dd; }
}
</style>

<style>
.document-chapter-context { font-size:12px; color:#536459; padding:0 4px; }
.document-welcome { max-width:640px; margin:12vh auto; text-align:left; padding:36px; }
.document-eyebrow { color:#557867; font-size:12px; letter-spacing:2px; }
.document-welcome h2 { font-size:28px; margin:14px 0; }
.document-welcome p { color:#607165; line-height:1.7; }
.document-welcome .create-form { display:flex; gap:10px; margin:24px 0 16px; }
.document-welcome input { flex:1; min-width:0; }
.review-quote { border-left:3px solid #7ea994; background:#f2f7f3; padding:8px 10px; white-space:pre-wrap; overflow-wrap:anywhere; }
.review-location { display:flex; flex-wrap:wrap; gap:6px; }
.review-location p { width:100%; }
.wb--focus.wb--insp .wb-tabs { display:flex !important; }
.wb--focus.wb--insp.wb--tab-sources .wb-pane-sources,.wb--focus.wb--insp.wb--tab-ai .generation,.wb--focus.wb--insp.wb--tab-checks .wb-pane-checks,.wb--focus.wb--insp.wb--tab-history .wb-pane-history { display:block !important; }
.ld-toolbar { position:sticky; top:0; z-index:5; background:#f6f7f3; padding:8px 0; }
.ld-paper .tiptap { padding:clamp(18px,3vw,52px); font-size:16px; }
.ld-source { cursor:pointer; white-space:normal; }
.wb .spec-form,.wb .template-copy-panel,.wb .wb-delivery,.wb .wb-compare { top:var(--wb-head-bottom,53px); }
.wb .kb-picker .asset-bind-row { display:grid;grid-template-columns:minmax(0,1fr);gap:8px; }
.wb .kb-picker select { width:100%;min-width:0;height:38px;min-height:38px;max-height:38px;text-overflow:ellipsis; }
.wb .kb-picker button { width:100%;white-space:nowrap; }
.wb-pane-sources .asset-bind-row { min-width:0; }
.wb-pane-sources .asset-bind-row input,.wb-pane-sources .asset-bind-row select { min-width:0; }
.wb .section-head { margin:0 0 12px; }.wb .section-head h2 {font-size:16px}.wb .section-head p {font-size:11px}
@media(max-width:599px){
 .wb .wb-top { padding:6px 10px;gap:4px 8px; }
 .wb .wb-brand h1 {font-size:16px;margin:0}.wb .wb-brand-note{display:none}
 .wb .wb-crumb{font-size:11px;max-width:190px;margin-left:auto}
 .wb .wb-top-actions{flex-wrap:nowrap;overflow-x:auto;width:100%;max-width:100%;margin-left:0;gap:6px;padding:4px 0}
 .wb .wb-top-actions>*{flex-shrink:0}.wb .wb-top-actions button,.wb .wb-top-actions a{font-size:11px;padding:5px 8px;white-space:nowrap}
 .wb .wb-top-actions .wb-mode,.wb .wb-top-actions .wb-save,.wb .wb-top-actions .badge,.wb .wb-top-actions .evidence-link,.wb .wb-top-actions .wb-devmenu{display:none}
 .wb .wb-top-actions .wb-cluster{border-left:0;padding-left:0}
 .document-chapter-context{font-size:11px}
 .wb .section-head{min-height:0;margin:0 0 4px;gap:4px}.wb .section-head h2,.wb .section-head p{display:none}.wb .section-head button{font-size:11px;padding:4px 8px}
 .wb .chapters{margin-top:0}.ld-toolbar{flex-wrap:nowrap;overflow-x:auto;white-space:nowrap;max-width:100%;padding:4px 0;gap:3px}.ld-toolbar>*{flex-shrink:0}.ld-toolbar button{font-size:12px;padding:4px 7px}.ld-toolbar .ld-save{display:none}
 .ld-paper .tiptap{padding:18px}.document-welcome {margin:5vh auto;padding:20px}.document-welcome .create-form{flex-direction:column}.ld-find {flex-wrap:wrap}.ld-find input{min-width:0;max-width:100%}}

.full-workflow-backdrop{position:fixed;inset:0;z-index:1050;display:flex;justify-content:center;align-items:center;background:rgba(18,39,36,.35);padding:24px;box-sizing:border-box}
.full-workflow-dialog{width:min(1050px,100%);max-height:calc(100dvh - 48px);overflow:auto;overscroll-behavior:contain;min-width:0;background:#fff;border-radius:14px;box-shadow:0 24px 80px rgba(16,35,32,.24)}
.full-workflow-head{position:sticky;top:0;z-index:2;display:flex;align-items:center;justify-content:space-between;gap:16px;padding:16px 24px;border-bottom:1px solid #e0e8e5;background:#fff}
.activation-review>p,.activation-review>dl,.activation-consent{margin:20px 24px;line-height:1.7}.activation-review>.primary{margin:0 24px 24px}.activation-fields{display:grid;grid-template-columns:minmax(100px,180px) minmax(0,1fr);gap:12px}.activation-fields dt{font-weight:600}.activation-fields dd{margin:0;white-space:pre-wrap;overflow-wrap:anywhere}.activation-fields small{display:block;font-weight:400;color:#65776d}.activation-consent{display:flex;gap:10px;align-items:start}.activation-consent input{width:auto;flex:0 0 auto;margin-top:6px}
@media(max-width:640px){.full-workflow-backdrop{padding:0}.full-workflow-dialog{width:100%;max-height:100dvh;border-radius:0}.full-workflow-head{padding:12px 16px}.activation-fields{grid-template-columns:1fr}.activation-review>p,.activation-review>dl,.activation-consent{margin:16px}}
</style>

<style scoped>
.discarded-drafts{margin:0;color:#506259;min-width:0}.discarded-drafts>div{display:flex;justify-content:space-between;gap:16px;padding:8px 0;min-width:0}.discarded-drafts summary{cursor:pointer}
</style>

<style scoped>.discarded-drafts{margin:10px 0;color:#506259}.discarded-drafts>div{display:flex;justify-content:space-between;gap:16px;padding:8px 0}.discarded-drafts summary{cursor:pointer}</style>
