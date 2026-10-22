import { post } from '@/utils/request'
import type { Project, Result } from './workspace'

/**
 * 归档：项目生命周期的终态。
 *
 * 归档与丢弃是两回事：丢弃是 draft 项目的软删除标记（discarded_at），归档是
 * active 项目的 status = archived。只有 active 项目能归档，归档之后项目不再
 * 接受任何变更。
 *
 * 请求带着调用方读到的项目版本，陈旧的决定服务端拒绝；幂等键让重试只有一次
 * 效果——重试不会产生第二条归档审计。
 */
const path = (id: string) => `/api/v1/lingdoc/projects/${encodeURIComponent(id)}`

/** 归档项目：Owner 专属，服务端在 Owner 不可用时冻结该动作。 */
export const archiveProject = (id: string, expectedProjectVersion: number, key: string) =>
  post<Result<Project>>(`${path(id)}/archive`, { expected_project_version: expectedProjectVersion }, { headers: { 'Idempotency-Key': key } })
