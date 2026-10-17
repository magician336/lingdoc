import assert from 'node:assert/strict'
import test from 'node:test'
import { filterProjects } from './projectPicker'

test('empty query returns the loaded list untouched', () => {
  const list = [{ name: '甲', status: 'draft' }, { name: '乙', status: 'active' }]
  assert.equal(filterProjects(list, '').length, 2)
  assert.equal(filterProjects(list, '  ').length, 2)
})

test('filtering is case-insensitive on name and status', () => {
  const list = [{ name: '数字人文课题', status: 'draft' }, { name: 'AI 演示', status: 'active' }]
  assert.deepEqual(filterProjects(list, '数字').map(p => p.name), ['数字人文课题'])
  assert.deepEqual(filterProjects(list, 'ACTIVE').map(p => p.name), ['AI 演示'])
  assert.deepEqual(filterProjects(list, '不存在'), [])
})
