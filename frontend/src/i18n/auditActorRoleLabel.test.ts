import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { test } from 'node:test'

import { createI18n } from 'vue-i18n'

import {
  AUDIT_ACTOR_ROLE_I18N_ROOT,
  AUDIT_ACTOR_ROLE_UNKNOWN_KEY,
  auditActorRoleLabel,
} from './auditActorRoleLabel.ts'
import { LOCALE_BUNDLES, getLocaleValueAtPath, type LocaleName } from './localeKeyAudit.ts'

/**
 * The audit column `actor_role` carries the role the operator held in that
 * tenant. The vocabulary is the server's: the four tenant roles plus the two
 * platform-side values. These tests pin the read side of that contract --
 * what the three audit read points show for each value, and that they show
 * the same thing.
 */

/** The tenant roles (`internal/types/tenant_member.go`). */
const TENANT_ROLE_VALUES = ['owner', 'admin', 'contributor', 'viewer'] as const

/** The platform-side values the platform audit writer pins. */
const PLATFORM_ROLE_VALUES = ['system_admin', 'platform_api_key'] as const

const ALL_ROLE_VALUES = [...TENANT_ROLE_VALUES, ...PLATFORM_ROLE_VALUES]

/** The empty value is what the rows written before the column carried a role look like. */
const HISTORICAL_EMPTY_VALUE = ''

/** A word that is not a role at all: the reader must not echo it back. */
const OUTSIDE_VOCABULARY_VALUE = 'tenant_admin'

const FRONTEND_ROOT = join(dirname(fileURLToPath(import.meta.url)), '..', '..')

/**
 * The three surfaces that read the audit column `actor_role`.
 *
 * They have to agree: the same value may not mean two different things on
 * two screens, and a third screen is no different.
 */
const AUDIT_READ_POINTS = [
  'src/views/settings/TenantMembers.vue',
  'src/views/system/SystemAuditLog.vue',
  'src/views/lingdoc/PermissionEvidence.vue',
]

function composerFor(locale: LocaleName) {
  const i18n = createI18n({
    legacy: false,
    locale,
    fallbackLocale: false,
    messages: LOCALE_BUNDLES,
  })
  return i18n.global
}

test('every locale labels the whole actor-role vocabulary', () => {
  for (const [localeName, bundle] of Object.entries(LOCALE_BUNDLES) as Array<[LocaleName, unknown]>) {
    for (const value of ALL_ROLE_VALUES) {
      const label = getLocaleValueAtPath(bundle, `${AUDIT_ACTOR_ROLE_I18N_ROOT}.${value}`)
      assert.equal(typeof label, 'string', `${localeName}: missing the label for ${value}`)
      assert.ok((label as string).trim().length > 0, `${localeName}: the label for ${value} is empty`)
      assert.notEqual(label, value, `${localeName}: ${value} would be displayed as the raw value`)
    }
    const unknown = getLocaleValueAtPath(bundle, AUDIT_ACTOR_ROLE_UNKNOWN_KEY)
    assert.equal(typeof unknown, 'string', `${localeName}: missing the unknown-role label`)
    assert.ok((unknown as string).trim().length > 0, `${localeName}: the unknown-role label is empty`)
  }
})

test('the tenant roles read as their labels, not as raw words or i18n keys', () => {
  const i18n = composerFor('zh-CN')
  const expected: Record<(typeof TENANT_ROLE_VALUES)[number], string> = {
    owner: '所有者',
    admin: '管理员',
    contributor: '编辑',
    viewer: '访客',
  }

  for (const value of TENANT_ROLE_VALUES) {
    const label = auditActorRoleLabel(i18n, value)
    assert.equal(label, expected[value], `${value} must read as its label`)
    assert.notEqual(label, value, `${value} must not read as the raw word`)
    assert.notEqual(label, `${AUDIT_ACTOR_ROLE_I18N_ROOT}.${value}`, `${value} must not read as an i18n key`)
    assert.notEqual(label, `tenantMember.role.${value}`, `${value} must not read as a tenantMember.role key`)
  }
})

test('the platform-side values have a label of their own', () => {
  const i18n = composerFor('zh-CN')

  assert.equal(auditActorRoleLabel(i18n, 'system_admin'), '系统管理员')
  assert.equal(auditActorRoleLabel(i18n, 'platform_api_key'), '平台 API 密钥')

  for (const value of PLATFORM_ROLE_VALUES) {
    const label = auditActorRoleLabel(i18n, value)
    assert.notEqual(label, value, `${value} must not read as the raw word`)
    assert.notEqual(label, `${AUDIT_ACTOR_ROLE_I18N_ROOT}.${value}`, `${value} must not read as an i18n key`)
  }
})

test('the empty value and a value outside the vocabulary read as unknown', () => {
  const i18n = composerFor('zh-CN')

  for (const value of [HISTORICAL_EMPTY_VALUE, '   ', OUTSIDE_VOCABULARY_VALUE, 'member']) {
    const label = auditActorRoleLabel(i18n, value)
    assert.equal(label, '未知角色', `${JSON.stringify(value)} must read as the unknown role`)
    assert.notEqual(label, value, `${JSON.stringify(value)} must not read as the raw word`)
    assert.notEqual(label, `${AUDIT_ACTOR_ROLE_I18N_ROOT}.${value}`, `${JSON.stringify(value)} must not read as an i18n key`)
  }

  assert.equal(auditActorRoleLabel(i18n, null), '未知角色')
  assert.equal(auditActorRoleLabel(i18n, undefined), '未知角色')
})

test('every locale resolves every value the same way', () => {
  for (const [localeName, bundle] of Object.entries(LOCALE_BUNDLES) as Array<[LocaleName, unknown]>) {
    const i18n = composerFor(localeName)
    const unknown = getLocaleValueAtPath(bundle, AUDIT_ACTOR_ROLE_UNKNOWN_KEY)

    for (const value of ALL_ROLE_VALUES) {
      assert.equal(
        auditActorRoleLabel(i18n, value),
        getLocaleValueAtPath(bundle, `${AUDIT_ACTOR_ROLE_I18N_ROOT}.${value}`),
        `${localeName}: ${value} must resolve to its own label`,
      )
    }

    assert.equal(auditActorRoleLabel(i18n, HISTORICAL_EMPTY_VALUE), unknown, `${localeName}: the empty value must read as unknown`)
    assert.equal(auditActorRoleLabel(i18n, OUTSIDE_VOCABULARY_VALUE), unknown, `${localeName}: a value outside the vocabulary must read as unknown`)
  }
})

test('every audit read point reads the column through the shared resolver', () => {
  for (const view of AUDIT_READ_POINTS) {
    const source = readFileSync(join(FRONTEND_ROOT, view), 'utf8')
    assert.match(
      source,
      /from '@\/i18n\/auditActorRoleLabel'/,
      `${view} must read the actor role through the shared resolver`,
    )
    assert.doesNotMatch(
      source,
      /function auditActorRoleLabel\s*\(/,
      `${view} must not define its own actor-role resolver`,
    )
    assert.doesNotMatch(
      source,
      /tenantMember\.role\.'\s*\+\s*row\.actor_role/,
      `${view} must not read the audit column through the membership role namespace`,
    )
    assert.doesNotMatch(
      source,
      /v-if="row\.actor_role"/,
      `${view} must always render the role label, so an empty value reads as unknown`,
    )
    // A view may not carry its own fallback for the column: `|| '未知角色'`
    // made the third read point show the raw English word for a value the
    // other two read as a label.
    assert.doesNotMatch(
      source,
      /row\.actor_role\s*(\|\||\?\?)/,
      `${view} must not fall back on the audit column itself`,
    )
    // The unknown-role label lives in the locale bundle, next to the other
    // role labels -- not in a view.
    assert.doesNotMatch(
      source,
      /未知角色/,
      `${view} must not hard-code the unknown-role label`,
    )
    // The column reaches the resolver through the shared wrapper.
    assert.match(
      source,
      /formatAuditActorRole\(row\.actor_role\)/,
      `${view} must render the column through the shared wrapper`,
    )
    // Outside the import, the shared resolver may be mentioned exactly once --
    // the call. A view that defines its own resolver mentions it twice, and
    // the shared import would go unused.
    const body = source
      .split('\n')
      .filter((line) => !/^\s*import\b.*auditActorRoleLabel/.test(line))
      .join('\n')
    assert.equal(
      (body.match(/auditActorRoleLabel/g) ?? []).length,
      1,
      `${view} must use the shared resolver exactly once, with no local definition of its own`,
    )
  }
})

test('the three read points resolve the column the same way', () => {
  const calls = AUDIT_READ_POINTS.map((view) => {
    const source = readFileSync(join(FRONTEND_ROOT, view), 'utf8')
    const found = source.match(/auditActorRoleLabel\([^)]*\)/g) ?? []
    assert.equal(found.length, 1, `${view} must call the shared resolver exactly once`)
    return found[0]
  })

  // Same resolver, same adapter, same unmodified column value: the three
  // read points cannot drift apart while this holds.
  assert.equal(
    new Set(calls).size,
    1,
    `every read point must hand the raw column value to the same resolver call, got ${JSON.stringify(calls)}`,
  )
})

test('the call every read point makes turns each value into one label', () => {
  const i18n = composerFor('zh-CN')

  // The exact call the three read points make: the shared resolver, fed the
  // raw column value and the app's own i18n composer.
  const readPointCall = (role: string) => auditActorRoleLabel({ te: i18n.te, t: i18n.t }, role)

  for (const value of ALL_ROLE_VALUES) {
    const label = readPointCall(value)
    assert.notEqual(label, value, `${value} must not read as the raw word`)
    assert.notEqual(label, `${AUDIT_ACTOR_ROLE_I18N_ROOT}.${value}`, `${value} must not read as an i18n key`)
    assert.notEqual(label, `tenantMember.role.${value}`, `${value} must not read as a tenantMember.role key`)
  }

  for (const value of [HISTORICAL_EMPTY_VALUE, OUTSIDE_VOCABULARY_VALUE]) {
    assert.equal(readPointCall(value), '未知角色', `${JSON.stringify(value)} must read as the unknown role`)
  }
})
