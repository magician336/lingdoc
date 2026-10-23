import assert from 'node:assert/strict'
import test from 'node:test'
import { CatalogIntegrityError, catalogNoticeForView, invitationAssignment, invitationScopeDetail, invitationStatusLabel, invitationViewState, permissionIdentity, publishedChoices, recallNoticeForProject, resolveTemplateChoice, templateChoiceDescription, templateChoiceLabel, templateStatusErrorFor, toggleEvidenceSelection } from './firstPassEntryState'
import type { ProjectInvitation } from '@/api/lingdoc/collaboration'
import type { Project } from '@/api/lingdoc/workspace'
import type { TemplateCatalogVersion } from '@/api/lingdoc/templateGovernance'

const version = (id: string, v: string, state = 'published') => ({ template_id: id, version: v, state }) as TemplateCatalogVersion

test('published choice preserves newer demo versions and removes only the explicit fallback duplicate', () => {
  const choices = publishedChoices([version('template-demo','1'),version('template-demo','2'),version('research','1'),version('research','1'),version('research','2','withdrawn')])
  assert.deepEqual(choices.map(v => `${v.template_id}@${v.version}`), ['template-demo@2','research@1'])
})
test('catalog loading, failure and empty result remain distinct in the project chooser', () => {
  assert.match(catalogNoticeForView(true, '', 0), /正在读取/)
  assert.match(catalogNoticeForView(false, '目录暂不可读，请重试', 0), /暂不可读/)
  assert.doesNotMatch(catalogNoticeForView(false, '目录暂不可读，请重试', 0), /暂无其他/)
  assert.match(catalogNoticeForView(false, '', 0), /暂无其他已发布模板/)
  assert.equal(catalogNoticeForView(false, '', 0, '当前筛选下没有模板。'), '当前筛选下没有模板。')
  assert.equal(catalogNoticeForView(false, '', 2), '')
})
test('catalog choice exposes exact version, scope, origin and hashes without official claim', () => {
  const choice = { ...version('research', '2'), definition: { name: '科研申请模板' }, scope: '内部参考', source_type: 'curated', curator: '资料组', content_hash: 'content-2', ruleset_hash: 'rules-3' } as TemplateCatalogVersion
  assert.match(templateChoiceLabel(choice), /科研申请模板 · 2 · 内部参考/)
  const description = templateChoiceDescription(choice)
  for (const text of ['内部参考', 'curated', '资料组', 'content-2', 'rules-3', '不代表官方认证']) assert.ok(description.includes(text))
})
test('withdrawn or stale template never silently creates a demo project', () => {
  assert.throws(() => resolveTemplateChoice('research@2',[version('research','2','withdrawn')]), /未改用演示/)
  assert.throws(() => resolveTemplateChoice('missing@1',[]), /重新选择/)
  assert.deepEqual(resolveTemplateChoice('template-demo@1',[]), { template_id:'template-demo',template_version:'1' })
  assert.deepEqual(resolveTemplateChoice('template-demo@2',[version('template-demo','2')]), { template_id:'template-demo',template_version:'2' })
})
test('source template recall is shown only for the exact project copy; incomplete recalls are errors', () => {
  const project = { template_id: 'research', template_version: '2' } as Project
  const recall = { reason: '结构问题', migration_advice: '先复核课题副本', affected_project_ids: [], recorded_at: '2026-10-20T00:00:00Z' }
  assert.deepEqual(recallNoticeForProject(project, { ...version('research', '2', 'recalled'), recall } as TemplateCatalogVersion), recall)
  assert.equal(recallNoticeForProject(project, version('research', '2', 'withdrawn')), null)
  assert.throws(() => recallNoticeForProject(project, { ...version('research', '1', 'recalled'), recall } as TemplateCatalogVersion), CatalogIntegrityError)
  assert.throws(() => recallNoticeForProject(project, version('research', '2', 'recalled')), CatalogIntegrityError)
})
test('catalog integrity failures are distinct from an unavailable status read, including for demo templates', () => {
  const mismatch = new CatalogIntegrityError('来源模板与当前课题不匹配')
  assert.match(templateStatusErrorFor(mismatch, false), /完整性校验失败/)
  assert.match(templateStatusErrorFor(new Error('offline'), false), /暂不可读/)
  assert.match(templateStatusErrorFor(new Error('offline'), true), /演示模板状态暂不可读/)
})
test('invitation empty scopes do not present all chapters as writable', () => {
  const invite = { assignment:{ governance_role:'member', function_roles:['author','reviewer'], function_scopes:{author:[],reviewer:['chapter-one']} } } as unknown as ProjectInvitation
  assert.match(invitationAssignment(invite), /尚未分配对象，不授予/)
  assert.match(invitationAssignment(invite), /评审者：指定章节（1 章）/)
})
test('invitation scope summary hides raw ids while detail keeps them for verification', () => {
  const invite = { assignment:{ governance_role:'member', function_roles:['author'], function_scopes:{author:['d6947ea6-debd-45a5-898e-01856fc77293','b2']} } } as unknown as ProjectInvitation
  assert.doesNotMatch(invitationAssignment(invite), /d6947ea6/)
  assert.match(invitationAssignment(invite), /写作者：指定章节（2 章）/)
  assert.deepEqual(invitationScopeDetail(invite), ['写作者：d6947ea6-debd-45a5-898e-01856fc77293、b2'])
  const empty = { assignment:{ governance_role:'member', function_roles:['author'], function_scopes:{author:[]} } } as unknown as ProjectInvitation
  assert.deepEqual(invitationScopeDetail(empty), [])
})
test('researcher asset scopes and generic scopes are not mislabelled as chapters', () => {
  const invite = { assignment:{ governance_role:'member', function_roles:['researcher','observer'], function_scopes:{researcher:['asset-one'],observer:['object-one','object-two']} } } as unknown as ProjectInvitation
  const summary = invitationAssignment(invite)
  assert.match(summary, /调研者：指定资料（1 项）/)
  assert.match(summary, /观察者：指定对象（2 项）/)
  assert.doesNotMatch(summary, /指定章节|asset-one|object-one/)
})
const inviteWith = (status: string, expires_at: string) => ({ status, expires_at }) as unknown as ProjectInvitation
test('only fresh invited invitations stay acceptable; expired, accepted and closed become history', () => {
  const now = new Date('2026-10-14T12:00:00')
  assert.equal(invitationViewState(inviteWith('invited','2026-10-17T12:00:00'), now), 'active')
  assert.equal(invitationViewState(inviteWith('invited','2026-10-14T11:59:59'), now), 'expired')
  assert.equal(invitationViewState(inviteWith('invited','not-a-date'), now), 'expired')
  assert.equal(invitationViewState(inviteWith('accepted','2026-10-17T12:00:00'), now), 'accepted')
  assert.equal(invitationViewState(inviteWith('removed','2026-10-17T12:00:00'), now), 'closed')
  assert.equal(invitationViewState(inviteWith('revoked','2026-10-17T12:00:00'), now), 'closed')
  assert.equal(invitationStatusLabel(inviteWith('invited','2026-10-17T12:00:00'), now), '待接受')
  assert.equal(invitationStatusLabel(inviteWith('invited','2026-10-14T11:59:59'), now), '已过期')
  assert.equal(invitationStatusLabel(inviteWith('accepted','2026-10-17T12:00:00'), now), '已接受')
  assert.equal(invitationStatusLabel(inviteWith('removed','2026-10-17T12:00:00'), now), '已失效')
})
test('local permission refresh key is stable for content saves but changes with revocation or identity', () => {
  const p = { id:'p',name:'合成权限刷新',status:'active',project_version:1,spec_revision:1,current_context_revision:1,spec:{},template_id:'template-demo',template_version:'1',members:[{user_id:'u',role:'collaborator',status:'active',function_roles:['author'],function_scopes:{author:['c']}}] } as Project
  assert.equal(permissionIdentity(p,'u',1),permissionIdentity({...p,project_version:2},'u',1))
  assert.notEqual(permissionIdentity(p,'u',1),permissionIdentity({...p,members:[{...p.members[0],status:'suspended'}]},'u',1))
  assert.notEqual(permissionIdentity(p,'u',1),permissionIdentity(p,'v',1))
  assert.notEqual(permissionIdentity(p,'u',1),permissionIdentity(p,'u',2))
})
test('evidence selection preserves explicit multiple cards and replaces a stale revision', () => {
  const a = [{id:'a',version:1}]
  const two = toggleEvidenceSelection(a,[{id:'b',version:1}])
  assert.deepEqual(two,[{id:'a',version:1},{id:'b',version:1}])
  assert.deepEqual(toggleEvidenceSelection(two,[{id:'a',version:2}]),[{id:'a',version:2},{id:'b',version:1}])
  assert.deepEqual(toggleEvidenceSelection(two,[{id:'a',version:1}]),[{id:'b',version:1}])
  assert.deepEqual(a,[{id:'a',version:1}])
})
