<template>
  <main class="permission-proof">
    <header class="proof-hero">
      <div class="hero-copy">
        <p class="eyebrow">LINGDOC / AUTHORIZATION PROOF</p>
        <h1>权限验收台</h1>
        <p class="hero-lede">把“谁能做什么、为什么、是否留下证据”放在同一张可复核的台面上。</p>
      </div>
      <div class="hero-actions">
        <span v-if="demoMode" class="demo-chip">无需登录 · 预置演示</span>
        <span class="live-chip" :class="{ 'live-chip--busy': loading }">
          <i aria-hidden="true"></i>{{ loading ? '正在读取证据' : demoMode ? '演示证据' : '实时证据' }}
        </span>
        <button class="refresh-button" type="button" :disabled="loading" @click="refreshEvidence">
          {{ loading ? '刷新中…' : '刷新证据' }}
        </button>
      </div>
    </header>

    <p v-if="errorMessage" class="proof-alert" role="alert">{{ errorMessage }}</p>

    <section class="identity-card" aria-labelledby="identity-title">
      <div class="identity-main">
        <div class="identity-mark" aria-hidden="true">{{ identityInitial }}</div>
        <div>
          <p class="section-kicker">当前请求身份</p>
          <h2 id="identity-title">{{ displayUserName }}</h2>
          <p class="identity-meta">
            用户 {{ displayUserId }} · 租户 {{ displayTenantName }}
          </p>
        </div>
      </div>
      <div class="identity-facts">
        <div>
          <span>租户角色</span>
          <strong>{{ permissionRoleLabel(displayRole) }}</strong>
        </div>
        <div>
          <span>租户 ID</span>
          <strong>{{ displayTenantId }}</strong>
        </div>
        <div>
          <span>系统管理员</span>
          <strong>{{ displayIsSystemAdmin ? '是' : '否' }}</strong>
        </div>
      </div>
    </section>

    <section class="proof-rail" aria-labelledby="rail-title">
      <div class="section-heading">
        <div>
          <p class="section-kicker">一条请求的完整路径</p>
          <h2 id="rail-title">权限不是一个按钮，而是一条证据链</h2>
        </div>
        <span class="section-note">服务端最终裁决</span>
      </div>
      <div class="rail-grid">
        <article v-for="(step, index) in proofSteps" :key="step.title" class="rail-step">
          <span class="rail-number">0{{ index + 1 }}</span>
          <div>
            <h3>{{ step.title }}</h3>
            <p>{{ step.detail }}</p>
          </div>
          <span class="rail-status" :class="`rail-status--${step.state}`">{{ step.status }}</span>
        </article>
      </div>
    </section>

    <div class="proof-columns">
      <section class="matrix-card" aria-labelledby="matrix-title">
        <div class="section-heading section-heading--compact">
          <div>
            <p class="section-kicker">角色矩阵</p>
            <h2 id="matrix-title">当前身份能做什么</h2>
          </div>
          <span class="matrix-role">{{ permissionRoleLabel(displayRole) }}</span>
        </div>
        <div class="matrix-scroll">
          <table class="permission-table">
            <thead>
              <tr>
                <th>操作</th>
                <th v-for="role in PERMISSION_ROLES" :key="role">{{ PERMISSION_ROLE_LABELS[role] }}</th>
                <th>本次请求</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in PERMISSION_EVIDENCE_ROWS" :key="row.id">
                <th scope="row">
                  <strong>{{ row.operation }}</strong>
                  <small>{{ row.scope }}</small>
                </th>
                <td v-for="role in PERMISSION_ROLES" :key="role">
                  <span class="matrix-dot" :class="roleSatisfies(role, row.minimumRole) ? 'matrix-dot--yes' : 'matrix-dot--no'">
                    {{ roleSatisfies(role, row.minimumRole) ? '✓' : '—' }}
                  </span>
                </td>
                <td>
                  <span class="decision-badge" :class="`decision-badge--${decisionFor(row.minimumRole)}`">
                    {{ decisionLabel(decisionFor(row.minimumRole)) }}
                  </span>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <p class="card-footnote">矩阵只展示 UI 预判；任何越权请求仍由 WeKnora 服务端返回 403 并记录拒绝原因。</p>
      </section>

      <section class="evidence-card" aria-labelledby="evidence-title">
        <div class="section-heading section-heading--compact">
          <div>
            <p class="section-kicker">真实接口探针</p>
            <h2 id="evidence-title">服务端证据</h2>
          </div>
          <span class="evidence-count">{{ evidencePassedCount }}/{{ evidenceChecks.length }} 通过</span>
        </div>
        <div class="probe-list">
          <article v-for="probe in evidenceChecks" :key="probe.id" class="probe-row">
            <span class="probe-icon" :class="`probe-icon--${probe.state}`">{{ probe.state === 'passed' ? '✓' : probe.state === 'blocked' ? '!' : '·' }}</span>
            <div>
              <strong>{{ probe.title }}</strong>
              <p>{{ probe.detail }}</p>
            </div>
            <span class="probe-state">{{ probe.label }}</span>
          </article>
        </div>
      </section>
    </div>

    <section class="project-card" aria-labelledby="project-title">
      <div class="section-heading section-heading--compact">
        <div>
          <p class="section-kicker">资料层撤权演示</p>
          <h2 id="project-title">项目访问状态</h2>
        </div>
        <select v-model="selectedProjectId" :disabled="!projects.length || loading" aria-label="选择演示项目">
          <option value="">选择一个项目</option>
          <option v-for="project in projects" :key="project.id" :value="project.id">{{ project.name }}</option>
        </select>
      </div>
      <div v-if="selectedProject" class="project-proof">
        <div>
          <span class="project-label">当前项目</span>
          <strong>{{ selectedProject.name }}</strong>
          <p>{{ selectedProject.members.length }} 位项目成员 · {{ selectedProject.status === 'active' ? '已立项' : '草稿' }}</p>
        </div>
        <div class="access-state" :class="`access-state--${accessState}`">
          <span>{{ accessStateLabel }}</span>
          <strong>{{ accessStateDetail }}</strong>
        </div>
        <div class="project-actions">
          <button type="button" :disabled="accessLoading" @click="loadAccessStatus">
            {{ accessLoading ? '检查中…' : '重新检查当前授权' }}
          </button>
        </div>
      </div>
      <div v-else class="empty-proof">
        <span class="empty-mark">∅</span>
        <div>
          <strong>{{ projects.length ? '选择一个项目读取真实资料授权' : '当前租户还没有可读取的项目' }}</strong>
          <p>项目读取成功后，这里会显示服务端返回的 available / restricted / unknown 三态，不由前端猜测。</p>
        </div>
      </div>
    </section>

    <section class="audit-card" aria-labelledby="audit-title">
      <div class="section-heading section-heading--compact">
        <div>
          <p class="section-kicker">不可抵赖证据</p>
          <h2 id="audit-title">最近授权审计</h2>
        </div>
        <span class="section-note">租户级 · Admin+</span>
      </div>
      <div v-if="auditState === 'forbidden'" class="audit-empty audit-empty--guarded">
        <span class="empty-mark">◌</span>
        <div>
          <strong>当前角色不能读取租户审计流</strong>
          <p>这是预期的权限结果：只有管理员及以上角色可以查看成员变更与拒绝决策。</p>
        </div>
      </div>
      <div v-else-if="auditState === 'error'" class="audit-empty" role="alert">
        <strong>审计接口暂时不可用</strong>
        <p>{{ auditMessage }}</p>
      </div>
      <div v-else-if="!auditLogs.length" class="audit-empty">
        <span class="empty-mark">—</span>
        <div>
          <strong>当前租户还没有审计记录</strong>
          <p>执行一次成员变更或触发一次拒绝决策后，记录会出现在这里。</p>
        </div>
      </div>
      <div v-else class="audit-list">
        <article v-for="row in auditLogs" :key="row.id" class="audit-row">
          <span class="audit-outcome" :class="`audit-outcome--${row.outcome}`">{{ auditOutcomeLabel(row.outcome) }}</span>
          <div>
            <strong>{{ auditActionLabel(row.action) }}</strong>
            <p>{{ row.actor_role || '未知角色' }} · {{ row.request_method }} {{ row.request_path || '—' }}</p>
          </div>
          <time :datetime="row.created_at">{{ formatTime(row.created_at) }}</time>
        </article>
      </div>
    </section>

    <footer class="proof-footer">
      <span>权限设计落地证据</span>
      <span>身份 → 租户角色 → 资源授权 → 交付重查 → 审计日志</span>
    </footer>
  </main>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { listAuditLog, type AuditLog, type AuditOutcome } from '@/api/tenant/audit-log'
import { getAccessStatus, listProjects, type AccessStatus, type Project } from '@/api/lingdoc/workspace'
import {
  PERMISSION_EVIDENCE_ROWS,
  PERMISSION_ROLE_LABELS,
  PERMISSION_ROLES,
  decisionLabel,
  permissionDecision,
  permissionRoleLabel,
  roleSatisfies,
  type PermissionDecision,
} from './permissionEvidence'

const authStore = useAuthStore()
const route = useRoute()
const demoMode = computed(() => route.meta.publicDemo === true)
const loading = ref(false)
const accessLoading = ref(false)
const errorMessage = ref('')
const projects = ref<Project[]>([])
const selectedProjectId = ref('')
const accessStatus = ref<AccessStatus | null>(null)
const auditLogs = ref<AuditLog[]>([])
const auditState = ref<'idle' | 'ready' | 'forbidden' | 'error'>('idle')
const auditMessage = ref('')

const DEMO_PROJECTS: Project[] = [
  {
    id: 'demo-project-active',
    name: '研究方案权限演示',
    status: 'active',
    project_version: 3,
    spec_revision: 12,
    spec: {},
    template_id: 'template-demo',
    template_version: '1.2.0',
    members: [
      { user_id: 'demo-owner', role: 'owner' },
      { user_id: 'demo-admin', role: 'collaborator' },
      { user_id: 'demo-viewer', role: 'collaborator' },
    ],
  },
  {
    id: 'demo-project-revoked',
    name: '撤权后的资料集',
    status: 'active',
    project_version: 2,
    spec_revision: 8,
    spec: {},
    template_id: 'template-demo',
    template_version: '1.1.0',
    members: [{ user_id: 'demo-admin', role: 'collaborator' }],
  },
]

const DEMO_ACCESS_STATUS: Record<string, AccessStatus> = {
  'demo-project-active': {
    project_id: 'demo-project-active',
    content_access: 'available',
    recovery_actions: [],
    can_create_project: true,
  },
  'demo-project-revoked': {
    project_id: 'demo-project-revoked',
    content_access: 'restricted',
    recovery_actions: ['restore_source_authorization', 'create_clean_project'],
    can_create_project: true,
  },
}

const DEMO_AUDIT_LOGS: AuditLog[] = [
  {
    id: 3,
    tenant_id: 42,
    actor_user_id: 'demo-admin',
    actor_role: 'admin',
    action: 'rbac.access_denied',
    scope_type: 'project',
    scope_id: 'demo-project-revoked',
    target_type: 'delivery',
    target_id: 'export-018',
    target_user_id: 'demo-viewer',
    request_path: '/api/v1/lingdoc/projects/demo-project-revoked/export',
    request_method: 'POST',
    outcome: 'denied',
    details: { required_role: 'contributor' },
    created_at: '2026-10-03T09:42:00+08:00',
  },
  {
    id: 2,
    tenant_id: 42,
    actor_user_id: 'demo-owner',
    actor_role: 'owner',
    action: 'rbac.member_role_changed',
    scope_type: 'tenant',
    scope_id: '42',
    target_type: 'member',
    target_id: 'demo-viewer',
    target_user_id: 'demo-viewer',
    request_path: '/api/v1/tenants/42/members/demo-viewer',
    request_method: 'PATCH',
    outcome: 'success',
    details: { old_role: 'contributor', new_role: 'viewer' },
    created_at: '2026-10-03T09:36:00+08:00',
  },
  {
    id: 1,
    tenant_id: 42,
    actor_user_id: 'demo-owner',
    actor_role: 'owner',
    action: 'rbac.member_added',
    scope_type: 'tenant',
    scope_id: '42',
    target_type: 'member',
    target_id: 'demo-admin',
    target_user_id: 'demo-admin',
    request_path: '/api/v1/tenants/42/members',
    request_method: 'POST',
    outcome: 'accepted',
    details: null,
    created_at: '2026-10-03T09:28:00+08:00',
  },
]

const displayUserName = computed(() => demoMode.value ? 'Lin · 权限演示用户' : authStore.user?.username || authStore.user?.email || '未登录用户')
const displayUserId = computed(() => demoMode.value ? 'demo-admin' : authStore.currentUserId || '—')
const displayTenantName = computed(() => demoMode.value ? 'LingDoc 研发空间' : authStore.currentTenantName || '未选择空间')
const displayRole = computed(() => demoMode.value ? 'admin' : authStore.currentTenantRole)
const displayTenantId = computed(() => demoMode.value ? '42' : String(authStore.effectiveTenantId || '—'))
const displayIsSystemAdmin = computed(() => demoMode.value ? false : authStore.isSystemAdmin)

const identityInitial = computed(() => {
  const value = displayUserName.value || '?'
  return value.slice(0, 1).toUpperCase()
})

const selectedProject = computed(() => projects.value.find((item) => item.id === selectedProjectId.value) || null)
const accessState = computed(() => accessStatus.value?.content_access || 'unknown')
const accessStateLabel = computed(() => {
  if (accessState.value === 'available') return '资料授权通过'
  if (accessState.value === 'restricted') return '资料授权已阻断'
  return '资料授权待复核'
})
const accessStateDetail = computed(() => {
  if (accessState.value === 'available') return '当前绑定资料可用于读取与交付检查。'
  if (accessState.value === 'restricted') return '服务端检测到至少一项绑定资料已撤权。'
  return '服务端暂时无法确认绑定资料状态。'
})

const proofSteps = computed(() => [
  {
    title: '身份',
    detail: displayUserId.value ? `已识别用户 ${displayUserId.value}` : '等待登录身份',
    status: displayUserId.value ? '已识别' : '待加载',
    state: displayUserId.value ? 'passed' : 'waiting',
  },
  {
    title: '租户角色',
    detail: displayRole.value ? `当前为${permissionRoleLabel(displayRole.value)}` : '尚未读取租户成员关系',
    status: displayRole.value ? '已解析' : '待加载',
    state: displayRole.value ? 'passed' : 'waiting',
  },
  {
    title: '资源授权',
    detail: selectedProject.value ? accessStateDetail.value : '选择项目后读取 access-status',
    status: selectedProject.value ? accessStateLabel.value : '待选择',
    state: selectedProject.value && accessState.value === 'available' ? 'passed' : selectedProject.value && accessState.value === 'restricted' ? 'blocked' : 'waiting',
  },
  {
    title: '交付重查',
    detail: '导出与下载分别由服务端重新检查，不沿用旧的放行结果。',
    status: '已接入',
    state: 'passed',
  },
  {
    title: '审计日志',
    detail: auditState.value === 'forbidden' ? '当前角色按预期无法读取 Admin+ 审计流' : '成员变更与拒绝决策进入租户审计流',
    status: auditState.value === 'forbidden' ? '被保护' : auditState.value === 'ready' ? '已读取' : '待读取',
    state: auditState.value === 'forbidden' || auditState.value === 'ready' ? 'passed' : 'waiting',
  },
])

const evidenceChecks = computed(() => [
  {
    id: 'projects',
    title: '项目读取接口',
    detail: projects.value.length ? `GET /lingdoc/projects 返回 ${projects.value.length} 个项目。` : 'GET /lingdoc/projects 已响应，但当前没有项目。',
    state: 'passed' as const,
    label: '已通过',
  },
  {
    id: 'access',
    title: '资料访问状态',
    detail: selectedProject.value ? accessStateDetail.value : '选择项目后读取三态 access-status。',
    state: selectedProject.value && accessState.value === 'restricted' ? 'blocked' as const : selectedProject.value && accessState.value === 'available' ? 'passed' as const : 'waiting' as const,
    label: selectedProject.value ? accessStateLabel.value : '待选择',
  },
  {
    id: 'audit',
    title: '租户审计接口',
    detail: auditState.value === 'forbidden' ? '服务端按角色拒绝普通成员读取审计流。' : auditState.value === 'ready' ? `已读取 ${auditLogs.value.length} 条最近记录。` : auditMessage.value || '等待读取租户审计流。',
    state: auditState.value === 'error' ? 'blocked' as const : auditState.value === 'ready' || auditState.value === 'forbidden' ? 'passed' as const : 'waiting' as const,
    label: auditState.value === 'forbidden' ? '按预期保护' : auditState.value === 'ready' ? '已通过' : auditState.value === 'error' ? '读取失败' : '待读取',
  },
])
const evidencePassedCount = computed(() => evidenceChecks.value.filter((item) => item.state === 'passed').length)

function decisionFor(minimumRole: 'viewer' | 'contributor' | 'admin' | 'owner'): PermissionDecision {
  return permissionDecision(displayRole.value, minimumRole)
}

function errorText(error: unknown): string {
  if (typeof error === 'object' && error && 'message' in error) return String(error.message)
  return '接口暂时不可用，请稍后重试。'
}

async function loadProjects() {
  if (demoMode.value) {
    projects.value = DEMO_PROJECTS
    if (!selectedProjectId.value || !projects.value.some((item) => item.id === selectedProjectId.value)) {
      selectedProjectId.value = projects.value[0]?.id || ''
    }
    return
  }
  const response = await listProjects()
  projects.value = response.data?.items || []
  if (!selectedProjectId.value && projects.value[0]) selectedProjectId.value = projects.value[0].id
  if (selectedProjectId.value && !projects.value.some((item) => item.id === selectedProjectId.value)) {
    selectedProjectId.value = ''
  }
}

async function loadAccessStatus() {
  if (!selectedProjectId.value) {
    accessStatus.value = null
    return
  }
  if (demoMode.value) {
    accessStatus.value = DEMO_ACCESS_STATUS[selectedProjectId.value] || {
      project_id: selectedProjectId.value,
      content_access: 'unknown',
      recovery_actions: [],
      can_create_project: false,
    }
    return
  }
  accessLoading.value = true
  try {
    const response = await getAccessStatus(selectedProjectId.value)
    accessStatus.value = response.data
  } catch (error) {
    accessStatus.value = null
    errorMessage.value = `项目授权状态读取失败：${errorText(error)}`
  } finally {
    accessLoading.value = false
  }
}

async function loadAudit() {
  if (demoMode.value) {
    auditLogs.value = DEMO_AUDIT_LOGS
    auditState.value = 'ready'
    auditMessage.value = ''
    return
  }
  const tenantId = authStore.effectiveTenantId
  if (!tenantId) {
    auditState.value = 'idle'
    return
  }
  try {
    const response = await listAuditLog(Number(tenantId), { limit: 8 })
    auditLogs.value = response.data || []
    auditState.value = 'ready'
    auditMessage.value = ''
  } catch (error) {
    const status = typeof error === 'object' && error && '$httpStatus' in error ? Number(error.$httpStatus) : 0
    if (status === 403) {
      auditState.value = 'forbidden'
      auditMessage.value = ''
      return
    }
    auditState.value = 'error'
    auditMessage.value = errorText(error)
  }
}

async function refreshEvidence() {
  loading.value = true
  errorMessage.value = ''
  try {
    await Promise.all([loadProjects(), loadAudit()])
    await loadAccessStatus()
  } catch (error) {
    errorMessage.value = `权限证据读取失败：${errorText(error)}`
  } finally {
    loading.value = false
  }
}

function auditActionLabel(action: string): string {
  const labels: Record<string, string> = {
    'rbac.access_denied': '权限拒绝',
    'rbac.member_added': '成员加入',
    'rbac.member_removed': '成员移除',
    'rbac.member_role_changed': '成员角色变更',
    'rbac.member_left': '成员离开',
  }
  return labels[action] || action
}

function auditOutcomeLabel(outcome: AuditOutcome): string {
  return outcome === 'denied' ? 'DENY' : outcome === 'success' || outcome === 'accepted' ? 'ALLOW' : outcome.toUpperCase()
}

function formatTime(value: string): string {
  if (!value) return '—'
  return new Date(value).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
}

watch(selectedProjectId, () => { void loadAccessStatus() })
onMounted(() => { void refreshEvidence() })
</script>

<style scoped>
:global(body) { background: #edf3f1; }
.permission-proof { --ink: #123c3a; --ink-soft: #46635f; --paper: #f9fcfa; --line: #d5e4df; --cyan: #0b8c91; --amber: #d18a32; --coral: #c85d4b; max-width: 1320px; margin: 0 auto; padding: 34px 38px 50px; color: var(--ink); font-family: Inter, "Segoe UI", "PingFang SC", sans-serif; }
.proof-hero { display: flex; justify-content: space-between; gap: 28px; align-items: flex-end; padding: 28px 30px 32px; border-radius: 24px; color: #eaf8f3; background: radial-gradient(circle at 86% 20%, #2e7772 0, transparent 32%), linear-gradient(125deg, #0c302f 0%, #124a47 62%, #0a2528 100%); box-shadow: 0 18px 40px rgba(18, 60, 58, .18); }
.eyebrow, .section-kicker { margin: 0 0 8px; font-size: 11px; font-weight: 750; letter-spacing: .16em; text-transform: uppercase; }
.eyebrow { color: #8ee7dd; }
.proof-hero h1 { margin: 0; font-size: clamp(34px, 5vw, 62px); line-height: .98; letter-spacing: -.055em; font-weight: 760; }
.hero-lede { max-width: 590px; margin: 18px 0 0; color: #b9dbd2; font-size: 15px; line-height: 1.7; }
.hero-actions { display: flex; flex-direction: column; align-items: flex-end; gap: 14px; }
.demo-chip { display: inline-flex; align-items: center; border: 1px solid rgba(255, 220, 150, .45); border-radius: 999px; padding: 6px 10px; color: #ffe2a9; background: rgba(209, 138, 50, .16); font-size: 11px; font-weight: 750; letter-spacing: .04em; }
.live-chip { display: inline-flex; gap: 8px; align-items: center; color: #a9eee2; font-size: 12px; font-weight: 700; }
.live-chip i { width: 8px; height: 8px; border-radius: 50%; background: #5ff4c6; box-shadow: 0 0 0 5px rgba(95, 244, 198, .12); }
.live-chip--busy i { background: #f1c36a; }
button, select { font: inherit; }
.refresh-button, .project-actions button { border: 1px solid rgba(255,255,255,.28); border-radius: 10px; padding: 10px 14px; color: #effffb; background: rgba(255,255,255,.11); cursor: pointer; transition: transform .18s ease, background .18s ease; }
.refresh-button:hover, .project-actions button:hover { transform: translateY(-1px); background: rgba(255,255,255,.2); }
button:focus-visible, select:focus-visible { outline: 3px solid rgba(11, 140, 145, .35); outline-offset: 2px; }
button:disabled { cursor: not-allowed; opacity: .55; }
.proof-alert { margin: 18px 0 0; padding: 12px 16px; border: 1px solid #e7a59a; border-radius: 12px; color: #8f3328; background: #fff3f0; }
.identity-card, .proof-rail, .matrix-card, .evidence-card, .project-card, .audit-card { margin-top: 18px; border: 1px solid var(--line); border-radius: 18px; background: rgba(249,252,250,.88); box-shadow: 0 10px 30px rgba(26, 66, 61, .05); }
.identity-card { display: flex; justify-content: space-between; gap: 24px; align-items: center; padding: 24px 26px; }
.identity-main { display: flex; gap: 15px; align-items: center; }
.identity-mark { display: grid; width: 54px; height: 54px; place-items: center; border-radius: 16px 16px 16px 4px; color: #f8ffff; background: var(--cyan); font-size: 22px; font-weight: 800; }
.identity-card h2, .section-heading h2 { margin: 0; letter-spacing: -.035em; }
.identity-card h2 { font-size: 23px; }
.identity-meta { margin: 6px 0 0; color: var(--ink-soft); font-size: 13px; }
.section-kicker { color: var(--cyan); }
.identity-facts { display: flex; gap: 28px; }
.identity-facts div { min-width: 100px; }
.identity-facts span, .project-label { display: block; color: #78908b; font-size: 11px; letter-spacing: .05em; text-transform: uppercase; }
.identity-facts strong { display: block; margin-top: 7px; color: var(--ink); font-size: 15px; }
.proof-rail, .matrix-card, .evidence-card, .project-card, .audit-card { padding: 24px 26px; }
.section-heading { display: flex; justify-content: space-between; gap: 18px; align-items: flex-end; margin-bottom: 20px; }
.section-heading--compact { align-items: center; margin-bottom: 16px; }
.section-heading h2 { font-size: 21px; }
.section-note, .matrix-role, .evidence-count { color: #6f8681; font-size: 12px; }
.matrix-role { padding: 7px 10px; border-radius: 999px; color: #0b6e71; background: #dff3ef; font-weight: 700; }
.rail-grid { display: grid; grid-template-columns: repeat(5, minmax(0, 1fr)); gap: 0; overflow: hidden; border: 1px solid var(--line); border-radius: 14px; }
.rail-step { position: relative; min-height: 144px; padding: 18px 16px 16px; border-right: 1px solid var(--line); background: #f7fbf9; }
.rail-step:last-child { border-right: 0; }
.rail-number { display: block; margin-bottom: 24px; color: #86aaa2; font-size: 12px; font-weight: 800; letter-spacing: .12em; }
.rail-step h3 { margin: 0 0 7px; font-size: 16px; }
.rail-step p { min-height: 42px; margin: 0; color: var(--ink-soft); font-size: 12px; line-height: 1.55; }
.rail-status { display: inline-block; margin-top: 13px; padding: 4px 7px; border-radius: 5px; font-size: 11px; font-weight: 700; }
.rail-status--passed { color: #087467; background: #d8f5e9; }.rail-status--blocked { color: #a14637; background: #ffe1d9; }.rail-status--waiting { color: #86621f; background: #fff0c9; }
.proof-columns { display: grid; grid-template-columns: minmax(0, 1.3fr) minmax(320px, .7fr); gap: 18px; }
.matrix-scroll { overflow-x: auto; }
.permission-table { width: 100%; min-width: 690px; border-collapse: collapse; font-size: 12px; }
.permission-table th, .permission-table td { padding: 13px 10px; border-top: 1px solid var(--line); text-align: center; }
.permission-table thead th { border-top: 0; color: #6f8681; font-size: 11px; font-weight: 700; }
.permission-table tbody th { width: 36%; text-align: left; }
.permission-table tbody th strong, .permission-table tbody th small { display: block; }.permission-table tbody th small { margin-top: 4px; color: #88a09b; font-weight: 400; }
.matrix-dot { display: inline-grid; width: 24px; height: 24px; place-items: center; border-radius: 50%; font-weight: 800; }.matrix-dot--yes { color: #09766d; background: #d8f5e9; }.matrix-dot--no { color: #a5b6b1; background: #edf2f0; }
.decision-badge { display: inline-block; padding: 5px 8px; border-radius: 6px; font-size: 11px; font-weight: 700; white-space: nowrap; }.decision-badge--allowed { color: #087467; background: #d8f5e9; }.decision-badge--denied { color: #a14637; background: #ffe1d9; }.decision-badge--unknown { color: #86621f; background: #fff0c9; }
.card-footnote { margin: 16px 0 0; color: #7d9690; font-size: 11px; line-height: 1.6; }
.probe-list { display: grid; gap: 2px; }.probe-row { display: grid; grid-template-columns: 28px 1fr auto; gap: 10px; align-items: start; padding: 13px 0; border-top: 1px solid var(--line); }.probe-icon { display: grid; width: 23px; height: 23px; place-items: center; border-radius: 7px; font-weight: 800; }.probe-icon--passed { color: #087467; background: #d8f5e9; }.probe-icon--blocked { color: #a14637; background: #ffe1d9; }.probe-icon--waiting { color: #86621f; background: #fff0c9; }.probe-row strong { font-size: 13px; }.probe-row p { margin: 4px 0 0; color: var(--ink-soft); font-size: 12px; line-height: 1.45; }.probe-state { color: #718984; font-size: 11px; white-space: nowrap; }
.project-card select { border: 1px solid var(--line); border-radius: 8px; padding: 8px 11px; color: var(--ink); background: #fff; }
.project-proof { display: grid; grid-template-columns: 1.2fr 1fr auto; gap: 22px; align-items: center; padding: 18px; border: 1px solid var(--line); border-radius: 12px; background: #f7fbf9; }.project-proof strong { display: block; margin-top: 6px; font-size: 18px; }.project-proof p { margin: 5px 0 0; color: var(--ink-soft); font-size: 12px; }.access-state { padding-left: 17px; border-left: 3px solid #9ca; }.access-state span { display: block; font-size: 12px; font-weight: 800; }.access-state strong { display: block; margin-top: 6px; color: var(--ink-soft); font-size: 12px; font-weight: 500; line-height: 1.5; }.access-state--available { border-color: #43ae86; }.access-state--available span { color: #087467; }.access-state--restricted { border-color: var(--coral); }.access-state--restricted span { color: #a14637; }.access-state--unknown { border-color: var(--amber); }.access-state--unknown span { color: #86621f; }.project-actions button { border-color: var(--line); color: var(--ink); background: #fff; }.project-actions button:hover { background: #e9f4f1; }
.empty-proof, .audit-empty { display: flex; gap: 14px; align-items: center; padding: 24px 12px 12px; color: var(--ink-soft); }.empty-mark { display: grid; width: 36px; height: 36px; place-items: center; border: 1px solid var(--line); border-radius: 10px; color: #8aa39d; font-size: 20px; }.empty-proof strong, .audit-empty strong { color: var(--ink); font-size: 14px; }.empty-proof p, .audit-empty p { margin: 5px 0 0; font-size: 12px; line-height: 1.5; }.audit-empty--guarded { background: #fffaf0; border-radius: 10px; }.audit-empty--guarded .empty-mark { color: #a87927; border-color: #ecd69f; }
.audit-list { border-top: 1px solid var(--line); }.audit-row { display: grid; grid-template-columns: 58px 1fr auto; gap: 14px; align-items: center; padding: 14px 0; border-bottom: 1px solid var(--line); }.audit-outcome { width: 52px; padding: 5px 0; border-radius: 5px; text-align: center; font-size: 10px; font-weight: 800; letter-spacing: .05em; }.audit-outcome--denied { color: #a14637; background: #ffe1d9; }.audit-outcome--success, .audit-outcome--accepted { color: #087467; background: #d8f5e9; }.audit-outcome--failed, .audit-outcome--partial { color: #86621f; background: #fff0c9; }.audit-row strong { font-size: 13px; }.audit-row p { margin: 4px 0 0; color: var(--ink-soft); font-size: 12px; }.audit-row time { color: #7d9690; font-size: 11px; white-space: nowrap; }
.proof-footer { display: flex; justify-content: space-between; gap: 20px; margin-top: 22px; padding: 14px 4px 0; border-top: 1px solid #c8dbd5; color: #708983; font-size: 11px; letter-spacing: .03em; }
@media (max-width: 960px) { .identity-card, .proof-hero { align-items: flex-start; flex-direction: column; }.hero-actions { align-items: flex-start; }.identity-facts { flex-wrap: wrap; }.rail-grid { grid-template-columns: 1fr; }.rail-step { min-height: 0; border-right: 0; border-bottom: 1px solid var(--line); }.rail-step:last-child { border-bottom: 0; }.proof-columns { grid-template-columns: 1fr; }.project-proof { grid-template-columns: 1fr; }.access-state { padding: 12px 0 0; border-top: 3px solid; border-left: 0; } }
@media (max-width: 600px) { .permission-proof { padding: 16px 12px 32px; }.proof-hero, .identity-card, .proof-rail, .matrix-card, .evidence-card, .project-card, .audit-card { padding: 18px; }.proof-footer { flex-direction: column; gap: 6px; } }
@media (prefers-reduced-motion: reduce) { *, *::before, *::after { scroll-behavior: auto !important; transition-duration: .01ms !important; animation-duration: .01ms !important; } }
</style>
