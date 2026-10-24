/**
 * The audit column `actor_role` has exactly one meaning: the role the operator
 * held in that tenant at the time. The vocabulary is owned by the server
 * (`internal/types/tenant_member.go` -- the tenant roles, plus the two
 * platform-side values `system_admin` / `platform_api_key` that the platform
 * audit writer pins).
 *
 * Three surfaces read that column: the tenant settings audit drawer, the
 * platform audit log, and the permission evidence desk. They used to disagree
 * about what to show -- the drawer fell back to the untranslated i18n key
 * (`tenantMember.role.system_admin`), the platform log to the raw English word
 * (`system_admin`), and the evidence desk showed the raw English word for
 * every value and a hard-coded Chinese word for the empty one. The same value
 * therefore meant different things on different screens.
 *
 * This module is the one place that decides how a value becomes a label. It
 * holds no vocabulary of its own: the words live in the locale bundles under
 * `AUDIT_ACTOR_ROLE_I18N_ROOT`, so a value that has a label has exactly one
 * label, and a value that has none falls back to exactly one label as well.
 *
 * The four tenant roles are the exception to "the words live in the locale
 * bundle": their single Chinese wording is owned by `tenantRoleLabel.ts`,
 * and the zh-CN bundle spreads it into the namespace this module reads. So
 * when a locale does not carry a tenant role, this module still answers with
 * the one wording the product uses, instead of a second one.
 */

import { tenantRoleLabel } from './tenantRoleLabel'

/**
 * The locale root that holds the labels for the audit column's actor role.
 */
export const AUDIT_ACTOR_ROLE_I18N_ROOT = 'system.globalSettings.audit.actorRole'

/**
 * The label used when the column carries a value outside the vocabulary --
 * including the empty value, which is what the rows written before the column
 * carried a role look like.
 */
export const AUDIT_ACTOR_ROLE_UNKNOWN_KEY = `${AUDIT_ACTOR_ROLE_I18N_ROOT}.unknown`

/**
 * The two operations this module needs from vue-i18n.
 *
 * It is spelled out rather than taken from `Composer` on purpose: the
 * resolver only asks "is there a label for this key" and "what is it", and a
 * composer typed against a concrete message schema must not be able to change
 * the answer.
 */
export type AuditActorRoleI18n = {
  te: (key: string) => boolean
  t: (key: string) => string
}

/**
 * Resolve an `actor_role` value to its label.
 *
 * The empty value is the historical "unknown" case, so it resolves to the
 * unknown label rather than to an empty tag. A value outside the vocabulary
 * resolves to the same label: the column's meaning is "the role the operator
 * held", and a word that is not a role says exactly that much.
 *
 * A tenant role that the locale does not carry resolves to the one Chinese
 * wording the product uses for it, so no locale can end up with a second
 * word for the same value.
 */
export function auditActorRoleLabel(
  i18n: AuditActorRoleI18n,
  role: string | null | undefined,
): string {
  const value = typeof role === 'string' ? role.trim() : ''
  if (value) {
    const key = `${AUDIT_ACTOR_ROLE_I18N_ROOT}.${value}`
    if (i18n.te(key)) return i18n.t(key)
    const tenantLabel = tenantRoleLabel(value)
    if (tenantLabel) return tenantLabel
  }
  return i18n.t(AUDIT_ACTOR_ROLE_UNKNOWN_KEY)
}
