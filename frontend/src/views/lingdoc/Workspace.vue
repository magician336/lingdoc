<template>
  <main class="lingdoc-workspace">
    <header class="workspace-head">
      <div>
        <h1>灵档项目</h1>
        <p>当前使用两章演示模板，内容仅供团队验证流程。</p>
      </div>
      <button type="button" @click="loadProjects" :disabled="loading">刷新项目</button>
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
            <textarea id="generation-instruction" v-model="generationInstruct