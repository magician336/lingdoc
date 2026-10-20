import { get, post } from '@/utils/request'
import type { Result } from './workspace'
import type { ObjectVersionRef } from './mainlineChanges'

// The versioned route-roadmap editing session (#250).
//
// The client never states the required flag: the required route nodes are
// declared by the template rules, and a client that claims otherwise is
// refused by the server. The state of a node ('current' | 'pending_review' |
// 'stale' | 'missing' | 'retired') is server-decided too, so a screen can
// render the reason instead of re-deriving it.

export type RoadmapNodeStatus = 'planned' | 'pending_review' | 'ready' | 'blocked' | 'retired'
export type RoadmapNodeState = 'current' | 'pending_review' | 'stale' | 'missing' | 'retired'
export type RoadmapEditSessionStatus = 'open' | 'committed' | 'cancelled'
export type RoadmapNodeProposalKind = 'add' | 'update' | 'delete'
export type RoadmapEdgeProposalKind = 'add' | 'update' | 'delete'

export interface RouteEdge { id: string; project_id: string; version: number; source_id: string; target_id: string; relation: string; status: string; context_revision: number; actor_id: string; reason: string; updated_at: string }
export interface RoadmapNodeView { id: string; project_id: string; version: number; name: string; inputs: ObjectVersionRef[]; targets: ObjectVersionRef[]; required: boolean; status: RoadmapNodeStatus; state: RoadmapNodeState; state_reason?: string; context_revision: number; actor_id: string; reason: string; updated_at: string; frozen: boolean }
export interface RoadmapBaseline { version: number; context_revision: number; nodes: RoadmapNodeView[]; edges: RouteEdge[] }
export interface RoadmapNodeProposal { node_id: string; kind: RoadmapNodeProposalKind; name: string; status: string; inputs: ObjectVersionRef[]; targets: ObjectVersionRef[]; reason: string; temporary_version: number }
export interface RoadmapEdgeProposal { edge_id: string; kind: RoadmapEdgeProposalKind; source_id: string; target_id: string; relation: string; reason: string; temporary_version: number }
export interface RoadmapEditSession { id: string; project_id: string; status: RoadmapEditSessionStatus; baseline: RoadmapBaseline; node_proposals: RoadmapNodeProposal[]; edge_proposals: RoadmapEdgeProposal[]; result?: RoadmapCommitResult; actor_id: string; reason: string; created_at: string; updated_at: string }
export interface RoadmapCommitResult { session_id: string; baseline_version: number; committed_version: number; applied_nodes: RoadmapNodeView[]; applied_edges: RouteEdge[]; change_set_id?: string; semantic: boolean }
export interface RoadmapVersionEntry { version: number; name: string; status: string; required: boolean; state: RoadmapNodeState; state_reason?: string; context_revision: number; actor_id: string; reason: string; inputs: ObjectVersionRef[]; targets: ObjectVersionRef[]; updated_at: string }
export interface RoadmapFieldChange { field: string; from: string; to: string }
export interface RoadmapComparison { kind: string; id: string; from: number; to: number; changes: RoadmapFieldChange[] }
export interface RoadmapEditBeginInput { expected_context_revision: number; reason: string }
export interface RoadmapNodeProposalInput { node_id: string; kind: RoadmapNodeProposalKind; name: string; status: string; inputs?: ObjectVersionRef[]; targets?: ObjectVersionRef[]; reason: string }
export interface RoadmapEdgeProposalInput { edge_id: string; kind: RoadmapEdgeProposalKind; source_id: string; target_id: string; relation: string; reason: string }
export interface RoadmapCommitInput { expected_baseline_version: number; reason: string }
export interface RoadmapCancelInput { expected_baseline_version: number; reason: string }

const segment = (id: string) => encodeURIComponent(id)
const base = (id: string) => `/api/v1/lingdoc/projects/${segment(id)}`
const headers = (key: string) => ({ headers: { 'Idempotency-Key': key } })

export const readRoadmap = (id: string) => get<Result<RoadmapBaseline>>(`${base(id)}/roadmap`)
export const listRouteEdges = (id: string) => get<Result<RouteEdge[]>>(`${base(id)}/roadmap/edges`)
export const listRouteEdgeVersions = (id: string, edgeId: string) => get<Result<RouteEdge[]>>(`${base(id)}/roadmap/edges/${segment(edgeId)}/versions`)
export const listRoadmapNodeVersions = (id: string, nodeId: string) => get<Result<RoadmapVersionEntry[]>>(`${base(id)}/roadmap/nodes/${segment(nodeId)}/versions`)
export const compareRoadmapNodeVersions = (id: string, nodeId: string, from: number, to: number) => get<Result<RoadmapComparison>>(`${base(id)}/roadmap/nodes/${segment(nodeId)}/compare?from=${from}&to=${to}`)
export const listRoadmapEditSessions = (id: string) => get<Result<RoadmapEditSession[]>>(`${base(id)}/roadmap/edits`)
export const getRoadmapEditSession = (id: string, sessionId: string) => get<Result<RoadmapEditSession>>(`${base(id)}/roadmap/edits/${segment(sessionId)}`)
export const beginRoadmapEdit = (id: string, input: RoadmapEditBeginInput, key: string) => post<Result<RoadmapEditSession>>(`${base(id)}/roadmap/edits`, input, headers(key))
export const proposeRoadmapNode = (id: string, sessionId: string, input: RoadmapNodeProposalInput, key: string) => post<Result<RoadmapEditSession>>(`${base(id)}/roadmap/edits/${segment(sessionId)}/nodes`, input, headers(key))
export const proposeRoadmapEdge = (id: string, sessionId: string, input: RoadmapEdgeProposalInput, key: string) => post<Result<RoadmapEditSession>>(`${base(id)}/roadmap/edits/${segment(sessionId)}/edges`, input, headers(key))
export const commitRoadmapEdit = (id: string, sessionId: string, input: RoadmapCommitInput, key: string) => post<Result<RoadmapCommitResult>>(`${base(id)}/roadmap/edits/${segment(sessionId)}/commit`, input, headers(key))
export const cancelRoadmapEdit = (id: string, sessionId: string, input: RoadmapCancelInput, key: string) => post<Result<RoadmapEditSession>>(`${base(id)}/roadmap/edits/${segment(sessionId)}/cancel`, input, headers(key))
