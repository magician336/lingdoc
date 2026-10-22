import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'

/**
 * 生成请求的形状必须带得上动作声明。
 *
 * 这个模块在运行时引入了带路径别名的请求工具，直接 import 在这条测试的运行环境
 * 里解析不了别名——所以这里读源码断言形状。仓库里已有同样的做法（契约与前端
 * 类型的对照测试）。
 *
 * 断言的是**服务端契约要求的字段名**：动作声明的三个字段与上下文选择必须
 * 出现在生成请求里，否则前端发出去的请求走的是旧路径——服务端看到的就是
 * 「一个没有动作的自由文本请求」。
 */
const API_FILE = resolve(dirname(fileURLToPath(import.meta.url)), 'generation.ts')
const source = readFileSync(API_FILE, 'utf8')

function requestBody(): string {
  const start = source.indexOf('export interface GenerationRequest')
  assert.notEqual(start, -1, '生成请求的接口不见了')
  const end = source.indexOf('\n}', start)
  return source.slice(start, end)
}

test('the generation request carries the action declaration', () => {
  const body = requestBody()
  for (const field of ['writing_action', 'output_params', 'scope', 'context']) {
    assert.match(body, new RegExp(`\\b${field}\\?:`), `生成请求必须带 ${field}`)
  }
})

test('the generation run reports the dropped context', () => {
  const start = source.indexOf('export interface GenerationRun {')
  assert.notEqual(start, -1, '生成运行的接口不见了')
  const end = source.indexOf('\n}', start)
  const body = source.slice(start, end)
  assert.match(body, /dropped_context\?:/, '生成运行必须报告被丢掉的可选上下文')
})
