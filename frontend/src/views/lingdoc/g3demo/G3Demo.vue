<template>
  <main class="g3-console">
    <header class="console-hero">
      <div class="hero-copy">
        <p class="eyebrow">LINGDOC / G3 ACCEPTANCE CONSOLE</p>
        <h1>G3 验收台</h1>
        <p class="hero-lede">
          把 G3 的验收判据、Spec 锚点、状态徽标与证据位放在同一张台面上。
          左栏是主流程的分步骨架，右栏是判据矩阵——判据原文照录主基准，状态只用仓库门禁词表。
          真实模式会自建账号与隔离项目，把每一步真实调用（方法、路径、状态码、幂等键、耗时）留在左栏。
        </p>
      </div>
      <div class="hero-actions">
        <span class="demo-chip" :class="{ 'demo-chip--live': liveMode }">
          {{ liveMode ? '真实模式 · 本次运行' : '无需登录 · 免登录快照模式' }}
        </span>
        <span class="snapshot-chip">
          <i aria-hidden="true"></i>{{ snapshotNotice(snapshot) }}
        </span>
      </div>
    </header>

    <section class="identity-card" aria-labelledby="identity-title">
      <div class="identity-main">
        <div class="identity-mark" aria-hidden="true">G3</div>
        <div>
          <p class="section-kicker">当前请求身份</p>
          <h2 id="identity-title">
            {{ liveMode ? liveIdentityTitle : '匿名访客（未登录）' }}
          </h2>
          <p class="identity-meta">
            {{
              liveMode
                ? '真实模式：令牌写入前端既有认证存储（weknora_token），请求拦截器统一附加 Authorization 与 X-Tenant-ID。'
                : '免登录快照模式：页面不发起任何真实请求，也不读取当前会话的令牌。'
            }}
          </p>
        </div>
      </div>
      <div class="identity-facts">
        <div v-if="liveMode">
          <span>演示账号</span>
          <strong>{{ liveAccountEmail || '—' }}</strong>
          <small>运行时自建：随机用户名与随机口令，不依赖预置账号</small>
        </div>
        <div v-else>
          <span>租户</span>
          <strong>{{ snapshot.identity.tenantId }} · {{ snapshot.identity.tenantName }}</strong>
          <small>历史快照里的租户，不是当前会话的租户</small>
        </div>
        <div v-if="liveMode">
          <span>本次运行</span>
          <strong>{{ liveRunId || '—' }}</strong>
          <small>运行标识：幂等键由它派生，两次运行不会撞键</small>
        </div>
        <div v-else>
          <span>数据模式</span>
          <strong>{{ snapshot.mode }}</strong>
          <small>只读快照，不写入、不重放</small>
        </div>
        <div>
          <span>{{ liveMode ? '本次后端 head' : '快照 head' }}</span>
          <strong>{{ liveHead?.commitId || snapshot.head }}</strong>
          <small>
            {{
              liveMode
                ? `编译期注入的 CommitID${liveHead?.buildTime ? ' · ' + liveHead.buildTime : ''}`
                : '后端进程 commit ' + snapshot.runtimeCommit
            }}
          </small>
        </div>
      </div>
    </section>

    <section class="live-card" aria-labelledby="live-title">
      <div class="section-heading">
        <div>
          <p class="section-kicker">真实模式</p>
          <h2 id="live-title">自建账号 · 自建隔离项目 · 真实调用留痕</h2>
        </div>
        <span class="section-note">复用既有认证存储与请求拦截器，不新增平行鉴权</span>
      </div>

      <p class="live-lede">
        真实模式在运行时**自建**演示账号（随机用户名 + 随机口令，不依赖预置账号、不接受手工填入的固定口令），
        用演示模板 <code>template-demo</code> 新建一个只属于本次运行的隔离项目，
        走到「登记立项阅读 → 激活 → 两个章节就绪」。
        每一次运行都新建项目；演示账号本身会存到本地，可以跨运行复用。
      </p>

      <div class="live-actions">
        <button class="live-button live-button--primary" :disabled="running" @click="startRun(true)">
          {{ running ? '运行中…' : '创建演示账号并运行' }}
        </button>
        <button
          class="live-button"
          :disabled="running || !savedAccount"
          :title="savedAccount ? `登录入口：${savedAccount.email}` : '本地还没有保存过演示账号'"
          @click="startRun(false)"
        >
          用已保存的账号登录并运行
        </button>
        <button v-if="sessionTakenOver" class="live-button live-button--ghost" @click="releaseSession">
          退出演示账号
        </button>
      </div>

      <dl class="fact-list live-facts">
        <div>
          <dt>本轮运行标识</dt>
          <dd>{{ liveRunId || '尚未运行' }}</dd>
        </div>
        <div>
          <dt>演示账号</dt>
          <dd>{{ liveAccountEmail || '尚未创建' }}</dd>
        </div>
        <div>
          <dt>隔离项目</dt>
          <dd>{{ liveProjectId ? `${liveProjectName} · ${liveProjectId}` : '尚未创建' }}</dd>
        </div>
        <div>
          <dt>激活后的章节</dt>
          <dd>
            {{
              liveChapters.length
                ? liveChapters.map((chapter) => `${chapter.sectionId}（${chapter.title}）`).join('、')
                : '尚未就绪'
            }}
          </dd>
        </div>
        <div>
          <dt>本次后端 head</dt>
          <dd>{{ liveHead?.commitId || '未读到' }}</dd>
        </div>
        <div>
          <dt>上一次运行 head</dt>
          <dd>{{ previousHead?.commitId || '无上一次运行' }}</dd>
        </div>
      </dl>

      <p v-if="runError" class="live-error" role="alert">
        <strong>运行失败：</strong>{{ runError }}
      </p>
      <p v-if="staleNotice" class="live-warn" role="status">{{ staleNotice }}</p>
      <p v-if="missingSections.length" class="live-warn" role="status">
        模板必需章节还没就位：{{ missingSections.join('、') }}。
      </p>
      <p v-if="sessionTakenOver" class="card-footnote">
        真实模式会接管当前浏览器的登录令牌（复用既有认证存储 <code>weknora_token</code>），
        跑完可以点「退出演示账号」把原来的会话还回去。
      </p>
    </section>

    <section class="lifecycle-card" aria-labelledby="lifecycle-title">
      <div class="section-heading">
        <div>
          <p class="section-kicker">变更集生命周期</p>
          <h2 id="lifecycle-title">创建 → 读影响评估 → Owner 应用 → 受影响章节重新确认</h2>
        </div>
        <span class="section-note">页面与 node 脚本跑同一份执行器</span>
      </div>

      <p class="live-lede">
        这一段在真实模式里把变更集生命周期跑完：先建两个**同一基线**的变更集，
        应用其中一个（另一个会因基线推进落为陈旧），再复写受影响章节并按新规格修订重新确认，
        最后用**新建的逆向变更集**完成撤销。非 Owner 探测会另起一个同租户成员，
        让它调用应用与拒绝，按仓库错误码原样呈现。
      </p>

      <div class="live-actions">
        <button
          class="live-button live-button--primary"
          :disabled="lifecycleRunning || running"
          @click="startLifecycle"
        >
          {{ lifecycleRunning ? '运行中…' : '跑通变更集生命周期' }}
        </button>
        <span class="section-note">
          {{
            lifecycle
              ? '本轮已跑完：四张判定卡 + 变更集、章节确认、影响评估与权限探测'
              : '尚未运行；未运行时下面不显示任何证据位'
          }}
        </span>
      </div>

      <p v-if="lifecycleError" class="live-error" role="alert">
        <strong>运行失败：</strong>{{ lifecycleError }}
      </p>

      <template v-if="lifecycle">
        <div class="verdict-grid">
          <article
            v-for="verdict in lifecycle.verdicts"
            :key="verdict.id"
            class="verdict-card"
            :class="verdictCardClass(verdict)"
          >
            <header>
              <strong>{{ verdict.id }} · {{ verdict.title }}</strong>
              <span
                class="status-badge"
                :class="`status-badge--${statusTone(verdictStatus(verdict))}`"
              >
                {{ verdictStatus(verdict) }}
              </span>
            </header>
            <p class="verdict-detail">{{ verdict.detail }}</p>
            <ul class="verdict-evidence">
              <li v-for="(line, index) in verdict.evidence" :key="index"><code>{{ line }}</code></li>
            </ul>
          </article>
        </div>

        <h3 class="matrix-group-title">变更集</h3>
        <div class="matrix-scroll">
          <table class="g3-table g3-table--compact">
            <thead>
              <tr><th>变更集</th><th>状态</th><th>基线 → 目标修订</th><th>受影响章节</th><th>血缘</th></tr>
            </thead>
            <tbody>
              <tr v-for="row in changeSetRows" :key="row.id">
                <th scope="row">
                  <code>{{ row.id }}</code>
                  <small>{{ row.label }}</small>
                </th>
                <td>
                  <span class="status-badge" :class="changeSetTone(row.status)">{{ row.status }}</span>
                  <small v-if="row.staleReason">stale_reason={{ row.staleReason }}</small>
                </td>
                <td>{{ row.revisions }}</td>
                <td>{{ row.affected }}</td>
                <td>{{ row.lineage }}</td>
              </tr>
            </tbody>
          </table>
        </div>

        <h3 class="matrix-group-title">章节确认：失效 → 重新确认</h3>
        <div class="matrix-scroll">
          <table class="g3-table g3-table--compact">
            <thead>
              <tr><th>章节</th><th>应用前</th><th>应用后</th><th>步骤</th></tr>
            </thead>
            <tbody>
              <tr v-for="row in chapterRows" :key="row.id">
                <th scope="row">
                  <strong>{{ row.title }}</strong>
                  <small>{{ row.sectionId }} · {{ row.id }}</small>
                </th>
                <td>
                  <span class="fact-line">版本级确认标记 {{ row.beforeVersion }}</span>
                  <span class="fact-line">当前确认 {{ row.beforeConfirmation }}</span>
                </td>
                <td>
                  <span class="fact-line">版本级确认标记 {{ row.afterVersion }}</span>
                  <span class="fact-line">当前确认 {{ row.afterConfirmation }}</span>
                </td>
                <td>
                  <ol class="mini-steps">
                    <li v-for="step in row.steps" :key="step.title" :class="step.tone">
                      <strong>{{ step.title }}</strong>
                      <span>{{ step.text }}</span>
                    </li>
                  </ol>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <p class="card-footnote">
          「版本级确认标记」取自 <code>GET /chapters/{id}/versions</code> 的 <code>confirmation_valid</code>：
          只有被本次变更集列入失效范围的章节才会变 false。
          「当前确认」取自 <code>GET /chapters/{id}/confirmations/current</code>：
          服务端在规格修订推进后，对**所有**章节的当前确认都判为失效（确认绑定的规格修订落后），
          页面如实呈现这个口径差异。
        </p>

        <h3 class="matrix-group-title">影响评估预览</h3>
        <div class="matrix-scroll">
          <table class="g3-table g3-table--compact">
            <thead>
              <tr><th>影响对象</th><th>动作</th><th>确定性</th><th>来源与理由</th></tr>
            </thead>
            <tbody>
              <tr v-for="(impact, index) in lifecycle.assessment.impacts" :key="index">
                <th scope="row">
                  <code>{{ impact.target.kind }}/{{ impact.target.id }}</code>
                  <small>版本 {{ impact.target.version }}</small>
                </th>
                <td>{{ impact.action }}</td>
                <td>
                  <span class="status-badge" :class="impact.certainty === 'definite' ? 'status-badge--pass' : 'status-badge--warning'">
                    {{ impact.certainty === 'definite' ? '确定影响' : '可能影响，等待人工判断' }}
                  </span>
                </td>
                <td>
                  <span v-for="(cause, causeIndex) in impact.causes" :key="causeIndex" class="fact-line">
                    <code>{{ cause.source.kind }}/{{ cause.source.id }}</code> · {{ cause.reason }}
                  </span>
                </td>
              </tr>
              <tr v-if="!lifecycle.assessment.impacts.length">
                <td colspan="4">影响评估里没有影响条目。</td>
              </tr>
            </tbody>
          </table>
        </div>
        <p class="card-footnote">
          受影响对象（服务端 <code>assessment.objects</code>）：
          <code v-for="object in lifecycle.assessment.objects" :key="object.id">
            {{ object.kind }}/{{ object.id }} · {{ object.state }} · confirmed={{ object.confirmed }}
          </code>
        </p>
        <p class="card-footnote">
          <strong>未受影响理由：</strong>{{ lifecycle.unaffected.text }}
          <template v-if="!lifecycle.unaffected.serverReason">
            （服务端没有给出未受影响理由，这里只陈述从响应读到的观察事实。）
          </template>
        </p>

        <h3 class="matrix-group-title">非 Owner 权限探测</h3>
        <div v-if="lifecycle.permissionProbe" class="matrix-scroll">
          <table class="g3-table g3-table--compact">
            <thead>
              <tr><th>调用</th><th>状态码</th><th>错误码</th></tr>
            </thead>
            <tbody>
              <tr v-for="row in permissionRows" :key="row.label">
                <th scope="row">{{ row.label }}</th>
                <td>HTTP {{ row.status }}</td>
                <td><code>{{ row.code || '（成功，无错误码）' }}</code></td>
              </tr>
            </tbody>
          </table>
          <p class="card-footnote">
            非 Owner 是同租户、同项目的成员（治理角色 admin）：它**能读到**变更集，
            但调用应用与拒绝都被服务端按仓库错误码拒掉——服务端对「不是 Owner」统一回 404，
            不泄露资源存在性。
          </p>
        </div>
        <p v-else class="card-footnote">本轮没有跑非 Owner 探测。</p>
      </template>
    </section>

    <section class="lifecycle-card" aria-labelledby="dependency-title">
      <div class="section-heading">
        <div>
          <p class="section-kicker">依赖图与影响任务</p>
          <h2 id="dependency-title">建边 → 裁决 → 三组呈现 → 指派 → 闭环</h2>
        </div>
        <span class="section-note">页面与 node 脚本跑同一份执行器</span>
      </div>

      <p class="live-lede">
        这一段在真实模式里把依赖图与影响任务跑完：先建立**已确认**与**建议**两类依赖边，
        对建议边做确认 / 修改 / 拒绝裁决；再读影响评估，按**服务端返回的确定性**分成
        「确定受影响 / 可能受影响 / 当前未发现影响」三组（页面不自行重算）；
        最后处置影响任务——未指派不能直接完成（服务端回 404），指派给当前操作者后
        以真实闭环证据完成，非阻断任务可「接受本次不处理」，
        依据版本被替换的任务落为失效（终态，但不算闭环）。
      </p>

      <div class="live-actions">
        <button
          class="live-button live-button--primary"
          :disabled="dependencyRunning || running"
          @click="startDependency"
        >
          {{ dependencyRunning ? '运行中…' : '跑通依赖图与影响任务' }}
        </button>
        <span class="section-note">
          {{
            dependency
              ? '本轮已跑完：三组呈现 + 依赖边 + 任务状态迁移（含已接受不处理与失效任务）'
              : '尚未运行；未运行时下面不显示任何证据位'
          }}
        </span>
      </div>

      <p v-if="dependencyError" class="live-error" role="alert">
        <strong>运行失败：</strong>{{ dependencyError }}
      </p>

      <template v-if="dependency">
        <div class="verdict-grid">
          <article
            v-for="verdict in dependency.verdicts"
            :key="verdict.id"
            class="verdict-card"
            :class="verdictCardClass(verdict)"
          >
            <header>
              <strong>{{ verdict.id }} · {{ verdict.title }}</strong>
              <span
                class="status-badge"
                :class="`status-badge--${statusTone(verdictStatus(verdict))}`"
              >
                {{ verdictStatus(verdict) }}
              </span>
            </header>
            <p class="verdict-detail">{{ verdict.detail }}</p>
            <ul class="verdict-evidence">
              <li v-for="(line, index) in verdict.evidence" :key="index"><code>{{ line }}</code></li>
            </ul>
          </article>
        </div>

        <h3 class="matrix-group-title">影响预览分组（确定性完全来自服务端 certainty）</h3>
        <div class="matrix-scroll">
          <table class="g3-table g3-table--compact">
            <thead>
              <tr><th>分组</th><th>对象 / 目标</th><th>动作</th><th>来源对象与原因</th></tr>
            </thead>
            <tbody>
              <tr v-for="entry in dependency.grouping.definite" :key="`definite-${entry.target.id}-${entry.action}`">
                <th scope="row">
                  <span class="status-badge status-badge--pass">确定受影响</span>
                </th>
                <td>
                  <code>{{ entry.target.kind }}/{{ entry.target.id }}</code>
                  <small>版本 {{ entry.target.version }} · certainty={{ entry.certainty }}</small>
                </td>
                <td>{{ entry.action }}</td>
                <td>
                  <span v-for="(cause, causeIndex) in entry.causes" :key="causeIndex" class="fact-line">
                    <code>{{ cause.source.kind }}/{{ cause.source.id }}</code> · {{ cause.reason }}
                  </span>
                </td>
              </tr>
              <tr v-for="entry in dependency.grouping.possible" :key="`possible-${entry.target.id}-${entry.action}`">
                <th scope="row">
                  <span class="status-badge status-badge--warning">可能受影响</span>
                </th>
                <td>
                  <code>{{ entry.target.kind }}/{{ entry.target.id }}</code>
                  <small>版本 {{ entry.target.version }} · certainty={{ entry.certainty }}</small>
                </td>
                <td>{{ entry.action }}</td>
                <td>
                  <span v-for="(cause, causeIndex) in entry.causes" :key="causeIndex" class="fact-line">
                    <code>{{ cause.source.kind }}/{{ cause.source.id }}</code> · {{ cause.reason }}
                  </span>
                </td>
              </tr>
              <tr v-for="object in dependency.grouping.unaffected" :key="`unaffected-${object.id}`">
                <th scope="row">
                  <span class="status-badge status-badge--not-run">当前未发现影响</span>
                </th>
                <td>
                  <code>{{ object.kind }}/{{ object.id }}</code>
                  <small>版本 {{ object.version }}</small>
                </td>
                <td>—</td>
                <td>既不在 assessment.objects，也不在 impacts 里</td>
              </tr>
              <tr v-if="!dependency.grouping.definite.length && !dependency.grouping.possible.length && !dependency.grouping.unaffected.length">
                <td colspan="4">本轮评估里没有可分组的对象。</td>
              </tr>
            </tbody>
          </table>
        </div>
        <p class="card-footnote">
          <strong>确定影响对应的必需对象集合</strong>（服务端 <code>assessment.objects</code>）：
          <code v-for="object in dependency.grouping.requiredObjects" :key="object.id">
            {{ object.kind }}/{{ object.id }} · {{ object.state }} · confirmed={{ object.confirmed }}
          </code>
        </p>
        <p class="card-footnote">
          分组口径：确定性与可能性**完全采用服务端返回的 <code>impacts[].certainty</code>**，
          页面不自行重算；来源对象取自 <code>impacts[].causes[].source</code>。
          这里的分组由验收台自己读原始响应渲染，**没有改动**工作台影响面板的既有渲染。
        </p>

        <h3 class="matrix-group-title">依赖边</h3>
        <div class="matrix-scroll">
          <table class="g3-table g3-table--compact">
            <thead>
              <tr><th>边</th><th>状态 / 来源</th><th>来源对象</th><th>目标对象</th><th>动作</th><th>理由</th></tr>
            </thead>
            <tbody>
              <tr v-for="row in dependencyEdgeRows" :key="row.id">
                <th scope="row">
                  <code>{{ row.relation }}</code>
                  <small>{{ row.id }} · v{{ row.version }}</small>
                </th>
                <td>
                  <span class="status-badge" :class="row.status === 'confirmed' ? 'status-badge--pass' : (row.status === 'suggested' ? 'status-badge--warning' : 'status-badge--not-run')">
                    {{ row.status }}
                  </span>
                  <small>origin={{ row.origin }}</small>
                  <small v-if="row.superseded">修改自 {{ row.superseded }}</small>
                </td>
                <td><code>{{ row.source }}</code></td>
                <td><code>{{ row.target }}</code></td>
                <td>{{ row.action }}</td>
                <td>{{ row.reason }}</td>
              </tr>
              <tr v-if="!dependencyEdgeRows.length"><td colspan="6">本轮没有读到依赖边。</td></tr>
            </tbody>
          </table>
        </div>
        <p class="card-footnote">
          已确认边（<code>origin=user</code>）进入影响解析；建议边（<code>origin=ai</code>，
          <code>status=suggested</code>）只产生「可能受影响」；被拒绝的候选在同一版本里再投一次
          服务端不会新建边（<code>edge_id={{ dependency.decisions.reproposed.id }}</code>）。
        </p>

        <h3 class="matrix-group-title">影响任务：状态迁移</h3>
        <div class="matrix-scroll">
          <table class="g3-table g3-table--compact">
            <thead>
              <tr><th>任务</th><th>处置</th><th>状态迁移</th><th>闭环证据</th><th>说明</th></tr>
            </thead>
            <tbody>
              <tr v-for="row in dependencyTaskRows" :key="`${row.id}-${row.action}-${row.status}`">
                <th scope="row">
                  <code>{{ row.id }}</code>
                  <small>severity={{ row.severity }}</small>
                </th>
                <td>{{ row.action }}</td>
                <td>
                  <span class="status-badge" :class="row.to === 'DONE' ? 'status-badge--pass' : (row.to === 'DISMISSED' ? 'status-badge--warning' : (row.to === 'STALE' ? 'status-badge--not-run' : 'status-badge--todo'))">
                    {{ row.from }} → {{ row.to }}
                  </span>
                  <small>HTTP {{ row.status }}<template v-if="row.code"> · {{ row.code }}</template></small>
                </td>
                <td><code v-if="row.evidence">{{ row.evidence }}</code><span v-else>—</span></td>
                <td>{{ row.note }}</td>
              </tr>
              <tr v-if="!dependencyTaskRows.length"><td colspan="5">本轮没有任务处置记录。</td></tr>
            </tbody>
          </table>
        </div>
        <p class="card-footnote">
          未指派就完成被服务端按
          <strong>HTTP {{ dependency.unassignedCompletion.status }}</strong>
          （错误码 <code>{{ dependency.unassignedCompletion.code || '（无）' }}</code>）拒掉——
          服务端对「任务没有指派给你」回 404，不回 403。
          闭环只有 <code>DONE</code> 与 <code>DISMISSED</code>；<code>STALE</code> 是终态但**不算闭环**。
        </p>

        <h3 class="matrix-group-title">影响任务终态</h3>
        <div class="matrix-scroll">
          <table class="g3-table g3-table--compact">
            <thead>
              <tr><th>任务</th><th>目标</th><th>动作 / 严重度 / 责任域</th><th>状态</th><th>上游原因</th><th>处置说明</th></tr>
            </thead>
            <tbody>
              <tr v-for="row in dependencyFinalTasks" :key="row.id">
                <th scope="row"><code>{{ row.id }}</code></th>
                <td><code>{{ row.target }}</code></td>
                <td>{{ row.action }} · {{ row.severity }} · {{ row.domain }}</td>
                <td>
                  <span class="status-badge" :class="row.status === 'DONE' ? 'status-badge--pass' : (row.status === 'DISMISSED' ? 'status-badge--warning' : (row.status === 'STALE' ? 'status-badge--not-run' : 'status-badge--todo'))">
                    {{ row.status }}
                  </span>
                  <small v-if="row.staleReason">stale_reason={{ row.staleReason }}</small>
                </td>
                <td>{{ row.causes }} 条</td>
                <td>{{ row.disposition || '—' }}</td>
              </tr>
              <tr v-if="!dependencyFinalTasks.length"><td colspan="6">本轮没有影响任务。</td></tr>
            </tbody>
          </table>
        </div>
        <p class="card-footnote">
          这张表是**应用之后**读回的任务快照（<code>GET /impact-tasks</code>），
          状态迁移过程见上一张表。严重度由服务端按动作推导，页面不自行指定。
        </p>
      </template>
    </section>

    <section class="lifecycle-card" aria-labelledby="stabilization-title">
      <div class="section-heading">
        <div>
          <p class="section-kicker">稳定化与交付闭环</p>
          <h2 id="stabilization-title">阻断影响闭环 → 稳定化 → 交付检查 → 冻结 → 导出 → 下载核对</h2>
        </div>
        <span class="section-note">页面与 node 脚本跑同一份执行器</span>
      </div>

      <p class="live-lede">
        这一段在真实模式里把交付侧走完：先制造一次**未闭环的阻断影响**，
        稳定化此时以 <strong>200 成功响应</strong>返回 <code>BLOCKED</code> 并带出**完整阻断原因**
        （不是一句失败）；此时冻结交付快照也会被服务端拒掉。
        随后把全部阻断影响按「指派 → 开始 → 以闭环证据完成」闭环，
        再次稳定化转为<strong>已稳定</strong>，再走交付检查 → 冻结快照 → 发起导出 → 轮询产物 → 下载核对。
        判定导出成功只看<strong>产物的最终状态与文件哈希</strong>，不看受理接口的 202。
      </p>

      <div class="live-actions">
        <button
          class="live-button live-button--primary"
          :disabled="stabilizationRunning || running"
          @click="startStabilization"
        >
          {{ stabilizationRunning ? '运行中…' : '跑通稳定化与交付闭环' }}
        </button>
        <span class="section-note">
          {{
            stabilization
              ? '本轮已跑完：阻断态判定 + 已稳定 + 交付检查 + 快照 + 产物 + 下载核对'
              : '尚未运行；未运行时下面不显示任何证据位'
          }}
        </span>
      </div>

      <p class="live-warn" role="status">
        <strong>导出前请先确认：</strong>{{ DOCX_BODY_HEADING_HINT }}
      </p>

      <p v-if="stabilizationError" class="live-error" role="alert">
        <strong>运行失败：</strong>{{ stabilizationError }}
      </p>

      <template v-if="stabilization">
        <div class="verdict-grid">
          <article
            v-for="verdict in stabilization.verdicts"
            :key="verdict.id"
            class="verdict-card"
            :class="verdictCardClass(verdict)"
          >
            <header>
              <strong>{{ verdict.id }} · {{ verdict.title }}</strong>
              <span
                class="status-badge"
                :class="`status-badge--${statusTone(verdictStatus(verdict))}`"
              >
                {{ verdictStatus(verdict) }}
              </span>
            </header>
            <p class="verdict-detail">{{ verdict.detail }}</p>
            <ul class="verdict-evidence">
              <li v-for="(line, index) in verdict.evidence" :key="index"><code>{{ line }}</code></li>
            </ul>
          </article>
        </div>

        <h3 class="matrix-group-title">稳定化判定：阻断态 → 已稳定</h3>
        <div class="matrix-scroll">
          <table class="g3-table g3-table--compact">
            <thead>
              <tr><th>阶段</th><th>状态</th><th>检查时刻</th><th>阻断原因</th><th>失效任务</th></tr>
            </thead>
            <tbody>
              <tr>
                <th scope="row">
                  <strong>阻断影响未闭环</strong>
                  <small>稳定化是判定，不是错误</small>
                </th>
                <td>
                  <span class="status-badge status-badge--warning">{{ stabilization.blocked.status }}</span>
                  <small>上下文修订 {{ stabilization.blocked.contextRevision }}</small>
                </td>
                <td>{{ stabilization.blocked.checkedAt || '（服务端未给）' }}</td>
                <td>
                  <span v-for="(line, index) in stabilization.blockedLines" :key="index" class="fact-line">
                    {{ line }}
                  </span>
                  <span v-if="!stabilization.blockedLines.length">（服务端没有给出阻断原因）</span>
                </td>
                <td>
                  <code v-for="id in stabilization.blocked.staleTaskIds" :key="id">{{ id }}</code>
                  <span v-if="!stabilization.blocked.staleTaskIds.length">—</span>
                </td>
              </tr>
              <tr>
                <th scope="row">
                  <strong>全部阻断影响闭环后</strong>
                  <small>再次调用稳定化</small>
                </th>
                <td>
                  <span
                    class="status-badge"
                    :class="stabilization.stabilized.status === 'STABILIZED' ? 'status-badge--pass' : 'status-badge--warning'"
                  >
                    {{ stabilization.stabilized.status }}
                  </span>
                  <small>上下文修订 {{ stabilization.stabilized.contextRevision }}</small>
                </td>
                <td>{{ stabilization.stabilized.checkedAt || '（服务端未给）' }}</td>
                <td>
                  <span v-if="!stabilization.stabilized.blockers.length">（无阻断原因）</span>
                  <span v-for="(blocker, index) in stabilization.stabilized.blockers" :key="index" class="fact-line">
                    {{ blocker.code }}｜{{ blocker.targetId }}｜{{ blocker.reason }}
                  </span>
                </td>
                <td>
                  <code v-for="id in stabilization.stabilized.staleTaskIds" :key="id">{{ id }}</code>
                  <span v-if="!stabilization.stabilized.staleTaskIds.length">—</span>
                </td>
              </tr>
              <tr v-if="stabilization.stabilizedAfterReceipt">
                <th scope="row">
                  <strong>交付回执闭环后</strong>
                  <small>重算影响以交付检查回执闭环</small>
                </th>
                <td>
                  <span
                    class="status-badge"
                    :class="stabilization.stabilizedAfterReceipt.status === 'STABILIZED' ? 'status-badge--pass' : 'status-badge--warning'"
                  >
                    {{ stabilization.stabilizedAfterReceipt.status }}
                  </span>
                </td>
                <td>{{ stabilization.stabilizedAfterReceipt.checkedAt || '（服务端未给）' }}</td>
                <td>
                  <span v-if="!stabilization.stabilizedAfterReceipt.blockers.length">（无阻断原因）</span>
                  <span v-for="(blocker, index) in stabilization.stabilizedAfterReceipt.blockers" :key="index" class="fact-line">
                    {{ blocker.code }}｜{{ blocker.targetId }}｜{{ blocker.reason }}
                  </span>
                </td>
                <td>—</td>
              </tr>
            </tbody>
          </table>
        </div>
        <p class="card-footnote">
          稳定化之前的冻结尝试被服务端按
          <strong>HTTP {{ stabilization.freezeBeforeStable.status }}</strong>
          （错误码 <code>{{ stabilization.freezeBeforeStable.code || '（无）' }}</code>）拒掉——
          未关闭阻断项时既不能 STABILIZED，也不能导出。
          过期基线的变更集应用被拒
          <strong>HTTP {{ stabilization.staleApply.status }}</strong>；
          被拒之后上下文修订条数 {{ stabilization.contextRevisionsBefore }} → {{ stabilization.contextRevisionsAfter }}、
          影响任务条数 {{ stabilization.tasksBeforeStaleApply }} → {{ stabilization.tasksAfterStaleApply }}，没有半完成写入。
        </p>

        <h3 class="matrix-group-title">交付检查与冻结快照</h3>
        <div class="matrix-scroll">
          <table class="g3-table g3-table--compact">
            <thead>
              <tr><th>项</th><th>结论</th><th>回执 / 编号</th><th>说明</th></tr>
            </thead>
            <tbody>
              <tr>
                <th scope="row">交付检查</th>
                <td>
                  <span class="status-badge" :class="stabilization.check.status === 'passed' ? 'status-badge--pass' : 'status-badge--warning'">
                    {{ stabilization.check.status || '（无）' }}
                  </span>
                </td>
                <td><code>{{ stabilization.check.receiptId || '（无回执）' }}</code></td>
                <td>检查问题 {{ stabilization.check.issues.length }} 条 · 模板检查评估 {{ stabilization.templateCheck.evaluationCount }} 条</td>
              </tr>
              <tr>
                <th scope="row">检查回执</th>
                <td>{{ stabilization.receipt.status || '（未读）' }}</td>
                <td><code>{{ stabilization.receipt.id || '（无）' }}</code> · v{{ stabilization.receipt.version }}</td>
                <td>重算类影响用这份回执闭环</td>
              </tr>
              <tr>
                <th scope="row">交付快照</th>
                <td>
                  <span class="status-badge" :class="stabilization.snapshot.checkStatus === 'passed' ? 'status-badge--pass' : 'status-badge--warning'">
                    check.status={{ stabilization.snapshot.checkStatus || '（无）' }}
                  </span>
                </td>
                <td><code>{{ stabilization.snapshot.id || '（无）' }}</code></td>
                <td>is_current={{ stabilization.snapshot.isCurrent }} · 列表条目 {{ stabilization.releaseList.count }} 条 · truncated={{ stabilization.releaseList.truncated }}</td>
              </tr>
            </tbody>
          </table>
        </div>

        <h3 class="matrix-group-title">导出产物与下载核对</h3>
        <div class="matrix-scroll">
          <table class="g3-table g3-table--compact">
            <thead>
              <tr><th>项</th><th>值</th><th>说明</th></tr>
            </thead>
            <tbody>
              <tr>
                <th scope="row">受理接口</th>
                <td>HTTP {{ stabilization.exportAccepted.status }}</td>
                <td>202 只表示<strong>已受理</strong>，不表示导出成功</td>
              </tr>
              <tr>
                <th scope="row">产物最终状态</th>
                <td>
                  <span class="status-badge" :class="stabilization.exportArtifact.status === 'verified' ? 'status-badge--pass' : 'status-badge--not-run'">
                    {{ stabilization.exportArtifact.status || '（无）' }}
                  </span>
                </td>
                <td>状态序列 {{ stabilization.pollStatuses.join(' → ') }}</td>
              </tr>
              <tr>
                <th scope="row">产物哈希</th>
                <td><code>{{ stabilization.exportArtifact.fileSha256 || '（无）' }}</code></td>
                <td>
                  下载地址 <code>{{ stabilization.exportArtifact.downloadPath || '（无）' }}</code>
                  <template v-if="stabilization.exportArtifact.errorCode">
                    · 失败码 <code>{{ stabilization.exportArtifact.errorCode }}</code>
                    {{ stabilization.exportArtifact.errorMessage }}
                  </template>
                </td>
              </tr>
              <tr>
                <th scope="row">下载核对</th>
                <td>
                  {{ stabilization.download.size }} 字节 ·
                  <code>{{ stabilization.download.sha256 }}</code>
                </td>
                <td>
                  与产物哈希一致：
                  <span class="status-badge" :class="stabilization.download.matches ? 'status-badge--pass' : 'status-badge--not-run'">
                    {{ stabilization.download.matches ? '一致' : '不一致' }}
                  </span>
                </td>
              </tr>
            </tbody>
          </table>
        </div>

        <h3 class="matrix-group-title">影响任务闭环</h3>
        <div class="matrix-scroll">
          <table class="g3-table g3-table--compact">
            <thead>
              <tr><th>任务</th><th>动作</th><th>状态迁移</th><th>闭环证据</th><th>说明</th></tr>
            </thead>
            <tbody>
              <tr v-for="row in stabilization.transitions" :key="`${row.taskId}-${row.action}`">
                <th scope="row">
                  <code>{{ row.taskId }}</code>
                  <small>severity={{ row.severity }}</small>
                </th>
                <td>{{ row.action }}</td>
                <td>
                  <span class="status-badge" :class="row.to === 'DONE' ? 'status-badge--pass' : 'status-badge--warning'">
                    {{ row.from }} → {{ row.to }}
                  </span>
                  <small>HTTP {{ row.status }}<template v-if="row.code"> · {{ row.code }}</template></small>
                </td>
                <td>
                  <code v-for="ref in row.closureEvidence" :key="`${ref.kind}-${ref.id}`">
                    {{ ref.kind }}/{{ ref.id }}@{{ ref.version }}
                  </code>
                  <span v-if="!row.closureEvidence.length">—</span>
                </td>
                <td>{{ row.note }}</td>
              </tr>
              <tr v-if="!stabilization.transitions.length"><td colspan="5">本轮没有任务处置记录。</td></tr>
            </tbody>
          </table>
        </div>
        <p class="card-footnote">
          闭环证据按动作类型不同：<code>RECOMPUTE</code> 用当前交付检查的真实回执编号
          （<code>ComputationReceipt</code>），<code>RETRIEVE_CANDIDATES</code> 需要真实调研回执
          （<code>ResearchRun</code>，服务端要求至少两轮检索），其余动作用当前章节版本
          （<code>ChapterVersion</code>）。
        </p>
      </template>
    </section>

    <section class="lifecycle-card" aria-labelledby="model-title">
      <div class="section-heading">
        <div>
          <p class="section-kicker">非确定性结论 · 与确定性结论分列</p>
          <h2 id="model-title">旧异步结果 · 影响助手草稿 · 模型批量候选依赖</h2>
        </div>
        <span class="section-note">依赖真实模型 · 同样输入不保证同样输出</span>
      </div>

      <p class="card-intro">
        这一段跑的是**依赖真实模型**的三条验收项，结论是**非确定性**的，所以
        <strong>不与确定性结论混在一张表里</strong>：单独成列、状态一律
        <code>WARNING</code>，并附<strong>模型运行标识</strong>与<strong>耗时</strong>。
        模型不可用或缺外部凭据时，这三行记 <code>NOT RUN</code> 并写明原因，
        <strong>其余行照常判定</strong>。
      </p>

      <div class="live-actions">
        <button
          class="btn btn--primary"
          type="button"
          :disabled="modelRunning || running"
          @click="startModel"
        >
          {{ modelRunning ? '运行中…' : '跑通真实模型验收项' }}
        </button>
        <button
          class="btn"
          type="button"
          :disabled="!runSummary"
          @click="exportEvidence"
        >
          导出整链证据 JSON
        </button>
      </div>

      <p v-if="lastExport" class="live-ok" role="status">
        <strong>已导出：</strong><code>{{ lastExport.fileName }}</code>
        · {{ lastExport.bytes }} 字节 · 运行标识 <code>{{ lastExport.runId }}</code>
        · 后端 head <code>{{ lastExport.head || '（未知）' }}</code>
        · 并入 <strong>{{ lastExport.runCount }}</strong> 段运行
        · 含 {{ lastExport.rowCount }} 条验收行、{{ lastExport.verdictCount }} 条判定、
        {{ lastExport.gapCount }} 条实现缺口
        <span v-if="lastExport.downloadNote">· {{ lastExport.downloadNote }}</span>
      </p>

      <p class="card-footnote">
        导出的证据按仓库既有命名落到 <code>docs/08-本轮实施方案/G3-验收台-8-*.json</code>，
        <strong>不写进 Issue</strong>（<code>docs/agents/issue-tracker.md:54-56</code>）。
        导出体含运行标识、后端 head、每一步的请求与响应、每条验收行的判定与状态、
        证据来源（确定性 / 依赖真实模型）与实现缺口清单；换 head 后旧结论整体标为过期。
      </p>

      <p v-if="modelError" class="live-error" role="alert">
        <strong>运行失败：</strong>{{ modelError }}
      </p>

      <template v-if="model">
        <p v-if="!model.modelAvailable" class="live-warn" role="status">
          <strong>模型不可用：</strong>{{ model.unavailableReason }}
          —— 这三行按 <code>NOT RUN</code> 记，其余行照常判定。
        </p>

        <div class="matrix-scroll">
          <table class="g3-table g3-table--compact">
            <thead>
              <tr><th>验收项</th><th>状态</th><th>模型运行标识</th><th>模型 / 耗时</th><th>结论</th></tr>
            </thead>
            <tbody>
              <tr v-for="row in modelRows" :key="row.id">
                <th scope="row">
                  <strong>{{ row.name }}</strong>
                  <small>
                    {{ row.id }}
                    <span class="status-badge status-badge--warning">非确定性</span>
                  </small>
                </th>
                <td>
                  <span class="status-badge" :class="`status-badge--${statusTone(row.status)}`">
                    {{ row.status }}
                  </span>
                  <small>{{ row.holds ? '判定成立' : '判定不成立' }}</small>
                </td>
                <td><code>{{ row.runId }}</code></td>
                <td>
                  {{ row.modelId }}
                  <small>模式 {{ row.runMode }} · 耗时 {{ row.durationMs }} ms</small>
                </td>
                <td class="evidence">
                  <span>{{ row.detail }}</span>
                  <small v-for="line in row.evidence" :key="line">{{ line }}</small>
                </td>
              </tr>
            </tbody>
          </table>
        </div>

        <h3 class="matrix-group-title">模型调用记录（非确定性来源）</h3>
        <div class="matrix-scroll">
          <table class="g3-table g3-table--compact">
            <thead>
              <tr><th>运行标识</th><th>模型</th><th>模式</th><th>状态</th><th>耗时</th><th>tokens</th></tr>
            </thead>
            <tbody>
              <tr v-for="run in model.runs" :key="run.runId">
                <th scope="row"><code>{{ run.runId }}</code></th>
                <td>{{ run.modelId || '（未登记）' }}</td>
                <td>{{ run.runMode || '（未登记）' }}</td>
                <td>
                  <span class="status-badge" :class="run.status === 'completed' ? 'status-badge--pass' : 'status-badge--warning'">
                    {{ run.status }}
                  </span>
                  <small v-if="run.failureCode">失败码 {{ run.failureCode }}</small>
                </td>
                <td>{{ run.durationMs }} ms</td>
                <td>{{ run.inputTokens }} / {{ run.outputTokens }}</td>
              </tr>
              <tr v-if="!model.runs.length"><td colspan="6">本轮没有成功的模型调用。</td></tr>
            </tbody>
          </table>
        </div>
        <p class="card-footnote">
          这些调用记录会一起进入<strong>导出的证据</strong>，并标注为「依赖真实模型的非确定性来源」——
          少了这个标注，读者会把非确定性结论当成确定性结论。
        </p>
      </template>
    </section>

    <section class="lifecycle-card" aria-labelledby="blocked-title">
      <div class="section-heading">
        <div>
          <p class="section-kicker">稳定化阻断与恢复路径</p>
          <h2 id="blocked-title">阻断 → 处置 → 闭环 → 版本不匹配 → 重投 → 再次已稳定</h2>
        </div>
        <span class="section-note">页面与 node 脚本跑同一份执行器</span>
      </div>

      <p class="live-lede">
        这一段在真实模式里把**阻断与恢复**跑完：先制造一个未闭环的阻断影响，
        稳定化以 <strong>200 成功响应</strong>返回 <code>BLOCKED</code>，并**逐条**带出阻断原因
        （不是一句失败）；随后处置规则问题、把阻断影响按
        「指派 → 开始 → 复写章节并按新规格修订重新确认 → 完成」闭环，再次稳定化转为<strong>已稳定</strong>；
        再让目标对象被新版本替换——这一次稳定化**没有被放行**；
        按新基线重投对应影响任务并闭环之后，稳定化再次转为已稳定。
      </p>

      <div class="live-actions">
        <button
          class="live-button live-button--primary"
          :disabled="blockedRunning || running"
          @click="startBlocked"
        >
          {{ blockedRunning ? '运行中…' : '跑通稳定化阻断与恢复路径' }}
        </button>
        <span class="section-note">
          {{
            blocked
              ? '本轮已跑完：阻断原因逐条 + 恢复动作 + 前后两次结果'
              : '尚未运行；未运行时下面不显示任何证据位'
          }}
        </span>
      </div>

      <p v-if="blockedError" class="live-error" role="alert">
        <strong>运行失败：</strong>{{ blockedError }}
      </p>

      <template v-if="blocked">
        <div class="verdict-grid">
          <article
            v-for="verdict in blocked.verdicts"
            :key="verdict.id"
            class="verdict-card"
            :class="verdictCardClass(verdict)"
          >
            <header>
              <strong>{{ verdict.id }} · {{ verdict.title }}</strong>
              <span
                class="status-badge"
                :class="`status-badge--${statusTone(verdictStatus(verdict))}`"
              >
                {{ verdictStatus(verdict) }}
              </span>
            </header>
            <p class="verdict-detail">{{ verdict.detail }}</p>
            <ul class="verdict-evidence">
              <li v-for="(line, index) in verdict.evidence" :key="index"><code>{{ line }}</code></li>
            </ul>
          </article>
        </div>

        <h3 class="matrix-group-title">五次稳定化：恢复动作前后的结果</h3>
        <div class="matrix-scroll">
          <table class="g3-table g3-table--compact">
            <thead>
              <tr><th>阶段</th><th>状态</th><th>HTTP</th><th>阻断原因（逐条）</th><th>失效任务</th></tr>
            </thead>
            <tbody>
              <tr v-for="row in blockedStabilizeRows" :key="row.key">
                <th scope="row">
                  <strong>{{ row.title }}</strong>
                  <small>{{ row.note }}</small>
                </th>
                <td>
                  <span
                    class="status-badge"
                    :class="row.fact.status === 'STABILIZED' ? 'status-badge--pass' : 'status-badge--warning'"
                  >
                    {{ row.fact.status || '（被拒，未放行）' }}
                  </span>
                  <small>上下文修订 {{ row.fact.contextRevision }}</small>
                </td>
                <td>
                  HTTP {{ row.http }}
                  <small v-if="row.code">错误码 <code>{{ row.code }}</code></small>
                </td>
                <td>
                  <span v-if="!row.fact.blockers.length">（无阻断原因）</span>
                  <span v-for="(blocker, index) in row.fact.blockers" :key="index" class="fact-line">
                    {{ blocker.code }}｜对象 {{ blocker.targetId }}｜原因 {{ blocker.reason }}
                  </span>
                </td>
                <td>
                  <code v-for="id in row.fact.staleTaskIds" :key="id">{{ id }}</code>
                  <span v-if="!row.fact.staleTaskIds.length">—</span>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <p class="card-footnote">
          首次稳定化是 <strong>200 成功响应</strong>，服务端把
          {{ blocked.blockedLines.length }} 条阻断原因逐条带出（不折叠）；
          目标对象被新版本替换之后那一次稳定化
          <strong>HTTP {{ blocked.mismatchStatus }}</strong>
          （错误码 <code>{{ blocked.mismatchCode || '（无）' }}</code>）——
          这一版修订**没有被放行**；重投闭环之后才回到
          <code>{{ blocked.finalStabilized.status }}</code>。
        </p>

        <h3 class="matrix-group-title">规则问题：处置前 → 处置后</h3>
        <div class="matrix-scroll">
          <table class="g3-table g3-table--compact">
            <thead>
              <tr><th>问题</th><th>规则</th><th>严重度</th><th>目标</th><th>说明</th><th>处置</th></tr>
            </thead>
            <tbody>
              <tr v-for="row in blockedIssueRows" :key="row.id">
                <th scope="row"><code>{{ row.id }}</code></th>
                <td><code>{{ row.ruleId }}</code></td>
                <td>{{ row.severity }}</td>
                <td><code>{{ row.targetId }}</code></td>
                <td>
                  <span class="fact-line">{{ row.message }}</span>
                  <span class="fact-line">{{ row.afterNote }}</span>
                </td>
                <td>
                  <span v-if="row.dispositionAction">
                    <code>{{ row.dispositionAction }}</code>
                  </span>
                  <span v-else>（未处置）</span>
                  <span v-if="row.dispositionReason" class="fact-line">{{ row.dispositionReason }}</span>
                </td>
              </tr>
              <tr v-if="!blockedIssueRows.length"><td colspan="6">本轮交付检查没有给出规则问题。</td></tr>
            </tbody>
          </table>
        </div>
        <p v-if="blocked.disposition" class="card-footnote">
          处置动作实测：<code>dismiss</code> → HTTP {{ blocked.disposition.dismissStatus }}
          （错误码 <code>{{ blocked.disposition.dismissCode || '（无）' }}</code>）；
          <code>resolve</code> → HTTP {{ blocked.disposition.resolveStatus }}
          （错误码 <code>{{ blocked.disposition.resolveCode || '（无）' }}</code>）；
          重检时问题是否带上处置：{{ blocked.disposition.carriesDisposition ? '带上了' : '没有带上' }}。
        </p>

        <h3 class="matrix-group-title">「接受本次不处理」的边界</h3>
        <div class="matrix-scroll">
          <table class="g3-table g3-table--compact">
            <thead>
              <tr><th>尝试</th><th>任务</th><th>严重度</th><th>HTTP</th><th>错误码</th><th>结果状态</th><th>说明</th></tr>
            </thead>
            <tbody>
              <tr v-for="row in blockedDismissRows" :key="row.label">
                <th scope="row">{{ row.label }}</th>
                <td><code>{{ row.taskId }}</code></td>
                <td>{{ row.severity }}</td>
                <td>HTTP {{ row.status }}</td>
                <td><code>{{ row.code || '（无）' }}</code></td>
                <td>
                  <span
                    class="status-badge"
                    :class="row.finalStatus === 'DISMISSED' ? 'status-badge--warning' : 'status-badge--not-run'"
                  >
                    {{ row.finalStatus }}
                  </span>
                </td>
                <td>
                  {{ row.explanation || '（服务端接受：非阻断级影响可以接受本次不处理）' }}
                </td>
              </tr>
              <tr v-if="!blockedDismissRows.length"><td colspan="7">本轮没有「接受本次不处理」的尝试记录。</td></tr>
            </tbody>
          </table>
        </div>

        <h3 class="matrix-group-title">阻断项闭环：任务状态迁移</h3>
        <div class="matrix-scroll">
          <table class="g3-table g3-table--compact">
            <thead>
              <tr><th>任务</th><th>动作</th><th>状态迁移</th><th>闭环证据</th><th>说明</th></tr>
            </thead>
            <tbody>
              <tr v-for="row in blockedTransitionRows" :key="`${row.taskId}-${row.action}-${row.to}`">
                <th scope="row">
                  <code>{{ row.taskId }}</code>
                  <small>severity={{ row.severity }}</small>
                </th>
                <td>{{ row.action }}</td>
                <td>
                  <span
                    class="status-badge"
                    :class="row.to === 'DONE' ? 'status-badge--pass' : (row.to === 'STALE' ? 'status-badge--not-run' : 'status-badge--warning')"
                  >
                    {{ row.from }} → {{ row.to }}
                  </span>
                  <small>HTTP {{ row.status }}<template v-if="row.code"> · {{ row.code }}</template></small>
                </td>
                <td><code v-if="row.evidence">{{ row.evidence }}</code><span v-else>—</span></td>
                <td>{{ row.note }}</td>
              </tr>
              <tr v-if="!blockedTransitionRows.length"><td colspan="5">本轮没有任务处置记录。</td></tr>
            </tbody>
          </table>
        </div>

        <h3 class="matrix-group-title">被新版本替换掉的任务</h3>
        <div class="matrix-scroll">
          <table class="g3-table g3-table--compact">
            <thead>
              <tr><th>任务</th><th>动作</th><th>严重度</th><th>失效原因</th></tr>
            </thead>
            <tbody>
              <tr v-for="row in blockedStaleRows" :key="row.id">
                <th scope="row"><code>{{ row.id }}</code></th>
                <td>{{ row.action }}</td>
                <td>{{ row.severity }}</td>
                <td><code>{{ row.staleReason || '（服务端未给）' }}</code></td>
              </tr>
              <tr v-if="!blockedStaleRows.length"><td colspan="4">本轮没有被判为失效的任务。</td></tr>
            </tbody>
          </table>
        </div>
        <p class="card-footnote">
          重投出来的任务：
          <code v-for="id in blocked.reissuedTaskIds" :key="id">{{ id }}</code>
          <span v-if="!blocked.reissuedTaskIds.length">—</span>。
          <code>STALE</code> 是终态，但**不算闭环**——所以稳定化不会因为它而放行。
        </p>

        <h3 class="matrix-group-title">人工复核探针（另起一个隔离项目）</h3>
        <div v-if="blocked.reviewProbe" class="matrix-scroll">
          <table class="g3-table g3-table--compact">
            <thead>
              <tr><th>项</th><th>结果</th><th>说明</th></tr>
            </thead>
            <tbody>
              <tr>
                <th scope="row">复核任务</th>
                <td>
                  <code>{{ blocked.reviewProbe.taskId }}</code>
                  <small>{{ blocked.reviewProbe.taskAction }} · 目标 {{ blocked.reviewProbe.targetKind }}</small>
                </td>
                <td>研究主张 <code>{{ blocked.reviewProbe.claimId }}</code></td>
              </tr>
              <tr>
                <th scope="row">缺人工复核决策</th>
                <td>
                  HTTP {{ blocked.reviewProbe.completeWithoutReviewStatus }}
                  <code>{{ blocked.reviewProbe.completeWithoutReviewCode || '（无）' }}</code>
                </td>
                <td>异步作业成功关不掉人工复核任务</td>
              </tr>
              <tr>
                <th scope="row">带人工复核决策</th>
                <td>
                  HTTP {{ blocked.reviewProbe.completeWithReviewStatus }}
                  <code>{{ blocked.reviewProbe.completeWithReviewCode || '（无）' }}</code>
                </td>
                <td>
                  是否被服务端接受：
                  <span class="status-badge" :class="blocked.reviewProbe.completeWithReviewAccepted ? 'status-badge--pass' : 'status-badge--not-run'">
                    {{ blocked.reviewProbe.completeWithReviewAccepted ? '接受' : '未接受' }}
                  </span>
                </td>
              </tr>
              <tr>
                <th scope="row">真实模型那半段</th>
                <td>
                  <span class="status-badge" :class="blocked.reviewProbe.modelSideCovered ? 'status-badge--pass' : 'status-badge--not-run'">
                    {{ blocked.reviewProbe.modelSideCovered ? '本轮覆盖' : '本轮未覆盖' }}
                  </span>
                </td>
                <td>「证据重检返回新候选」需要真实模型，造证据卡要走模型侧</td>
              </tr>
            </tbody>
          </table>
        </div>
        <p v-else class="card-footnote">本轮没有跑人工复核探针。</p>
      </template>
    </section>

    <section class="lifecycle-card" aria-labelledby="gapclosure-title">
      <div class="section-heading">
        <div>
          <p class="section-kicker">缺口收口 · 三条新链路</p>
          <h2 id="gapclosure-title">路线图编辑会话 · 受控故障注入口径 · 助手两组说明与候选批次</h2>
        </div>
        <span class="section-note">页面与 node 脚本跑同一份执行器</span>
      </div>

      <p class="live-lede">
        这三段补的是此前「台面缺链路」的三行验收：<strong>AC-G3-03</strong>（应用原子性）、
        <strong>AC-G3-08 / AC-G3-16</strong>（路线图必需项与版本恢复）、
        <strong>AC-G3-14 / AC-G3-15</strong>（助手两组说明与依赖候选批次）。
        每一段各跑在自己的隔离项目上，结论**逐段**判，不跨项目串味。
      </p>

      <div class="live-actions">
        <button
          class="live-button live-button--primary"
          :disabled="roadmapRunning || running"
          @click="startRoadmap"
        >
          {{ roadmapRunning ? '运行中…' : '跑通路线图编辑会话' }}
        </button>
        <button
          class="live-button"
          :disabled="faultInjectionRunning || running"
          @click="startFaultInjection"
        >
          {{ faultInjectionRunning ? '运行中…' : '观测受控故障注入' }}
        </button>
        <button
          class="live-button"
          :disabled="assistantRunning || running"
          @click="startAssistant"
        >
          {{ assistantRunning ? '运行中…' : '读回助手两组说明与候选批次' }}
        </button>
      </div>

      <p v-if="roadmapError" class="live-error" role="alert">
        <strong>路线图链运行失败：</strong>{{ roadmapError }}
      </p>
      <p v-if="faultInjectionError" class="live-error" role="alert">
        <strong>故障注入观测运行失败：</strong>{{ faultInjectionError }}
      </p>
      <p v-if="assistantError" class="live-error" role="alert">
        <strong>助手读回链运行失败：</strong>{{ assistantError }}
      </p>

      <template v-if="roadmap">
        <h3 class="matrix-group-title">路线图编辑会话（AC-G3-08 / AC-G3-16）</h3>
        <div class="verdict-grid">
          <article
            v-for="verdict in roadmap.verdicts"
            :key="verdict.id"
            class="verdict-card"
            :class="verdictCardClass(verdict)"
          >
            <header>
              <strong>{{ verdict.id }} · {{ verdict.title }}</strong>
              <span
                class="status-badge"
                :class="`status-badge--${statusTone(verdictStatus(verdict))}`"
              >
                {{ verdictStatus(verdict) }}
              </span>
            </header>
            <p class="verdict-detail">{{ verdict.detail }}</p>
            <ul class="verdict-evidence">
              <li v-for="(line, index) in verdict.evidence" :key="index"><code>{{ line }}</code></li>
            </ul>
          </article>
        </div>

        <div class="matrix-scroll">
          <table class="g3-table g3-table--compact">
            <thead>
              <tr><th>阶段</th><th>基线版本</th><th>节点</th><th>必需</th><th>状态（服务端判定）</th></tr>
            </thead>
            <tbody>
              <tr>
                <th scope="row">提交前</th>
                <td>{{ roadmap.baselineBefore.version }}</td>
                <td>{{ roadmap.baselineBefore.nodes.length }}</td>
                <td>{{ roadmap.baselineBefore.nodes.filter((n) => n.required).length }}</td>
                <td>
                  <span v-for="node in roadmap.baselineBefore.nodes" :key="`before-${node.id}`" class="fact-line">
                    <code>{{ node.id }}</code> · {{ node.state }}
                  </span>
                </td>
              </tr>
              <tr>
                <th scope="row">提交后</th>
                <td>{{ roadmap.baselineAfterCommit.version }}</td>
                <td>{{ roadmap.baselineAfterCommit.nodes.length }}</td>
                <td>{{ roadmap.baselineAfterCommit.nodes.filter((n) => n.required).length }}</td>
                <td>
                  <span v-for="node in roadmap.baselineAfterCommit.nodes" :key="`after-${node.id}`" class="fact-line">
                    <code>{{ node.id }}</code> · {{ node.state }}
                  </span>
                </td>
              </tr>
              <tr>
                <th scope="row">取消后</th>
                <td>{{ roadmap.baselineAfterCancel.version }}</td>
                <td>{{ roadmap.baselineAfterCancel.nodes.length }}</td>
                <td>{{ roadmap.baselineAfterCancel.nodes.filter((n) => n.required).length }}</td>
                <td>
                  <span v-for="node in roadmap.baselineAfterCancel.nodes" :key="`cancel-${node.id}`" class="fact-line">
                    <code>{{ node.id }}</code> · {{ node.state }}
                  </span>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <p class="card-footnote">
          冻结版本序列：<code v-for="entry in roadmap.versions" :key="`v-${entry.version}`">
            v{{ entry.version }}:{{ entry.state }}
          </code><span v-if="!roadmap.versions.length">—</span>。
          被取消会话占用的临时版本：
          <code v-for="ref in roadmap.temporaryRefs" :key="`${ref.kind}-${ref.id}-${ref.version}`">
            {{ ref.kind }}/{{ ref.id }}@v{{ ref.version }}
          </code><span v-if="!roadmap.temporaryRefs.length">—</span>
          ——它们**从来没有被冻结**。
        </p>
      </template>

      <template v-if="faultInjection">
        <h3 class="matrix-group-title">受控故障注入的可观测口径（AC-G3-03）</h3>
        <div class="verdict-grid">
          <article
            v-for="verdict in faultInjection.verdicts"
            :key="verdict.id"
            class="verdict-card"
            :class="verdictCardClass(verdict)"
          >
            <header>
              <strong>{{ verdict.id }} · {{ verdict.title }}</strong>
              <span
                class="status-badge"
                :class="`status-badge--${statusTone(verdictStatus(verdict))}`"
              >
                {{ verdictStatus(verdict) }}
              </span>
            </header>
            <p class="verdict-detail">{{ verdict.detail }}</p>
            <ul class="verdict-evidence">
              <li v-for="(line, index) in verdict.evidence" :key="index"><code>{{ line }}</code></li>
            </ul>
          </article>
        </div>
        <p class="card-footnote">
          本轮后端是否装配了受控故障注入：
          <span
            class="status-badge"
            :class="faultInjection.observation.faultInjectionAssembled ? 'status-badge--pass' : 'status-badge--not-run'"
          >
            {{ faultInjection.observation.faultInjectionAssembled ? '已装配' : '未装配' }}
          </span>
          ——注入是**组合根按环境变量装配的夹具**，不是产品端点：页面无法触发，只能观测。
          apply 返回 HTTP {{ faultInjection.observation.status }}（错误码
          <code>{{ faultInjection.observation.code || '（无）' }}</code>）。
        </p>
      </template>

      <template v-if="assistant">
        <h3 class="matrix-group-title">助手两组说明与依赖候选批次（AC-G3-14 / AC-G3-15）</h3>
        <div class="verdict-grid">
          <article
            v-for="verdict in assistant.verdicts"
            :key="verdict.id"
            class="verdict-card"
            :class="verdictCardClass(verdict)"
          >
            <header>
              <strong>{{ verdict.id }} · {{ verdict.title }}</strong>
              <span
                class="status-badge"
                :class="`status-badge--${statusTone(verdictStatus(verdict))}`"
              >
                {{ verdictStatus(verdict) }}
              </span>
            </header>
            <p class="verdict-detail">{{ verdict.detail }}</p>
            <ul class="verdict-evidence">
              <li v-for="(line, index) in verdict.evidence" :key="index"><code>{{ line }}</code></li>
            </ul>
          </article>
        </div>
        <p class="card-footnote">
          助手草稿：<code>{{ assistant.draft?.changeSetId || '（本轮未读回）' }}</code>
          （状态 <code>{{ assistant.draft?.status || '（无）' }}</code>）·
          「确定受影响」{{ assistant.draft?.explanation?.definiteAffected.length ?? 0 }} 条 ·
          「可能受影响」{{ assistant.draft?.explanation?.possibleAffected.length ?? 0 }} 条。
          <template v-if="assistant.draft?.explanationMissing">
            ——草稿在但**助手未给两组说明**（模型边界收紧后的容错情形，如实记录，不崩掉）。
          </template>
        </p>
      </template>
    </section>

    <div class="console-columns">
      <section class="flow-card" aria-labelledby="flow-title">
        <div class="section-heading">
          <div>
            <p class="section-kicker">左栏 · 主流程</p>
            <h2 id="flow-title">
              {{ liveSteps.length ? '本次运行的真实调用' : '从建项到下载的十五步' }}
            </h2>
          </div>
          <span class="section-note">
            {{ liveSteps.length ? '真实证据 · 逐步留痕' : '形态预览 · 未执行' }}
          </span>
        </div>
        <ol v-if="liveSteps.length" class="flow-list">
          <li v-for="step in liveSteps" :key="`${step.index}-${step.name}`" class="flow-step">
            <span class="flow-number">{{ String(step.index).padStart(2, '0') }}</span>
            <div class="flow-body">
              <h3>{{ step.name }}</h3>
              <p class="flow-endpoint">
                <span class="method" :class="`method--${step.method.toLowerCase()}`">{{ step.method }}</span>
                <code>{{ step.path }}</code>
                <span class="flow-status" :class="step.ok ? 'flow-status--ok' : 'flow-status--bad'">
                  HTTP {{ step.status }}
                </span>
              </p>
              <p class="flow-precondition">{{ step.conclusion }}</p>
              <p class="flow-meta">
                <span v-if="step.idempotencyKey">
                  幂等键 <code>{{ step.idempotencyKey }}</code>
                </span>
                <span>耗时 {{ step.durationMs }} ms</span>
                <span v-if="step.details" class="flow-details">
                  {{ Object.entries(step.details).map(([k, v]) => `${k}=${v}`).join(' · ') }}
                </span>
              </p>
            </div>
          </li>
        </ol>
        <ol v-else class="flow-list">
          <li v-for="step in G3_FLOW_STEPS" :key="step.index" class="flow-step">
            <span class="flow-number">{{ String(step.index).padStart(2, '0') }}</span>
            <div class="flow-body">
              <h3>{{ step.name }}</h3>
              <p class="flow-endpoint">
                <span class="method" :class="`method--${step.method.toLowerCase()}`">{{ step.method }}</span>
                <code>{{ step.path }}</code>
              </p>
              <p class="flow-precondition">{{ step.precondition }}</p>
            </div>
            <span class="flow-state">{{ G3_FLOW_EXECUTION_STATE }}</span>
          </li>
        </ol>
        <p class="card-footnote">
          {{
            liveSteps.length
              ? '以上是本次运行打出的真实调用：方法、路径、状态码、幂等键、耗时都在。GET 一律不带幂等键；契约未标幂等键的写路由（POST /checks、POST /template-checks）也不带。'
              : '本票的真实模式跑通后，这里会换成真实调用记录；未运行时只固定形态与顺序。'
          }}
        </p>
      </section>

      <section class="matrix-card" aria-labelledby="matrix-title">
        <div class="section-heading">
          <div>
            <p class="section-kicker">右栏 · 验收矩阵</p>
            <h2 id="matrix-title">切片七行 + 验收场景十六行</h2>
          </div>
          <span class="section-note">
            共 {{ G3_MATRIX_ROWS.length }} 行 · 判据原文照录
          </span>
        </div>

        <div class="matrix-legend">
          <span
            v-for="status in G3_STATUS_VALUES"
            :key="status"
            class="status-badge"
            :class="`status-badge--${statusTone(status)}`"
            :title="statusSummary(status)"
          >
            {{ status }}
          </span>
        </div>
        <p class="legend-note">
          状态词表取自 <code>docs/agents/development-rules.md:78-84</code>，只有这七个值，页面不引入自造词。
          同一份文件 :84 规定：矩阵出现 <code>TODO</code> / <code>NOT RUN</code> / <code>BLOCKED</code> 时 PR 不能放行。
        </p>

        <h3 class="matrix-group-title">G3.1–G3.7 切片完成判据</h3>
        <div class="matrix-scroll">
          <table class="g3-table">
            <thead>
              <tr>
                <th>验收项</th>
                <th>判据原文</th>
                <th>Spec 锚点</th>
                <th>状态</th>
                <th>证据位</th>
                <th>本次会话覆盖</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in sliceRows" :key="row.id">
                <th scope="row">
                  <strong>{{ row.name }}</strong>
                  <small>{{ row.id }} · {{ row.deliverable }}</small>
                  <small>
                    <span class="source-chip" :class="sourceTone(sourceOf(row.id))">
                      {{ sourceLabel(sourceOf(row.id)) }}
                    </span>
                  </small>
                </th>
                <td class="criterion">{{ row.criterion }}</td>
                <td class="anchor">
                  <code>{{ anchorLabel(row.anchor) }}</code>
                  <small>{{ anchorPath(row.anchor) }}</small>
                </td>
                <td>
                  <span class="status-badge" :class="`status-badge--${statusTone(row.status)}`">
                    {{ row.status }}
                  </span>
                </td>
                <td class="evidence">
                  <span>{{ row.evidence }}</span>
                  <small v-if="row.note">{{ row.note }}</small>
                </td>
                <td class="coverage">
                  <span class="coverage-chip" :class="coverageTone(coverageById.get(row.id))">
                    {{ coverageLabel(coverageById.get(row.id)) }}
                  </span>
                  <small v-if="coverageRunId(row.id)">
                    来自运行 <code>{{ coverageRunId(row.id) }}</code>
                  </small>
                  <small v-if="coverageHead(row.id)">
                    后端 head <code>{{ coverageHead(row.id) }}</code>
                  </small>
                  <small>{{ coverageById.get(row.id)?.note }}</small>
                </td>
              </tr>
            </tbody>
          </table>
        </div>

        <h3 class="matrix-group-title">AC-G3-01…16 领域验收场景</h3>
        <div class="matrix-scroll">
          <table class="g3-table">
            <thead>
              <tr>
                <th>验收项</th>
                <th>判据原文</th>
                <th>Spec 锚点</th>
                <th>状态</th>
                <th>证据位</th>
                <th>本次会话覆盖</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in acRows" :key="row.id">
                <th scope="row">
                  <strong>{{ row.name }}</strong>
                  <small>
                    {{ row.id }}
                    <template v-if="row.domain"> · 责任域 {{ row.domain }}</template>
                    <template v-if="row.severity"> · 严重度 {{ row.severity }}</template>
                  </small>
                  <small>
                    <span class="source-chip" :class="sourceTone(sourceOf(row.id))">
                      {{ sourceLabel(sourceOf(row.id)) }}
                    </span>
                    <span v-if="gapIdsOf(row.id).length" class="source-chip source-chip--gap">
                      实现缺口 {{ gapIdsOf(row.id).join(' / ') }}
                    </span>
                    <span v-else-if="limitingGapIdsOf(row.id).length" class="source-chip source-chip--limited">
                      覆盖受限 {{ limitingGapIdsOf(row.id).join(' / ') }}
                    </span>
                  </small>
                </th>
                <td class="criterion">{{ row.criterion }}</td>
                <td class="anchor">
                  <code>{{ anchorLabel(row.anchor) }}</code>
                  <small>{{ anchorPath(row.anchor) }}</small>
                </td>
                <td>
                  <span class="status-badge" :class="`status-badge--${statusTone(row.status)}`">
                    {{ row.status }}
                  </span>
                </td>
                <td class="evidence">
                  <span>{{ row.evidence }}</span>
                  <small v-if="row.note">{{ row.note }}</small>
                  <small v-if="row.followUpIssue" class="issue-link">
                    跟进 Issue #{{ row.followUpIssue }}
                  </small>
                </td>
                <td class="coverage">
                  <span class="coverage-chip" :class="coverageTone(coverageById.get(row.id))">
                    {{ coverageLabel(coverageById.get(row.id)) }}
                  </span>
                  <small v-if="coverageRunId(row.id)">
                    来自运行 <code>{{ coverageRunId(row.id) }}</code>
                  </small>
                  <small v-if="coverageHead(row.id)">
                    后端 head <code>{{ coverageHead(row.id) }}</code>
                  </small>
                  <small>{{ coverageById.get(row.id)?.note }}</small>
                </td>
              </tr>
            </tbody>
          </table>
        </div>

        <p class="card-footnote">
          状态分布：PASS {{ counts['PASS'] }} · PASS (follow-up) {{ counts['PASS (follow-up)'] }} ·
          N/A {{ counts['N/A'] }} · WARNING {{ counts['WARNING'] }} · NOT RUN {{ counts['NOT RUN'] }} ·
          BLOCKED {{ counts['BLOCKED'] }} · TODO {{ counts['TODO'] }}。
          「覆盖」是**本次会话累计的真实运行**与每一行的关系：前置链跑通**不构成**任何一条判据的证据，
          所以没被覆盖的行状态原样保留，绝不会被判成 <code>PASS</code>。
          页面上每段链各跑在自己的隔离项目上，所以每一行都标出**它由哪一段运行判定**、
          跑在哪个后端 head 上。证据位（矩阵登记的那一份）只写「链 + 判定卡 + 落证文件」：
          运行标识与 head 每次重跑都会变，写死在静态矩阵里就是写下**下一次就过期**的串。
        </p>

        <h3 class="matrix-group-title">实现缺口（无法实跑的验收行与渲染缺口）</h3>
        <p class="legend-note">
          每条缺口写清三件事：<strong>缺什么</strong>、<strong>对应 Spec 位置</strong>、
          <strong>解除条件</strong>，并链接缺口清单 Issue
          <a class="issue-link" :href="`https://github.com/magician336/lingdoc/issues/${G3_GAP_LIST_ISSUE}`" target="_blank" rel="noreferrer">#{{ G3_GAP_LIST_ISSUE }}</a>。
          缺口对应的行**不写 <code>PASS</code>**：缺能力记 <code>BLOCKED</code>，
          部分覆盖记 <code>PASS (follow-up)</code>，宿主环境限制记 <code>WARNING</code>，
          页面实跑证据待落记 <code>NOT RUN</code>。
        </p>
        <div class="gap-list">
          <article v-for="gap in gapList" :key="gap.id" class="gap-item">
            <header class="gap-head">
              <strong>{{ gap.id }} · {{ gap.title }}</strong>
              <span class="status-badge" :class="`status-badge--${statusTone(gap.rowStatus)}`">
                {{ gap.rowStatus }}
              </span>
            </header>
            <p class="gap-meta">
              <span>对应行：{{ [...gap.rows, ...gap.limits].join(' / ') || '—' }}</span>
              <span>Spec 锚点：<code>{{ formatAnchor(gap.specAnchor) }}</code></span>
              <span>
                缺口清单：
                <a class="issue-link" :href="`https://github.com/magician336/lingdoc/issues/${gap.issue}`" target="_blank" rel="noreferrer">#{{ gap.issue }}</a>
              </span>
            </p>
            <p class="gap-text"><strong>缺什么：</strong>{{ gap.missing }}</p>
            <p class="gap-text"><strong>解除条件：</strong>{{ gap.unblockCondition }}</p>
            <p v-if="gap.fact" class="gap-text gap-text--fact"><strong>实测事实：</strong>{{ gap.fact }}</p>
          </article>
        </div>
      </section>
    </div>

    <section class="scope-card" aria-labelledby="scope-title">
      <div class="section-heading">
        <div>
          <p class="section-kicker">口径声明</p>
          <h2 id="scope-title">ChangeSet 状态集合有三种说法，以主基准为准</h2>
        </div>
        <span class="section-note">不替读者裁决，但把各自位置摆出来</span>
      </div>
      <div class="scope-grid">
        <article
          v-for="reading in G3_LIFECYCLE_READINGS"
          :key="reading.id"
          class="scope-item"
          :class="{ 'scope-item--primary': reading.primary }"
        >
          <header>
            <strong>{{ reading.source }}</strong>
            <span v-if="reading.primary" class="primary-chip">本页面采用</span>
          </header>
          <p class="scope-anchor">
            <code>{{ formatAnchor(reading.anchor) }}</code>
          </p>
          <p class="scope-states">{{ reading.states.join(' / ') }}</p>
          <p class="scope-note">{{ reading.note }}</p>
        </article>
      </div>
      <div class="satisfied-reading">
        <p class="section-kicker">SATISFIED 的归属</p>
        <p>
          <strong>按主基准（:{{ G3_SATISFIED_READING.primary.anchor.start }}）读作：</strong>
          {{ G3_SATISFIED_READING.primary.text }}
        </p>
        <p>
          <strong>另一种读法（{{ G3_SATISFIED_READING.alternative.source }}:{{ G3_SATISFIED_READING.alternative.anchor.start }}）：</strong>
          {{ G3_SATISFIED_READING.alternative.text }}
        </p>
      </div>
    </section>

    <section class="glossary-card" aria-labelledby="glossary-title">
      <div class="section-heading">
        <div>
          <p class="section-kicker">术语</p>
          <h2 id="glossary-title">严重度、责任域与 NO_ACTION</h2>
        </div>
        <span class="section-note">取自仓库根 GLOSSARY.md 与 ADR 0003</span>
      </div>
      <div class="glossary-grid">
        <article v-for="severity in G3_SEVERITIES" :key="severity" class="glossary-item">
          <strong>{{ severity }}</strong>
          <p>{{ G3_SEVERITY_DESCRIPTIONS[severity] }}</p>
        </article>
        <article v-for="domain in G3_RESPONSIBILITY_DOMAINS" :key="domain" class="glossary-item">
          <strong>{{ domain }} 域</strong>
          <p>{{ G3_DOMAIN_DESCRIPTIONS[domain] }}</p>
        </article>
        <article class="glossary-item glossary-item--warn">
          <strong>NO_ACTION</strong>
          <p>
            是影响解析结果，不是影响任务状态——页面上不会把 <code>NO_ACTION</code> 显示成任务状态。
            影响任务状态只有 {{ IMPACT_TASK_STATUSES.join(' / ') }}。
          </p>
        </article>
      </div>
    </section>

    <section class="snapshot-card" aria-labelledby="snapshot-title">
      <div class="section-heading">
        <div>
          <p class="section-kicker">证据快照</p>
          <h2 id="snapshot-title">G3 B3 实跑留下的历史证据</h2>
        </div>
        <span class="snapshot-chip snapshot-chip--inline">
          <i aria-hidden="true"></i>{{ snapshotNotice(snapshot) }}
        </span>
      </div>

      <article class="evidence-block">
        <header class="evidence-head">
          <strong>变更集与上下文修订</strong>
          <span class="evidence-stamp">{{ snapshotNotice(snapshot) }}</span>
        </header>
        <dl class="fact-list">
          <div><dt>项目</dt><dd>{{ snapshot.project.name }} · {{ snapshot.project.id }}</dd></div>
          <div><dt>变更集</dt><dd>{{ snapshot.changeSet.id }}（{{ snapshot.changeSet.status }}）</dd></div>
          <div>
            <dt>上下文修订</dt>
            <dd>
              {{ snapshot.changeSet.baseContextRevision }} → {{ snapshot.changeSet.targetContextRevision }}
              （项目当前修订 {{ snapshot.project.currentContextRevision }}）
            </dd>
          </div>
          <div><dt>变更理由</dt><dd>{{ snapshot.changeSet.reason }}</dd></div>
        </dl>
      </article>

      <article class="evidence-block">
        <header class="evidence-head">
          <strong>影响任务</strong>
          <span class="evidence-stamp">{{ snapshotNotice(snapshot) }}</span>
        </header>
        <div class="table-scroll">
          <table class="g3-table g3-table--compact">
            <thead>
              <tr><th>任务</th><th>动作</th><th>状态</th><th>闭环证据</th></tr>
            </thead>
            <tbody>
              <tr v-for="task in snapshot.impactTasks" :key="task.id">
                <th scope="row"><code>{{ task.id }}</code></th>
                <td>{{ task.action }}</td>
                <td><span class="status-badge status-badge--pass">{{ task.status }}</span></td>
                <td>
                  <code>{{ task.closureEvidenceKind }} {{ task.closureEvidenceId }}</code>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </article>

      <article class="evidence-block">
        <header class="evidence-head">
          <strong>冻结与导出产物</strong>
          <span class="evidence-stamp">{{ snapshotNotice(snapshot) }}</span>
        </header>
        <div class="table-scroll">
          <table class="g3-table g3-table--compact">
            <thead>
              <tr><th>产物</th><th>状态</th><th>大小</th><th>SHA-256</th></tr>
            </thead>
            <tbody>
              <tr v-for="artifact in snapshot.exports" :key="artifact.id">
                <th scope="row"><code>{{ artifact.id }}</code></th>
                <td>{{ artifact.status }}</td>
                <td>{{ artifact.bytes }} 字节</td>
                <td>
                  <code>{{ artifact.fileSha256 }}</code>
                  <small v-if="artifact.identicalAfterRestart">
                    重启后逐字节一致（{{ artifact.bytesAfterRestart }} 字节）
                  </small>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </article>

      <article class="evidence-block">
        <header class="evidence-head">
          <strong>当时的真实 HTTP 链路</strong>
          <span class="evidence-stamp">{{ snapshotNotice(snapshot) }}</span>
        </header>
        <ol class="chain-list">
          <li v-for="step in snapshot.chain" :key="step.step" class="chain-step">
            <span class="chain-number">{{ String(step.step).padStart(2, '0') }}</span>
            <div>
              <strong>{{ step.name }}</strong>
              <p class="chain-endpoint">
                <span class="method" :class="`method--${step.method.toLowerCase()}`">{{ step.method }}</span>
                <code>{{ step.path }}</code>
              </p>
              <p class="chain-result">{{ step.result }}</p>
            </div>
          </li>
        </ol>
      </article>

      <p class="card-footnote">
        原始证据目录：<code>{{ snapshot.sourceDir }}</code>。
        以上全部是**历史快照 · 非本次运行**：本页不发起任何请求，也不重放那条链路。
      </p>
    </section>

    <footer class="console-footer">
      <span>判据基准 · 状态词表 · 双栏结构</span>
      <span>G3 验收台-1 · 骨架、判据基准与免登录快照模式</span>
    </footer>
  </main>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useAuthStore } from '@/stores/auth'
import {
  G3_LIFECYCLE_READINGS,
  G3_MATRIX_ROWS,
  G3_SATISFIED_READING,
  formatAnchor,
  statusCounts,
  type SpecAnchor,
} from './g3Matrix'
import { G3_FLOW_EXECUTION_STATE, G3_FLOW_STEPS } from './g3Flow'
import {
  G3_DOMAIN_DESCRIPTIONS,
  G3_RESPONSIBILITY_DOMAINS,
  G3_SEVERITIES,
  G3_SEVERITY_DESCRIPTIONS,
  G3_STATUS_VALUES,
  IMPACT_TASK_STATUSES,
  statusSummary,
  statusTone,
  type G3Status,
} from './g3Status'
import { G3_SNAPSHOT_RAW, loadG3Snapshot, snapshotNotice } from './g3Snapshot'
import { createRequestClient } from './g3HttpAdapter'
import {
  applyRunEvidence,
  appendRunHistory,
  contextFromSummary,
  contextsFromSummaries,
  headChangedSince,
  loadRunHistory,
  loadRunSummary,
  saveRunHistory,
  saveRunSummary,
  summarizeRun,
  type G3RowEvidence,
  type G3RunSummary,
} from './g3Evidence'
import {
  G3_CONCLUSION_SOURCE_LABELS,
  buildChainEvidenceExport,
  conclusionSourceOf,
  exportFileName,
  type G3ConclusionSource,
} from './g3Export'
import {
  G3_GAP_LIST_ISSUE,
  G3_IMPLEMENTATION_GAPS,
  gapIdsForRow,
  limitingGapIdsForRow,
} from './g3Gaps'
import { runChain } from './runner/chain'
import { runChangeSetChain, toLifecycleSnapshot, type G3LifecycleSnapshot } from './runner/changeset'
import {
  runDependencyChain,
  toDependencySnapshot,
  type G3DependencySnapshot,
} from './runner/dependencies'
import {
  DOCX_BODY_HEADING_HINT,
  runStabilizationChain,
  toStabilizationSnapshot,
  type G3StabilizationSnapshot,
} from './runner/stabilization'
import {
  runModelChain,
  toModelSnapshot,
  type G3ModelRunFact,
  type G3ModelSnapshot,
} from './runner/model'
import {
  runBlockedChain,
  toBlockedSnapshot,
  type G3BlockedSnapshot,
} from './runner/blocked'
import {
  runRoadmapChain,
  toRoadmapSnapshot,
  type G3RoadmapSnapshot,
} from './runner/roadmap'
import {
  runFaultInjectionChain,
  toFaultSnapshot,
  type G3FaultSnapshot,
} from './runner/faultInjection'
import {
  runAssistantChain,
  toAssistantSnapshot,
  type G3AssistantSnapshot,
} from './runner/assistant'
import { createDemoAccount, loadDemoAccount, saveDemoAccount, type G3DemoAccount } from './runner/identity'
import { createRunId, type G3BackendHead, type G3ChapterFact, type G3RunContext, type G3RunVerdict, type G3StepRecord } from './runner/runContext'

// 快照在装载期就校验：数据坏了就抛错，不让页面渲染半份证据。
const snapshot = loadG3Snapshot(G3_SNAPSHOT_RAW)

const auth = useAuthStore()
const client = createRequestClient()

const running = ref(false)
const runError = ref('')
const staleNotice = ref('')
const sessionTakenOver = ref(false)
const savedAccount = ref<G3DemoAccount | null>(null)
const runSummary = ref<G3RunSummary | null>(null)
const previousSummary = ref<G3RunSummary | null>(null)
/**
 * 本次会话累计的运行段。
 *
 * 页面上有几段链（变更集 / 依赖 / 稳定化 / 阻断 / 模型），每段跑在自己的
 * 隔离项目上。**结论要累计**：跑完变更集链再跑模型链之后，右栏矩阵仍要
 * 保留变更集链那一段的判定，而不是被后一段覆盖掉。
 */
const runHistory = ref<G3RunSummary[]>([])
const liveRunId = ref('')
const liveAccountEmail = ref('')
const liveProjectId = ref('')
const liveProjectName = ref('')
const liveChapters = ref<G3ChapterFact[]>([])
const liveHead = ref<G3BackendHead | null>(null)
const liveSteps = ref<G3StepRecord[]>([])
const missingSections = ref<string[]>([])
const lifecycle = ref<G3LifecycleSnapshot | null>(null)
const lifecycleError = ref('')
const lifecycleRunning = ref(false)
const dependency = ref<G3DependencySnapshot | null>(null)
const dependencyError = ref('')
const dependencyRunning = ref(false)
const stabilization = ref<G3StabilizationSnapshot | null>(null)
const stabilizationError = ref('')
const stabilizationRunning = ref(false)
const model = ref<G3ModelSnapshot | null>(null)
const modelError = ref('')
const modelRunning = ref(false)
/** 上一次导出的回执：让「导出点了之后到底产出了什么」在页面上可见、可核对。 */
const lastExport = ref<{
  fileName: string
  bytes: number
  runId: string
  head: string | null
  rowCount: number
  verdictCount: number
  gapCount: number
  /** 这一次导出并进去的运行段数。 */
  runCount: number
  downloadNote: string
} | null>(null)
const blocked = ref<G3BlockedSnapshot | null>(null)
const blockedError = ref('')
const blockedRunning = ref(false)
/** 路线图编辑会话链（#295）。 */
const roadmap = ref<G3RoadmapSnapshot | null>(null)
const roadmapError = ref('')
const roadmapRunning = ref(false)
/** 受控故障注入的可观测口径（#295）。 */
const faultInjection = ref<G3FaultSnapshot | null>(null)
const faultInjectionError = ref('')
const faultInjectionRunning = ref(false)
/** 助手两组说明与依赖候选批次的读回（#295）。 */
const assistant = ref<G3AssistantSnapshot | null>(null)
const assistantError = ref('')
const assistantRunning = ref(false)

/** 接管会话之前先把用户原来的令牌记住，好还回去。 */
let originalToken: string | null = null
let originalTenantId: string | null = null

const liveMode = computed(() => liveSteps.value.length > 0)
const liveIdentityTitle = computed(() =>
  liveAccountEmail.value ? `演示账号 ${liveAccountEmail.value}` : '真实模式',
)
const previousHead = computed(() => previousSummary.value?.head ?? null)

const liveContext = computed(() =>
  runSummary.value ? contextFromSummary(runSummary.value) : null,
)
/**
 * 本次会话累计的运行上下文。
 *
 * 矩阵投影读的是**这一份清单**，不是最后一次运行：
 * 跑完变更集链再跑模型链之后，变更集链那一段的判定仍然留在矩阵里。
 */
const sessionContexts = computed(() => contextsFromSummaries(runHistory.value))
const matrixView = computed(() =>
  applyRunEvidence(G3_MATRIX_ROWS, sessionContexts.value.length > 0
    ? sessionContexts.value
    : (liveContext.value ?? {
      runId: '',
      startedAt: '',
      accountEmail: '',
      steps: [],
      head: null,
      chapters: [],
      stale: false,
    })),
)
const sliceRows = computed(() => matrixView.value.rows.filter((row) => row.group === 'slice'))
const acRows = computed(() => matrixView.value.rows.filter((row) => row.group === 'ac'))
const counts = computed(() => statusCounts(matrixView.value.rows))
const coverageById = computed(() => {
  const map = new Map<string, G3RowEvidence>()
  for (const item of matrixView.value.evidence) {
    map.set(item.rowId, item)
  }
  return map
})

/**
 * 覆盖徽标：只有被**判定**的行才算覆盖，其余一律「未被覆盖」。
 *
 * 口径是**本次会话累计**：页面上跑过的每一段链（变更集 / 依赖 / 稳定化 /
 * 阻断 / 模型）留下的判定都算数，后跑的一段不会抹掉前一段的结论。
 */
const coverageLabel = (item?: G3RowEvidence) => {
  if (!item) return '未被覆盖'
  if (item.decided) return '已判定'
  if (item.covered) return '已跑到'
  return '未被覆盖'
}
const coverageTone = (item?: G3RowEvidence) => {
  if (!item) return 'coverage-chip--none'
  if (item.decided) return 'coverage-chip--covered'
  if (item.covered) return 'coverage-chip--partial'
  return 'coverage-chip--none'
}

/** 这一行的结论来自哪一段运行（多段累计时要说清，不能让人以为是同一段）。 */
const coverageRunId = (rowId: string): string => {
  const item = coverageById.value.get(rowId)
  if (!item?.decided) return ''
  return item.runId ?? ''
}

/**
 * 这一行的结论跑在哪个后端 head 上。
 *
 * 证据位（矩阵登记的那一份）里**不写 head**：head 每次重跑都会变，
 * 写死等于写下「下一次就过期」的串。真正的那一次运行的 head 由运行上下文
 * 带出来，在这一列显示——读者看到的运行标识与 head 永远是真的。
 */
const coverageHead = (rowId: string): string => {
  const item = coverageById.value.get(rowId)
  if (!item?.decided) return ''
  return item.head ?? ''
}

const anchorLabel = (anchor: SpecAnchor) => formatAnchor(anchor)
const anchorPath = (anchor: SpecAnchor) => anchor.path

/** 实现缺口清单（权威副本是缺口清单 Issue #246）。 */
const gapList = G3_IMPLEMENTATION_GAPS

/** 某一行涉及的直接缺口编号。 */
const gapIdsOf = (rowId: string) => gapIdsForRow(rowId)

/** 某一行涉及的「覆盖受限」缺口编号。 */
const limitingGapIdsOf = (rowId: string) => limitingGapIdsForRow(rowId)

/**
 * 一行的结论来源：本次运行得出 / 非本次运行（历史快照或既有实跑）/ 尚未判定。
 *
 * 两者在页面上**视觉与文案互斥**：本次运行用青色实心标记，非本次运行用琥珀色
 * 标记（与页头、证据块的既有标识一致）。
 *
 * 切片行的静态状态记的是「该行最后一次被真实判定得到的结论」，所以它们在
 * 本次会话没跑那条链时是「非本次运行」——**不是**「尚未判定」。
 */
const sourceOf = (rowId: string): G3ConclusionSource => {
  const row = matrixView.value.rows.find((item) => item.id === rowId)
  if (!row) return 'not_judged'
  const decided = coverageById.value.get(rowId)?.decided === true
  return conclusionSourceOf(row, decided)
}

const sourceLabel = (source: G3ConclusionSource) => G3_CONCLUSION_SOURCE_LABELS[source]

const sourceTone = (source: G3ConclusionSource) => {
  if (source === 'this_run') return 'source-chip--live'
  if (source === 'historical_snapshot') return 'source-chip--snapshot'
  return 'source-chip--none'
}

/** 导出文件名的日期戳（沿用 08 实施文档目录的族命名）。 */
function dateStamp(): string {
  const now = new Date()
  const pad = (value: number) => String(value).padStart(2, '0')
  return `${now.getFullYear()}${pad(now.getMonth() + 1)}${pad(now.getDate())}`
}

/**
 * 判定卡徽标上的状态。
 *
 * 判定卡**自带**状态时以它为准：例如 AC-G3-04 只部分成立时，
 * 判定卡带的是 `PASS (follow-up)`，不能因为 `holds=false` 就显示成
 * `NOT RUN`——那会把「已判定但有缺口」说成「没跑」。
 */
const verdictStatus = (verdict: G3RunVerdict): G3Status =>
  verdict.status ?? (verdict.holds ? 'PASS' : 'NOT RUN')

/** 判定卡外框的色调：完全成立 / 部分成立（有跟进） / 其余。 */
const verdictCardClass = (verdict: G3RunVerdict): string => {
  const status = verdictStatus(verdict)
  if (status === 'PASS') return 'verdict-card--ok'
  if (status === 'PASS (follow-up)' || status === 'WARNING') return 'verdict-card--warn'
  return 'verdict-card--bad'
}

/** 变更集状态的显示色调（域内状态，不是门禁词表）。 */
const changeSetTone = (status: string) => {
  if (status === 'applied') return 'status-badge--pass'
  if (status === 'stale') return 'status-badge--not-run'
  if (status === 'rejected') return 'status-badge--warning'
  if (status === 'assessed') return 'status-badge--follow-up'
  return 'status-badge--todo'
}

/** 变更集表：四条记录（局部影响、并发、重评估、逆向）。 */
const changeSetRows = computed(() => {
  const snapshot = lifecycle.value
  if (!snapshot) return []
  const rows = [
    { label: '局部影响（研究目标）', fact: snapshot.changeSets.scoped },
    { label: '并发（同一基线）', fact: snapshot.changeSets.concurrent },
    { label: '重评估记录（原记录留历史）', fact: snapshot.changeSets.reassessed },
    { label: '逆向撤销', fact: snapshot.changeSets.reversed },
  ]
  return rows.map((row) => ({
    id: row.fact.id,
    label: row.label,
    status: row.fact.status,
    staleReason: row.fact.staleReason,
    revisions:
      `上下文 ${row.fact.baseContextRevision} → ${row.fact.targetContextRevision ?? '—'}` +
      ` · 规格 ${row.fact.baseSpecRevision} → ${row.fact.targetSpecRevision ?? '—'}`,
    affected: row.fact.impacts.length
      ? row.fact.impacts.map((impact) => impact.title || impact.chapter_id).join('、')
      : '（无受影响章节）',
    lineage:
      [
        row.fact.reversesChangeSetId ? `reverses=${row.fact.reversesChangeSetId}` : '',
        row.fact.reassessedFromId ? `reassessed_from=${row.fact.reassessedFromId}` : '',
        row.fact.staleReason ? `stale_reason=${row.fact.staleReason}` : '',
      ]
        .filter(Boolean)
        .join(' · ') || '—',
  }))
})

/** 章节确认表：受影响章节展示「失效 → 重新确认」两步。 */
const chapterRows = computed(() => {
  const snapshot = lifecycle.value
  if (!snapshot) return []
  const affected = snapshot.chapters
  return [
    {
      id: affected.affectedBefore.chapterId,
      sectionId: affected.affectedBefore.sectionId,
      title: affected.affectedBefore.title,
      beforeVersion: affected.affectedBefore.versionConfirmationValid,
      beforeConfirmation: affected.affectedBefore.currentConfirmation
        ? `id=${affected.affectedBefore.currentConfirmation.id} · 规格修订 ${affected.affectedBefore.currentConfirmation.specRevision}`
        : '（无）',
      afterVersion: affected.affectedAfter.versionConfirmationValid,
      afterConfirmation: affected.affectedAfter.currentConfirmation
        ? `id=${affected.affectedAfter.currentConfirmation.id} · 规格修订 ${affected.affectedAfter.currentConfirmation.specRevision}`
        : '（无）',
      steps: [
        {
          title: '失效',
          tone: 'mini-step--bad',
          text:
            `应用后章节版本的确认标记 ${affected.affectedBefore.versionConfirmationValid} → ` +
            `${affected.affectedAfter.versionConfirmationValid}；当前确认：${affected.affectedAfter.currentConfirmation ? '仍在' : '已失效（待复核）'}`,
        },
        {
          title: '重新确认',
          tone: affected.affectedReconfirmed.currentConfirmation ? 'mini-step--ok' : 'mini-step--idle',
          text: affected.affectedReconfirmed.currentConfirmation
            ? `复写后按规格修订 ${affected.affectedReconfirmed.currentConfirmation.specRevision} 与章节版本 ` +
              `${affected.affectedReconfirmed.currentConfirmation.chapterVersionId} 确认 → id=${affected.affectedReconfirmed.currentConfirmation.id}`
            : '尚未重新确认',
        },
      ],
    },
    {
      id: affected.unaffectedBefore.chapterId,
      sectionId: affected.unaffectedBefore.sectionId,
      title: affected.unaffectedBefore.title,
      beforeVersion: affected.unaffectedBefore.versionConfirmationValid,
      beforeConfirmation: affected.unaffectedBefore.currentConfirmation
        ? `id=${affected.unaffectedBefore.currentConfirmation.id} · 规格修订 ${affected.unaffectedBefore.currentConfirmation.specRevision}`
        : '（无）',
      afterVersion: affected.unaffectedAfter.versionConfirmationValid,
      afterConfirmation: affected.unaffectedAfter.currentConfirmation
        ? `id=${affected.unaffectedAfter.currentConfirmation.id} · 规格修订 ${affected.unaffectedAfter.currentConfirmation.specRevision}`
        : '（无）',
      steps: [
        {
          title: '未被本次变更集失效',
          tone: 'mini-step--ok',
          text:
            `未列入受影响范围：章节版本确认标记 ${affected.unaffectedBefore.versionConfirmationValid} → ` +
            `${affected.unaffectedAfter.versionConfirmationValid}，没有为它产生影响任务`,
        },
        {
          title: '口径差异（如实标注）',
          tone: 'mini-step--idle',
          text: affected.unaffectedAfter.currentConfirmation
            ? '当前确认仍在'
            : '服务端在规格修订推进后，对所有章节的当前确认都判为失效（确认绑定的规格修订落后）',
        },
      ],
    },
  ]
})

/** 非 Owner 权限探测表。 */
const permissionRows = computed(() => {
  const probe = lifecycle.value?.permissionProbe
  if (!probe) return []
  return [
    { label: '读变更集', status: probe.read.status, code: probe.read.code },
    { label: '应用变更集', status: probe.apply.status, code: probe.apply.code },
    { label: '拒绝变更集', status: probe.reject.status, code: probe.reject.code },
  ]
})

/** 依赖边表：已确认 / 建议 / 拒绝三种语义各自成行。 */
const dependencyEdgeRows = computed(() => {
  const snapshot = dependency.value
  if (!snapshot) return []
  return snapshot.edges.map((edge) => ({
    id: edge.id,
    relation: edge.relation,
    action: edge.action,
    origin: edge.origin,
    status: edge.status,
    source: `${edge.source.kind}/${edge.source.id}`,
    target: `${edge.target.kind}/${edge.target.id}`,
    version: edge.version,
    reason: edge.reason,
    superseded: edge.supersededRelation ? `${edge.supersededRelation} → ${edge.relation}` : '',
  }))
})

/** 影响任务表：状态迁移按服务端回应如实呈现。 */
const dependencyTaskRows = computed(() => {
  const snapshot = dependency.value
  if (!snapshot) return []
  return snapshot.transitions.map((item) => ({
    id: item.taskId,
    action: item.action,
    severity: item.severity,
    from: item.from,
    to: item.to,
    status: item.status,
    code: item.code,
    note: item.note,
    evidence: item.closureEvidence
      .map((ref) => `${ref.kind}/${ref.id}@${ref.version}`)
      .join('、'),
  }))
})

/** 依赖链的最终任务状态。 */
const dependencyFinalTasks = computed(() => {
  const snapshot = dependency.value
  if (!snapshot) return []
  return snapshot.tasks.map((task) => ({
    id: task.id,
    action: task.action,
    severity: task.severity,
    status: task.status,
    domain: task.responsibilityDomain,
    causes: task.causes.length,
    target: `${task.target.kind}/${task.target.id}`,
    staleReason: task.staleReason,
    disposition: task.disposition,
  }))
})

/**
 * 非确定性结论表：三条依赖真实模型的验收项**单独成列**。
 *
 * 状态、模型运行标识与耗时都来自本轮真实运行；模型不可用时状态是 NOT RUN，
 * 并写明缺外部凭据——其余行（确定性结论）不受影响，照常判定。
 */
const modelRows = computed(() => {
  const snapshot = model.value
  if (!snapshot) return []
  const verdicts = new Map(snapshot.verdicts.map((item) => [item.id, item]))
  const build = (
    id: string,
    name: string,
    run: G3ModelRunFact | null,
    detail: string,
  ) => {
    const verdict = verdicts.get(id)
    return {
      id,
      name,
      status: verdict?.status ?? 'NOT RUN',
      runId: run?.runId ?? '（无）',
      modelId: run?.modelId || '（未登记）',
      runMode: run?.runMode || '（未登记）',
      durationMs: run?.durationMs ?? 0,
      holds: verdict?.holds ?? false,
      detail: verdict?.detail ?? detail,
      evidence: verdict?.evidence ?? [],
    }
  }
  return [
    build('AC-G3-06', '旧异步结果（历史候选）', snapshot.ac06.run, snapshot.ac06.detail),
    build('AC-G3-14', '影响助手：自然语言 → 变更草稿（模型侧）', snapshot.ac14.run, snapshot.ac14.detail),
    build('AC-G3-15', '模型批量候选依赖 → 确认 / 修改 / 拒绝（模型侧）', snapshot.ac15.run, snapshot.ac15.detail),
  ]
})

/**
 * 稳定化阻断与恢复链的五次结果。
 *
 * 顺序就是这条链的状态机：阻断 → 规则问题处置后 → 阻断项闭环后 →
 * 目标对象版本被替换后（不放行） → 重投闭环后。
 */
const blockedStabilizeRows = computed(() => {
  const snapshot = blocked.value
  if (!snapshot) return []
  return [
    {
      key: 'blocked',
      title: '阻断态（首次稳定化）',
      note: '200 成功响应；阻断原因逐条带出，不折叠成一句失败',
      http: 200,
      code: '',
      fact: snapshot.blocked,
    },
    {
      key: 'after-disposition',
      title: '规则问题处置后',
      note: '人工处置只是工作流元数据，求值结论不变',
      http: 200,
      code: '',
      fact: snapshot.afterDisposition,
    },
    {
      key: 'stabilized',
      title: '阻断项闭环后',
      note: '按真实闭环证据完成之后再次稳定化',
      http: 200,
      code: '',
      fact: snapshot.stabilized,
    },
    {
      key: 'version-mismatch',
      title: '目标对象版本被替换后',
      note: '这一版修订没有被放行：服务端重读闭环证据时发现版本不匹配',
      http: snapshot.mismatchStatus,
      code: snapshot.mismatchCode,
      fact: snapshot.versionMismatch,
    },
    {
      key: 'final',
      title: '重投闭环后',
      note: '按新基线重投对应影响任务并闭环之后',
      http: 200,
      code: '',
      fact: snapshot.finalStabilized,
    },
  ]
})

/** 规则问题：处置前 → 处置后（问题逐条列出，不折叠）。 */
const blockedIssueRows = computed(() => {
  const snapshot = blocked.value
  if (!snapshot) return []
  const after = new Map(snapshot.checkAfter.issues.map((issue) => [issue.id, issue]))
  return snapshot.checkBefore.issues.map((issue) => {
    const later = after.get(issue.id)
    return {
      id: issue.id,
      ruleId: issue.ruleId,
      severity: issue.severity,
      targetId: issue.targetId,
      message: issue.message,
      dispositionAction: later?.dispositionAction || issue.dispositionAction,
      dispositionReason: later?.dispositionReason || issue.dispositionReason,
      afterNote: later
        ? '处置后仍在问题列表里（带上了处置）'
        : '处置后已不在问题列表里',
    }
  })
})

/** 「接受本次不处理」的边界：阻断级不可接受，非阻断级可接受。 */
const blockedDismissRows = computed(() => blocked.value?.dismissAttempts ?? [])

/** 阻断项闭环过程：任务状态迁移。 */
const blockedTransitionRows = computed(() => {
  const snapshot = blocked.value
  if (!snapshot) return []
  return snapshot.transitions.map((item) => ({
    taskId: item.taskId,
    action: item.action,
    severity: item.severity,
    from: item.from,
    to: item.to,
    status: item.status,
    code: item.code,
    note: item.note,
    evidence: item.closureEvidence.map((ref) => `${ref.kind}/${ref.id}@${ref.version}`).join('、'),
  }))
})

/** 被新版本替换掉的任务。 */
const blockedStaleRows = computed(() => blocked.value?.staleTasks ?? [])

/** 读一次本地状态：演示账号与上一次运行。 */
function restoreLocalState(): void {
  try {
    savedAccount.value = loadDemoAccount(localStorage)
    runSummary.value = loadRunSummary(localStorage)
    // 累计清单：本次会话跑过的每一段都在里面，刷新后矩阵仍能保留前几段的判定。
    runHistory.value = loadRunHistory(localStorage)
    if (runSummary.value) {
      liveSteps.value = runSummary.value.steps
      liveHead.value = runSummary.value.head
      liveRunId.value = runSummary.value.runId
      liveAccountEmail.value = runSummary.value.accountEmail
      liveProjectId.value = runSummary.value.projectId ?? ''
      liveProjectName.value = runSummary.value.projectName ?? ''
      liveChapters.value = runSummary.value.chapters
      missingSections.value = runSummary.value.missingSections
    }
    // 各段快照：取累计清单里**最后一次**带该快照的那一段。
    const latest = <T extends keyof G3RunSummary>(key: T): G3RunSummary[T] | undefined => {
      for (let i = runHistory.value.length - 1; i >= 0; i -= 1) {
        const value = runHistory.value[i][key]
        if (value !== undefined && value !== null) return value as G3RunSummary[T]
      }
      return undefined
    }
    lifecycle.value = latest('lifecycle') ?? null
    dependency.value = latest('dependency') ?? null
    stabilization.value = latest('stabilization') ?? null
    model.value = latest('model') ?? null
    blocked.value = latest('blocked') ?? null
  } catch {
    // 本地存储不可用时如实当作「没有历史」，不假装有。
    savedAccount.value = null
    runSummary.value = null
    runHistory.value = []
  }
}
restoreLocalState()

/**
 * 提交一次运行的结果：摘要落盘 + 左栏实时状态 + 并入本次会话的累计清单。
 *
 * 前置链与变更集生命周期链共用这一段，所以两条链留下的证据进同一份摘要，
 * 矩阵的「本轮覆盖」也读同一份步骤表。
 *
 * **每一段都并进累计清单**（而不是覆盖上一段）：页面上有几段链，各跑在自己的
 * 隔离项目上，跑完后一段不能把前一段的判定从矩阵里抹掉。
 */
function commitRun(
  result: Awaited<ReturnType<typeof runChain>>,
  account: G3DemoAccount,
  registerFirst: boolean,
  snapshot?: G3LifecycleSnapshot,
  dependencySnapshot?: G3DependencySnapshot,
  stabilizationSnapshot?: G3StabilizationSnapshot,
  modelSnapshot?: G3ModelSnapshot,
  blockedSnapshot?: G3BlockedSnapshot,
): void {
  if (registerFirst) {
    saveDemoAccount(localStorage, account)
    savedAccount.value = account
  }

  const previous = runSummary.value
  const change = headChangedSince(previous, result.ctx.head)
  if (change.changed && previous) {
    previousSummary.value = { ...previous, stale: true, staleReason: change.reason }
    staleNotice.value = change.reason
  } else {
    previousSummary.value = previous
  }

  const summary = summarizeRun(result.ctx, {
    missingSections: result.missingSections,
    lifecycle: snapshot,
    dependency: dependencySnapshot,
    stabilization: stabilizationSnapshot,
    model: modelSnapshot,
    blocked: blockedSnapshot,
  })
  runSummary.value = summary
  saveRunSummary(localStorage, summary)
  // 累计清单：每一段都留一份，矩阵与整链证据都读它。
  runHistory.value = appendRunHistory(runHistory.value, summary)
  saveRunHistory(localStorage, runHistory.value)

  liveSteps.value = result.ctx.steps
  liveHead.value = result.ctx.head
  liveRunId.value = result.ctx.runId
  liveAccountEmail.value = account.email
  liveProjectId.value = result.project.id
  liveProjectName.value = result.project.name
  liveChapters.value = result.chapters
  missingSections.value = result.missingSections
  // 各段快照**只更新自己那一段**：跑完模型链不该把变更集链的快照清空。
  if (snapshot) lifecycle.value = snapshot
  if (dependencySnapshot) dependency.value = dependencySnapshot
  if (stabilizationSnapshot) stabilization.value = stabilizationSnapshot
  if (modelSnapshot) model.value = modelSnapshot
  if (blockedSnapshot) blocked.value = blockedSnapshot
}

/**
 * 跑一次真实链。
 *
 * @param registerFirst true = 运行时自建新账号；false = 复用本地已存的账号。
 */
async function startRun(registerFirst: boolean): Promise<void> {
  if (running.value) return
  running.value = true
  runError.value = ''
  staleNotice.value = ''

  const account = registerFirst ? createDemoAccount() : (savedAccount.value ?? createDemoAccount())
  const runId = createRunId()

  try {
    if (originalToken === null) {
      originalToken = localStorage.getItem('weknora_token')
      originalTenantId = localStorage.getItem('weknora_selected_tenant_id')
    }
    // 演示账号是另一个租户；先把激活空间摘掉，避免把别的空间的 X-Tenant-ID 带过去。
    auth.setSelectedTenant(null, null)

    const result = await runChain(client, {
      account,
      registerFirst,
      runId,
      onToken: (token) => {
        // 令牌写进既有认证存储，请求拦截器随后自动带上 Authorization。
        auth.setToken(token)
        sessionTakenOver.value = true
      },
      onIdentity: (identity) => {
        const tenantId = Number(identity.tenantId)
        if (Number.isFinite(tenantId) && tenantId > 0) {
          auth.setSelectedTenant(tenantId, `${identity.username} 的验收台空间`)
        }
      },
    })

    commitRun(result, account, registerFirst)
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error)
    runError.value = message
    // 失败也要留下已经跑过的那几步，不能让人以为什么都没发生。
    const failed = (error as { runContext?: G3RunContext } | null)?.runContext
    if (failed) {
      liveSteps.value = failed.steps
      liveHead.value = failed.head
      liveRunId.value = failed.runId
      liveAccountEmail.value = account.email
    }
  } finally {
    running.value = false
  }
}

/**
 * 跑一次变更集生命周期链。
 *
 * 复用同一份执行器（`runner/changeset.ts`）：页面与 node 脚本跑的是同一套代码，
 * 所以页面上的判定与落盘的证据不会走样。
 */
async function startLifecycle(): Promise<void> {
  if (lifecycleRunning.value || running.value) return
  lifecycleRunning.value = true
  lifecycleError.value = ''

  const account = savedAccount.value ?? createDemoAccount()
  const registerFirst = !savedAccount.value
  const runId = createRunId()

  try {
    if (originalToken === null) {
      originalToken = localStorage.getItem('weknora_token')
      originalTenantId = localStorage.getItem('weknora_selected_tenant_id')
    }
    auth.setSelectedTenant(null, null)

    const result = await runChain(client, {
      account,
      registerFirst,
      runId,
      onToken: (token) => {
        auth.setToken(token)
        sessionTakenOver.value = true
      },
      onIdentity: (identity) => {
        const tenantId = Number(identity.tenantId)
        if (Number.isFinite(tenantId) && tenantId > 0) {
          auth.setSelectedTenant(tenantId, `${identity.username} 的验收台空间`)
        }
      },
    })

    const lifecycleResult = await runChangeSetChain(result.ctx, client, result.token, {
      projectId: result.project.id,
      chapters: result.chapters.map((chapter) => ({
        id: chapter.id,
        sectionId: chapter.sectionId,
        title: chapter.title,
      })),
      tenantId: result.identity.tenantId,
      withNonOwnerToken: async (token, run) => {
        // 浏览器侧的请求拦截器会用存储里的令牌覆盖 Authorization，
        // 所以要以非 Owner 身份发请求，必须把令牌临时换过去，再换回来。
        const previous = localStorage.getItem('weknora_token')
        auth.setToken(token)
        try {
          return await run()
        } finally {
          if (previous) {
            auth.setToken(previous)
          } else {
            localStorage.removeItem('weknora_token')
          }
        }
      },
    })

    const snapshot = toLifecycleSnapshot(lifecycleResult)
    lifecycle.value = snapshot
    commitRun(result, account, registerFirst, snapshot)
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error)
    lifecycleError.value = message
    const failed = (error as { runContext?: G3RunContext } | null)?.runContext
    if (failed) {
      liveSteps.value = failed.steps
      liveHead.value = failed.head
      liveRunId.value = failed.runId
    }
  } finally {
    lifecycleRunning.value = false
  }
}

/**
 * 跑一次「依赖图 → 影响评估 → 影响任务」链。
 *
 * 复用同一份执行器（`runner/dependencies.ts`）：页面与 node 脚本跑的是同一套代码，
 * 所以页面上的判定与落盘的证据不会走样。
 */
async function startDependency(): Promise<void> {
  if (dependencyRunning.value || running.value) return
  dependencyRunning.value = true
  dependencyError.value = ''

  const account = savedAccount.value ?? createDemoAccount()
  const registerFirst = !savedAccount.value
  const runId = createRunId()

  try {
    if (originalToken === null) {
      originalToken = localStorage.getItem('weknora_token')
      originalTenantId = localStorage.getItem('weknora_selected_tenant_id')
    }
    auth.setSelectedTenant(null, null)

    const result = await runChain(client, {
      account,
      registerFirst,
      runId,
      onToken: (token) => {
        auth.setToken(token)
        sessionTakenOver.value = true
      },
      onIdentity: (identity) => {
        const tenantId = Number(identity.tenantId)
        if (Number.isFinite(tenantId) && tenantId > 0) {
          auth.setSelectedTenant(tenantId, `${identity.username} 的验收台空间`)
        }
      },
    })

    const dependencyResult = await runDependencyChain(result.ctx, client, result.token, {
      projectId: result.project.id,
      chapters: result.chapters.map((chapter) => ({
        id: chapter.id,
        sectionId: chapter.sectionId,
        title: chapter.title,
      })),
      operatorId: result.identity.userId,
    })

    const snapshot = toDependencySnapshot(dependencyResult)
    dependency.value = snapshot
    commitRun(result, account, registerFirst, undefined, snapshot)
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error)
    dependencyError.value = message
    const failed = (error as { runContext?: G3RunContext } | null)?.runContext
    if (failed) {
      liveSteps.value = failed.steps
      liveHead.value = failed.head
      liveRunId.value = failed.runId
    }
  } finally {
    dependencyRunning.value = false
  }
}

/**
 * 跑一次「稳定化 → 交付闭环」链。
 *
 * 复用同一份执行器（`runner/stabilization.ts`）：页面与 node 脚本跑的是同一套代码，
 * 所以页面上的判定与落盘的证据不会走样。
 */
async function startStabilization(): Promise<void> {
  if (stabilizationRunning.value || running.value) return
  stabilizationRunning.value = true
  stabilizationError.value = ''

  const account = savedAccount.value ?? createDemoAccount()
  const registerFirst = !savedAccount.value
  const runId = createRunId()

  try {
    if (originalToken === null) {
      originalToken = localStorage.getItem('weknora_token')
      originalTenantId = localStorage.getItem('weknora_selected_tenant_id')
    }
    auth.setSelectedTenant(null, null)

    const result = await runChain(client, {
      account,
      registerFirst,
      runId,
      onToken: (token) => {
        auth.setToken(token)
        sessionTakenOver.value = true
      },
      onIdentity: (identity) => {
        const tenantId = Number(identity.tenantId)
        if (Number.isFinite(tenantId) && tenantId > 0) {
          auth.setSelectedTenant(tenantId, `${identity.username} 的验收台空间`)
        }
      },
    })

    const stabilizationResult = await runStabilizationChain(result.ctx, client, result.token, {
      projectId: result.project.id,
      chapters: result.chapters.map((chapter) => ({
        id: chapter.id,
        sectionId: chapter.sectionId,
        title: chapter.title,
      })),
      operatorId: result.identity.userId,
    })

    const snapshot = toStabilizationSnapshot(stabilizationResult)
    stabilization.value = snapshot
    commitRun(result, account, registerFirst, undefined, undefined, snapshot)
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error)
    stabilizationError.value = message
    const failed = (error as { runContext?: G3RunContext } | null)?.runContext
    if (failed) {
      liveSteps.value = failed.steps
      liveHead.value = failed.head
      liveRunId.value = failed.runId
    }
  } finally {
    stabilizationRunning.value = false
  }
}

/**
 * 跑一次「真实模型」验收链。
 *
 * 复用同一份执行器（`runner/model.ts`）：页面与 node 脚本跑的是同一套代码，
 * 所以页面上的判定与落盘的证据不会走样。
 */
async function startModel(): Promise<void> {
  if (modelRunning.value || running.value) return
  modelRunning.value = true
  modelError.value = ''

  const account = savedAccount.value ?? createDemoAccount()
  const registerFirst = !savedAccount.value
  const runId = createRunId()

  try {
    if (originalToken === null) {
      originalToken = localStorage.getItem('weknora_token')
      originalTenantId = localStorage.getItem('weknora_selected_tenant_id')
    }
    auth.setSelectedTenant(null, null)

    const result = await runChain(client, {
      account,
      registerFirst,
      runId,
      onToken: (token) => {
        auth.setToken(token)
        sessionTakenOver.value = true
      },
      onIdentity: (identity) => {
        const tenantId = Number(identity.tenantId)
        if (Number.isFinite(tenantId) && tenantId > 0) {
          auth.setSelectedTenant(tenantId, `${identity.username} 的验收台空间`)
        }
      },
    })

    const modelResult = await runModelChain(result.ctx, client, result.token, {
      projectId: result.project.id,
      chapters: result.chapters.map((chapter) => ({
        id: chapter.id,
        sectionId: chapter.sectionId,
        title: chapter.title,
      })),
      operatorId: result.identity.userId,
    })

    const snapshot = toModelSnapshot(modelResult)
    model.value = snapshot
    commitRun(result, account, registerFirst, undefined, undefined, undefined, snapshot)
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error)
    modelError.value = message
    const failed = (error as { runContext?: G3RunContext } | null)?.runContext
    if (failed) {
      liveSteps.value = failed.steps
      liveHead.value = failed.head
      liveRunId.value = failed.runId
    }
  } finally {
    modelRunning.value = false
  }
}

/**
 * 跑一次「稳定化阻断 → 处置 → 闭环 → 版本不匹配 → 重投 → 再次已稳定」链。
 *
 * 复用同一份执行器（`runner/blocked.ts`）：页面与 node 脚本跑的是同一套代码，
 * 所以页面上的判定与落盘的证据不会走样。
 */
async function startBlocked(): Promise<void> {
  if (blockedRunning.value || running.value) return
  blockedRunning.value = true
  blockedError.value = ''

  const account = savedAccount.value ?? createDemoAccount()
  const registerFirst = !savedAccount.value
  const runId = createRunId()

  try {
    if (originalToken === null) {
      originalToken = localStorage.getItem('weknora_token')
      originalTenantId = localStorage.getItem('weknora_selected_tenant_id')
    }
    auth.setSelectedTenant(null, null)

    const result = await runChain(client, {
      account,
      registerFirst,
      runId,
      onToken: (token) => {
        auth.setToken(token)
        sessionTakenOver.value = true
      },
      onIdentity: (identity) => {
        const tenantId = Number(identity.tenantId)
        if (Number.isFinite(tenantId) && tenantId > 0) {
          auth.setSelectedTenant(tenantId, `${identity.username} 的验收台空间`)
        }
      },
    })

    const blockedResult = await runBlockedChain(result.ctx, client, result.token, {
      projectId: result.project.id,
      chapters: result.chapters.map((chapter) => ({
        id: chapter.id,
        sectionId: chapter.sectionId,
        title: chapter.title,
      })),
      operatorId: result.identity.userId,
      // 人工复核那半段另起一个隔离项目跑，避免复核类任务让主链到不了「已稳定」。
      runReviewProbe: true,
    })

    const snapshot = toBlockedSnapshot(blockedResult)
    blocked.value = snapshot
    commitRun(result, account, registerFirst, undefined, undefined, undefined, undefined, snapshot)
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error)
    blockedError.value = message
    const failed = (error as { runContext?: G3RunContext } | null)?.runContext
    if (failed) {
      liveSteps.value = failed.steps
      liveHead.value = failed.head
      liveRunId.value = failed.runId
    }
  } finally {
    blockedRunning.value = false
  }
}

/**
 * 跑一次「路线图编辑会话」链。
 *
 * 复用同一份执行器（`runner/roadmap.ts`）：页面与 node 脚本跑的是同一套代码，
 * 所以页面上的判定与落盘的证据不会走样。
 */
async function startRoadmap(): Promise<void> {
  if (roadmapRunning.value || running.value) return
  roadmapRunning.value = true
  roadmapError.value = ''

  const account = savedAccount.value ?? createDemoAccount()
  const registerFirst = !savedAccount.value
  const runId = createRunId()

  try {
    if (originalToken === null) {
      originalToken = localStorage.getItem('weknora_token')
      originalTenantId = localStorage.getItem('weknora_selected_tenant_id')
    }
    auth.setSelectedTenant(null, null)

    const result = await runChain(client, {
      account,
      registerFirst,
      runId,
      onToken: (token) => {
        auth.setToken(token)
        sessionTakenOver.value = true
      },
      onIdentity: (identity) => {
        const tenantId = Number(identity.tenantId)
        if (Number.isFinite(tenantId) && tenantId > 0) {
          auth.setSelectedTenant(tenantId, `${identity.username} 的验收台空间`)
        }
      },
    })

    const roadmapResult = await runRoadmapChain(result.ctx, client, result.token, {
      projectId: result.project.id,
      operatorId: result.identity.userId,
    })

    roadmap.value = toRoadmapSnapshot(roadmapResult)
    commitRun(result, account, registerFirst, undefined, undefined, undefined, undefined, undefined)
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error)
    roadmapError.value = message
    const failed = (error as { runContext?: G3RunContext } | null)?.runContext
    if (failed) {
      liveSteps.value = failed.steps
      liveHead.value = failed.head
      liveRunId.value = failed.runId
    }
  } finally {
    roadmapRunning.value = false
  }
}

/**
 * 跑一次「受控故障注入的可观测口径」链。
 *
 * 这一段**不能**触发注入：注入是组合根按环境变量装配的夹具，不是产品端点。
 * 页面只能观测 apply 的真实响应：命中注入时是 503 + `injected_fault`，
 * 未装配时 apply 正常成功。后者一律记 NOT RUN，不得判 PASS。
 */
async function startFaultInjection(): Promise<void> {
  if (faultInjectionRunning.value || running.value) return
  faultInjectionRunning.value = true
  faultInjectionError.value = ''

  const account = savedAccount.value ?? createDemoAccount()
  const registerFirst = !savedAccount.value
  const runId = createRunId()

  try {
    if (originalToken === null) {
      originalToken = localStorage.getItem('weknora_token')
      originalTenantId = localStorage.getItem('weknora_selected_tenant_id')
    }
    auth.setSelectedTenant(null, null)

    const result = await runChain(client, {
      account,
      registerFirst,
      runId,
      onToken: (token) => {
        auth.setToken(token)
        sessionTakenOver.value = true
      },
      onIdentity: (identity) => {
        const tenantId = Number(identity.tenantId)
        if (Number.isFinite(tenantId) && tenantId > 0) {
          auth.setSelectedTenant(tenantId, `${identity.username} 的验收台空间`)
        }
      },
    })

    const faultResult = await runFaultInjectionChain(result.ctx, client, result.token, {
      projectId: result.project.id,
    })

    faultInjection.value = toFaultSnapshot(faultResult)
    commitRun(result, account, registerFirst, undefined, undefined, undefined, undefined, undefined)
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error)
    faultInjectionError.value = message
    const failed = (error as { runContext?: G3RunContext } | null)?.runContext
    if (failed) {
      liveSteps.value = failed.steps
      liveHead.value = failed.head
      liveRunId.value = failed.runId
    }
  } finally {
    faultInjectionRunning.value = false
  }
}

/**
 * 跑一次「助手两组说明与依赖候选批次」链。
 *
 * 复用同一份执行器（`runner/assistant.ts`）：页面与 node 脚本跑的是同一套代码，
 * 所以页面上的判定与落盘的证据不会走样。
 */
async function startAssistant(): Promise<void> {
  if (assistantRunning.value || running.value) return
  assistantRunning.value = true
  assistantError.value = ''

  const account = savedAccount.value ?? createDemoAccount()
  const registerFirst = !savedAccount.value
  const runId = createRunId()

  try {
    if (originalToken === null) {
      originalToken = localStorage.getItem('weknora_token')
      originalTenantId = localStorage.getItem('weknora_selected_tenant_id')
    }
    auth.setSelectedTenant(null, null)

    const result = await runChain(client, {
      account,
      registerFirst,
      runId,
      onToken: (token) => {
        auth.setToken(token)
        sessionTakenOver.value = true
      },
      onIdentity: (identity) => {
        const tenantId = Number(identity.tenantId)
        if (Number.isFinite(tenantId) && tenantId > 0) {
          auth.setSelectedTenant(tenantId, `${identity.username} 的验收台空间`)
        }
      },
    })

    const assistantResult = await runAssistantChain(result.ctx, client, result.token, {
      projectId: result.project.id,
    })

    assistant.value = toAssistantSnapshot(assistantResult)
    commitRun(result, account, registerFirst, undefined, undefined, undefined, undefined, undefined)
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error)
    assistantError.value = message
    const failed = (error as { runContext?: G3RunContext } | null)?.runContext
    if (failed) {
      liveSteps.value = failed.steps
      liveHead.value = failed.head
      liveRunId.value = failed.runId
    }
  } finally {
    assistantRunning.value = false
  }
}

/**
 * 导出的证据：把**本次会话整链**的结论落成一个 JSON 文件。
 *
 * 这一份导出体是 **#244 的结构化证据**：运行标识、head、每一步的请求与响应、
 * 每条验收行的判定与状态、证据来源（确定性 / 依赖真实模型）、实现缺口清单。
 *
 * 导的是**整条链**，不是最后一次运行：页面上每段链各跑在自己的隔离项目上，
 * 各带自己的运行标识与 head，导出体把它们并成一份，并逐段记账（`runs[]`）。
 * 只有一段时按整链命名，多段时同样按整链命名——文件里写清楚有几段。
 *
 * 三条约束写死在 `g3Export.ts` 里：
 * 1. 模型调用记录**必须**进这份导出的证据，并标注为「依赖真实模型的非确定性来源」——
 *    少了这个标注，读者会把非确定性结论当成确定性结论。
 * 2. 「本次运行得出」与「历史快照 · 非本次运行」在导出体里分开列示，不混读。
 * 3. 文件名沿用 08 实施文档目录的既有族命名（`G3-验收台-8-*.json`）；
 *    证据**不写进 Issue**（`docs/agents/issue-tracker.md:54-56`）。
 */
function exportEvidence(): void {
  if (!runSummary.value) return
  const runs = runHistory.value.length > 0
    ? runHistory.value
    : [runSummary.value]
  const payload = buildChainEvidenceExport({
    runs: runs.map((item) => ({
      runId: item.runId,
      startedAt: item.startedAt,
      head: item.head,
      steps: item.steps,
      verdicts: item.verdicts ?? [],
      stale: item.stale,
      staleReason: item.staleReason,
      // 依赖真实模型的那一段：它带模型判定，导出体据此分列非确定性来源。
      modelDependent: (item.verdicts ?? []).some((verdict) => verdict.modelDependent === true),
    })),
    rows: matrixView.value.rows,
    evidence: matrixView.value.evidence,
    modelRuns: (model.value?.runs ?? []) as G3ModelRunFact[],
    dateStamp: dateStamp(),
    snapshot: {
      sourceDir: snapshot.sourceDir,
      head: snapshot.head ?? null,
      notice: snapshotNotice(snapshot),
    },
  })
  const blob = new Blob([`${JSON.stringify(payload, null, 2)}\n`], { type: 'application/json' })
  const url = URL.createObjectURL(blob)
  const fileName = exportFileName(dateStamp())
  const link = document.createElement('a')
  link.href = url
  link.download = fileName
  link.rel = 'noopener'
  // 锚点必须挂到文档里再点：游离元素的 click() 在部分内核里会被忽略。
  document.body.appendChild(link)
  link.click()
  document.body.removeChild(link)
  // objectURL 不能同步 revoke：Chromium 里同步撤销会连带取消这次下载。
  // 延后释放，让下载先把字节流取走。
  window.setTimeout(() => URL.revokeObjectURL(url), 30_000)
  lastExport.value = {
    fileName,
    bytes: blob.size,
    runId: payload.run_id,
    head: payload.head?.commitId ?? null,
    rowCount: payload.ac_rows.length,
    verdictCount: payload.verdicts.length,
    gapCount: payload.gaps.length,
    runCount: payload.runs?.length ?? runs.length,
    downloadNote: downloadNote(),
  }
}

/**
 * 导出后的提示语。
 *
 * 这里**不判断**文件是否落盘：宿主是否持久化下载是浏览器环境的事，
 * 页面没有可靠判据（曾经用 `showDirectoryPicker` 的存在与否推断，
 * 实测受管宿主并不落盘，属于判据与被测物脱钩）。所以只说已经发生的事：
 * 导出体已生成、下载已触发；要拿落盘证据请用 node 脚本。
 */
function downloadNote(): string {
  return '下载已触发；需要落盘证据请用 node 脚本（scripts/g3demo/run-evidence.ts）落到 docs/08-本轮实施方案/'
}

/** 把浏览器会话还给用户。 */
function releaseSession(): void {
  if (originalToken) {
    auth.setToken(originalToken)
  } else {
    localStorage.removeItem('weknora_token')
  }
  if (originalTenantId) {
    auth.setSelectedTenant(Number(originalTenantId))
  }
  originalToken = null
  originalTenantId = null
  sessionTakenOver.value = false
}
</script>

<style scoped>
:global(body) { background: #edf3f1; }
.g3-console { --ink: #123c3a; --ink-soft: #46635f; --paper: #f9fcfa; --line: #d5e4df; --cyan: #0b8c91; --amber: #d18a32; --coral: #c85d4b; max-width: 1480px; margin: 0 auto; padding: 34px 38px 50px; color: var(--ink); font-family: Inter, "Segoe UI", "PingFang SC", sans-serif; }
.console-hero { display: flex; justify-content: space-between; gap: 28px; align-items: flex-end; padding: 28px 30px 32px; border-radius: 24px; color: #eaf8f3; background: radial-gradient(circle at 86% 20%, #2e7772 0, transparent 32%), linear-gradient(125deg, #0c302f 0%, #124a47 62%, #0a2528 100%); box-shadow: 0 18px 40px rgba(18, 60, 58, .18); }
.eyebrow, .section-kicker { margin: 0 0 8px; font-size: 11px; font-weight: 750; letter-spacing: .16em; text-transform: uppercase; }
.eyebrow { color: #8ee7dd; }
.console-hero h1 { margin: 0; font-size: clamp(34px, 5vw, 58px); line-height: .98; letter-spacing: -.055em; font-weight: 760; }
.hero-lede { max-width: 640px; margin: 18px 0 0; color: #b9dbd2; font-size: 15px; line-height: 1.7; }
.hero-actions { display: flex; flex-direction: column; align-items: flex-end; gap: 12px; }
.demo-chip { display: inline-flex; align-items: center; border: 1px solid rgba(255, 220, 150, .45); border-radius: 999px; padding: 6px 10px; color: #ffe2a9; background: rgba(209, 138, 50, .16); font-size: 11px; font-weight: 750; letter-spacing: .04em; }
.snapshot-chip { display: inline-flex; gap: 8px; align-items: center; border-radius: 999px; padding: 7px 12px; color: #a9eee2; background: rgba(255, 255, 255, .1); font-size: 12px; font-weight: 700; }
.snapshot-chip i { width: 8px; height: 8px; border-radius: 50%; background: #f1c36a; box-shadow: 0 0 0 5px rgba(241, 195, 106, .16); }
.snapshot-chip--inline { color: #7c5a12; background: #fff0c9; }
.snapshot-chip--inline i { background: #d18a32; box-shadow: 0 0 0 5px rgba(209, 138, 50, .16); }
.identity-card, .flow-card, .matrix-card, .scope-card, .glossary-card, .snapshot-card, .live-card { margin-top: 18px; border: 1px solid var(--line); border-radius: 18px; background: rgba(249, 252, 250, .88); box-shadow: 0 10px 30px rgba(26, 66, 61, .05); }
.identity-card { display: flex; justify-content: space-between; gap: 24px; align-items: center; padding: 24px 26px; }
.identity-main { display: flex; gap: 15px; align-items: center; }
.identity-mark { display: grid; width: 54px; height: 54px; place-items: center; border-radius: 16px 16px 16px 4px; color: #f8ffff; background: var(--cyan); font-size: 18px; font-weight: 800; }
.identity-card h2, .section-heading h2 { margin: 0; letter-spacing: -.035em; }
.identity-card h2 { font-size: 23px; }
.identity-meta { margin: 6px 0 0; color: var(--ink-soft); font-size: 13px; }
.section-kicker { color: var(--cyan); }
.identity-facts { display: flex; gap: 28px; }
.identity-facts div { min-width: 170px; }
.identity-facts span { display: block; color: #78908b; font-size: 11px; letter-spacing: .05em; text-transform: uppercase; }
.identity-facts strong { display: block; margin-top: 7px; color: var(--ink); font-size: 15px; }
.identity-facts small { display: block; margin-top: 5px; color: #88a09b; font-size: 11px; }
.console-columns { display: grid; grid-template-columns: minmax(300px, .62fr) minmax(0, 1.38fr); gap: 18px; align-items: start; }
.flow-card, .matrix-card, .scope-card, .glossary-card, .snapshot-card, .live-card { padding: 24px 26px; }
.section-heading { display: flex; justify-content: space-between; gap: 18px; align-items: flex-end; margin-bottom: 20px; }
.section-heading h2 { font-size: 21px; }
.section-note { color: #6f8681; font-size: 12px; }
.flow-list { margin: 0; padding: 0; list-style: none; border-top: 1px solid var(--line); }
.flow-step { display: grid; grid-template-columns: 30px 1fr auto; gap: 12px; align-items: start; padding: 14px 0; border-bottom: 1px solid var(--line); }
.flow-number { color: #86aaa2; font-size: 12px; font-weight: 800; letter-spacing: .1em; }
.flow-body h3 { margin: 0; font-size: 15px; letter-spacing: -.02em; }
.flow-endpoint, .chain-endpoint { display: flex; flex-wrap: wrap; gap: 7px; align-items: center; margin: 6px 0 0; }
.flow-endpoint code, .chain-endpoint code { color: #4a6b65; font-family: ui-monospace, SFMono-Regular, Consolas, monospace; font-size: 11px; word-break: break-all; }
.flow-precondition { margin: 6px 0 0; color: var(--ink-soft); font-size: 12px; line-height: 1.5; }
.flow-state { padding: 4px 8px; border-radius: 999px; color: #7a5a1a; background: #fff0c9; font-size: 11px; font-weight: 750; white-space: nowrap; }
.method { padding: 2px 6px; border-radius: 5px; font-family: ui-monospace, SFMono-Regular, Consolas, monospace; font-size: 10px; font-weight: 800; }
.method--post { color: #0b6e71; background: #dff3ef; }
.method--get { color: #5b4c9d; background: #eeebff; }
.method--put { color: #86621f; background: #fff0c9; }
.matrix-legend { display: flex; flex-wrap: wrap; gap: 6px; }
.legend-note { margin: 10px 0 4px; color: #6f8681; font-size: 11px; line-height: 1.6; }
.legend-note code { font-family: ui-monospace, SFMono-Regular, Consolas, monospace; }
.matrix-group-title { margin: 22px 0 10px; font-size: 15px; letter-spacing: -.02em; }
.matrix-scroll, .table-scroll { overflow-x: auto; }
.g3-table { width: 100%; min-width: 900px; border-collapse: collapse; font-size: 12px; }
.g3-table th, .g3-table td { padding: 12px 10px; border-top: 1px solid var(--line); text-align: left; vertical-align: top; }
.g3-table thead th { border-top: 0; color: #6f8681; font-size: 11px; font-weight: 700; }
.g3-table tbody th { width: 18%; }
.g3-table tbody th strong, .g3-table tbody th small { display: block; }
.g3-table tbody th small { margin-top: 4px; color: #88a09b; font-size: 10px; font-weight: 400; }
.criterion { width: 30%; color: #2c4a45; line-height: 1.6; }
.anchor { width: 14%; }
.anchor code, .g3-table--compact code { color: #4a6b65; font-family: ui-monospace, SFMono-Regular, Consolas, monospace; font-size: 10.5px; word-break: break-all; }
.anchor small { display: block; margin-top: 4px; color: #88a09b; font-size: 10px; word-break: break-all; }
.evidence { width: 20%; }
.evidence span { display: block; }
.evidence small { display: block; margin-top: 5px; color: #6f8681; font-size: 11px; line-height: 1.5; }
.evidence small.issue-link { color: #0b6e71; font-weight: 700; }
.status-badge { display: inline-block; padding: 4px 8px; border-radius: 6px; font-size: 11px; font-weight: 750; white-space: nowrap; }
.status-badge--pass { color: #087467; background: #d8f5e9; }
.status-badge--follow-up { color: #0b6e71; background: #dff3ef; }
.status-badge--na { color: #5b6670; background: #eceff1; }
.status-badge--warning { color: #86621f; background: #fff0c9; }
.status-badge--not-run { color: #a14637; background: #ffe1d9; }
.status-badge--blocked { color: #a14637; background: #ffe1d9; }
.status-badge--todo { color: #5b6670; background: #eceff1; }
.card-footnote { margin: 16px 0 0; color: #7d9690; font-size: 11px; line-height: 1.7; }
.scope-grid { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 12px; }
.scope-item { padding: 16px; border: 1px solid var(--line); border-radius: 14px; background: #fff; }
.scope-item--primary { border-color: #83bcb0; background: #f4fbf8; }
.scope-item header { display: flex; gap: 8px; align-items: center; justify-content: space-between; }
.scope-item header strong { font-size: 13px; }
.primary-chip { padding: 3px 7px; border-radius: 999px; color: #087467; background: #d8f5e9; font-size: 10px; font-weight: 800; white-space: nowrap; }
.scope-anchor { margin: 8px 0 0; }
.scope-anchor code { color: #4a6b65; font-family: ui-monospace, SFMono-Regular, Consolas, monospace; font-size: 10.5px; word-break: break-all; }
.scope-states { margin: 8px 0 0; color: #2c4a45; font-family: ui-monospace, SFMono-Regular, Consolas, monospace; font-size: 11px; line-height: 1.6; }
.scope-note { margin: 8px 0 0; color: #6f8681; font-size: 11.5px; line-height: 1.6; }
.satisfied-reading { margin-top: 14px; padding: 16px; border-left: 3px solid var(--cyan); border-radius: 0 12px 12px 0; background: #f4fbf8; }
.satisfied-reading p { margin: 6px 0 0; font-size: 12.5px; line-height: 1.65; }
.glossary-grid { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 12px; }
.glossary-item { padding: 14px 16px; border: 1px solid var(--line); border-radius: 12px; background: #fff; }
.glossary-item strong { display: block; font-size: 13px; }
.glossary-item p { margin: 6px 0 0; color: #46635f; font-size: 12px; line-height: 1.6; }
.glossary-item--warn { border-color: #ecd69f; background: #fffaf0; }
.evidence-block { margin-top: 14px; padding: 16px 18px; border: 1px solid var(--line); border-radius: 14px; background: #fff; }
.evidence-head { display: flex; justify-content: space-between; gap: 12px; align-items: center; flex-wrap: wrap; }
.evidence-head strong { font-size: 14px; }
.evidence-stamp { padding: 4px 9px; border-radius: 999px; color: #7c5a12; background: #fff0c9; font-size: 10.5px; font-weight: 750; }
.fact-list { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 12px 20px; margin: 14px 0 0; }
.fact-list dt { color: #78908b; font-size: 11px; letter-spacing: .05em; text-transform: uppercase; }
.fact-list dd { margin: 5px 0 0; color: var(--ink); font-size: 12.5px; line-height: 1.55; word-break: break-all; }
.g3-table--compact { min-width: 640px; }
.chain-list { margin: 14px 0 0; padding: 0; list-style: none; }
.chain-step { display: grid; grid-template-columns: 30px 1fr; gap: 12px; padding: 12px 0; border-top: 1px solid var(--line); }
.chain-number { color: #86aaa2; font-size: 12px; font-weight: 800; letter-spacing: .1em; }
.chain-step strong { font-size: 13.5px; }
.chain-result { margin: 6px 0 0; color: var(--ink-soft); font-size: 12px; line-height: 1.55; }
.console-footer { display: flex; justify-content: space-between; gap: 20px; margin-top: 22px; padding: 14px 4px 0; border-top: 1px solid #c8dbd5; color: #708983; font-size: 11px; letter-spacing: .03em; }
.demo-chip--live { border-color: rgba(140, 235, 220, .55); color: #b6f4ea; background: rgba(11, 140, 145, .28); }
.lifecycle-card { margin-top: 18px; padding: 24px 26px; border: 1px solid var(--line); border-radius: 18px; background: rgba(249, 252, 250, .88); box-shadow: 0 10px 30px rgba(26, 66, 61, .05); }
.verdict-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 12px; margin-top: 16px; }
.verdict-card { padding: 16px 18px; border: 1px solid var(--line); border-radius: 14px; background: #fff; }
.verdict-card--ok { border-color: #9ed3c6; background: #f4fbf8; }
.verdict-card--bad { border-color: #e6b6a8; background: #fff7f4; }
.verdict-card--warn { border-color: #e8cf9a; background: #fffaf0; }
.verdict-card header { display: flex; gap: 10px; align-items: center; justify-content: space-between; }
.verdict-card header strong { font-size: 13.5px; }
.verdict-detail { margin: 8px 0 0; color: #2c4a45; font-size: 12.5px; line-height: 1.65; }
.verdict-evidence { margin: 10px 0 0; padding-left: 18px; }
.verdict-evidence li { margin: 4px 0; color: #46635f; font-size: 11.5px; line-height: 1.55; word-break: break-all; }
.verdict-evidence code { font-family: ui-monospace, SFMono-Regular, Consolas, monospace; }
.fact-line { display: block; color: #46635f; font-size: 11.5px; line-height: 1.6; word-break: break-all; }
.mini-steps { margin: 0; padding-left: 16px; }
.mini-steps li { margin: 4px 0; font-size: 11.5px; line-height: 1.55; }
.mini-steps strong { display: inline-block; margin-right: 6px; }
.mini-steps span { color: #46635f; word-break: break-all; }
.mini-step--ok strong { color: #087467; }
.mini-step--bad strong { color: #a14637; }
.mini-step--idle strong { color: #86621f; }
.live-lede { max-width: 900px; margin: 0; color: var(--ink-soft); font-size: 13px; line-height: 1.75; }
.live-lede code { font-family: ui-monospace, SFMono-Regular, Consolas, monospace; font-size: 12px; }
.live-actions { display: flex; flex-wrap: wrap; gap: 10px; margin-top: 16px; }
.live-button { padding: 9px 16px; border: 1px solid var(--line); border-radius: 10px; color: var(--ink); background: #fff; font-size: 13px; font-weight: 700; cursor: pointer; }
.live-button:hover:not(:disabled) { border-color: #83bcb0; }
.live-button:disabled { opacity: .5; cursor: not-allowed; }
.live-button--primary { border-color: var(--cyan); color: #fff; background: var(--cyan); }
.live-button--ghost { color: #a14637; background: #ffe1d9; border-color: #f0c2b6; }
.live-facts { margin-top: 18px; }
.live-error { margin: 16px 0 0; padding: 12px 14px; border-left: 3px solid #c85d4b; border-radius: 0 10px 10px 0; color: #8a3b2c; background: #ffe9e3; font-size: 13px; line-height: 1.6; }
.live-warn { margin: 12px 0 0; padding: 12px 14px; border-left: 3px solid #d18a32; border-radius: 0 10px 10px 0; color: #7c5a12; background: #fff5dd; font-size: 12.5px; line-height: 1.6; }
.live-ok { margin: 12px 0 0; padding: 12px 14px; border-left: 3px solid #0f8a76; border-radius: 0 10px 10px 0; color: #0a5f51; background: #e2f7f0; font-size: 12.5px; line-height: 1.6; }
.flow-status { padding: 2px 7px; border-radius: 6px; font-size: 10.5px; font-weight: 800; white-space: nowrap; }
.flow-status--ok { color: #087467; background: #d8f5e9; }
.flow-status--bad { color: #a14637; background: #ffe1d9; }
.flow-meta { display: flex; flex-wrap: wrap; gap: 6px 14px; margin: 8px 0 0; color: #6f8681; font-size: 11px; }
.flow-meta code { font-family: ui-monospace, SFMono-Regular, Consolas, monospace; font-size: 10.5px; word-break: break-all; }
.flow-details { color: #88a09b; }
.coverage { width: 18%; }
.coverage-chip { display: inline-block; padding: 4px 8px; border-radius: 6px; font-size: 10.5px; font-weight: 750; white-space: nowrap; }
.coverage-chip--none { color: #5b6670; background: #eceff1; }
.coverage-chip--partial { color: #86621f; background: #fff0c9; }
.coverage-chip--covered { color: #087467; background: #d8f5e9; }
.coverage small { display: block; margin-top: 5px; color: #6f8681; font-size: 11px; line-height: 1.5; }
/* 结论来源：本次运行与历史快照**视觉与文案互斥**。 */
.source-chip { display: inline-block; margin-top: 5px; margin-right: 6px; padding: 3px 8px; border-radius: 999px; font-size: 10.5px; font-weight: 750; white-space: nowrap; }
.source-chip--live { color: #fff; background: var(--cyan); }
.source-chip--snapshot { color: #7c5a12; background: #fff0c9; }
.source-chip--none { color: #5b6670; background: #eceff1; }
.source-chip--gap { color: #8a3b2c; background: #ffe1d9; }
.source-chip--limited { color: #5b4c9d; background: #eeebff; }
.gap-list { display: grid; gap: 10px; margin-top: 12px; }
.gap-item { padding: 12px 14px; border: 1px solid var(--line); border-left: 3px solid var(--coral); border-radius: 12px; background: #fffdfa; }
.gap-head { display: flex; justify-content: space-between; gap: 12px; align-items: center; }
.gap-head strong { font-size: 13px; letter-spacing: -.01em; }
.gap-meta { display: flex; flex-wrap: wrap; gap: 6px 16px; margin: 8px 0 0; color: #6f8681; font-size: 11px; }
.gap-meta code { font-family: ui-monospace, SFMono-Regular, Consolas, monospace; font-size: 10.5px; }
.gap-text { margin: 8px 0 0; color: var(--ink-soft); font-size: 12px; line-height: 1.65; }
.gap-text--fact { color: #6f8681; font-size: 11.5px; }
.issue-link { color: #0b6e71; text-decoration: underline; }
@media (max-width: 1100px) { .console-columns { grid-template-columns: 1fr; }.scope-grid, .glossary-grid { grid-template-columns: 1fr; } }
@media (max-width: 960px) { .identity-card, .console-hero { align-items: flex-start; flex-direction: column; }.hero-actions { align-items: flex-start; }.identity-facts { flex-wrap: wrap; }.fact-list { grid-template-columns: 1fr; } }
@media (max-width: 600px) { .g3-console { padding: 16px 12px 32px; }.console-hero, .identity-card, .flow-card, .matrix-card, .scope-card, .glossary-card, .snapshot-card { padding: 18px; }.console-footer { flex-direction: column; gap: 6px; } }
@media (prefers-reduced-motion: reduce) { *, *::before, *::after { scroll-behavior: auto !important; transition-duration: .01ms !important; animation-duration: .01ms !important; } }
</style>
