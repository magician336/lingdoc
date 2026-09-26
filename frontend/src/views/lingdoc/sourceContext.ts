import type { Source, SourceContext, SourceContextSegment } from '@/api/lingdoc/workspace'

// 「查看原文」那条内联面板的判定与文案，与渲染分开（同 accessStatus.ts / deliveryState.ts）。
//
// 这一层只回答两件事：窗口里的每一段该怎么措辞，以及展开不了时该说什么。
// 它**不**判断这段文字是不是原文——那是服务端 segment.verbatim 的结论，界面照抄。
// 把「没有 verbatim 就当原文展示」写进界面，等于让渲染层去猜一个它没有依据的判据。

/**
 * `Source.status` 三态的中文说法。
 *
 * 键写成 `string` 而不是字面量联合，与 accessStatus.ts 的 KNOWN_ACTIONS 同一条理由：
 * 服务端将来多一个取值时，界面必须把它**原样**报出来，不能因为自己的类型里没有它
 * 就当成不存在。
 */
const STATUS_LABELS: Record<string, string> = {
  available: '可以指回原文',
  stale: '坐标已不可信',
  unavailable: '此刻取不回来',
}

export function sourceStatusLabel(status: Source['status'] | string): string {
  return STATUS_LABELS[status] ?? status
}

/** 展开不了时的抬头。它是一条说明，不是错误——引用本身可能完全正常。 */
export const CONTEXT_UNAVAILABLE_NOTICE = '这一条引用暂时展不开上下文。'

/** 面板里逐段的说明文字。逐字对上的那一段没有说明——它不需要解释。 */
const VERBATIM_NOTE = ''
const MISSING_TEXT_NOTE = '这一段与原文坐标对不上，所以只给出位置。'
const UNPROVEN_TEXT_NOTE = '这段正文取自分块记录，无法与原文逐字核对。'

export interface ContextLine {
  id: string
  label: string
  text: string
  /** 逐字等于原文坐标上的那一段。界面据此决定要不要标「原文」二字。 */
  verbatim: boolean
  /** 非逐字时的说明。逐字时是空串。 */
  note: string
}

/**
 * 把窗口摊成可渲染的行，按 `chunk_index` 升序。
 *
 * 排序是幂等的：服务端已经按「前段在前、后段在后、各自索引递增」给过一遍，这里重排一次
 * 是为了让界面不依赖那个顺序。窗口的号码本来就把缺口说清了（中间那段可能被删过），
 * 靠数组下标去暗示顺序才是脆的。
 */
export function contextLines(context: SourceContext | null | undefined): ContextLine[] {
  const segments: SourceContextSegment[] = [...(context?.segments ?? [])]
  segments.sort((left, right) => left.chunk_index - right.chunk_index)
  return segments.map((segment) => ({
    id: segment.source_id,
    label: `第 ${segment.chunk_index} 段（${segment.relation === 'before' ? '前' : '后'}）`,
    text: segment.text,
    verbatim: segment.verbatim,
    note: segment.verbatim ? VERBATIM_NOTE : segment.text ? UNPROVEN_TEXT_NOTE : MISSING_TEXT_NOTE,
  }))
}

/**
 * 展开不了时的那句说明。
 *
 * 两种展不开要分开说：派生块（摘要、图片说明……）本来就没有指向原文的坐标，而失效的引用
 * 是**本来能展开、现在不能了**。混成一句会把「这段是摘要」说成「这条引用坏了」，
 * 反过来又会把一次失效说成「它本来就没有原文」。
 */
export function contextNotice(context: SourceContext | null | undefined): string {
  if (!context || context.context_available) return ''
  return context.source.status === 'available'
    ? '这一条引用的位置落在派生内容上（摘要、图片说明等），没有指向原文的坐标，所以展开不了。'
    : `这一条引用此刻${sourceStatusLabel(context.source.status)}。先重新定位，再看上下文。`
}

/** 窗口大小的一句说明，放在面板抬头上。 */
export function windowLabel(context: SourceContext | null | undefined): string {
  if (!context || !context.context_available) return ''
  const { before, after } = context.window
  return `引用处前后各 ${before} 段`
}

/**
 * 引用失效时替代 `quoted_text` 的那句话。
 *
 * 逐字对不上时服务端不给引文（`quoted_text` 是空串），而空白读起来像界面坏了。
 */
export function quotedTextOf(source: Source | null | undefined): string {
  if (!source) return ''
  return source.quoted_text || '当前版本无法取回原文片段。'
}
