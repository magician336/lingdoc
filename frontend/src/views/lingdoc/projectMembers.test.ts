import assert from 'node:assert/strict'
import test from 'node:test'
import type { ProjectMember } from '@/api/lingdoc/workspace'
import {
  assetIdsForSelection, chapterScopesForSelection, createProjectMemberDrafts,
  hasOrphanedFunctionScopes, hasUnsupportedMemberState, serializeProjectMemberDrafts,
  setAssetScopes, setChapterScopes, setProjectFunctionRole,
} from './projectMembers'

test('member drafts keep owner read-only and clone nested permission state', () => {
  const members: ProjectMember[] = [
    { user_id: 'owner', role: 'owner', governance_role: 'owner', status: 'active' },
    { user_id: 'author', role: 'collaborator', governance_role: 'member', function_roles: ['author'], function_scopes: { author: ['chapter-1'] }, status: 'active' },
  ]
  const drafts = createProjectMemberDrafts(members)
  assert.deepEqual(drafts.map(member => member.user_id), ['author'])
  drafts[0].function_scopes.author.push('chapter-2')
  assert.deepEqual(members[1].function_scopes?.author, ['chapter-1'])
})

test('chapter and asset selectors preserve scopes owned by other editors', () => {
  const scopes = ['chapter-1', 'asset:asset-1', 'delivery:delivery-1', 'knowledge:kb-1']
  assert.deepEqual(chapterScopesForSelection(scopes, ['chapter-1', 'chapter-2']), ['chapter-1'])
  assert.deepEqual(setChapterScopes(scopes, ['chapter-2'], ['chapter-1', 'chapter-2']), ['asset:asset-1', 'delivery:delivery-1', 'knowledge:kb-1', 'chapter-2'])
  assert.deepEqual(assetIdsForSelection(scopes), ['asset-1'])
  assert.deepEqual(setAssetScopes(scopes, ['asset-2']), ['chapter-1', 'delivery:delivery-1', 'knowledge:kb-1', 'asset:asset-2'])
})

test('removing a function role also removes its scopes; empty scope is explicit', () => {
  const member = createProjectMemberDrafts([{
    user_id: 'reviewer', role: 'collaborator', governance_role: 'member',
    function_roles: ['reviewer'], function_scopes: { reviewer: ['chapter-1'] }, status: 'active',
  }])[0]
  setProjectFunctionRole(member, 'reviewer', false)
  assert.deepEqual(member.function_roles, [])
  assert.equal('reviewer' in member.function_scopes, false)
  setProjectFunctionRole(member, 'author', true)
  assert.deepEqual(member.function_scopes.author, [])
})

test('unsupported membership state and orphaned scopes are surfaced instead of silently rewritten', () => {
  assert.equal(hasUnsupportedMemberState({ user_id: 'legacy', role: 'collaborator' }), false)
  assert.equal(hasUnsupportedMemberState({ user_id: 'u', role: 'collaborator', status: 'suspended_by_tenant' }), true)
  assert.equal(hasOrphanedFunctionScopes({ user_id: 'u', role: 'collaborator', function_roles: [], function_scopes: { author: ['chapter-1'] } }), true)
})

test('serialized request does not share arrays with editable drafts', () => {
  const draft = createProjectMemberDrafts([{
    user_id: 'researcher', role: 'collaborator', governance_role: 'admin', function_roles: ['researcher'],
    function_scopes: { researcher: ['asset:asset-1'] }, status: 'active',
  }])
  const payload = serializeProjectMemberDrafts(draft)
  payload[0].function_scopes?.researcher.push('asset:asset-2')
  assert.deepEqual(draft[0].function_scopes.researcher, ['asset:asset-1'])
})
