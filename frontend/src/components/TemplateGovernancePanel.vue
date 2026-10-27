<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import type { Chapter, Source } from '@/api/lingdoc/workspace'
import {
 acceptRuleSuggestion, createRuleSetDraft, createTemplateProfile, getRuleSuggestion, governRuleSet,
 listReplayFixtures, listRuleDefinitions, listRuleSetVersions, listRuleSuggestions, listTemplateAuditHistory, listTemplateProfiles, listTemplateVersions,
 publishTemplateVersion, recallTemplateVersion, rejectRuleSuggestion, requestRuleSuggestion, saveTemplateDraft,
 withdrawTemplateVersion, type GovernanceProject, type ReplayFixtureSummary, type RuleBinding, type RuleDefinition, type RuleSet,
 type RuleSuggestionRun, type SaveTemplateDraftInput, type TemplateAuditEvent, type TemplateCatalogVersion, type TemplateProfile,
} from '@/api/lingdoc/templateGovernance'
import { newIdempotencyKey } from '@/api/lingdoc/candidateAdoption'
import { catalogMatchesProject, cloneDraftInput, fixturesForReplayTarget, governanceOperationError, mutationAttempt, parseSourceRefs, projectBasis, projectRuleSet, runModeLabel,
 splitIds, stateLabel, suggestionBindings, type MutationAttempt, type ReplayTarget } from './templateGovernanceState'
import TemplateGovernanceFixturePicker from './TemplateGovernanceFixturePicker.vue'

const props = withDefaults(defineProps<{ project: GovernanceProject; chapters: Chapter[]; sources?: Source[]; canManageCatalog?: boolean }>(), { sources: () => [], canManageCatalog: false })
const emit = defineEmits<{ refresh: []; error: [message: string]; dataDirty: [dirty: boolean]; close: [] }>()
const busy = ref(false), errorMessage = ref(''), message = ref(''), readbackNotice = ref('')
const managerDenied = ref(false), managerAccessNotice = ref('')
const managerAccess = computed(() => props.canManageCatalog && !managerDenied.value)
const tab = ref<'catalog' | 'rules' | 'suggestions'>('catalog')
const profiles = ref<TemplateProfile[]>([]), versions = ref<TemplateCatalogVersion[]>([])
const catalogLoading = ref(false), catalogError = ref('')
const templateIdFilter = ref('')
const visibleProfiles = computed(() => {
 const id = templateIdFilter.value.trim().toLowerCase()
 return id ? profiles.value.filter(profile => profile.template_id.toLowerCase().includes(id)) : profiles.value
})
const catalogNotice = computed(() => {
 if (catalogLoading.value) return '正在读取模板目录…'
 if (catalogError.value) return `模板目录读取失败：${catalogError.value} 请重试。`
 if (visibleProfiles.value.length) return ''
 return templateIdFilter.value.trim() || scopeFilter.value.trim() || stateFilter.value ? '当前筛选下没有模板。' : '当前没有模板档案。'
})
const profileId = ref(''), selectedVersionId = ref(''), versionError = ref(''), versionNotice = ref('')
const auditEvents = ref<TemplateAuditEvent[]>([]), auditNextId = ref<number | undefined>()
const auditLoading = ref(false), auditError = ref('')
const definitions = ref<RuleDefinition[]>([]), rulesets = ref<RuleSet[]>([])
const ruleSetId = ref(''), ruleSetVersion = ref(''), ruleReadError = ref('')
const runs = ref<RuleSuggestionRun[]>([]), selectedRunId = ref('')
const scopeFilter = ref(''), stateFilter = ref('')
const replayFixtures = ref<ReplayFixtureSummary[]>([]), fixtureCatalogError = ref('')
const fixtureTarget = computed<ReplayTarget | undefined>(() => {
 if (tab.value === 'rules') {
  const id = replayTemplate.id.trim(), version = replayTemplate.version.trim()
  return id && version ? { template_id: id, template_version: version } : undefined
 }
 const version = selectedVersion.value
 return version ? { template_id: version.template_id, template_version: version.version } : undefined
})
const targetFixtures = computed(() => fixturesForReplayTarget(replayFixtures.value, fixtureTarget.value))
const profileForm = reactive({ template_id: '', name: '', reason: '' })
const governance = reactive({ reason: '', fixtures: '', migration_advice: '' })
const replayTemplate = reactive({ id: props.project.template_id, version: props.project.template_version })
const attempts = new Map<string, MutationAttempt>()
const selectedVersion = computed(() => versions.value.find(v => v.version === selectedVersionId.value))
const selectedRuleSet = computed(() => rulesets.value.find(v => v.version === ruleSetVersion.value))
const selectedRun = computed(() => runs.value.find(v => v.id === selectedRunId.value))
const currentBinding = computed(() => projectRuleSet(props.project))
const knownSources = computed(() => props.sources.filter(source => source.project_id === props.project.id && source.status === 'available'))
const selectedSourceIds = ref<string[]>([])
const targetKind = ref<'field' | 'chapter'>('field'), targetId = ref('')
const targetOptions = computed(() => targetKind.value === 'field'
 ? (selectedVersion.value?.definition.fields || []).map(field => ({ id: field.field_id, title: field.label }))
 : (selectedVersion.value?.definition.sections || []).map(section => ({ id: section.section_id, title: section.title })))
const canRequest = computed(() => catalogMatchesProject(props.project, selectedVersion.value) && selectedVersion.value?.state === 'published' && !!targetId.value &&
 !!currentBinding.value.ruleset_id && !!currentBinding.value.version && !!currentBinding.value.ruleset_hash)
interface TemplateDraftUI { input: SaveTemplateDraftInput; termsJSON: string; sourcesText: string; policiesJSON: string; initial: string }
const draftCache = reactive<Record<string, TemplateDraftUI>>({}), draftKey = ref('')
const templateDraft = computed(() => draftCache[draftKey.value])
const fingerprint = (draft: TemplateDraftUI) => JSON.stringify({ input: draft.input, terms: draft.termsJSON, sources: draft.sourcesText, policies: draft.policiesJSON })
const templateDirty = computed(() => Object.values(draftCache).some(draft => fingerprint(draft) !== draft.initial))
interface BindingEditor { binding: RuleBinding; params: string; positive: string; negative: string }
interface RuleDraftUI { ruleset_id: string; version: string; reason: string; bindings: BindingEditor[]; initial: string }
const ruleDraft = ref<RuleDraftUI | null>(null), retainedRuleDrafts = reactive<RuleDraftUI[]>([]), decisionReason = ref('')
const acceptDraftCache = reactive<Record<string, RuleDraftUI>>({})
const acceptDraft = computed<RuleDraftUI | null>({ get: () => acceptDraftCache[selectedRunId.value] || null, set: value => { if (value) acceptDraftCache[selectedRunId.value] = value; else delete acceptDraftCache[selectedRunId.value] } })
function ruleFingerprint(draft: RuleDraftUI) { return JSON.stringify({ id: draft.ruleset_id, version: draft.version, reason: draft.reason, bindings: draft.bindings }) }
const hasUnsavedChanges = computed(() => templateDirty.value ||
 !!ruleDraft.value && ruleFingerprint(ruleDraft.value) !== ruleDraft.value.initial ||
 Object.values(acceptDraftCache).some(draft => ruleFingerprint(draft) !== draft.initial) ||
 retainedRuleDrafts.some(draft => ruleFingerprint(draft) !== draft.initial) ||
 !!profileForm.template_id || !!profileForm.name || !!profileForm.reason || !!decisionReason.value)
watch(hasUnsavedChanges, dirty => emit('dataDirty', dirty), { immediate: true })
defineExpose({ hasUnsavedChanges, busy })
function key(operation: string, input: unknown) {
 const attempt = mutationAttempt(attempts.get(operation), input, newIdempotencyKey)
 attempts.set(operation, attempt); return attempt.key
}
let catalogAccessEpoch = 0
let catalogRequestEpoch = 0
let profileSelectionEpoch = 0
let auditRequestEpoch = 0
const staleCatalogOperationMessage = '项目或权限已变化，操作结果尚未确认；请重新读取后再继续。'
const confirmedCatalogOperationMessage = '操作已成功，但项目或权限已变化；请重新读取当前状态。'
const deniedCatalogOperationMessage = '权限不足或资源当前不可访问，请重新读取并联系管理员。'
let confirmedWrite = false
function permissionDenied(error: unknown) {
 if (!error || typeof error !== 'object') return false
 const payload = error as Record<string, unknown>
 const nested = payload.error && typeof payload.error === 'object' ? payload.error as Record<string, unknown> : undefined
 // A source denial or a human-decision requirement does not establish loss of
 // catalog management permission. Keep the user's draft for correction.
 if (nested?.code === 'source_access_denied' || nested?.code === 'human_decision_required') return false
 return payload.$httpStatus === 403 || payload.status === 403 || nested?.code === 'permission_denied'
}
function resourceUnavailable(error: unknown) {
 if (!error || typeof error !== 'object') return false
 const payload = error as Record<string, unknown>
 const nested = payload.error && typeof payload.error === 'object' ? payload.error as Record<string, unknown> : undefined
 return payload.$httpStatus === 404 || payload.status === 404 || nested?.code === 'not_found'
}
function revokeManagerView(unavailable = false) {
 managerAccessNotice.value = unavailable
  ? '模板资源不存在或当前不可访问，已清除原有目录和版本数据。请重新读取目录。'
  : '服务器拒绝了当前访问，已清除原有目录和版本数据。'
 // Invalidate every in-flight catalog/profile/version read before hiding data.
 // Late responses from the former access scope must never restore draft data.
 managerDenied.value = true; catalogAccessEpoch++; catalogRequestEpoch++; profileSelectionEpoch++; auditRequestEpoch++
 catalogLoading.value = false; catalogError.value = unavailable ? '模板资源不存在或当前不可访问。' : '权限状态已变化。'
 auditEvents.value = []; auditNextId.value = undefined; auditLoading.value = false
 profiles.value = []; versions.value = []; selectedVersionId.value = ''; versionError.value = '权限状态已变化；请重新读取版本。'; versionNotice.value = ''
 definitions.value = []; rulesets.value = []; replayFixtures.value = []; ruleDraft.value = null; retainedRuleDrafts.splice(0)
 for (const id of Object.keys(draftCache)) delete draftCache[id]
 draftKey.value = ''; governance.reason = ''; governance.fixtures = ''; governance.migration_advice = ''
 if (tab.value === 'rules') tab.value = 'catalog'
 if (stateFilter.value === 'draft') stateFilter.value = ''
}
function operationError(error: unknown) {
 if (error && typeof error === 'object') {
  const payload = error as Record<string, unknown>
  const nested = payload.error && typeof payload.error === 'object' ? payload.error as Record<string, unknown> : undefined
  if (permissionDenied(error)) return '权限不足或权限已发生变化，请重新读取并联系管理员。'
  if (nested?.code === 'version_conflict' || payload.code === 'version_conflict') return governanceOperationError(error)
  // The request wrapper rejects plain objects, so Error.message alone misses
  // server explanations such as source denial and required replay fixtures.
  const serverMessage = typeof nested?.message === 'string' ? nested.message : payload.message
  if (typeof serverMessage === 'string' && serverMessage.trim()) return serverMessage
 }
 return governanceOperationError(error)
}
function clearAudit() {
 auditRequestEpoch++
 auditEvents.value = []; auditNextId.value = undefined; auditLoading.value = false; auditError.value = ''
}
async function loadAudit(append = false) {
 const id = profileId.value, epoch = catalogAccessEpoch
 if (!props.canManageCatalog || !id) { clearAudit(); return }
 const afterId = append ? auditNextId.value : undefined
 if (append && !afterId) return
 const requestEpoch = ++auditRequestEpoch
 auditLoading.value = true; auditError.value = ''
 if (!append) { auditEvents.value = []; auditNextId.value = undefined }
 try {
  const response = await listTemplateAuditHistory(id, afterId)
  if (epoch !== catalogAccessEpoch || requestEpoch !== auditRequestEpoch || !props.canManageCatalog || profileId.value !== id) return
  auditEvents.value = append ? [...auditEvents.value, ...response.data.events] : response.data.events
  managerDenied.value = false; managerAccessNotice.value = ''
  auditNextId.value = response.data.next_after_id
 } catch (error: unknown) {
  if (epoch === catalogAccessEpoch && requestEpoch === auditRequestEpoch && props.canManageCatalog && profileId.value === id) {
   // Do not retain previously authorized data after a failed reauthorization.
   auditEvents.value = []; auditNextId.value = undefined
   if (permissionDenied(error) || resourceUnavailable(error)) revokeManagerView(resourceUnavailable(error))
   auditError.value = operationError(error)
  }
 } finally {
  if (epoch === catalogAccessEpoch && requestEpoch === auditRequestEpoch) auditLoading.value = false
 }
}
async function perform(action: () => Promise<void>) {
 if (busy.value) return
 const epoch = catalogAccessEpoch
 busy.value = true; errorMessage.value = ''; message.value = ''; readbackNotice.value = ''; confirmedWrite = false
 // Workspace also displays this panel's errors. Clear both surfaces together
 // so a successful retry is not still presented as a failed operation.
 emit('error', '')
 try {
  await action()
  if (epoch !== catalogAccessEpoch) {
   errorMessage.value = confirmedWrite ? confirmedCatalogOperationMessage : managerDenied.value ? deniedCatalogOperationMessage : staleCatalogOperationMessage
   emit('error', errorMessage.value)
  }
 }
 catch (error: unknown) {
 if (epoch !== catalogAccessEpoch) {
  errorMessage.value = confirmedWrite ? confirmedCatalogOperationMessage : managerDenied.value ? deniedCatalogOperationMessage : staleCatalogOperationMessage
  emit('error', errorMessage.value)
 } else {
  if (permissionDenied(error)) revokeManagerView()
  errorMessage.value = operationError(error)
  emit('error', errorMessage.value)
 }
 } finally { busy.value = false }
}
async function loadCatalog() {
 const epoch = catalogAccessEpoch, requestEpoch = ++catalogRequestEpoch
 catalogLoading.value = true; catalogError.value = ''
 try {
  const result = await listTemplateProfiles({ scope: scopeFilter.value || undefined, state: stateFilter.value || undefined })
  if (epoch !== catalogAccessEpoch || requestEpoch !== catalogRequestEpoch) return
  profiles.value = result.data
  if (profileId.value && !profiles.value.some(profile => profile.template_id === profileId.value)) {
   profileSelectionEpoch++
   profileId.value = ''; versions.value = []; selectedVersionId.value = ''; versionError.value = ''; clearAudit()
  }
  if (!props.canManageCatalog && managerDenied.value && !profileId.value) { managerDenied.value = false; managerAccessNotice.value = '' }
 } catch (error: unknown) {
  if (epoch === catalogAccessEpoch && requestEpoch === catalogRequestEpoch) {
   profiles.value = []; versions.value = []; profileId.value = ''; selectedVersionId.value = ''
   if (permissionDenied(error) || resourceUnavailable(error)) revokeManagerView(resourceUnavailable(error))
   catalogError.value = operationError(error)
   versionError.value = ''; clearAudit()
  }
 } finally {
  if (epoch === catalogAccessEpoch && requestEpoch === catalogRequestEpoch) catalogLoading.value = false
 }
}
async function refreshVersions() {
 const epoch = catalogAccessEpoch, id = profileId.value
 if (!id) return
 try {
  const result = await listTemplateVersions(id)
  if (epoch === catalogAccessEpoch && profileId.value === id) {
   const previous = selectedVersionId.value
   versions.value = result.data; versionError.value = ''; versionNotice.value = ''
   if (previous && !result.data.some(version => version.version === previous)) {
    selectedVersionId.value = ''; versionNotice.value = `历史版本 ${previous} 当前不可用，请选择其他版本或重新读取。`
   } else if (!previous) selectedVersionId.value = result.data[0]?.version || ''
  }
 } catch (error: unknown) {
  if (epoch === catalogAccessEpoch && profileId.value === id) {
   if (permissionDenied(error) || resourceUnavailable(error)) revokeManagerView(resourceUnavailable(error))
   versionError.value = operationError(error)
  }
  throw error
 }
}
async function retryOrdinaryCatalogAccess() {
 if (props.canManageCatalog) return
 await perform(async () => {
  const priorId = profileId.value
  await loadCatalog()
  if (catalogError.value) return
  // Directory visibility alone does not prove the selected version can still
  // be read; reauthorize that detail before dismissing the unavailable state.
  if (priorId && profileId.value === priorId) await refreshVersions()
  if (!props.canManageCatalog) {
   managerDenied.value = false; managerAccessNotice.value = ''
   if (priorId && !profileId.value) message.value = '原模板当前不在可见目录中，请选择其他模板。'
  }
 })
}
async function chooseProfile(id: string) {
 await perform(async () => {
 const epoch = catalogAccessEpoch, selectionEpoch = ++profileSelectionEpoch
 let response: Awaited<ReturnType<typeof listTemplateVersions>>
 try { response = await listTemplateVersions(id) }
 catch (error: unknown) {
  if (epoch === catalogAccessEpoch && selectionEpoch === profileSelectionEpoch && (permissionDenied(error) || resourceUnavailable(error))) revokeManagerView(resourceUnavailable(error))
  throw error
 }
 if (epoch !== catalogAccessEpoch || selectionEpoch !== profileSelectionEpoch) return
 profileId.value = id; versions.value = response.data; selectedVersionId.value = response.data[0]?.version || ''; versionError.value = ''; versionNotice.value = ''
 if (!props.canManageCatalog) { managerDenied.value = false; managerAccessNotice.value = '' }
 clearAudit()
 if (props.canManageCatalog) await loadAudit()
 if (epoch !== catalogAccessEpoch || selectionEpoch !== profileSelectionEpoch || profileId.value !== id) return
 draftKey.value = ''; governance.reason = ''; governance.fixtures = ''; governance.migration_advice = ''
 if (selectedVersion.value) { replayTemplate.id = selectedVersion.value.template_id; replayTemplate.version = selectedVersion.value.version }
 const setId = selectedVersion.value?.ruleset.ruleset_id
 if (setId && managerAccess.value) {
  const ruleResponse = await listRuleSetVersions(setId)
  if (epoch !== catalogAccessEpoch || selectionEpoch !== profileSelectionEpoch) return
  ruleSetId.value = setId; rulesets.value = ruleResponse.data; ruleSetVersion.value = selectedVersion.value?.ruleset.version || ''
 }
 })
}
function chooseVersion(id: string) {
 selectedVersionId.value = id; draftKey.value = ''; versionNotice.value = ''
 if (selectedVersion.value) { replayTemplate.id = selectedVersion.value.template_id; replayTemplate.version = selectedVersion.value.version }
 governance.reason = ''; governance.fixtures = ''; governance.migration_advice = ''
}
// A successful mutation and the subsequent reads are different outcomes.
// Never invite a retry of a write that the server has already accepted.
async function refreshAfterTemplateWrite() {
 const epoch = catalogAccessEpoch, id = profileId.value
 const failures: string[] = []
 try { await refreshVersions() } catch { failures.push('版本') }
 if (epoch !== catalogAccessEpoch || profileId.value !== id) return
 if (props.canManageCatalog) {
  await loadAudit()
  if (auditError.value) failures.push('审计')
 }
 if (epoch !== catalogAccessEpoch || profileId.value !== id) return
 await loadCatalog()
 if (catalogError.value) failures.push('目录')
 if (epoch === catalogAccessEpoch && failures.length) readbackNotice.value = `操作已成功，但${failures.join('、')}读取未完成。请重新读取；不要重复提交。`
 else if (epoch === catalogAccessEpoch && id && !profiles.value.some(profile => profile.template_id === id)) readbackNotice.value = '操作已成功，但当前筛选未包含此模板。请调整筛选后继续。'
}
async function createProfile() {
 if (!managerAccess.value) return
 await perform(async () => {
 const epoch = catalogAccessEpoch
 const input = { ...profileForm }, result = await createTemplateProfile(input, key('create-profile', input))
 confirmedWrite = true
 if (epoch !== catalogAccessEpoch) throw new Error(staleCatalogOperationMessage)
 profileForm.template_id = ''; profileForm.name = ''; profileForm.reason = ''
 profileId.value = result.data.template_id; versions.value = []; selectedVersionId.value = ''; versionError.value = ''; clearAudit()
 message.value = '模板档案已创建，请准备版本草稿。'; emit('refresh')
 await refreshAfterTemplateWrite()
 })
}
function emptyDraft(): SaveTemplateDraftInput {
 return { template_id: profileId.value, version: '', expected_revision: 0, definition: { fields: [], sections: [], terms: [], required_fields: [] },
 binding_policies: [], ruleset: { ruleset_id: ruleSetId.value, version: selectedRuleSet.value?.version || '' }, source_type: 'project_copy', scope: '', source_refs: [], curator: '', reason: '' }
}
function editTemplate(asNew: boolean) {
 if (!profileId.value || !asNew && selectedVersion.value?.state !== 'draft') return
 const source = selectedVersion.value, cacheKey = asNew ? `${profileId.value}/new/${Date.now()}` : `${profileId.value}/${source?.version}`
 if (!draftCache[cacheKey]) {
 const input = source ? cloneDraftInput(source) : emptyDraft()
 if (asNew) { input.version = ''; input.expected_revision = 0 }
 const draft: TemplateDraftUI = { input, policiesJSON: JSON.stringify(input.binding_policies || [], null, 2), termsJSON: JSON.stringify(input.definition.terms || [], null, 2),
 sourcesText: input.source_refs.map(ref => `${ref.source_id}@${ref.source_version}`).join('\n'), initial: '' }
 draft.initial = fingerprint(draft); draftCache[cacheKey] = draft
 }
 draftKey.value = cacheKey
}
function addField() { templateDraft.value?.input.definition.fields.push({ field_id: '', label: '', type: 'string', required: false, order: (templateDraft.value?.input.definition.fields.length || 0) + 1 }) }
function enumOptions(index: number, event: Event) { const field=templateDraft.value?.input.definition.fields[index]; if(field) field.options=(event.target as HTMLTextAreaElement).value.split(/\r?\n/).map(value=>value.trim()).filter(Boolean) }
function addSection() {
 const draft = templateDraft.value
 if (draft) draft.input.definition.sections.push({ section_id: '', title: '', required: false, order: draft.input.definition.sections.length + 1 })
}
async function saveDraft() {
 const editor = templateDraft.value
 if (!editor || versionError.value || !managerAccess.value) return
 await perform(async () => {
 const epoch = catalogAccessEpoch
 const input: SaveTemplateDraftInput = JSON.parse(JSON.stringify(editor.input))
 input.definition.terms = JSON.parse(editor.termsJSON)
 input.binding_policies = JSON.parse(editor.policiesJSON)
 if (!Array.isArray(input.binding_policies)) throw new Error("覆盖政策须是数组；默认空数组表示不允许覆盖。")
 input.definition.required_fields = input.definition.fields.filter(field => field.required).map(field => field.field_id)
 input.source_refs = parseSourceRefs(editor.sourcesText)
 const response = await saveTemplateDraft(input, key(`save-draft/${input.template_id}/${input.version}`, input))
 confirmedWrite = true
 if (epoch !== catalogAccessEpoch) throw new Error(staleCatalogOperationMessage)
 editor.input = cloneDraftInput(response.data); editor.policiesJSON = JSON.stringify(editor.input.binding_policies || [], null, 2); editor.termsJSON = JSON.stringify(editor.input.definition.terms || [], null, 2)
 editor.sourcesText = editor.input.source_refs.map(ref => `${ref.source_id}@${ref.source_version}`).join('\n'); editor.initial = fingerprint(editor)
 versions.value = [...versions.value.filter(version => version.version !== response.data.version), response.data]
 selectedVersionId.value = response.data.version
 message.value = `草稿已保存：${stateLabel(response.data.state)}，修订 ${response.data.revision}。`; emit('refresh')
 await refreshAfterTemplateWrite()
 })
}
async function governTemplate(action: 'publish' | 'withdraw' | 'recall') {
 const version = selectedVersion.value
 if (!version || versionError.value || !managerAccess.value) return
 await perform(async () => {
 const epoch = catalogAccessEpoch
 const input = { template_id: version.template_id, version: version.version, expected_revision: version.revision, reason: governance.reason }
 const payload = action === 'publish' ? { ...input, replay_fixture_ids: splitIds(governance.fixtures) } : action === 'recall' ? { ...input, migration_advice: governance.migration_advice } : input
 const operationKey = key(`template/${action}/${version.template_id}/${version.version}`, payload)
 const response = action === 'publish' ? await publishTemplateVersion({ ...input, replay_fixture_ids: splitIds(governance.fixtures) }, operationKey)
 : action === 'recall' ? await recallTemplateVersion({ ...input, migration_advice: governance.migration_advice }, operationKey)
 : await withdrawTemplateVersion(input, operationKey)
 confirmedWrite = true
 if (epoch !== catalogAccessEpoch) throw new Error(staleCatalogOperationMessage)
 versions.value = versions.value.map(item => item.version === response.data.version ? response.data : item)
 message.value = `模板操作已成功：${stateLabel(response.data.state)}。`; emit('refresh')
 await refreshAfterTemplateWrite()
 })
}
function bindingEditor(binding: RuleBinding): BindingEditor {
 return { binding: JSON.parse(JSON.stringify(binding)), params: JSON.stringify(binding.parameters || {}, null, 2),
 positive: (binding.positive_example_refs || []).join(' '), negative: (binding.negative_example_refs || []).join(' ') }
}
function makeRuleDraft(bindings: RuleBinding[], id: string): RuleDraftUI {
 const draft: RuleDraftUI = { ruleset_id: id, version: '', reason: '', bindings: bindings.map(bindingEditor), initial: '' }
 draft.initial = ruleFingerprint(draft); return draft
}
function retainRuleDraft() { if (ruleDraft.value && !retainedRuleDrafts.includes(ruleDraft.value)) retainedRuleDrafts.push(ruleDraft.value) }
function editRuleSet() { retainRuleDraft(); ruleDraft.value = makeRuleDraft(selectedRuleSet.value?.bindings || [], ruleSetId.value) }
function resumeRuleDraft(draft: RuleDraftUI) { retainRuleDraft(); ruleDraft.value = draft }
function addBinding() {
 if (!ruleDraft.value) return
 const definition = definitions.value[0]
 if (!definition) { errorMessage.value = '规则类型目录尚未读取成功。'; return }
 ruleDraft.value.bindings.push(bindingEditor({ rule_id: '', definition_id: definition.definition_id,
 definition_version: definition.definition_version, origin: 'organization', severity: 'info',
 trigger_scenes: ['write_check', 'preflight'], enabled: true, owner: '' }))
}
function changeDefinition(editor: BindingEditor) {
 const definition = definitions.value.find(item => item.definition_id === editor.binding.definition_id)
 if (definition) editor.binding.definition_version = definition.definition_version
}
function serializeBindings(editors: BindingEditor[]) {
 return editors.map(editor => ({ ...JSON.parse(JSON.stringify(editor.binding)) as RuleBinding, parameters: JSON.parse(editor.params),
 positive_example_refs: splitIds(editor.positive), negative_example_refs: splitIds(editor.negative) }))
}
async function refreshRuleSets() {
 const epoch = catalogAccessEpoch, id = ruleSetId.value
 try {
  const response = await listRuleSetVersions(id)
  if (epoch === catalogAccessEpoch && managerAccess.value && ruleSetId.value === id) {
   rulesets.value = response.data; ruleReadError.value = ''
  }
 } catch (error: unknown) {
  if (epoch === catalogAccessEpoch && ruleSetId.value === id) {
   if (permissionDenied(error)) revokeManagerView()
   ruleReadError.value = operationError(error)
  }
  throw error
 }
}
async function loadRuleSets() { await perform(async () => { await refreshRuleSets(); ruleSetVersion.value = rulesets.value[0]?.version || '' }) }
async function refreshAfterOtherWrite(read: () => Promise<void>, label: string) {
 try { await read() } catch { readbackNotice.value = `操作已成功，但${label}读取未完成。请重新读取；不要重复提交。` }
}
async function saveRuleSet() {
 const editor = ruleDraft.value
 if (!editor || !managerAccess.value) return
 await perform(async () => {
 const epoch = catalogAccessEpoch
 const draft: RuleSet = { ruleset_id: editor.ruleset_id, version: editor.version, state: 'draft', bindings: serializeBindings(editor.bindings) }
 const body = { draft, reason: editor.reason }, result = await createRuleSetDraft(draft, editor.reason, key('create-rule-draft', body))
 confirmedWrite = true
 if (epoch !== catalogAccessEpoch) throw new Error(staleCatalogOperationMessage)
 ruleSetId.value = result.data.ruleset_id; ruleSetVersion.value = result.data.version; editor.initial = ruleFingerprint(editor)
 rulesets.value = [...rulesets.value.filter(item => item.version !== result.data.version), result.data]
 message.value = `规则草稿已保存：${stateLabel(result.data.state)}。`; emit('refresh')
 await refreshAfterOtherWrite(refreshRuleSets, '规则版本')
 })
}
async function governRule(action: 'publish' | 'withdraw' | 'recall') {
 const set = selectedRuleSet.value
 if (!set || ruleReadError.value || !managerAccess.value) return
 await perform(async () => {
 const epoch = catalogAccessEpoch
 const input = { expected_state: set.state, expected_ruleset_hash: set.ruleset_hash || '', reason: governance.reason,
 replay_fixture_ids: splitIds(governance.fixtures), template_id: replayTemplate.id, template_version: replayTemplate.version }
 const response = await governRuleSet(set.ruleset_id, set.version, action, input, key(`rule/${action}/${set.ruleset_id}/${set.version}`, input))
 confirmedWrite = true
 if (epoch !== catalogAccessEpoch) throw new Error(staleCatalogOperationMessage)
 rulesets.value = rulesets.value.map(item => item.version === response.data.version ? response.data : item)
 message.value = `规则操作已成功：${stateLabel(response.data.state)}。`; emit('refresh')
 await refreshAfterOtherWrite(refreshRuleSets, '规则版本')
 })
}
async function loadSuggestions() { const projectId=props.project.id; const result=await listRuleSuggestions(projectId); if (props.project.id===projectId) runs.value=result.data }
async function requestSuggestion() {
 const version = selectedVersion.value
 if (!version || !canRequest.value) return
 await perform(async () => {
 const projectId = props.project.id, scope = { project_id: projectId, basis: projectBasis(props.project), template_id: version.template_id,
 template_version: version.version, catalog_revision: version.revision, ruleset: { ...version.ruleset },
 target_kind: targetKind.value, target_id: targetId.value, source_refs: knownSources.value.filter(source => selectedSourceIds.value.includes(source.id))
 .map(source => ({ source_id: source.id, source_version: String(source.asset_revision), locator: source.locator })) }
 const response = await requestRuleSuggestion(scope, key(`suggestion/${projectId}`, scope))
 confirmedWrite = true
 if (props.project.id !== projectId) { message.value = '结果已保留在原项目，请返回原项目读取。'; return }
 runs.value = [...runs.value.filter(item => item.id !== response.data.id), response.data]
 selectedRunId.value = response.data.id; message.value = `建议请求已提交：${stateLabel(response.data.status)}。`
 await refreshAfterOtherWrite(loadSuggestions, '建议列表')
 })
}
async function refreshRun() {
 const run = selectedRun.value
 if (!run) return
 await perform(async () => { const result = await getRuleSuggestion(run.project_id, run.id); runs.value = runs.value.map(item => item.id === run.id ? result.data : item) })
}
function editSuggestion() {
 const run = selectedRun.value
 if (run?.status === 'suggestion' && !acceptDraft.value) acceptDraft.value = makeRuleDraft(suggestionBindings(run), run.scope.ruleset.ruleset_id)
}
async function acceptSuggestion() {
 const run = selectedRun.value, editor = acceptDraft.value
 if (!run || !editor) return
 await perform(async () => {
 const epoch = catalogAccessEpoch
 const input = { expected_revision: run.revision, ruleset_id: editor.ruleset_id, version: editor.version, bindings: serializeBindings(editor.bindings), reason: editor.reason }
 const result = await acceptRuleSuggestion(run.project_id, run.id, input, key(`accept/${run.id}`, input))
 confirmedWrite = true
 if (epoch !== catalogAccessEpoch) throw new Error(staleCatalogOperationMessage)
 editor.initial = ruleFingerprint(editor); delete acceptDraftCache[run.id]
 runs.value = runs.value.map(item => item.id === result.data.id ? result.data : item)
 message.value = `建议已接受：${stateLabel(result.data.status)}。仍需规则管理员复核和发布。`; emit('refresh')
 await refreshAfterOtherWrite(loadSuggestions, '建议列表')
 })
}
async function rejectSuggestion() {
 const run = selectedRun.value
 if (!run) return
 await perform(async () => {
 const epoch = catalogAccessEpoch
 const input = { expected_revision: run.revision, reason: decisionReason.value }, result = await rejectRuleSuggestion(run.project_id, run.id, input, key(`reject/${run.id}`, input))
 confirmedWrite = true
 if (epoch !== catalogAccessEpoch) throw new Error(staleCatalogOperationMessage)
 decisionReason.value = ''; delete acceptDraftCache[run.id]
 runs.value = runs.value.map(item => item.id === result.data.id ? result.data : item)
 message.value = `建议已拒绝：${stateLabel(result.data.status)}。`; emit('refresh')
 await refreshAfterOtherWrite(loadSuggestions, '建议列表')
 })
}
watch(() => props.project.id, () => { runs.value = []; selectedRunId.value = ''; selectedSourceIds.value = []; acceptDraft.value = null; decisionReason.value = '' })
watch([() => props.project.id, () => props.canManageCatalog], ([, canManage]) => {
 const epoch = ++catalogAccessEpoch
 catalogRequestEpoch++; profileSelectionEpoch++; managerDenied.value = false; managerAccessNotice.value = ''
 errorMessage.value = ''; message.value = ''; readbackNotice.value = ''; catalogLoading.value = false; catalogError.value = ''; versionError.value = ''; versionNotice.value = ''; ruleReadError.value = ''
 clearAudit()
 if (tab.value === 'rules') tab.value = 'catalog'
 if (stateFilter.value === 'draft') stateFilter.value = ''
 profiles.value = []; versions.value = []; profileId.value = ''; selectedVersionId.value = ''
 if (!canManage) {
  definitions.value = []; rulesets.value = []; replayFixtures.value = []; ruleDraft.value = null; retainedRuleDrafts.splice(0)
  for (const key of Object.keys(draftCache)) delete draftCache[key]
  draftKey.value = ''
 }
 void loadCatalog()
})
watch([selectedVersionId, targetKind], () => { targetId.value = '' })
async function loadProtectedDefinitions() {
 const epoch = catalogAccessEpoch
 try {
  const result = await listRuleDefinitions()
  if (epoch === catalogAccessEpoch && managerAccess.value) definitions.value = result.data
 } catch (error: unknown) {
  if (epoch === catalogAccessEpoch && (permissionDenied(error) || resourceUnavailable(error))) revokeManagerView(resourceUnavailable(error))
  throw error
 }
}
async function loadProtectedFixtures() {
 const epoch = catalogAccessEpoch
 try {
  const result = await listReplayFixtures()
  if (epoch === catalogAccessEpoch && managerAccess.value) { replayFixtures.value = result.data; fixtureCatalogError.value = '' }
 } catch (error: unknown) {
  if (epoch === catalogAccessEpoch) {
   if (permissionDenied(error) || resourceUnavailable(error)) revokeManagerView(resourceUnavailable(error))
   else fixtureCatalogError.value = operationError(error)
  }
 }
}
async function loadInitialData() {
 const epoch = catalogAccessEpoch
 const fixtureLoad = props.canManageCatalog ? loadProtectedFixtures() : Promise.resolve()
 const requests: Promise<unknown>[] = [loadCatalog(), loadSuggestions()]
 if (props.canManageCatalog) requests.push(loadProtectedDefinitions())
 const results = await Promise.allSettled(requests)
 await fixtureLoad
 if (epoch !== catalogAccessEpoch) return
 const failures = results.filter((result): result is PromiseRejectedResult => result.status === 'rejected')
 if (failures.length) throw new Error(failures.map(result => operationError(result.reason)).join('；'))
}
onMounted(() => perform(loadInitialData))
</script>


<template>
 <section class="template-governance" aria-labelledby="template-governance-title">
  <header class="panel-header"><div><p class="eyebrow">模板与规则</p><h2 id="template-governance-title">治理工作台</h2><p class="subtle">{{ project.name }} · 项目版本 {{ project.project_version }} · {{ chapters.length }} 个章节</p></div><button type="button" :disabled="busy" @click="emit('close')">关闭</button></header>
  <fieldset class="interaction-boundary" :disabled="busy"><legend class="visually-hidden">模板治理操作</legend>
  <nav class="tabs" aria-label="治理区域"><button type="button" :aria-pressed="tab === 'catalog'" @click="tab = 'catalog'">模板目录</button><button v-if="managerAccess" type="button" :aria-pressed="tab === 'rules'" @click="tab = 'rules'">规则版本</button><button type="button" :aria-pressed="tab === 'suggestions'" @click="tab = 'suggestions'">项目建议</button></nav>
  <p v-if="errorMessage" class="notice error" role="alert">{{ errorMessage }} <template v-if="hasUnsavedChanges && !managerDenied">编辑输入已保留；版本冲突时先重新读取并复核差异。</template></p>
  <p v-if="message" class="notice" role="status">{{ message }}</p><p v-if="readbackNotice" class="notice error" role="alert">{{ readbackNotice }}</p><p v-if="managerDenied" class="notice error" role="alert">{{ managerAccessNotice }} <button v-if="canManageCatalog && profileId" type="button" @click="loadAudit()">重新核对权限</button><button v-else-if="!canManageCatalog" type="button" @click="retryOrdinaryCatalogAccess()">重新读取目录和版本</button></p><p v-if="busy" class="subtle" role="status">正在读取或提交，请稍候…</p>
  <div v-if="tab === 'catalog'" class="catalog-layout">
   <aside class="catalog-list">
    <form class="filters" @submit.prevent="perform(loadCatalog)"><label>模板 ID<input v-model="templateIdFilter" placeholder="在可见目录中筛选" /></label><label>适用范围<input v-model="scopeFilter" placeholder="全部范围" /></label><label>版本状态<select v-model="stateFilter"><option value="">全部状态</option><option value="published">已发布</option><option v-if="managerAccess" value="draft">草稿</option><option value="withdrawn">已撤回</option><option value="recalled">已召回</option></select></label><button :disabled="busy">读取目录</button></form>
    <p v-if="catalogNotice" :class="catalogError ? 'notice error' : 'subtle'" :role="catalogError ? 'alert' : 'status'">{{ catalogNotice }}</p>
    <button v-for="profile in visibleProfiles" :key="profile.template_id" class="list-entry" :class="{ selected: profileId === profile.template_id }" :disabled="busy" @click="chooseProfile(profile.template_id)"><strong>{{ profile.name }}</strong><small>{{ profile.template_id }}</small></button>
    <details v-if="managerAccess"><summary>创建模板档案</summary><form class="stack" @submit.prevent="createProfile"><label>稳定标识<input v-model="profileForm.template_id" required /></label><label>名称<input v-model="profileForm.name" required /></label><label>创建理由<textarea v-model="profileForm.reason" required /></label><button :disabled="busy">创建档案</button></form></details>
   </aside>
   <main class="version-content">
    <p v-if="!profileId" class="subtle">选择一个模板档案，查看来源与版本。</p>
    <template v-else>
     <div class="actions"><label>历史版本<select :value="selectedVersionId" @change="chooseVersion(($event.target as HTMLSelectElement).value)"><option v-if="!versions.length" value="">{{ versionError ? '版本暂不可读' : '尚无版本' }}</option><option v-else-if="!selectedVersionId" value="">请选择可用版本</option><option v-for="version in versions" :key="version.version" :value="version.version">{{ version.version }} · {{ stateLabel(version.state) }}</option></select></label><button v-if="managerAccess" :disabled="busy" @click="editTemplate(true)">准备新版本</button><button v-if="managerAccess && selectedVersion?.state === 'draft'" :disabled="busy" @click="editTemplate(false)">编辑草稿</button><button :disabled="busy" @click="perform(refreshVersions)">重新读取版本</button></div>
     <p v-if="versionError" class="notice error" role="alert">版本读取未完成：{{ versionError }} 请重新读取版本后再操作。</p><p v-else-if="versionNotice" role="status" class="subtle">{{ versionNotice }}</p>
     <section v-if="selectedVersion" class="card">
      <h3>{{ selectedVersion.definition.name }} · {{ selectedVersion.template_id }} / {{ selectedVersion.version }} <span class="badge">{{ stateLabel(selectedVersion.state) }}</span></h3>
      <dl><dt>来源 / 适用范围</dt><dd>{{ selectedVersion.source_type }} / {{ selectedVersion.scope }}</dd><dt>整理者</dt><dd>{{ selectedVersion.curator }}</dd><dt>明确规则版本</dt><dd>{{ selectedVersion.ruleset.ruleset_id }} / {{ selectedVersion.ruleset.version }}</dd><dt>项目覆盖政策</dt><dd>{{ selectedVersion.binding_policies?.length ? `${selectedVersion.binding_policies.length} 条已审允许范围` : '默认关闭；不允许项目覆盖' }}</dd></dl>
      <details><summary>核对哈希与来源材料</summary><p class="hash">内容：{{ selectedVersion.content_hash }}<br />规则集：{{ selectedVersion.ruleset_hash }}<br />覆盖政策：{{ selectedVersion.policy_hash || '服务器未返回政策哈希' }}</p><ul v-if="selectedVersion.binding_policies?.length"><li v-for="policy in selectedVersion.binding_policies" :key="policy.rule_id">{{ policy.rule_id }} · {{ policy.may_disable ? '允许停用' : '不可停用' }} · 严重度范围 {{ (policy.allowed_severities || []).join('、') || '不可调整' }}</li></ul><ul v-if="managerAccess"><li v-for="source in selectedVersion.source_refs" :key="source.source_id">{{ source.source_id }} @ {{ source.source_version }}</li></ul><p v-else class="subtle">来源材料标识仅供模板管理员核对；适用范围和版本哈希仍可查看。</p></details>
      <p class="subtle">来源类型不代表官方认证。已发布版本供新项目复制；当前项目继续保留自己的副本。</p>
      <div v-if="selectedVersion.recall" class="notice error"><strong>召回原因：{{ selectedVersion.recall.reason }}</strong><p>{{ selectedVersion.recall.migration_advice }}</p><details v-if="managerAccess && selectedVersion.recall.affected_project_ids.length" class="recall-projects"><summary>受影响课题 {{ selectedVersion.recall.affected_project_ids.length }} 个 · 展开查看编号</summary><ul><li v-for="projectId in selectedVersion.recall.affected_project_ids" :key="projectId" class="hash">{{ projectId }}</li></ul></details><p v-else>请核对当前课题的模板副本；其他课题的影响范围由模板管理员审计。</p></div>
      <details v-if="managerAccess && !versionError && ['draft', 'published'].includes(selectedVersion.state)"><summary>发布与撤回操作</summary><div class="stack"><p>当前操作版本：{{ selectedVersion.template_id }} / {{ selectedVersion.version }} · 修订 {{ selectedVersion.revision }}</p><label>治理理由<textarea v-model="governance.reason" /></label><template v-if="selectedVersion.state === 'draft'"><details><summary>真实回放样例（服务器注册 · 当前目标 {{ fixtureTarget?.template_id || '未选择' }} / {{ fixtureTarget?.template_version || '—' }}）</summary><TemplateGovernanceFixturePicker v-model="governance.fixtures" :fixtures="targetFixtures" :disabled="busy" :catalog-error="fixtureCatalogError" /></details><label>已保存的真实回放样例 ID<textarea v-model="governance.fixtures" placeholder="空格或换行分隔；服务端将实际执行正反例" /></label><button :disabled="busy || !governance.reason.trim() || !splitIds(governance.fixtures).length" @click="governTemplate('publish')">核验回放并发布</button></template><template v-else><button :disabled="busy || !governance.reason.trim()" @click="governTemplate('withdraw')">撤回新项目复制权限</button><label>迁移建议<textarea v-model="governance.migration_advice" /></label><button :disabled="busy || !governance.reason.trim() || !governance.migration_advice.trim()" @click="governTemplate('recall')">记录召回与影响范围</button></template></div></details>
     </section>
     <section v-if="managerAccess" class="card" aria-label="模板审计记录">
      <h3>模板审计记录</h3>
      <p class="subtle">仅模板管理员可查看。记录来自服务端，按时间倒序显示。</p>
      <p v-if="auditLoading" role="status">正在读取审计记录…</p>
      <div v-else-if="auditError" class="notice error" role="alert">审计记录读取失败：{{ auditError }} <button type="button" @click="loadAudit()">重试读取审计</button></div>
      <p v-else-if="!auditEvents.length" class="subtle">当前没有可见审计记录。</p>
      <ol v-if="auditEvents.length"><li v-for="event in auditEvents" :key="event.id"><strong>{{ event.action }}</strong> · {{ event.outcome }} · {{ event.created_at }}<br />操作者 {{ event.actor_user_id }}（租户角色 {{ event.actor_role }}） · 目标 {{ event.target_id }}<details><summary>服务端记录详情</summary><pre class="code-input">{{ JSON.stringify(event.details, null, 2) }}</pre></details></li></ol>
      <button v-if="auditNextId && !auditLoading && !auditError" type="button" @click="loadAudit(true)">读取更早记录</button>
     </section>
     <div v-if="Object.keys(draftCache).length" class="actions"><span class="subtle">保留的编辑：</span><button v-for="(draft, id) in draftCache" :key="id" @click="draftKey = String(id)">{{ draft.input.template_id }} / {{ draft.input.version || '新版本待命名' }}{{ fingerprint(draft) !== draft.initial ? ' · 未保存' : '' }}</button></div>
     <form v-if="managerAccess && templateDraft" class="card stack" @submit.prevent="saveDraft">
      <h3>模板草稿</h3><div class="form-row"><label>版本号<input v-model="templateDraft.input.version" :readonly="templateDraft.input.expected_revision > 0" required /></label><label>适用范围<input v-model="templateDraft.input.scope" required /></label><label>整理者<input v-model="templateDraft.input.curator" required /></label></div>
      <fieldset><legend>项目字段</legend><div v-for="(field, index) in templateDraft.input.definition.fields" :key="index" class="form-row"><label>稳定标识<input v-model="field.field_id" required /></label><label>显示名称<input v-model="field.label" required /></label><label>值类型<select v-model="field.type"><option v-for="kind in ['string','text','number','integer','boolean','enum']" :key="kind" :value="kind">{{ kind }}</option></select></label><label v-if="field.type === 'enum'">候选值（每行一个）<textarea :value="(field.options || []).join('\n')" @input="enumOptions(index, $event)" /></label><label class="checkbox"><input v-model="field.required" type="checkbox" />必填</label><button type="button" @click="templateDraft.input.definition.fields.splice(index, 1)">移除</button></div><button type="button" @click="addField">添加字段</button></fieldset>
      <fieldset><legend>章节结构</legend><div v-for="(section, index) in templateDraft.input.definition.sections" :key="index" class="form-row"><label>稳定标识<input v-model="section.section_id" required /></label><label>标题<input v-model="section.title" required /></label><label>顺序<input v-model.number="section.order" type="number" /></label><label class="checkbox"><input v-model="section.required" type="checkbox" />必需</label><button type="button" @click="templateDraft.input.definition.sections.splice(index, 1)">移除</button></div><button type="button" @click="addSection">添加章节</button></fieldset>
      <div class="form-row"><label>已发布规则集 ID<input v-model="templateDraft.input.ruleset.ruleset_id" required /></label><label>精确规则版本<input v-model="templateDraft.input.ruleset.version" required /></label></div>
      <details><summary>来源材料与术语</summary><div class="stack"><label>来源类型<select v-model="templateDraft.input.source_type"><option value="lingdoc_builtin">灵档内置</option><option value="organization_input">组织整理</option><option value="external_reference">外部参考</option><option value="project_copy">项目副本</option></select></label><label>真实来源引用（每行 source_id@source_version）<textarea v-model="templateDraft.sourcesText" /></label><label>受控术语（结构化数组）<textarea v-model="templateDraft.termsJSON" class="code-input" rows="5" /></label></div></details>
      <details><summary>项目覆盖政策 · 默认关闭</summary><p class="subtle">仅目录整理者可在草稿阶段批准特定规则的覆盖范围。强制阻断规则不可停用或降级，发布后政策和 policy_hash 固定。编辑后须使用绑定当前政策的服务器复核正反例重新回放。</p><label>明确批准的政策（JSON 数组，未批准保留 []）<textarea v-model="templateDraft.policiesJSON" class="code-input" rows="6" spellcheck="false" /></label><p class="subtle">可审字段：rule_id、may_disable、allowed_target_ids、allowed_severities、allowed_parameter_keys、parameter_bounds。身份、executor 和来源版本不在覆盖范围内。</p></details>
      <label>保存理由<textarea v-model="templateDraft.input.reason" required /></label><p class="subtle">删除和类型变更不会自动迁移 active 项目。项目升级仍需预览影响并完成稳定化。</p><button :disabled="busy || !!versionError">保存待审核草稿</button>
     </form>
    </template>
   </main>
  </div>


  <div v-else-if="tab === 'rules'" class="stack">
   <div class="actions"><label>规则集 ID<input v-model="ruleSetId" /></label><button :disabled="busy || !ruleSetId.trim()" @click="loadRuleSets">读取版本</button><label>版本<select v-model="ruleSetVersion"><option v-for="set in rulesets" :key="set.version" :value="set.version">{{ set.version }} · {{ stateLabel(set.state) }}</option></select></label><button :disabled="busy" @click="editRuleSet">准备新规则草稿</button></div>
   <p v-if="ruleReadError" class="notice error" role="alert">规则版本读取未完成：{{ ruleReadError }} 请重新读取版本后再治理。</p>
   <section v-if="selectedRuleSet" class="card"><h3>{{ selectedRuleSet.ruleset_id }} / {{ selectedRuleSet.version }} <span class="badge">{{ stateLabel(selectedRuleSet.state) }}</span></h3><p>{{ selectedRuleSet.bindings.length }} 条绑定；旧版本的结果与快照继续保留。</p><details><summary>绑定与版本哈希</summary><p class="hash">{{ selectedRuleSet.ruleset_hash || '尚未发布，服务端将在发布时计算哈希' }}</p><ul><li v-for="binding in selectedRuleSet.bindings" :key="binding.rule_id">{{ binding.rule_id }} · {{ binding.target_kind || '全局检查' }} {{ binding.target_id }} · {{ binding.enabled ? '启用' : '停用' }} · {{ binding.severity }}</li></ul></details><div v-if="!ruleReadError" class="stack"><label>治理理由<textarea v-model="governance.reason" /></label><template v-if="selectedRuleSet.state === 'draft'"><div class="form-row"><label>回放模板 ID<input v-model="replayTemplate.id" /></label><label>精确模板版本<input v-model="replayTemplate.version" /></label></div><p class="subtle">第一次发布可选择项目当前的已知内置模板；样例必须由服务器注册并绑定当前规则、模板和政策哈希。</p><details><summary>真实回放样例（服务器注册 · 当前目标 {{ fixtureTarget?.template_id || '未填写' }} / {{ fixtureTarget?.template_version || '—' }}）</summary><TemplateGovernanceFixturePicker v-model="governance.fixtures" :fixtures="targetFixtures" :disabled="busy" :catalog-error="fixtureCatalogError" /></details><label>真实回放样例 ID<textarea v-model="governance.fixtures" /></label><button :disabled="busy || !governance.reason.trim() || !splitIds(governance.fixtures).length || !replayTemplate.id.trim() || !replayTemplate.version.trim()" @click="governRule('publish')">核验并发布规则版本</button></template><div v-else-if="selectedRuleSet.state === 'published'" class="actions"><button :disabled="busy || !governance.reason.trim()" @click="governRule('withdraw')">撤回新绑定</button><button :disabled="busy || !governance.reason.trim()" @click="governRule('recall')">记录召回</button></div></div></section>
   <details v-if="retainedRuleDrafts.length"><summary>保留的规则编辑</summary><button v-for="(draft,index) in retainedRuleDrafts" :key="index" type="button" @click="resumeRuleDraft(draft)">{{ draft.ruleset_id || '待命名规则集' }} / {{ draft.version || '新版本' }}</button></details>
   <form v-if="ruleDraft" class="card stack" @submit.prevent="saveRuleSet"><h3>规则草稿</h3><div class="form-row"><label>规则集 ID<input v-model="ruleDraft.ruleset_id" required /></label><label>新版本号<input v-model="ruleDraft.version" required /></label></div>
    <fieldset v-for="(editor, index) in ruleDraft.bindings" :key="index"><legend>规则 {{ index + 1 }}</legend><div class="form-row"><label>稳定标识<input v-model="editor.binding.rule_id" required /></label><label>已注册类型<select v-model="editor.binding.definition_id" @change="changeDefinition(editor)"><option v-for="definition in definitions" :key="definition.definition_id" :value="definition.definition_id">{{ definition.definition_id }} / {{ definition.definition_version }}</option></select></label><label>负责人<input v-model="editor.binding.owner" required /></label><label class="checkbox"><input v-model="editor.binding.enabled" type="checkbox" />启用</label></div><div class="form-row"><label>目标类型<input v-model="editor.binding.target_kind" placeholder="全局检查留空" /></label><label>目标标识<input v-model="editor.binding.target_id" /></label><label>严重度<select v-model="editor.binding.severity"><option value="info">提示</option><option value="warning">警告</option><option value="blocking">阻断（需人工评审）</option></select></label></div><label>解释文本<input v-model="editor.binding.message" /></label><details><summary>声明式参数与真实正反例</summary><div class="stack"><label>参数<textarea v-model="editor.params" class="code-input" /></label><label>正例引用 ID<input v-model="editor.positive" /></label><label>反例引用 ID<input v-model="editor.negative" /></label></div></details><button type="button" @click="ruleDraft.bindings.splice(index, 1)">移除此草稿规则</button></fieldset>
    <button type="button" :disabled="!definitions.length" @click="addBinding">添加声明式规则</button><label>保存理由<textarea v-model="ruleDraft.reason" required /></label><button :disabled="busy">保存规则草稿</button>
   </form>
  </div>
  <div v-else class="suggestion-layout">
   <aside class="stack"><h3>当前项目建议</h3><p class="subtle">模板版本 {{ project.template_id }} / {{ project.template_version }}<br />规则版本 {{ currentBinding.ruleset_id || '未返回' }} / {{ currentBinding.version || '未返回' }}</p><button :disabled="busy" @click="perform(loadSuggestions)">重新读取建议</button><p v-if="!runs.length && !busy" class="subtle">当前项目暂无规则建议。</p><button v-for="run in runs" :key="run.id" class="list-entry" :class="{ selected: selectedRunId === run.id }" @click="selectedRunId = run.id"><strong>{{ stateLabel(run.status) }}</strong><small>{{ run.created_at }}</small></button></aside>
   <main class="stack">
    <form class="card stack" @submit.prevent="requestSuggestion"><h3>明确请求建议</h3><p v-if="!selectedVersion || selectedVersion.state !== 'published'" class="subtle">先在模板目录选择当前项目绑定的已发布精确版本。</p><p v-if="selectedVersion && !catalogMatchesProject(project, selectedVersion)" class="notice error">所选目录与当前项目模板不一致；请选择 {{ project.template_id }} / {{ project.template_version }} 后请求。</p><div class="form-row"><label>检查对象<select v-model="targetKind"><option value="field">项目字段</option><option value="chapter">模板章节</option></select></label><label>目标<select v-model="targetId"><option value="">请选择</option><option v-for="target in targetOptions" :key="target.id" :value="target.id">{{ target.title }}（{{ target.id }}）</option></select></label></div><fieldset><legend>本次允许资料</legend><p v-if="!knownSources.length" class="subtle">未提供可用资料，可仅依据模板结构请求。服务端仍会重新鉴权。</p><label v-for="source in knownSources" :key="source.id" class="checkbox"><input v-model="selectedSourceIds" type="checkbox" :value="source.id" />{{ source.locator || source.id }} · 资料版本 {{ source.asset_revision }}</label></fieldset><p v-if="!currentBinding.ruleset_id || !currentBinding.version || !currentBinding.ruleset_hash" class="notice error">当前项目尚未返回明确 RuleSet 身份、版本和哈希。请重新读取项目绑定后再请求。</p><button :disabled="busy || !canRequest">请求模型建议</button><p class="subtle">建议只进入待审核草稿。模型、耗时和真实运行方式会记录在结果中。</p></form>
    <section v-if="selectedRun" class="card stack"><div class="actions"><h3>{{ stateLabel(selectedRun.status) }}</h3><button :disabled="busy" @click="refreshRun">读取本次结果</button></div><p class="subtle">{{ runModeLabel(selectedRun.run_mode) }} · {{ selectedRun.model_id || '模型未返回' }} · {{ selectedRun.duration_ms }} ms</p><p>固定上下文：第 {{ selectedRun.scope.basis.context_revision }} 版；模板 {{ selectedRun.scope.template_id }} / {{ selectedRun.scope.template_version }}；RuleSet {{ selectedRun.scope.ruleset.ruleset_id }} / {{ selectedRun.scope.ruleset.version }}</p><p v-if="selectedRun.error_code" class="notice error">{{ selectedRun.error_code }}：{{ selectedRun.error_message }}。可修复后用新请求重新发起。</p><template v-if="selectedRun.output"><p>{{ selectedRun.output.explanation }}</p><article v-for="proposal in selectedRun.output.proposals" :key="proposal.rule_id" class="proposal"><strong>{{ proposal.message || proposal.definition_id }}</strong><p>{{ proposal.rationale }}</p><small>依据：{{ proposal.evidence_source_ids.join('、') || '模板结构；未引用资料' }}</small></article></template><p v-if="selectedRun.draft_ruleset">已创建待审核规则草稿：{{ selectedRun.draft_ruleset.ruleset_id }} / {{ selectedRun.draft_ruleset.version }}</p><button v-if="selectedRun.status === 'suggestion'" :disabled="busy" @click="editSuggestion">人工编辑并准备接受</button><form v-if="['suggestion','stale','failed'].includes(selectedRun.status)" class="stack" @submit.prevent="rejectSuggestion"><label>拒绝理由<textarea v-model="decisionReason" required /></label><button :disabled="busy">拒绝本次建议</button></form></section>
    <form v-if="acceptDraft && selectedRun?.status === 'suggestion'" class="card stack" @submit.prevent="acceptSuggestion"><h3>人工复核后保存草稿</h3><label>待审核的新规则版本<input v-model="acceptDraft.version" required /></label><fieldset v-for="editor in acceptDraft.bindings" :key="editor.binding.rule_id"><legend>{{ editor.binding.rule_id }}</legend><p>{{ editor.binding.target_kind }} / {{ editor.binding.target_id }}</p><label>人工修订解释<input v-model="editor.binding.message" /></label><label>声明式参数<textarea v-model="editor.params" class="code-input" /></label><label>严重度<select v-model="editor.binding.severity"><option value="info">提示</option><option value="warning">警告</option></select></label><div class="form-row"><label>正例引用 ID<input v-model="editor.positive" /></label><label>反例引用 ID<input v-model="editor.negative" /></label></div></fieldset><label>人工决定理由<textarea v-model="acceptDraft.reason" required /></label><button :disabled="busy">接受为待审核规则草稿</button></form>
   </main>
  </div>
 </fieldset>
 </section>
</template>

<style scoped>
.interaction-boundary { border: 0; margin: 0; padding: 0; min-width: 0; display: block; }.visually-hidden { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); }
.template-governance { color: #172337; background: #fff; padding: 24px; min-width: 0; }
.panel-header { display: flex; justify-content: space-between; align-items: flex-start; gap: 20px; }
h2, h3, p { margin: 0 0 12px; } h2 { font-size: 24px; } h3 { font-size: 17px; }
.eyebrow { color: #527269; font-size: 12px; letter-spacing: .08em; }.subtle, small { color: #68778a; font-size: 13px; line-height: 1.65; }
.tabs { display: flex; gap: 8px; margin: 16px 0 22px; border-bottom: 1px solid #e5eaf0; padding-bottom: 12px; }
button { border: 1px solid #d6dee7; border-radius: 7px; background: #fff; color: #24374f; padding: 9px 13px; cursor: pointer; }
button:hover, button[aria-pressed="true"], .selected { background: #eef6f1; border-color: #6c9982; } button:disabled { opacity: .5; cursor: default; }
.catalog-layout, .suggestion-layout { display: grid; grid-template-columns: 250px minmax(0, 1fr); gap: 24px; }
.suggestion-layout > aside { align-self: start; align-content: start; }
.catalog-list { border-right: 1px solid #e6ebef; padding-right: 18px; }.list-entry { display: grid; text-align: left; width: 100%; gap: 5px; margin-bottom: 8px; overflow-wrap: anywhere; }
.stack, .filters { display: grid; gap: 14px; }.filters { margin-bottom: 18px; }.actions, .form-row { display: flex; flex-wrap: wrap; align-items: flex-end; gap: 12px; }.form-row > label { flex: 1; min-width: 130px; }
label { display: grid; gap: 6px; font-size: 13px; color: #425269; }
input, select, textarea { box-sizing: border-box; min-width: 0; width: 100%; border: 1px solid #d5dfe7; border-radius: 6px; padding: 9px; color: #18263a; background: #fff; font: inherit; }
textarea { min-height: 75px; resize: vertical; }.checkbox { display: flex; align-items: center; gap: 7px; }.checkbox input { width: auto; }
.card { border: 1px solid #dde5eb; border-radius: 12px; padding: 20px; margin-bottom: 20px; }fieldset { border: 1px solid #e2e8ed; padding: 14px; border-radius: 8px; display: grid; gap: 12px; }legend { color: #425269; font-size: 13px; padding: 0 6px; }
.badge { display: inline-block; font-size: 12px; color: #4b6c5c; background: #edf5ef; padding: 4px 8px; border-radius: 5px; margin-left: 10px; }
.notice { background: #f0f6f2; border: 1px solid #d2e3d7; padding: 12px 15px; border-radius: 8px; font-size: 13px; line-height: 1.7; margin-bottom: 16px; }.error { background: #fff6f0; border-color: #ecd3c1; color: #8a4c2a; }
dl { display: grid; grid-template-columns: 130px 1fr; font-size: 13px; gap: 10px; }dt { color: #6b7b8e; }dd { margin: 0; overflow-wrap: anywhere; }details { margin: 10px 0; }summary { cursor: pointer; font-size: 13px; color: #496579; margin-bottom: 12px; }
.hash, .code-input { font-family: ui-monospace, monospace; font-size: 12px; overflow-wrap: anywhere; }.proposal { border-left: 3px solid #a9c0b2; padding: 10px 14px; background: #f8faf9; }
@media (max-width: 850px) { .catalog-layout, .suggestion-layout { grid-template-columns: 1fr; }.catalog-list { border-right: 0; border-bottom: 1px solid #e6ebef; padding: 0 0 18px; }.interaction-boundary { border: 0; margin: 0; padding: 0; min-width: 0; display: block; }.visually-hidden { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); }
.template-governance { padding: 16px; } }
</style>
