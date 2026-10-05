import assert from 'node:assert/strict'
import test from 'node:test'
import {
  PERMISSION_EVIDENCE_ROWS,
  decisionLabel,
  permissionDecision,
  permissionRoleLabel,
  roleSatisfies,
} from './permissionEvidence'

test('keeps WeKnora role ordering visible to the demo', () => {
  assert.equal(roleSatisfies('viewer', 'viewer'), true)
  assert.equal(roleSatisfies('viewer', 'contributor'), false)
  assert.equal(roleSatisfies('admin', 'contributor'), true)
  assert.equal(roleSatisfies('owner', 'owner'), true)
})

test('does not turn an absent identity into an allow decision', () => {
  assert.equal(permissionDecision('', 'viewer'), 'unknown')
  assert.equal(decisionLabel('unknown'), '等待身份')
  assert.equal(permissionRoleLabel('mystery-role'), 'mystery-role')
})

test('covers the permission proof points in the page', () => {
  assert.deepEqual(PERMISSION_EVIDENCE_ROWS.map((row) => row.id), [
    'read', 'edit', 'members', 'deliver', 'transfer',
  ])
})
