import type { Asset } from '@/api/lingdoc/workspace'

// 来源区那两条「说清发生了什么」的提示，与渲染分开（同 accessStatus.ts）。
//
// 两条都是**缺了就会误导**的提示：
//   - 检索被拒时后端整批 422（一条都不处理），而界面此前只显示 error.message
//     ——「请求中存在未获授权的资料，未开始处理。」说了有资料不行，没说**哪一份、为什么**；
//   - 绑定成功但资料还没就绪时，它 201 回来了、随后从项目资料列表里消失，没有任何解释。

/** 被拒的资料。契约的 `denied[]` 每项**恰好**是这两个键。 */
export interface DeniedSource {
  assetId: string
  reason: string
}

export const DENIED_NOTICE = '有资料未获授权，这次检索没有开始。'

/**
 * 三种拒绝原因各自的对策。
 *
 * 键写成 `string` 而非字面量联合，与 accessStatus.ts 的 KNOWN_ACTIONS 同一条理由：
 * 服务端多一个取值时界面要原样报出来，不能因为自己的类型里没有它就静默丢掉一条。
 */
const DENY_REASONS: Record<string, { label: string; next: string }> = {
  not_authorized: {
    label: '未获授权',
    next: '请资料的所有者把它重新授权给这个项目，然后重新检索。',
  },
  not_ready: {
    label: '尚未就绪',
    next: '这一份还在解析。等它变成「已就绪」再重新检索。',
  },
  not_found: {
    label: '找不到，或不属于本项目',
    next: '它可能已被删除，或没有绑到这个项目上。请重新绑定。',
  },
}

/**
 * 从一次失败的请求里取出被拒明细。
 *
 * 路径是 `error.details.denied`——`details` 在 `error` **里面**（后端 sendErrorDetails
 * 就是这么发的），而 utils/request.ts 把整个响应体展开到了 rejection 对象上，所以从那里
 * 往下摸得到。取不到就返回空数组：这条提示是**补充**，形状对不上时不该把一次网络失败
 * 变成一次渲染崩溃。
 */
export function deniedSourcesOf(error: unknown): DeniedSource[] {
  const details = (error as { error?: { details?: { denied?: unknown } } })?.error?.details
  if (!Array.isArray(details?.denied)) return []
  return details.denied.flatMap((entry) => {
    const value = entry as { asset_id?: unknown; reason?: unknown }
    if (typeof value?.asset_id !== 'string' || !value.asset_id) return []
    return [{ assetId: value.asset_id, reason: typeof value.reason === 'string' ? value.reason : '' }]
  })
}

/**
 * 一条拒绝原因怎么读、下一步做什么。
 *
 * 认不出的原因**原样报出**（`label` 就是那个码），对策退回一句不猜的话：编一段中文解释
 * 等于替服务端猜它的语义，而用户会照着猜出来的对策去操作。
 *
 * 注意这里拿不到资料标题：`listAssets` 只回允许集合，被拒的资料在列表里没有痕迹
 * （F07 的另一刀，见 T09 文档的已知边界）。所以界面显示的是 ID——它不是给人读的，
 * 但比编一个标题诚实。
 */
export function denyReasonOf(reason: string): { label: string; next: string } {
  return DENY_REASONS[reason] ?? {
    label: reason || '未说明原因',
    next: '请检查这一份资料的授权与解析状态。',
  }
}

/** `Asset.processing_state` 非 ready 时的中文说法。ready 不在表里——那时没有提示可给。 */
const PENDING_STATES: Record<string, string> = {
  pending: '还没开始解析',
  processing: '正在解析',
  failed: '解析失败',
  replaced: '已被新版本取代',
}

/** 资料在界面上的名字。标题可能为空（手工资料或底座没写），落回 ID。 */
export function assetLabel(asset: Asset): string {
  return asset.title || asset.knowledge_id
}

/**
 * 绑定成功之后的提示。就绪时不返回内容。
 *
 * 必须说清「它为什么不在上面的列表里」：`listAssets` 只回允许集合，非 ready 的资料
 * 绑得上、却立刻从列表消失。不说这一句，用户看到的是「绑定成功了但什么都没发生，
 * 再绑一次还是这样」——一个看起来像丢数据的成功。
 */
export function bindingNotice(asset: Asset | null | undefined): string {
  if (!asset || asset.processing_state === 'ready') return ''
  const state = PENDING_STATES[asset.processing_state] ?? asset.processing_state
  return `已绑定《${assetLabel(asset)}》，但它现在${state}——项目资料列表只显示已就绪的资料，所以它暂时不会出现在上面。`
}
