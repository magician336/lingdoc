import type { TemplateCopyDefinition, TemplateMigrationPreview } from '@/api/lingdoc/workspace'

export interface TemplatePreviewBasis {
  projectId: string
  projectVersion: number
  copyVersion: number
  fingerprint: string
}

export function previewMatchesBasis(
  preview: TemplateMigrationPreview | null,
  requested: TemplatePreviewBasis | null,
  current: TemplatePreviewBasis | null,
): boolean {
  return !!preview && !!requested && !!current
    && requested.projectId === current.projectId
    && requested.projectVersion === current.projectVersion
    && requested.copyVersion === current.copyVersion
    && requested.fingerprint === current.fingerprint
    && preview.project_id === requested.projectId
    && preview.expected_project_version === requested.projectVersion
    && preview.source_copy_version === requested.copyVersion
}

// The lists and their meanings come from the server. Missing data is not a
// clean preview, and all three reported risks require resolution before saving.
export function previewBlocksSave(preview: TemplateMigrationPreview | null): boolean {
  if (!preview || !Array.isArray(preview.missing_required) ||
      !Array.isArray(preview.orphaned) || !Array.isArray(preview.incompatible)) return true
  return preview.missing_required.length > 0 || preview.orphaned.length > 0 || preview.incompatible.length > 0
}

export interface StructureChange {
  id: string
  name: string
  kind: 'added' | 'removed' | 'changed'
  beforeOrder?: number
  afterOrder?: number
  details?: string[]
}

type OrderedItem = { id: string; name: string; order?: number; attributes?: Record<string, string> }
function orderedChanges(before: OrderedItem[], after: OrderedItem[]): StructureChange[] {
  const oldItems = new Map(before.map((item, index) => [item.id, { ...item, position: item.order ?? index + 1 }]))
  const newItems = new Map(after.map((item, index) => [item.id, { ...item, position: item.order ?? index + 1 }]))
  const changes: StructureChange[] = []
  for (const [id, oldItem] of oldItems) {
    const newItem = newItems.get(id)
    if (!newItem) changes.push({ id, name: oldItem.name, kind: 'removed', beforeOrder: oldItem.position })
    else {
      const details: string[] = []
      if (oldItem.name !== newItem.name) details.push(`名称：${oldItem.name} → ${newItem.name}`)
      if (oldItem.position !== newItem.position) details.push(`顺序：${oldItem.position} → ${newItem.position}`)
      for (const key of new Set([...Object.keys(oldItem.attributes ?? {}), ...Object.keys(newItem.attributes ?? {})])) {
        const oldValue = oldItem.attributes?.[key] ?? '无', newValue = newItem.attributes?.[key] ?? '无'
        if (oldValue !== newValue) details.push(`${key}：${oldValue} → ${newValue}`)
      }
      if (details.length) changes.push({ id, name: newItem.name, kind: 'changed', beforeOrder: oldItem.position, afterOrder: newItem.position, details })
    }
  }
  for (const [id, newItem] of newItems) if (!oldItems.has(id))
    changes.push({ id, name: newItem.name, kind: 'added', afterOrder: newItem.position })
  return changes
}

export function templateStructureChanges(before: TemplateCopyDefinition, after: TemplateCopyDefinition) {
  return {
    fields: orderedChanges(
      before.fields.map(field => ({ id: field.field_id, name: field.label, order: field.order, attributes: {
        类型: field.type ?? '', 必填: field.required || (before.required_fields ?? []).includes(field.field_id) ? '是' : '否',
        选项: (field.options ?? []).join('、') || '无', 说明: field.description ?? '无',
      } })),
      after.fields.map(field => ({ id: field.field_id, name: field.label, order: field.order, attributes: {
        类型: field.type ?? '', 必填: field.required || (after.required_fields ?? []).includes(field.field_id) ? '是' : '否',
        选项: (field.options ?? []).join('、') || '无', 说明: field.description ?? '无',
      } })),
    ),
    sections: orderedChanges(
      before.sections.map(section => ({ id: section.section_id, name: section.title, order: section.order, attributes: { 必需: section.required ? '是' : '否', 说明: section.description ?? '无' } })),
      after.sections.map(section => ({ id: section.section_id, name: section.title, order: section.order, attributes: { 必需: section.required ? '是' : '否', 说明: section.description ?? '无' } })),
    ),
  }
}
