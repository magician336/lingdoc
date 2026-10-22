import assert from 'node:assert/strict'
import test from 'node:test'

import { generationActionDeclaration } from './generationAction'

/**
 * 动作面板与生成请求之间的字段名差别必须只收在一处。
 *
 * 面板说「动作」，请求说「writing_action」；产出参数与目标范围两边同名。
 * 收在一处之后，组件与请求体都不必各自记一份映射——映射散开就会出现两份
 * 说法，而其中一份没人跑。
 */
test('the action panel payload becomes the generation request declaration', () => {
  const declaration = generationActionDeclaration({
    action: 'generate',
    scope: 'chapter',
    output_params: { length: 'medium', tone: 'formal' },
  })
  assert.deepEqual(declaration, {
    writing_action: 'generate',
    scope: 'chapter',
    output_params: { length: 'medium', tone: 'formal' },
  })
})

/**
 * 动作声明不碰请求的其余部分：旧请求不带任何声明字段，摘要因此不变。
 */
test('the declaration carries only the three action fields', () => {
  const declaration = generationActionDeclaration({
    action: 'generate',
    scope: 'section',
    output_params: { length: 'short' },
  })
  assert.deepEqual(Object.keys(declaration).sort(), ['output_params', 'scope', 'writing_action'])
})
