import assert from 'node:assert/strict'
import { readdirSync, readFileSync } from 'node:fs'
import { dirname, join, relative, sep } from 'node:path'
import { fileURLToPath } from 'node:url'
import { test } from 'node:test'

import {
  AUDIT_ACTOR_ROLE_I18N_ROOT,
  auditActorRoleLabel,
} from './auditActorRoleLabel.ts'
import { LOCALE_BUNDLES, getLocaleValueAtPath } from './localeKeyAudit.ts'
import {
  TENANT_ROLE_LABELS,
  TENANT_ROLE_VALUES,
  isTenantRoleValue,
  tenantRoleLabel,
} from './tenantRoleLabel.ts'

/**
 * The tenant role vocabulary is the server's four values
 * (`internal/types/tenant_member.go` -- `types.TenantRole`). They used to
 * reach the screen through two wordings: the audit surfaces and the
 * membership drawer said 所有者 / 管理员 / 编辑 / 访客, while the permission
 * evidence desk said 所有者 / 管理员 / 协作者 / 查看者.
 *
 * These tests pin the one wording and the one place that produces it, so a
 * second table cannot grow back next to it (issue #576).
 */

const FRONTEND_ROOT = join(dirname(fileURLToPath(import.meta.url)), '..', '..')
const SOURCE_ROOT = join(FRONTEND_ROOT, 'src')
const SOURCE_EXTENSIONS = /\.(vue|ts|js|mjs)$/
const TEST_FILE_PATTERN = /\.test\.(ts|mjs|js)$/
const SKIPPED_DIRECTORIES = new Set(['node_modules', 'dist'])
const LOCALE_BUNDLE_DIR = `${sep}i18n${sep}locales${sep}`

/** The one module that produces a Chinese label for the tenant roles. */
const CANONICAL_SOURCE = 'src/i18n/tenantRoleLabel.ts'

/**
 * The other vocabularies that carry the same words for their own roles.
 *
 * They are different axes -- the project governance role and the topic role
 * are not the tenant role -- so they keep their own wording and are not
 * merged into the tenant vocabulary. What they must not do is pass for it,
 * so each one has to say which dimension it belongs to.
 */
const DIFFERENT_DIMENSION_SOURCES = [
  { path: 'src/views/lingdoc/ProjectMembers.vue', dimension: '维度：项目治理角色' },
  { path: 'src/views/lingdoc/firstPassEntryState.ts', dimension: '维度：课题角色' },
] as const

/**
 * An entry that maps a tenant role value to a word: `owner: '所有者'`.
 *
 * Only a *Chinese* value counts. A raw value (`owner: 'Owner'`) is not a
 * label, and the locale bundles are excluded from this scan because they
 * carry several role vocabularies side by side -- the tenant-role namespaces
 * there are pinned by the runtime assertion below instead.
 */
const ROLE_LABEL_ENTRY = /(?:^|[^\w])(owner|admin|contributor|viewer)\s*:\s*(['"`])([^'"`]*)\2/g
const CONTAINS_CJK = /[\u3400-\u9fff]/

function listSourceFiles(dir: string, results: string[] = []): string[] {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name)
    if (entry.isDirectory()) {
      if (SKIPPED_DIRECTORIES.has(entry.name)) continue
      listSourceFiles(path, results)
      continue
    }
    if (!SOURCE_EXTENSIONS.test(path)) continue
    if (TEST_FILE_PATTERN.test(path)) continue
    if (path.includes(LOCALE_BUNDLE_DIR)) continue
    results.push(path)
  }
  return results
}

type LabelSource = { path: string; roles: string[] }

/** Every source file that maps a tenant role value to a Chinese label. */
function tenantRoleLabelSources(): LabelSource[] {
  const sources: LabelSource[] = []
  for (const file of listSourceFiles(SOURCE_ROOT)) {
    const source = readFileSync(file, 'utf8')
    const roles = new Set<string>()
    for (const match of source.matchAll(ROLE_LABEL_ENTRY)) {
      if (!CONTAINS_CJK.test(match[3])) continue
      roles.add(match[1])
    }
    if (roles.size === 0) continue
    sources.push({
      path: relative(FRONTEND_ROOT, file).split(sep).join('/'),
      roles: [...roles].sort(),
    })
  }
  return sources
}

test('the vocabulary is the server four, and only they have a label', () => {
  assert.deepEqual([...TENANT_ROLE_VALUES], ['owner', 'admin', 'contributor', 'viewer'])

  for (const value of TENANT_ROLE_VALUES) {
    assert.ok(isTenantRoleValue(value), `${value} must be a tenant role`)
    assert.equal(tenantRoleLabel(value), TENANT_ROLE_LABELS[value], `${value} must have a label`)
  }

  for (const value of ['system_admin', 'platform_api_key', 'member', '', '  ', 'tenant_admin']) {
    assert.equal(isTenantRoleValue(value), false, `${JSON.stringify(value)} must not be a tenant role`)
    assert.equal(tenantRoleLabel(value), undefined, `${JSON.stringify(value)} must not have a tenant label`)
  }

  assert.equal(tenantRoleLabel(null), undefined)
  assert.equal(tenantRoleLabel(undefined), undefined)
})

test('the tenant roles have one Chinese wording', () => {
  assert.deepEqual(TENANT_ROLE_LABELS, {
    owner: '所有者',
    admin: '管理员',
    contributor: '编辑',
    viewer: '访客',
  })
})

test('the tenant role vocabulary has exactly one label source in the frontend', () => {
  const sources = tenantRoleLabelSources()

  // The scan has to find the canonical source, or it proves nothing.
  const canonical = sources.filter((source) => source.path === CANONICAL_SOURCE)
  assert.equal(
    canonical.length,
    1,
    `the canonical source must be found exactly once, got ${JSON.stringify(sources)}`,
  )
  assert.deepEqual(
    canonical[0].roles,
    [...TENANT_ROLE_VALUES].sort(),
    'the canonical source must carry all four tenant roles',
  )

  // No other module may carry a tenant role label of its own. The two
  // vocabularies that legitimately mention the same words are different
  // dimensions, and they are allowed below -- but nothing else is.
  const allowed = new Set<string>(DIFFERENT_DIMENSION_SOURCES.map((entry) => entry.path))
  const unexpected = sources.filter(
    (source) => source.path !== CANONICAL_SOURCE && !allowed.has(source.path),
  )
  assert.deepEqual(
    unexpected,
    [],
    `only the canonical source may label a tenant role; found ${JSON.stringify(unexpected)}`,
  )
})

test('the other role vocabularies say which dimension they belong to', () => {
  for (const entry of DIFFERENT_DIMENSION_SOURCES) {
    const source = readFileSync(join(FRONTEND_ROOT, entry.path), 'utf8')
    assert.ok(
      source.includes(entry.dimension),
      `${entry.path} must mark its dimension as ${entry.dimension}`,
    )
    assert.ok(
      source.includes('不是租户角色'),
      `${entry.path} must say that it is not the tenant role vocabulary`,
    )
  }
})

test('the zh-CN bundle says the tenant roles the one way', () => {
  const zh = LOCALE_BUNDLES['zh-CN']

  for (const value of TENANT_ROLE_VALUES) {
    assert.equal(
      getLocaleValueAtPath(zh, `tenantMember.role.${value}`),
      TENANT_ROLE_LABELS[value],
      `tenantMember.role.${value} must read as the one wording`,
    )
    assert.equal(
      getLocaleValueAtPath(zh, `${AUDIT_ACTOR_ROLE_I18N_ROOT}.${value}`),
      TENANT_ROLE_LABELS[value],
      `${AUDIT_ACTOR_ROLE_I18N_ROOT}.${value} must read as the one wording`,
    )
  }
})

test('the zh-CN bundle takes its wording from the canonical source', () => {
  const source = readFileSync(join(SOURCE_ROOT, 'i18n', 'locales', 'zh-CN.ts'), 'utf8')
  assert.match(
    source,
    /TENANT_ROLE_LABELS/,
    'the zh-CN bundle must spread the canonical labels instead of spelling them out',
  )
})

test('the permission evidence desk reads the shared resolver and carries no table of its own', () => {
  const source = readFileSync(join(SOURCE_ROOT, 'views', 'lingdoc', 'permissionEvidence.ts'), 'utf8')

  assert.match(
    source,
    /from '\.\.\/\.\.\/i18n\/auditActorRoleLabel'/,
    'the evidence desk must read the role labels through the shared resolver',
  )
  assert.doesNotMatch(
    source,
    /PERMISSION_ROLE_LABELS/,
    'the evidence desk must not carry a role label table of its own',
  )
  assert.doesNotMatch(
    source,
    /查看者|协作者/,
    'the evidence desk must not spell a tenant role in a second way',
  )
})

test('a locale that lacks a tenant role still answers with the one wording', () => {
  const withoutLabels = { te: () => false, t: (key: string) => key }

  for (const value of TENANT_ROLE_VALUES) {
    assert.equal(
      auditActorRoleLabel(withoutLabels, value),
      TENANT_ROLE_LABELS[value],
      `${value} must read as the one wording even without a locale label`,
    )
  }

  assert.equal(
    auditActorRoleLabel(withoutLabels, 'system_admin'),
    `${AUDIT_ACTOR_ROLE_I18N_ROOT}.unknown`,
    'a value outside the vocabulary must not borrow a tenant role wording',
  )
})
