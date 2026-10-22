import type { WritingActionOutputParams, WritingActionScope } from './writingAction'

/**
 * 一次动作化生成请求里的动作声明。
 *
 * 面板说「动作」，请求说「writing_action」：字段名的差别在这一份声明里收口，
 * 组件与请求体不必各自记一份映射。
 *
 * 这个模块刻意不引入任何运行时依赖——它只是形状与一次改名，所以它能被直接
 * 读懂，也能被直接测。
 */
export interface GenerationActionDeclaration {
  /** 动作标识。取值由服务端的动作词汇包给出。 */
  writing_action: string
  /** 目标范围。 */
  scope: WritingActionScope
  /** 产出条件。取值必须落在动作声明的闭合取值集内。 */
  output_params: WritingActionOutputParams
}

/**
 * 把动作面板的提交载荷变成生成请求里的动作声明。
 */
export const generationActionDeclaration = (payload: {
  action: string
  scope: WritingActionScope
  output_params: WritingActionOutputParams
}): GenerationActionDeclaration => ({
  writing_action: payload.action,
  scope: payload.scope,
  output_params: payload.output_params,
})
