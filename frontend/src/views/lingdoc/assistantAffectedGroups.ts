import type { AssistantAffectedEntry, AssistantAffectedExplanation } from '@/api/lingdoc/workspace'

// The G3 impact assistant explains what a proposed change affects in two
// groups. This module only formats what the server already derived; it never
// invents an affected object and never reuses the impact resolver's
// `certainty` vocabulary.

export type AssistantAffectedGroup = 'definite_affected' | 'possible_affected'

export interface AssistantAffectedGroupView {
  group: AssistantAffectedGroup
  title: string
  entries: AssistantAffectedEntry[]
}

const groupTitles: Record<AssistantAffectedGroup, string> = {
  definite_affected: '确定受影响',
  possible_affected: '可能受影响',
}

export function assistantAffectedGroups(explanation?: AssistantAffectedExplanation | null): AssistantAffectedGroupView[] {
  if (!explanation) return []
  const groups: AssistantAffectedGroupView[] = []
  for (const group of ['definite_affected', 'possible_affected'] as const) {
    const entries = Array.isArray(explanation[group]) ? explanation[group] : []
    if (!entries.length) continue
    groups.push({ group, title: groupTitles[group], entries })
  }
  return groups
}

export function assistantAffectedObjectLabel(entry: AssistantAffectedEntry): string {
  const object = entry.object
  if (!object) return ''
  return [object.kind, object.id, object.version].filter(part => !!part).join(' · ')
}

export function assistantAffectedBasisLabel(entry: AssistantAffectedEntry): string {
  const basis = entry.basis
  if (!basis) return ''
  return [basis.source_type, basis.source_id, basis.detail].filter(part => !!part).join(' · ')
}
