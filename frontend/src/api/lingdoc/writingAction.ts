import { get } from '@/utils/request'

/**
 * 写作动作的客户端形状。
 *
 * 动作表归服务端所有：这里只有形状，没有第二份动作清单。面板从
 * `fetchWritingActions` 读动作、允许的目标范围、可用的产出参数、产出形状与
 * 允许的采纳目标。
 */

/** 一次动作执行的目标范围。 */
export type WritingActionScope = 'chapter' | 'section'

/** 产出形状：候选正文、章节骨架或评审 findings。 */
export type WritingActionProduce = 'prose' | 'outline' | 'findings'

/** 一次采纳把候选写到哪里。 */
export type WritingActionAdoptionTarget =
  | 'working_copy_partial'
  | 'working_copy_whole'
  | 'formal_version_whole'

/** 动作标识。取值由服务端给出，客户端不枚举。 */
export type WritingActionId = string

/**
 * 一个可用的产出参数维度。
 *
 * `values` 是这个维度的闭合取值集；连续区间型的维度（比例）取值为空，
 * 边界在 `min` / `max` 上。
 */
export interface WritingActionParamOption {
  name: string
  values: string[]
  min: number
  max: number
  applies_to?: string
}

/** 一个动作在某个目标范围下的描述。 */
export interface WritingActionDescription {
  action: WritingActionId
  label: string
  scopes: WritingActionScope[]
  output_params: WritingActionParamOption[]
  produce: WritingActionProduce
  targets: WritingActionAdoptionTarget[]
}

/** 客户端提交的产出条件。取值必须落在动作声明的闭合取值集内。 */
export interface WritingActionOutputParams {
  length?: string
  tone?: string
  ratio?: number
  transform?: {
    mode?: string
    target_language?: string
    audience?: string
  }
}

export interface WritingActionEnvelope<T> {
  data: T
  request_id: string
  meta: { replayed: boolean; refresh_required: boolean }
}

const base = '/api/v1/lingdoc/projects'
const segment = (id: string) => encodeURIComponent(id)

/**
 * 读取某个目标范围下可用的动作描述。
 *
 * 动作表是静态的：它由服务端的动作词汇包拥有。
 */
export const fetchWritingActions = (projectId: string, scope: WritingActionScope = 'section') =>
  get<WritingActionEnvelope<WritingActionDescription[]>>(
    `${base}/${segment(projectId)}/writing-actions?scope=${encodeURIComponent(scope)}`,
  )

/**
 * 一个产出参数维度在这次选择下算不算数。
 *
 * 带 `applies_to` 的维度只在那个子模式下算数：转写的目标语言只属于翻译，读者
 * 层次只属于解释，两者互斥。两个都要求就永远填不齐——面板于是永远发不出请求。
 */
const optionApplies = (
  option: WritingActionParamOption,
  params: WritingActionOutputParams,
): boolean => option.applies_to === undefined || option.applies_to === params.transform?.mode

/**
 * 判断一份产出参数是否落在动作声明的取值集内。
 *
 * 面板在发请求之前用它拦下非法参数：取值不合法就不发请求，而不是让服务端
 * 替它拒绝。
 */
export const outputParamsFit = (
  description: WritingActionDescription,
  params: WritingActionOutputParams,
): boolean => {
  for (const option of description.output_params) {
    if (option.name === 'ratio') {
      if (params.ratio === undefined) return false
      if (params.ratio < option.min || params.ratio > option.max) return false
      continue
    }
    const given = option.name === 'length'
      ? params.length
      : option.name === 'tone'
        ? params.tone
        : option.name === 'transform_mode'
          ? params.transform?.mode
          : option.name === 'target_language'
            ? params.transform?.target_language
            : params.transform?.audience
    if (!optionApplies(option, params)) {
      // 不适用的维度必须没给：给了就是混用两个互斥的子参数。
      if (given !== undefined) return false
      continue
    }
    if (given === undefined) return false
    if (option.values.length > 0 && !option.values.includes(given)) return false
  }
  return true
}
