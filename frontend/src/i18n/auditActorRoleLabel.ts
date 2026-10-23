/**
 * The audit column `actor_role` has exactly one meaning: the role the operator
 * held in that tenant at the time. The vocabulary is owned by the server
 * (`internal/types/tenant_member.go` -- the tenant roles, plus the two
 * platform-side values `system_admin` / `platform_api_key` that the platform
 * audit writer pins).
 *
 * Three surfaces read that column: the tenant settings audit drawer, the
 * platform audit log, and the permission evidence desk. They used to disagree
 * about what to show when the value was outside the vocabulary -- one fell
 * back to the untranslated i18n key (`tenantMember.role.system_admin`), one to
 * the raw English word (`system_admin`), and one showed the raw English word
 * for every value and a hard-coded Chinese word for the empty one. The same
 * value therefore meant two different things on two screens, and none of them
 * said "unknown" except by accident.
 *
 * This module is the one place that decides how a value becomes a label. It
 * holds no vocabulary of its own: the words live in the locale bundles under
 * `AUDIT_ACTOR_ROLE_I18N_ROOT`, so a value that has a label has exactly one
 * label, and a value that has none falls back to exactly one label as well.
 */

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
 */
export function auditActorRoleLabel(
  i18n: AuditActorRoleI18n,
  role: string | null | undefined,
): string {
  const value = typeof role === 'string' ? role.trim() : ''
  if (value) {
    const key = `${AUDIT_ACTOR_ROLE_I18N_ROOT}.${value}`
    if (i18n.te(key)) return i18n.t(key)
  }
  return i18n.t(AUDIT_ACTOR_ROLE_UNKNOWN_KEY)
}
