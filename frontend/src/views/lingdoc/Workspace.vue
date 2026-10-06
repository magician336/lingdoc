<template>
  <main class="lingdoc-workspace">
    <header class="workspace-head">
      <div>
        <h1>灵档项目</h1>
        <p>当前使用两章演示模板，内容仅供团队验证流程。</p>
      </div>
      <div class="workspace-head__actions">
        <RouterLink class="evidence-link" to="/platform/lingdoc/permissions">权限验收</RouterLink>
        <button type="button" @click="loadProjects" :disabled="loading">刷新项目</button>
      </div>
    </header>

    <p v-if="errorMessage" role="alert" class="alert">{{ errorMessage }}</p>
    <div class="workspace-grid">
      <aside class="panel">
        <h2>我的项目</h2>
        <form class="create-form" @submit.prevent="create">
          <label for="project-name">新项目名称</label>
          <input id="project-name" v-model="newName" maxlength="120" required placeholder="例如：演示申报项目" />
          <button type="submit" :disabled="busy || !newName.trim()">创建项目</button>
        </form>
        <p v-if="loading">正在加载…</p>
        <p v-else-if="projects.length === 0" class="muted">还没有项目。</p>
        <ul v-else class="project-list">
          <li v-for="item in projects" :key="item.id">
            <button type="button" :class="{ selected: project?.id === item.id }" @click="selectProject(item.id)">
              <strong>{{ item.name }}</strong><small>{{ item.status === 'draft' ? '草稿' : '已立项' }}</small>
            </button>
          </li>
        </ul>
        <p v-if="truncated" class="muted">只显示最近 50 个项目。</p>
      </aside>

      <section class="panel work-area" v-if="project">
        <div class="section-head">
          <div><h2>{{ project.name }}</h2><p class="muted">研究条件版本 {{ project.spec_revision }} · 项目版本 {{ project.project_version }}</p></div>
          <button type="button" @click="selectProject(project.id, true)">重新读取</button>
        </div>
        <p class="muted">项目成员 {{ project.members.length }} 人。现阶段由项目成员协作编辑。</p>

        <section class="template-copy-panel" aria-label="项目模板副本">
          <h3>项目模板副本</h3>
          <template v-if="project.template_copy">
            <p class="muted">来源 {{ project.template_copy.source_template_id }} · 原始版本 {{ project.template_copy.source_template_version }} · 项目副本第 {{ project.template_copy.version }} 版（{{ templateCopyStatusLabel(project.template_copy.status) }}）</p>
            <p class="template-hash">内容摘要：{{ project.template_copy.content_hash }}</p>
            <p class="template-hash">规则集摘要：{{ project.template_copy.ruleset_hash }}</p>
            <p v-if="project.status === 'active'" class="muted">项目已立项；普通保存不可用。模板改动会创建 ChangeSet，列出受影响章节并由 Owner 确认应用。</p>
            <template v-if="templateCopyDraft">
              <p class="muted">这里只能调整展示名称；字段 ID、字段类型和规则保持原样。{{ project.status === 'draft' ? '改动先预览，再保存为新的不可变草稿版本。' : '改动需经过模板升级 ChangeSet，应用后旧版本和章节确认历史仍保留。' }}</p>
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
                </label>
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
                  <button type="button" :disabled="busy || !pendingTemplateUpgradeMatches" @click="applyTemplateUpgrade">由 Owner 应用并开始复核</button>
                  <button type="button" :disabled="busy" @click="rejectTemplateUpgrade">驳回评估</button>
                </div>
                <p v-if="!pendingTemplateUpgradeMatches" class="muted">模板草稿或理由已变化；请重新创建评估后再应用。</p>
              </section>
            </template>
          </template>
          <p v-else class="muted">此项目没有可读取的模板副本；请重新读取项目状态。</p>
        </section>

        <section class="assets">
          <h3>项目资料</h3>
          <form class="asset-bind-form" @submit.prevent="bindProjectAsset">
            <label for="knowledge-id">绑定知识资料</label>
            <div class="asset-bind-row">
              <input id="knowledge-id" v-model="knowledgeId" :disabled="busy" placeholder="输入 knowledge_id" />
              <button type="submit" :disabled="busy || !knowledgeId.trim()">绑定</button>
            </div>
          </form>
          <!-- 绑定成功但资料还没就绪时，它随后会从下面的列表里消失（listAssets 只回允许集合）。
               不说这一句，用户看到的是「绑定成功了但什么都没发生，再绑一次还是这样」。 -->
          <p v-if="bindingNoticeText" class="binding-notice" role="status">{{ bindingNoticeText }}</p>
          <p v-if="assets.length === 0" class="muted">当前项目没有可用的已就绪资料。</p>
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

        <section v-if="project.status === 'active' && chapter" class="generation">
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
              <button type="submit" :disabled="busy || generationPending || !selectedAssetIds.length || !generationInstruction.trim()">生成候选稿</button>
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
          <h3>研究条件</h3>
          <label for="subject">研究主题</label>
          <input id="subject" v-model="subject" :disabled="busy" placeholder="写清项目研究什么" />
          <label for="goal">研究目标</label>
          <textarea id="goal" v-model="goal" :disabled="busy" rows="3" placeholder="写清本轮要验证什么" />
          <div class="actions">
            <button type="submit" :disabled="busy || !specChanged">保存研究条件</button>
            <button v-if="project.status === 'draft'" type="button" @click="activate" :disabled="busy || specChanged || !subject.trim() || !goal.trim()">立项并创建章节</button>
          </div>
          <p v-if="specChanged && project.status === 'draft'" class="muted">立项前请先保存研究条件。</p>
        </form>

        <section v-if="project.status === 'active' && (specChanged || lastChangeSet)" class="change-set-panel" aria-label="研究条件变更复核">
          <div v-if="specChanged">
            <h3>提交研究条件变更</h3>
            <p class="muted">研究条件变更会使选中章节的旧确认失效，完成重新确认后才能继续冻结和导出。</p>
            <label for="change-reason">变更理由</label>
            <input id="change-reason" v-model="changeReason" :disabled="busy" maxlength="2000" placeholder="说明为什么需要变更" />
            <fieldset :disabled="busy">
              <legend>需要重新复核的章节</legend>
              <label v-for="item in chapters" :key="item.id" class="chapter-impact-choice">
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
                <button type="button" :disabled="busy || !pendingDraftMatches" @click="applyConditionsChange">
                  应用预览并开始复核
                </button>
                <button type="button" :disabled="busy" @click="rejectConditionsChange">
                  驳回预览
                </button>
              </div>
              <p v-if="!pendingDraftMatches" class="muted">研究条件已改变，请重新生成预览。</p>
            </div>
          </div>
          <p v-if="lastChangeSet" class="muted" role="status">
            变更 {{ lastChangeSet.id.slice(0, 8) }} 已{{ changeSetStatusLabel(lastChangeSet.status) }}<template v-if="lastChangeSet.status === 'applied'">；请重新确认受影响章节。</template><template v-else-if="lastChangeSet.status === 'stale'">；基线已过期，请重新生成预览。</template><template v-else>。</template>
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
            <button v-for="item in chapters" :key="item.id" type="button"
              :class="{ selected: chapter?.id === item.id }" @click="selectChapter(item)">
              {{ item.title }} <small>{{ item.current_version_id ? '已编辑' : '未填写' }}</small>
            </button>
          </div>
          <form v-if="chapter" @submit.prevent="saveText" class="chapter-form">
            <label for="chapter-body">{{ chapter.title }}正文</label>
            <textarea id="chapter-body" v-model="bodyDraft" :disabled="busy || rewriteBusy || workingCopySaving || workingCopyLoading" @input="scheduleWorkingCopySave" rows="12" placeholder="从这里开始撰写章节" />
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
            <section class="selected-rewrite" aria-label="选段改写">
              <h4>选段 AI 改写</h4>
              <p class="muted">先在正文中选中一段，再填写改写要求。生成只会产生候选，不会直接改正文。</p>
              <label for="rewrite-instruction">改写要求</label>
              <textarea id="rewrite-instruction" v-model="rewriteInstruction" :disabled="busy || rewriteBusy" rows="2" maxlength="2000" />
              <fieldset class="rewrite-sources">
                <legend>允许候选使用的来源</legend>
                <label v-for="source in sources" :key="source.id" class="rewrite-source">
                  <input type="checkbox" :checked="rewriteSourceIds.includes(source.id)" :disabled="source.status !== 'available' || rewriteBusy"
                    @change="toggleRewriteSource(source.id, $event)" />
                  <span>{{ source.locator }}<small>{{ source.quoted_text.slice(0, 120) }}</small></span>
                </label>
                <p v-if="!sources.length" class="muted">先检索项目资料，或在已有正文中保留来源标记。</p>
              </fieldset>
              <button type="button" @click="requestSelectedRewrite" :disabled="busy || rewriteBusy || workingCopySaving || workingCopyLoading || !workingCopy">
                {{ rewriteBusy ? '正在生成候选…' : '改写选中段落' }}
              </button>
              <section v-if="rewriteCandidate" class="rewrite-preview" aria-live="polite">
                <p class="muted">候选状态：{{ rewriteCandidate.status }} · 运行模式：{{ rewriteCandidate.run_mode }}</p>
                <p v-if="rewriteCandidate.status === 'failed'" class="warning">候选生成失败（{{ rewriteCandidate.error_code || 'unknown' }}）；可以修改要求后重新生成。</p>
                <p v-else-if="rewriteCandidate.status === 'stale'" class="warning">候选基于旧工作副本或来源，不能采纳；请重新选择并生成。</p>
                <template v-else-if="rewriteCandidate.status === 'ready' || rewriteCandidate.status === 'applying'">
                  <h5>原文选段</h5>
                  <pre>{{ rewriteCandidate.selection.selected_text }}</pre>
                  <h5>改写候选</h5>
                  <pre>{{ rewriteCandidate.replacement_markdown }}</pre>
                  <p class="muted">候选来源：{{ rewriteCandidate.source_ids.length ? rewriteCandidate.source_ids.join('、') : '无' }}</p>
                  <ul v-if="rewriteCandidate.review_items.length" class="rewrite-review-list">
                    <li v-for="item in rewriteCandidate.review_items" :key="item.id">待核：{{ item.statement }}</li>
                  </ul>
                  <button type="button" @click="applyRewriteCandidate" :disabled="rewriteBusy || busy || chapterChanged || !workingCopy || (rewriteCandidate.status !== 'applying' && workingCopy.working_copy_revision !== rewriteCandidate.working_copy_revision)">
                    {{ rewriteCandidate.status === 'applying' ? '重试确认采纳结果' : '接受并应用到工作副本' }}
                  </button>
                  <button type="button" @click="rejectRewriteCandidate" :disabled="rewriteBusy || busy || rewriteCandidate.status === 'applying'">拒绝候选</button>
                </template>
              </section>
            </section>
            <p v-if="displayedReviewItems.length" class="warning">本章有 {{ displayedReviewItems.length }} 条待核事项。保存修改会保留这些事项，仍需逐项核查。</p>
            <section v-if="displayedReviewItems.length" class="review-items" aria-label="待核项处置">
              <h4>逐项处置待核事项</h4>
              <fieldset v-for="item in displayedReviewItems" :key="item.id" class="review-item">
                <legend>{{ item.statement }}</legend>
                <label :for="`disposition-${item.id}`">处理结果</label>
                <select :id="`disposition-${item.id}`" v-model="reviewDraft(item.id).disposition" :disabled="busy">
                  <option value="resolved">已核实并解决</option>
                  <option value="retained_warning">保留为提示</option>
                </select>
                <label :for="`reason-${item.id}`">处理理由</label>
                <textarea :id="`reason-${item.id}`" v-model="reviewDraft(item.id).reason" :disabled="busy" rows="2" maxlength="2000" placeholder="记录核查依据或保留原因" />
              </fieldset>
            </section>
            <p v-if="!chapter.current_version_id" class="muted">空章节尚无可确认版本；仍可继续交付检查，检查结果会指出内容缺失。</p>
            <p v-else-if="chapter.confirmation_valid" class="confirmed">当前章节版本已确认。保存新版本后需要重新确认。</p>
            <p v-else-if="workingCopyDiffersFromFormal" class="muted">工作副本与正式版本不同；保存工作副本不会创建正式版本。</p>
            <button v-if="chapter.current_version_id" type="button" @click="confirmCurrentChapter"
              :disabled="busy || workingCopySaving || chapterChanged || workingCopyDiffersFromFormal || !reviewDecisionsReady">
              {{ chapter.confirmation_valid ? '重新确认当前版本' : '确认当前章节版本' }}
            </button>
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
          </form>
        </div>

        <DeliveryPanel v-if="project.status === 'active'" :project="project" :chapters="chapters"
          @refresh-project="refreshProjectVersion" />
      </section>
      <section v-else class="panel empty-work"><p>选一个项目开始；也可以先创建项目。</p></section>
    </div>
  </main>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import type { Candidate } from '@/api/lingdoc/candidateAdoption'
import {
  cancelGeneration, getGeneratedCandidate, getGeneration, listGenerationCandidates, startGeneration,
  type GenerationCandidateSummary, type GenerationRun,
} from '@/api/lingdoc/generation'
import DeliveryPanel from './DeliveryPanel.vue'
import LingDocCandidateAdoptionDialog from '@/components/LingDocCandidateAdoptionDialog.vue'
import { RESTRICTED_NOTICE, isRestricted, recoveryActionsOf } from './accessStatus'
import { chapterCitations, MALFORMED_CITATION_MESSAGE } from './chapterCitations'
import { clearGenerationAttempt, generationIdempotencyKey } from './generationAttempt'
import {
  CONTEXT_UNAVAILABLE_NOTICE, contextLines, contextNotice, quotedTextOf, sourceStatusLabel, windowLabel,
} from './sourceContext'
import { DENIED_NOTICE, bindingNotice, denyReasonOf, deniedSourcesOf } from './sourceNotices'
import {
  activateProject, applyChangeSet, bindAsset, commitWorkingCopy, confirmChapter, createChangeSet, createProject, getAccessStatus, getProject,
  getSource, getSourceContext, getWorkingCopy, getChangeSet, listAssets, listChangeSets, listChapterVersions, listChapters, listProjects,
  applySelectedRewrite, createSelectedRewrite, getSelectedRewrite,
  previewTemplateCopyEdit, rejectChangeSet, restoreWorkingCopy, retrieveSources, saveSpec, saveTemplateCopyEdit, saveWorkingCopy,
  type AccessStatus, type Asset, type Chapter, type ChapterVersion, type ChangeSet, type CitationUsage, type Project, type ReviewDecision,
  type TemplateCopyDefinition, type TemplateCopyEditInput, type TemplateMigrationPreview,
  type SelectedRewriteCandidate, type Source, type SourceContext, type WorkingCopy,
} from '@/api/lingdoc/workspace'

const projects = ref<Project[]>([])
const truncated = ref(false)
const project = ref<Project | null>(null)
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
const rewriteInstruction = ref('在不增加无依据结论的前提下，提升表达清晰度与逻辑衔接。')
const rewriteSourceIds = ref<string[]>([])
const rewriteBusy = ref(false)
const newName = ref('')
const subject = ref('')
const goal = ref('')
const changeReason = ref('')
const selectedImpactChapterIds = ref<string[]>([])
const pendingChangeSet = ref<ChangeSet | null>(null)
const pendingDraft = ref<{ subject: string; goal: string; reason: string; chapterIds: string[] } | null>(null)
const lastChangeSet = ref<ChangeSet | null>(null)
const bodyDraft = ref('')
const busy = ref(false)
const loading = ref(false)
const errorMessage = ref('')
const reviewDrafts = ref<Record<string, { disposition: ReviewDecision['disposition']; reason: string }>>({})
const citationUsageDrafts = ref<Record<string, CitationUsage>>({})
const generationInstruction = ref('根据已允许的项目资料起草本章，引用来源并列出所有待核事项。')
const generationRun = ref<GenerationRun | null>(null)
const generationCandidate = ref<Candidate | null>(null)
const generationCandidates = ref<GenerationCandidateSummary[]>([])
const generationBusy = ref(false)
const accessStatus = ref<AccessStatus | null>(null)
const accessBusy = ref(false)
const adoptionOpen = ref(false)
const restricted = computed(() => isRestricted(accessStatus.value))
const recoveryActions = computed(() => recoveryActionsOf(accessStatus.value))
const readyAssets = computed(() => assets.value.filter(item => item.processing_state === 'ready'))
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

const specChanged = computed(() => !!project.value && (
  subject.value !== (project.value.spec.research_subject ?? '') ||
  goal.value !== (project.value.spec.research_goal ?? '')
))
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
    pendingDraft.value.reason === changeReason.value.trim() &&
    JSON.stringify(pendingDraft.value.chapterIds) === JSON.stringify([...selectedImpactChapterIds.value].sort())
})
const uncheckedChapterTitles = computed(() => chapters.value.filter(item => !selectedImpactChapterIds.value.includes(item.id)).map(item => item.title))
const citationUsagesChanged = computed(() => {
  const parsed = chapterCitations(bodyDraft.value)
  if (parsed.kind === 'malformed') return false
  const next = parsed.sourceIds.map(sourceId => citationUsageDrafts.value[sourceId] ?? { source_id: sourceId, purpose: '', limitation: '' })
  return JSON.stringify(next) !== JSON.stringify(workingCopy.value?.citation_usages ?? [])
})
const chapterChanged = computed(() => bodyChanged.value || citationUsagesChanged.value)
function changeSetStatusLabel(status: ChangeSet['status']): string {
  return ({ assessed: '生成预览', applied: '应用', rejected: '驳回', stale: '标记为过期' })[status]
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
    pendingChangeSet.value = result.data
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
    await selectProject(id, true, true)
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
    await selectProject(id, true, true)
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
const workingCopyDiffersFromFormal = computed(() => !!workingCopy.value && !!chapter.value && (
  workingCopy.value.body_markdown !== chapter.value.body_markdown ||
  JSON.stringify([...workingCopy.value.source_ids].sort()) !== JSON.stringify([...chapter.value.source_ids].sort()) ||
  JSON.stringify(workingCopy.value.review_items) !== JSON.stringify(chapter.value.review_items) ||
  JSON.stringify(workingCopy.value.citation_usages ?? []) !== JSON.stringify(chapter.value.citation_usages ?? []) ||
  workingCopy.value.base_chapter_version_id !== chapter.value.current_version_id
))
const displayedReviewItems = computed(() => workingCopy.value?.review_items ?? chapter.value?.review_items ?? [])
const workingCopyStatusLabel = computed(() => ({
  saved: '已保存', unsaved: '有待保存修改', saving: '保存中…', error: '保存失败，输入仍保留',
}[workingCopyStatus.value]))

// 正文里的来源标记：提取与体检一次算完。模板因此不必自己去拆这个联合类型。
const draftCitations = computed(() => {
  const parsed = chapterCitations(bodyDraft.value)
  return parsed.kind === 'ok'
    ? { sourceIds: parsed.sourceIds, malformed: false }
    : { sourceIds: [] as string[], malformed: true }
})
const reviewDecisionsReady = computed(() => !!chapter.value && chapter.value.review_items.every(item => {
  const decision = reviewDrafts.value[`${chapter.value!.id}:${item.id}`]
  return !!decision?.reason.trim()
}))

function reviewDraft(itemId: string) {
  const key = `${chapter.value?.id ?? ''}:${itemId}`
  return reviewDrafts.value[key] ??= { disposition: 'resolved', reason: '' }
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

async function loadChapterDraft(projectId: string, chapterId: string) {
  workingCopyLoading.value = true
  try {
    const [copyResult, versionResult] = await Promise.all([
      getWorkingCopy(projectId, chapterId), listChapterVersions(projectId, chapterId),
    ])
    if (project.value?.id !== projectId || chapter.value?.id !== chapterId) return
    workingCopy.value = copyResult.data
    hydrateCitationUsages(copyResult.data)
    chapterVersions.value = versionResult.data.items
    bodyDraft.value = copyResult.data.body_markdown
    workingCopyStatus.value = 'saved'
  } catch (error) {
    workingCopyStatus.value = 'error'
    failure(error)
  } finally {
    if (project.value?.id === projectId && chapter.value?.id === chapterId) workingCopyLoading.value = false
  }
}

function scheduleWorkingCopySave() {
  if (!workingCopy.value || !chapter.value || !project.value) return
  if (workingCopyTimer) clearTimeout(workingCopyTimer)
  if (!chapterChanged.value) {
    workingCopyStatus.value = 'saved'
    return
  }
  workingCopyStatus.value = 'unsaved'
  workingCopyTimer = setTimeout(() => {
    workingCopyTimer = undefined
    void saveText()
  }, 900)
}

async function create() {
  if (busy.value) return
  busy.value = true
  errorMessage.value = ''
  const name = newName.value.trim()
  const key = operationKey('create', { name, template_id: 'template-demo' })
  try {
    const result = await createProject(name, key)
    attempts.delete('create')
    newName.value = ''
    await loadProjects()
    await selectProject(result.data.id)
  } catch (error) { failure(error) }
  finally { busy.value = false }
}

async function selectProject(id: string, force = false, skipConfirm = false) {
  if (!skipConfirm && !force && ((specChanged.value && project.value?.id !== id) || chapterChanged.value || (project.value?.id !== id && templateCopyChanged.value)) &&
      !window.confirm('当前编辑尚未保存，确定切换项目吗？')) return
  if (!skipConfirm && force && (specChanged.value || chapterChanged.value || templateCopyChanged.value) &&
      !window.confirm('重新读取会丢弃当前未保存的输入，确定继续吗？')) return
  if (generationTimer) clearTimeout(generationTimer)
  if (workingCopyTimer) clearTimeout(workingCopyTimer)
  workingCopyTimer = undefined
  generationRun.value = null
  generationCandidate.value = null
  generationCandidates.value = []
  adoptionOpen.value = false
  errorMessage.value = ''
  pendingChangeSet.value = null
  pendingDraft.value = null
  pendingTemplateUpgrade.value = null
  templateUpgradeReason.value = ''
  try {
    const result = await getProject(id)
    project.value = result.data
    hydrateTemplateCopyDraft(result.data)
    subject.value = result.data.spec.research_subject ?? ''
    goal.value = result.data.spec.research_goal ?? ''
    const chapterResult = result.data.status === 'active' ? await listChapters(id) : null
    chapters.value = chapterResult?.data ?? []
    const assetResult = await listAssets(id)
    const changeSetsResult = await listChangeSets(id)
    const assessedTemplateUpgrade = [...(changeSetsResult.data ?? [])].reverse().find(item => item.status === 'assessed' && item.template_upgrade)
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
    // 检索结果、上一次的提问、被拒明细与绑定的提示都只属于**上一个项目**：
    // 留着它们，新项目一打开就会带着别人的结论（包括那句「暂无可定位来源」）。
    sources.value = []
    searchedQuery.value = ''
    deniedRows.value = []
    bindingNoticeText.value = ''
    closeSourceContext()
    chapter.value = chapters.value[0] ?? null
    workingCopy.value = null
    chapterVersions.value = []
    rewriteCandidate.value = null
    rewriteSourceIds.value = []
    bodyDraft.value = chapter.value?.body_markdown ?? ''
    if (chapter.value) {
      await loadChapterDraft(id, chapter.value.id)
      await loadSavedRewriteCandidate(id, chapter.value.id)
    }
    await loadGenerationCandidates(chapter.value?.id)
    await resumeGeneration()
  } catch (error) { failure(error) }
}

// 交付面板的写入都要交 expected_project_version，过期时它请这里重读一次。
//
// 只读项目、不动别的：走 selectProject 会重选章节、清掉未保存的正文草稿，而用户要的只是
// 把版本号更新到当前。协作编辑是这条路的现实来源——别人改了项目，版本就旧了。
async function refreshProjectVersion() {
  if (!project.value) return
  try {
    const result = await getProject(project.value.id)
    project.value = result.data
  } catch (error) { failure(error) }
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
    const result = await retrieveSources(project.value.id, query, assets.value.map(item => item.id))
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
  const expected = project.value.spec_revision
  const fields = { ...project.value.spec, research_subject: subject.value, research_goal: goal.value }
  const key = operationKey(`spec:${id}`, { expected_spec_revision: expected, fields })
  try {
    const result = await saveSpec(id, expected, fields, key)
    attempts.delete(`spec:${id}`)
    project.value = result.data
    await loadProjects()
    if (result.meta.refresh_required) await selectProject(id)
  } catch (error) { failure(error) }
  finally { busy.value = false }
}

async function createConditionsChangePreview() {
  if (!project.value || busy.value || !specChanged.value || project.value.status !== 'active') return
  busy.value = true
  errorMessage.value = ''
  const id = project.value.id
  const fields: Record<string, { old_value: string; new_value: string }> = {}
  const oldSubject = project.value.spec.research_subject ?? ''
  const oldGoal = project.value.spec.research_goal ?? ''
  if (subject.value !== oldSubject) fields.research_subject = { old_value: oldSubject, new_value: subject.value }
  if (goal.value !== oldGoal) fields.research_goal = { old_value: oldGoal, new_value: goal.value }
  const input = {
    expected_context_revision: project.value.current_context_revision,
    fields,
    affected_chapter_ids: [...selectedImpactChapterIds.value],
    reason: changeReason.value.trim(),
  }
    const createKey = operationKey(`change-set-create:${id}`, input)
  try {
    const created = await createChangeSet(id, input, createKey)
    pendingChangeSet.value = created.data
    pendingDraft.value = { subject: subject.value, goal: goal.value, reason: changeReason.value.trim(), chapterIds: [...selectedImpactChapterIds.value].sort() }
  } catch (error) { failure(error) }
  finally { busy.value = false }
}

async function applyConditionsChange() {
  if (!project.value || busy.value || !pendingChangeSet.value || !pendingDraftMatches.value) return
  busy.value = true
  errorMessage.value = ''
  const id = project.value.id
  const changeSetId = pendingChangeSet.value.id
  const applyKey = operationKey(`change-set-apply:${id}:${changeSetId}`, { change_set_id: changeSetId })
  try {
    const applied = await applyChangeSet(id, changeSetId, applyKey)
    attempts.delete(`change-set-create:${id}`)
    attempts.delete(`change-set-apply:${id}:${changeSetId}`)
    pendingChangeSet.value = null
    pendingDraft.value = null
    await selectProject(id)
    lastChangeSet.value = applied.data
  } catch (error) {
    const item = error as { status?: number }
    if (item?.status === 409) {
      try {
        const current = await getChangeSet(id, changeSetId)
        if (current.data.status === 'stale') {
          pendingChangeSet.value = null
          pendingDraft.value = null
          lastChangeSet.value = current.data
        }
      } catch (readError) {
        failure(readError)
        return
      }
    }
    failure(error)
  }
  finally { busy.value = false }
}

async function rejectConditionsChange() {
  if (!project.value || busy.value || !pendingChangeSet.value) return
  busy.value = true
  errorMessage.value = ''
  const id = project.value.id
  const changeSetId = pendingChangeSet.value.id
  const rejectKey = operationKey(`change-set-reject:${id}:${changeSetId}`, { change_set_id: changeSetId })
  try {
    const rejected = await rejectChangeSet(id, changeSetId, rejectKey)
    attempts.delete(`change-set-create:${id}`)
    attempts.delete(`change-set-reject:${id}:${changeSetId}`)
    pendingChangeSet.value = null
    pendingDraft.value = null
    await selectProject(id)
    lastChangeSet.value = rejected.data
  } catch (error) { failure(error) }
  finally { busy.value = false }
}

async function activate() {
  if (!project.value || busy.value || specChanged.value) return
  busy.value = true
  errorMessage.value = ''
  const id = project.value.id
  const expected = project.value.spec_revision
  const key = operationKey(`activate:${id}`, { expected_spec_revision: expected })
  try {
    await activateProject(id, expected, key, project.value.project_version)
    attempts.delete(`activate:${id}`)
    await loadProjects()
    await selectProject(id)
  } catch (error) { failure(error) }
  finally { busy.value = false }
}

function selectChapter(item: Chapter) {
  if (chapterChanged.value && !window.confirm('当前章节尚未保存，确定切换吗？')) return
  if (workingCopyTimer) clearTimeout(workingCopyTimer)
  workingCopyTimer = undefined
  if (generationTimer) clearTimeout(generationTimer)
  generationTimer = undefined
  chapter.value = item
  workingCopy.value = null
  chapterVersions.value = []
  rewriteCandidate.value = null
  rewriteSourceIds.value = []
  bodyDraft.value = item.body_markdown
  hydrateCitationUsages(item)
  errorMessage.value = ''
  generationRun.value = null
  generationCandidate.value = null
  generationCandidates.value = []
  // 对话框挂着上一个章节的幂等键与失败文案时，props 会被换成新的章节而组件不会重建
  //（同一位置、同一类型）。关掉它，保证每次打开都是干净的一份。
  adoptionOpen.value = false
  if (project.value) void loadChapterDraft(project.value.id, item.id)
  if (project.value) void loadSavedRewriteCandidate(project.value.id, item.id)
  void loadGenerationCandidates(item.id)
  void resumeGeneration()
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
    if (key.startsWith(`${chapterId}:`)) delete reviewDrafts.value[key]
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
  } catch (error) { failure(error) }
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
  if (!project.value || !chapter.value?.current_version_id || busy.value || workingCopySaving.value || chapterChanged.value || workingCopyDiffersFromFormal.value || !reviewDecisionsReady.value) return
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
    attempts.delete(`confirm:${current.id}`)
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
  if (!project.value || !chapter.value || busy.value || !selectedAssetIds.value.length || !generationInstruction.value.trim()) return
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
    asset_ids: [...selectedAssetIds.value].sort(),
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
    body_markdown: bodyDraft.value,
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

async function requestSelectedRewrite() {
  if (!project.value || !chapter.value || !workingCopy.value || rewriteBusy.value || busy.value) return
  const editor = document.getElementById('chapter-body') as HTMLTextAreaElement | null
  const start = editor?.selectionStart ?? -1
  const end = editor?.selectionEnd ?? -1
  if (start < 0 || end <= start) {
    errorMessage.value = '请先在正文编辑框中选中要改写的文字。'
    return
  }
  const selection = { start_utf16: start, end_utf16: end, selected_text: bodyDraft.value.slice(start, end) }
  if (!selection.selected_text.trim()) {
    errorMessage.value = '选中的内容为空白，请选择一段正文。'
    return
  }
  if (!rewriteInstruction.value.trim()) {
    errorMessage.value = '请填写改写要求。'
    return
  }
  const projectId = project.value.id
  const chapterId = chapter.value.id
  if (chapterChanged.value) {
    if (workingCopyTimer) clearTimeout(workingCopyTimer)
    workingCopyTimer = undefined
    await saveText()
    if (chapterChanged.value || workingCopyStatus.value === 'error') return
  }
  if (!workingCopy.value || !project.value || project.value.id !== projectId || chapter.value?.id !== chapterId) return
  const input = {
    base_chapter_version_id: workingCopy.value.base_chapter_version_id,
    expected_spec_revision: project.value.spec_revision,
    expected_working_copy_revision: workingCopy.value.working_copy_revision,
    selection,
    instruction: rewriteInstruction.value.trim(),
    source_ids: [...rewriteSourceIds.value].sort(),
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
  const candidate = rewriteCandidate.value
  if (candidate.status !== 'ready' && candidate.status !== 'applying') return
  if (candidate.status !== 'applying' && workingCopy.value.working_copy_revision !== candidate.working_copy_revision) return
  if (!window.confirm('只把候选替换到所选文字，并保存为工作副本；不会创建正式版本。确定接受吗？')) return
  const projectId = project.value.id
  const chapterId = chapter.value.id
  const input = {
    expected_spec_revision: candidate.spec_revision,
    expected_working_copy_revision: candidate.working_copy_revision,
    base_chapter_version_id: candidate.base_chapter_version_id,
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
    if (result.meta.refresh_required) await loadChapterDraft(projectId, chapterId)
  } catch (error) { failure(error) }
  finally { rewriteBusy.value = false }
}

function rejectRewriteCandidate() {
  clearRewriteCandidate()
}

async function commitDraft() {
  if (!project.value || !chapter.value || !workingCopy.value || busy.value || workingCopySaving.value || chapterChanged.value) return
  if (!workingCopyDiffersFromFormal.value) return
  if (!window.confirm('将当前工作副本提交为新的正式章节版本？提交后需要重新确认本章。')) return
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
    clearRewriteCandidate(projectId, chapterId)
  } catch (error) { failure(error) }
  finally { busy.value = false }
}

onMounted(loadProjects)
onUnmounted(() => {
  if (generationTimer) clearTimeout(generationTimer)
  if (workingCopyTimer) clearTimeout(workingCopyTimer)
})
</script>

<style scoped>
.workspace-head__actions { display: flex; gap: 10px; align-items: center; }
.evidence-link { display: inline-flex; align-items: center; padding: 8px 12px; border: 1px solid #0b8c91; border-radius: 7px; color: #0b6e71; background: #e5f6f2; text-decoration: none; }
.evidence-link:hover { background: #d5f0ea; }
.lingdoc-workspace { max-width: 1200px; margin: 0 auto; padding: 32px; color: #24342e; }
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
.candidate-preview pre { white-space: pre-wrap; overflow-wrap: anywhere; font: inherit; }
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
</style>
