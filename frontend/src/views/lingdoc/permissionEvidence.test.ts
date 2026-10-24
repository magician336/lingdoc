import assert from 'node:assert/strict'
import test from 'node:test'

import { createI18n } from 'vue-i18n'

import { auditActorRoleLabel } from '../../i18n/auditActorRoleLabel.ts'
import { LOCALE_BUNDLES } from '../../i18n/localeKeyAudit.ts'
import { TENANT_ROLE_LABELS, TENANT_ROLE_VALUES } from '../../i18n/tenantRoleLabel.ts'
import {
  PERMISSION_EVIDENCE_ROWS,
  PERMISSION_ROLES,
  decisionLabel,
  permissionDecision,
  permissionRoleLabel,
  roleSatisfies,
} from './permissionEvidence'

const i18n = createI18n({
  legacy: false,
  locale: 'zh-CN',
  fallbackLocale: false,
  messages: LOCALE_BUNDLES,
})
const { t, te } = i18n.global

test('keeps WeKnora role ordering visible to the demo', () => {
  assert.equal(roleSatisfies('viewer', 'viewer'), true)
  assert.equal(roleSatisfies('viewer', 'contributor'), false)
  assert.equal(roleSatisfies('admin', 'contributor'), true)
  assert.equal(roleSatisfies('owner', 'owner'), true)
})

test('does not turn an absent identity into an allow decision', () => {
  assert.equal(permissionDecision('', 'viewer'), 'unknown')
  assert.equal(decisionLabel('unknown'), '等待身份')
  assert.equal(permissionRoleLabel({ te, t }, 'mystery-role'), '未知角色')
})

test('the matrix offers the tenant roles, ascending by privilege', () => {
  assert.deepEqual(PERMISSION_ROLES, ['viewer', 'contributor', 'admin', 'owner'])
})

test('the desk reads the tenant role vocabulary the one way', () => {
  for (const value of TENANT_ROLE_VALUES) {
    assert.equal(
      permissionRoleLabel({ te, t }, value),
      TENANT_ROLE_LABELS[value],
      `${value} must read as the one wording`,
    )
    assert.equal(
      permissionRoleLabel({ te, t }, value),
      auditActorRoleLabel({ te, t }, value),
      `${value} must read the same way here as in the audit surfaces`,
    )
  }
})

test('covers the permission proof points in the page', () => {
  assert.deepEqual(PERMISSION_EVIDENCE_ROWS.map((row) => row.id), [
    'read', 'edit', 'members', 'deliver', 'transfer',
  ])
})
