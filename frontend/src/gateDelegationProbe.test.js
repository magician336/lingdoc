// #734 真实验证样本：本分支唯一目的是让 frontend.yml 的 verify job 失败，
// 令 gate 的委托失败核验（ADR 0014 §三）走「failure → gate 红」路径。
// 验证完成后整个分支删除，不合入。
import { describe, expect, test } from 'vitest'

describe('gate delegation failure probe (#734)', () => {
  test('verify job must fail so the gate names the delegated failure', () => {
    expect(true).toBe(false)
  })
})
