<template>
  <section class="member-panel" aria-labelledby="project-members-heading">
    <div class="member-panel__heading">
      <div>
        <h3 id="project-members-heading">项目成员与权限</h3>
        <p class="muted">查看项目内配置推导出的能力和章节/资料范围；租户状态及资料实时授权仍由服务端逐请求复核。</p>
      </div>
      <button v-if="editable" type="button" :disabled="!canSave" @click="save">
        {{ saving ? '保存中…' : '保存权限' }}
      </button>
    </div>

    <p v-if="!editable" class="muted">你可以查看成员权限；只有项目 Owner 或 Admin 可以管理成员。</p>
    <p v-if="canPersistReason && editable" class="member-warning" role="status">{{ canPersistReason }}</p>
    <p v-if="editable && changed && canSave" class="muted" role="status">有未保存的权限改动。</p>

    <p v-if="externalChange" class="member-warning" role="alert">成员或课题版本已更新；本地权限草稿已保留。提交仍按当前版本校验，请先比较成员范围。</p>
    <ul class="member-list">
      <li v-for="member in project.members" :key="member.user_id" class="member-card">
        <div class="member-card__summary">
          <strong>{{ member.user_id }}</strong>
          <span class="member-badge">{{ governanceLabel(member) }}</span>
          <span class="member-badge" :class="{ 'member-badge--muted': member.status === 'suspended' }">{{ statusLabel(member.status) }}</span>
          <p class="member-capabilities">可执行：{{ capabilitySummary(member) }}</p>
          <p class="muted">职能：{{ functionRoleSummary(member) }}</p>
          <p v-for="role in member.function_roles ?? []" :key="role" class="muted member-scope-summary">
            {{ functionRoleLabel(role) }}范围：{{ scopeSummary(member, role) }}
          </p>
        </div>

        <fieldset v-if="editable && member.role !== 'owner'" class="member-editor"
          :disabled="saving || member.status === 'suspended' || !supportedMember(member)">
          <legend>编辑 {{ member.user_id }}</legend>
          <label class="member-field">
            <span>治理角色</span>
            <select v-model="draftFor(member.user_id).governance_role" :aria-label="member.user_id + ' 治理角色'">
              <option value="member">普通成员</option>
              <option value="admin">管理员</option>
            </select>
          </label>
          <fieldset class="member-functions">
            <legend>职能角色（可多选）</legend>
            <label v-for="role in functionRoleOptions" :key="role" class="member-check">
              <input type="checkbox" :checked="draftFor(member.user_id).function_roles.includes(role)"
                :aria-label="member.user_id + ' ' + functionRoleLabel(role)"
                @change="toggleRole(member.user_id, role, $event)" />
              {{ functionRoleLabel(role) }}
            </label>
          </fieldset>

          <label v-for="role in scopedRolesFor(member.user_id)" :key="role" class="member-field member-scope-editor">
            <span>{{ functionRoleLabel(role) }}章节范围</span>
            <select multiple :value="chapterSelection(member.user_id, role)"
              :aria-label="member.user_id + ' ' + functionRoleLabel(role) + '章节范围'"
              @change="updateChapterSelection(member.user_id, role, $event)">
              <option v-for="chapter in chapters" :key="chapter.id" :value="chapter.id" :selected="chapterSelection(member.user_id, role).includes(chapter.id)">{{ chapter.title || chapter.id }}</option>
            </select>
            <small>不选择章节 = 该职能没有章节写入/评审权限。</small>
          </label>

          <label v-if="draftFor(member.user_id).function_roles.includes('researcher')" class="member-field member-scope-editor">
            <span>研究者资料范围</span>
            <select multiple :value="assetSelection(member.user_id)"
              :aria-label="member.user_id + ' 研究者资料范围'"
              @change="updateAssetSelection(member.user_id, $event)">
              <option v-for="asset in assets" :key="asset.id" :value="asset.id" :selected="assetSelection(member.user_id).includes(asset.id)">{{ asset.title || asset.id }}</option>
            </select>
            <small>不选择资料 = 该职能没有项目资料使用范围。</small>
          </label>
          <p v-if="member.status === 'suspended'" class="muted">已暂停成员在本切片中只读；恢复/暂停流程后续实现。</p>
        </fieldset>
        <p v-else-if="editable && member.role !== 'owner' && !supportedMember(member)" class="member-warning">
          该成员包含当前编辑器无法安全保留的权限状态/范围；为避免覆盖权限，暂不可批量保存。
        </p>
      </li>
    </ul>
    <p v-if="editable" class="muted member-lifecycle-note">
      本次仅管理已有成员的角色和范围；邀请、重新发送、暂停/移除及租户恢复流程尚未包含在此切片。
    </p>
  </section>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { Asset, Chapter, Project, ProjectFunctionRole, ProjectMember, ProjectMemberInput } from '@/api/lingdoc/workspace'
import {
  assetIdsForSelection, chapterScopesForSelection, createProjectMemberDrafts,
  hasOrphanedFunctionScopes, hasUnsupportedFunctionRoles, hasUnsupportedMemberState,
  effectiveCapabilitySummary, projectFunctionRoles, serializeProjectMemberDrafts, setAssetScopes, setChapterScopes,
  setProjectFunctionRole, type ProjectMemberDraft,
} from './projectMembers'

const props = defineProps<{
  project: Project
  chapters: Chapter[]
  assets: Asset[]
  editable: boolean
  saving: boolean
}>()
const emit = defineEmits<{ save: [members: ProjectMemberInput[]]; dirty: [value: boolean] }>()
const externalChange = ref(false)
const drafts = ref<ProjectMemberDraft[]>(createProjectMemberDrafts(props.project.members))
const original = ref(JSON.stringify(serializeProjectMemberDrafts(drafts.value)))
const functionRoleOptions = projectFunctionRoles
const byUser = computed(() => new Map(drafts.value.map(member => [member.user_id, member])))
const changed = computed(() => JSON.stringify(serializeProjectMemberDrafts(drafts.value)) !== original.value)
const unsupported = computed(() => props.project.members.some(member =>
  hasUnsupportedMemberState(member) || hasUnsupportedFunctionRoles(member) || hasOrphanedFunctionScopes(member)))
const canPersistReason = computed(() => {
  if (unsupported.value) return '成员存在本编辑器未覆盖的状态或权限范围；请先完成对应生命周期/权限处理，再编辑其他成员。'
  return ''
})
const canSave = computed(() => props.editable && changed.value && !props.saving && !unsupported.value)

watch(changed, value => emit('dirty', value), { immediate: true })
watch(() => props.project, (project, previous) => {
  const next = createProjectMemberDrafts(project.members)
  const nextOriginal = JSON.stringify(serializeProjectMemberDrafts(next))
  if (project.id === previous?.id && changed.value && nextOriginal !== JSON.stringify(serializeProjectMemberDrafts(drafts.value))) {
    externalChange.value = true
    return
  }
  drafts.value = next
  original.value = nextOriginal
  externalChange.value = false
}, { deep: true })

function draftFor(userId: string): ProjectMemberDraft {
  const draft = byUser.value.get(userId)
  if (!draft) throw new Error('项目成员草稿不存在')
  return draft
}

function functionRoleLabel(role: string): string {
  return ({ author: '撰写者', researcher: '资料研究者', reviewer: '评审者', observer: '观察者' } as Record<string, string>)[role] ?? role
}

function functionRoleSummary(member: ProjectMember): string {
  const roles = member.function_roles ?? []
  return roles.length ? roles.map(functionRoleLabel).join('、') : '未分配职能角色'
}

function capabilitySummary(member: ProjectMember): string {
  return effectiveCapabilitySummary(member)
}

function governanceLabel(member: ProjectMember): string {
  const role = member.governance_role ?? (member.role === 'owner' ? 'owner' : 'member')
  return ({ owner: 'Owner', admin: '管理员', member: '普通成员' } as Record<string, string>)[role] ?? role
}

function statusLabel(status?: string): string {
  return ({ active: '有效', suspended: '已暂停' } as Record<string, string>)[status ?? 'active'] ?? (status || '状态未知')
}

function scopeSummary(member: ProjectMember, role: string): string {
  const scopes = member.function_scopes?.[role] ?? []
  if (!scopes.length) return '未指定（无该职能的范围权限）'
  return scopes.map(scope => {
    if (scope.startsWith('asset:')) {
      const id = scope.slice('asset:'.length)
      return props.assets.find(asset => asset.id === id)?.title ?? id
    }
    return props.chapters.find(chapter => chapter.id === scope)?.title ?? scope
  }).join('、')
}

function scopedRolesFor(userId: string): ProjectFunctionRole[] {
  return (['author', 'reviewer'] as ProjectFunctionRole[]).filter(role => draftFor(userId).function_roles.includes(role))
}

function toggleRole(userId: string, role: ProjectFunctionRole, event: Event): void {
  const enabled = (event.target as HTMLInputElement).checked
  setProjectFunctionRole(draftFor(userId), role, enabled)
}

function chapterSelection(userId: string, role: ProjectFunctionRole): string[] {
  return chapterScopesForSelection(draftFor(userId).function_scopes[role] ?? [], props.chapters.map(chapter => chapter.id))
}

function updateChapterSelection(userId: string, role: ProjectFunctionRole, event: Event): void {
  const select = event.target as HTMLSelectElement
  const selected = Array.from(select.selectedOptions, option => option.value)
  const draft = draftFor(userId)
  draft.function_scopes[role] = setChapterScopes(draft.function_scopes[role] ?? [], selected, props.chapters.map(chapter => chapter.id))
}

function assetSelection(userId: string): string[] {
  return assetIdsForSelection(draftFor(userId).function_scopes.researcher ?? [])
}

function updateAssetSelection(userId: string, event: Event): void {
  const select = event.target as HTMLSelectElement
  const selected = Array.from(select.selectedOptions, option => option.value)
  const draft = draftFor(userId)
  draft.function_scopes.researcher = setAssetScopes(draft.function_scopes.researcher ?? [], selected)
}

function supportedMember(member: ProjectMember): boolean {
  return !hasUnsupportedMemberState(member) && !hasUnsupportedFunctionRoles(member) && !hasOrphanedFunctionScopes(member)
}

function save(): void {
  if (!canSave.value) return
  emit('save', serializeProjectMemberDrafts(drafts.value))
}
</script>

<style scoped>
.member-panel { margin: 18px 0; padding: 16px; border: 1px solid #dbe5dd; border-radius: 8px; background: #fbfdfb; }
.member-panel__heading { display: flex; justify-content: space-between; align-items: flex-start; gap: 16px; }
.member-panel h3 { margin: 0 0 6px; font-size: 17px; }
.member-list { display: grid; gap: 12px; margin: 14px 0 0; padding: 0; list-style: none; }
.member-card { padding: 12px; border: 1px solid #dbe5dd; border-radius: 7px; background: #fff; }
.member-card__summary > strong { margin-right: 8px; }
.member-capabilities { margin: 8px 0; padding: 8px 10px; border-left: 3px solid #0b8c91; background: #f1f8f5; font-size: 13px; }
.member-badge { display: inline-block; margin: 0 5px 4px 0; padding: 2px 7px; border-radius: 12px; color: #176356; background: #e5f6f2; font-size: 12px; }
.member-badge--muted { color: #735215; background: #fff3d8; }
.member-scope-summary { margin-left: 8px; }
.member-editor, .member-functions { display: grid; gap: 8px; margin: 12px 0 0; padding: 12px; border: 1px solid #dbe5dd; border-radius: 7px; }
.member-field { display: grid; grid-template-columns: 150px minmax(180px, 1fr); align-items: start; gap: 8px 12px; font-size: 13px; }
.member-field select[multiple] { min-height: 88px; }
.member-field small { grid-column: 2; color: #6b7670; }
.member-check { display: inline-flex; align-items: center; gap: 6px; margin-right: 14px; font-weight: 400; }
.member-warning { color: #8b5b10; font-size: 13px; }
.member-lifecycle-note { margin-top: 12px; }
fieldset:disabled { opacity: .7; }
@media (max-width: 700px) { .member-panel__heading { flex-direction: column; } .member-field { grid-template-columns: 1fr; } .member-field small { grid-column: 1; } }
</style>
