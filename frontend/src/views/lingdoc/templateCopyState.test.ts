import assert from 'node:assert/strict'
import test from 'node:test'
import { previewBlocksSave, previewMatchesBasis, templateStructureChanges } from './templateCopyState'
import type { TemplateCopyDefinition, TemplateMigrationPreview } from '@/api/lingdoc/workspace'

const basis = { projectId: 'p1', projectVersion: 4, copyVersion: 2, fingerprint: 'draft-a' }
const preview = {
  project_id: 'p1', expected_project_version: 4, source_copy_version: 2,
  missing_required: [], orphaned: [], incompatible: [],
} as unknown as TemplateMigrationPreview

test('only the same project, versions, submitted draft and server response make a preview current', () => {
  assert.equal(previewMatchesBasis(preview, basis, basis), true)
  for (const current of [
    { ...basis, projectId: 'p2' }, { ...basis, projectVersion: 5 },
    { ...basis, copyVersion: 3 }, { ...basis, fingerprint: 'draft-b' },
  ]) assert.equal(previewMatchesBasis(preview, basis, current), false)
  assert.equal(previewMatchesBasis({ ...preview, project_id: 'p2' }, basis, basis), false)
  assert.equal(previewMatchesBasis({ ...preview, expected_project_version: 5 }, basis, basis), false)
  assert.equal(previewMatchesBasis({ ...preview, source_copy_version: 3 }, basis, basis), false)
  assert.equal(previewMatchesBasis(null, basis, basis), false)
})

test('an incomplete preview or any server-reported migration risk blocks draft save', () => {
  assert.equal(previewBlocksSave(preview), false)
  for (const kind of ['missing_required', 'orphaned', 'incompatible'] as const)
    assert.equal(previewBlocksSave({ ...preview, [kind]: ['field-a'] }), true)
  assert.equal(previewBlocksSave({ ...preview, missing_required: undefined } as unknown as TemplateMigrationPreview), true)
  assert.equal(previewBlocksSave(null), true)
})

test('structure preview shows additions, removals and order changes, even without a current value', () => {
  const before = {
    fields: [{ field_id: 'a', label: 'A', order: 1 }, { field_id: 'b', label: 'B', order: 2 }],
    sections: [{ section_id: 's1', title: 'S1', order: 1 }, { section_id: 's2', title: 'S2', order: 2 }],
  } as TemplateCopyDefinition
  const after = {
    fields: [{ field_id: 'b', label: 'B', order: 1 }, { field_id: 'c', label: 'C', order: 2 }],
    sections: [{ section_id: 's1', title: 'S1', order: 2 }, { section_id: 's3', title: 'S3', order: 1 }],
  } as TemplateCopyDefinition
  const changes = templateStructureChanges(before, after)
  assert.deepEqual(changes.fields, [
    { id: 'a', name: 'A', kind: 'removed', beforeOrder: 1 },
    { id: 'b', name: 'B', kind: 'changed', beforeOrder: 2, afterOrder: 1, details: ['顺序：2 → 1'] },
    { id: 'c', name: 'C', kind: 'added', afterOrder: 2 },
  ])
  assert.deepEqual(changes.sections, [
    { id: 's1', name: 'S1', kind: 'changed', beforeOrder: 1, afterOrder: 2, details: ['顺序：1 → 2'] },
    { id: 's2', name: 'S2', kind: 'removed', beforeOrder: 2 },
    { id: 's3', name: 'S3', kind: 'added', afterOrder: 1 },
  ])
})


test('type, required status and enum options appear in structure changes', () => {
  const before = { fields: [{ field_id: 'a', label: 'A', type: 'string', required: false }], sections: [], terms: [], rules: [], required_fields: [] } as TemplateCopyDefinition
  const after = { fields: [{ field_id: 'a', label: 'A', type: 'enum', required: true, options: ['one', 'two'] }], sections: [], terms: [], rules: [], required_fields: ['a'] } as TemplateCopyDefinition
  assert.deepEqual(templateStructureChanges(before, after).fields[0].details, ['类型：string → enum', '必填：否 → 是', '选项：无 → one、two'])
})
