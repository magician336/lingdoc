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
                <th>本轮覆盖</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in sliceRows" :key="row.id">
                <th scope="row">
                  <strong>{{ row.name }}</strong>
                  <small>{{ row.id }} · {{ row.deliverable }}</small>
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
                <th>本轮覆盖</th>
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
          「本轮覆盖」是**本票真实运行**与每一行的关系：前置链跑通**不构成**任何一条判据的证据，
          所以没被覆盖的行状态原样保留，绝不会被判成 <code>PASS</code>。
        </p>
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
} from './g3Status'
import { G3_SNAPSHOT_RAW, loadG3Snapshot, snapshotNotice } from './g3Snapshot'
import { createRequestClient } from './g3HttpAdapter'
import {
  applyRunEvidence,
  contextFromSummary,
  headChangedSince,
  loadRunSummary,
  saveRunSummary,
  summarizeRun,
  type G3RowEvidence,
  type G3RunSummary,
} from './g3Evidence'
import { runChain } from './runner/chain'
import { createDemoAccount, loadDemoAccount, saveDemoAccount, type G3DemoAccount } from './runner/identity'
import { createRunId, type G3BackendHead, type G3ChapterFact, type G3RunContext, type G3StepRecord } from './runner/runContext'

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
const liveRunId = ref('')
const liveAccountEmail = ref('')
const liveProjectId = ref('')
const liveProjectName = ref('')
const liveChapters = ref<G3ChapterFact[]>([])
const liveHead = ref<G3BackendHead | null>(null)
const liveSteps = ref<G3StepRecord[]>([])
const missingSections = ref<string[]>([])

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
const matrixView = computed(() =>
  applyRunEvidence(G3_MATRIX_ROWS, liveContext.value ?? {
    runId: '',
    startedAt: '',
    accountEmail: '',
    steps: [],
    head: null,
    chapters: [],
    stale: false,
  }),
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

/** 覆盖徽标：只有被本轮**判定**的行才算覆盖，其余一律「未被本轮覆盖」。 */
const coverageLabel = (item?: G3RowEvidence) => {
  if (!item) return '未被本轮覆盖'
  if (item.decided) return '本轮已判定'
  if (item.covered) return '本轮已跑到'
  return '未被本轮覆盖'
}
const coverageTone = (item?: G3RowEvidence) => {
  if (!item) return 'coverage-chip--none'
  if (item.decided) return 'coverage-chip--covered'
  if (item.covered) return 'coverage-chip--partial'
  return 'coverage-chip--none'
}

const anchorLabel = (anchor: SpecAnchor) => formatAnchor(anchor)
const anchorPath = (anchor: SpecAnchor) => anchor.path

/** 读一次本地状态：演示账号与上一次运行。 */
function restoreLocalState(): void {
  try {
    savedAccount.value = loadDemoAccount(localStorage)
    runSummary.value = loadRunSummary(localStorage)
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
  } catch {
    // 本地存储不可用时如实当作「没有历史」，不假装有。
    savedAccount.value = null
    runSummary.value = null
  }
}
restoreLocalState()

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

    const summary = summarizeRun(result.ctx, { missingSections: result.missingSections })
    runSummary.value = summary
    saveRunSummary(localStorage, summary)

    liveSteps.value = result.ctx.steps
    liveHead.value = result.ctx.head
    liveRunId.value = result.ctx.runId
    liveAccountEmail.value = account.email
    liveProjectId.value = result.project.id
    liveProjectName.value = result.project.name
    liveChapters.value = result.chapters
    missingSections.value = result.missingSections
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
@media (max-width: 1100px) { .console-columns { grid-template-columns: 1fr; }.scope-grid, .glossary-grid { grid-template-columns: 1fr; } }
@media (max-width: 960px) { .identity-card, .console-hero { align-items: flex-start; flex-direction: column; }.hero-actions { align-items: flex-start; }.identity-facts { flex-wrap: wrap; }.fact-list { grid-template-columns: 1fr; } }
@media (max-width: 600px) { .g3-console { padding: 16px 12px 32px; }.console-hero, .identity-card, .flow-card, .matrix-card, .scope-card, .glossary-card, .snapshot-card { padding: 18px; }.console-footer { flex-direction: column; gap: 6px; } }
@media (prefers-reduced-motion: reduce) { *, *::before, *::after { scroll-behavior: auto !important; transition-duration: .01ms !important; animation-duration: .01ms !important; } }
</style>
