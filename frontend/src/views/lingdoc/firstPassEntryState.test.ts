import assert from 'node:assert/strict'
import test from 'node:test'
import { invitationAssignment, invitationScopeDetail, invitationStatusLabel, invitationViewState, permissionIdentity, publishedChoices, resolveTemplateChoice, toggleEvidenceSelection } from './firstPassEntryState'
import type { ProjectInvitation } from '@/api/lingdoc/collaboration'
import type { Project } from '@/api/lingdoc/workspace'
import type { TemplateCatalogVersion } from '@/api/lingdoc/templateGovernance'

const version = (id: string, v: string, state = 'published') => ({ template_id: id, version: v, state }) as TemplateCatalogVersion

test('published choice preserves newer demo versions and removes only the explicit fallback duplicate', () => {
  const choices = publishedChoices([version('template-demo','1'),version('template-demo','2'),version('research','1'),version('research','1'),version('research','2','withdrawn')])
  assert.deepEqual(choices.map(v => `${v.template_id}@${v.version}`), ['template-demo@2','research@1'])
})
test('withdrawn or stale template never silently creates a demo project', () => {
  assert.throws(() => resolveTemplateChoice('research@2',[version('research','2','withdrawn')]), /未改用演示/)
  assert.throws(() => resolveTemplateChoice('missing@1',[]), /重新选择/)
  assert.deepEqual(resolveTemplateChoice('template-demo@1',[]), { template_id:'template-demo',template_version:'1' })
  assert.deepEqual(resolveTemplateChoice('template-demo@2',[version('template-demo','2')]), { template_id:'template-demo',template_version:'2' })
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
