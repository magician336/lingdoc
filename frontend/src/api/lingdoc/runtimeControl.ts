// 运行控制与运行观测客户端。
//
// 这组端点原先寄居在 api/lingdoc/mainlineChanges.ts 里，与 G3 的变更集 /
// 影响任务共用一个模块。独立化之后，「运行观测」这块知识有自己的落点：读它
// 的人不必先读懂变更集。
//
// 后端没有发布这组路由的 OpenAPI 路径，所以这里是它们唯一的机器可校验记录：
// runtimeControl.test.ts 固定了每条路径、每段编码与响应外壳。
import { get, post } from '@/utils/request'
import type { Result } from './workspace'

export interface RuntimeScope { kind: 'tenant' | 'project' | 'template_version' | 'ruleset_hash' | 'runtime_mode'; value: string }
export interface RuntimeControl { id: string; project_id: string; scope: RuntimeScope; version: number; status: 'PAUSED' | 'ACTIVE'; actor_id: string; reason: string; recovery_verified: boolean; snapshot_id?: string; export_id?: string; updated_at: string }
export interface RuntimeControlInput { expected_version: number; scope: RuntimeScope; action: 'pause' | 'resume'; reason: string; snapshot_id?: string; export_id?: string }
export interface RuntimeEvent { id: string; origin: 'runtime' | 'reported'; tenant_alias: string; project_alias: string; correlation_id: string; causation_id?: string; request_id?: string; task_id?: string; changeset_id?: string; impact_task_id?: string; snapshot_id?: string; export_id?: string; template_version?: string; ruleset_hash?: string; context_revision: number; target_revision: number; runtime_mode: string; resource_category: string; status: string; error_code?: string; duration_ms: number; created_at: string }
export interface RuntimeMetrics { event_count: number; counts: Record<string, number>; p95_ms: number | null; p99_ms: number | null; queue_depth: number; missing: string[] }
export interface RuntimeAlert { id: string; rule_id: string; event_id: string; severity: string; action: string; responsible_id: string; status: string }
export interface RuntimeAlertRule { signal: string; severity: 'P0' | 'P1' | 'P2' | 'P3'; action: 'notify' | 'retry' | 'pause' | 'block_export' | 'rollback' | 'human_confirm'; responsible_id: string; scope?: RuntimeScope }

const segment = (id: string) => encodeURIComponent(id)
const base = (id: string) => `/api/v1/lingdoc/projects/${segment(id)}`
const headers = (key: string) => ({ headers: { 'Idempotency-Key': key } })

export const listRuntimeControls = (id: string) => get<Result<RuntimeControl[]>>(`${base(id)}/runtime/controls`)
export const controlRuntime = (id: string, input: RuntimeControlInput, key: string) => post<Result<RuntimeControl>>(`${base(id)}/runtime/controls`, input, headers(key))
export const listRuntimeEvents = (id: string, correlation = '') => get<Result<RuntimeEvent[]>>(`${base(id)}/runtime/events${correlation ? `?correlation_id=${segment(correlation)}` : ''}`)
export const getRuntimeMetrics = (id: string) => get<Result<RuntimeMetrics>>(`${base(id)}/runtime/metrics`)
export const listRuntimeAlerts = (id: string) => get<Result<RuntimeAlert[]>>(`${base(id)}/runtime/alerts`)
export const saveRuntimeAlertRule = (id: string, input: RuntimeAlertRule, key: string) => post<Result<RuntimeAlertRule>>(`${base(id)}/runtime/alert-rules`, input, headers(key))
