import { createContext, useContext } from 'react'
import type { NativeSaveState } from '../../../native-adapter.js'
import type { Decision, TaskRecord } from '../data/scenario'
export type NativeAnchor = { hostSessionId?: string; documentId: number; revision: number; selection: { text?: string; [key: string]: unknown }; excerpt: string }
export type LiveRecord = TaskRecord & { anchor?: NativeAnchor | null; resultVersion?: { diskVersion: number; docSha256: string } | null; completionReason?: string; history?: Array<{ action?: string; type?: string; at?: string; reason?: string; decision?: Decision; text?: string; resultVersion?: { diskVersion: number } }>; version?: number }
export type SourceVersion = { text: string; title: string; id?: string; versionId?: string; paragraphId?: string }
export type LiveState = {
  revision: number; baselineId: string; projectId: string; sourceLatest: number; changeApplied: boolean
  sourceVersions: { v1: SourceVersion; v2: SourceVersion }; records: Record<string, LiveRecord>
  comment: { id?: string; text: string; excerpt: string; author: string }; backend?: Record<string, unknown>
}
type Host = { engine: (request: Record<string, unknown>) => Promise<Record<string, unknown>>; api: (endpoint: string, request: unknown) => Promise<{ data: string; sha256: string }> }
export type HostWindow = Window & { wordcraftHost?: Host }
export type Action = { type: string; id?: string; decision?: Decision; reason?: string; text?: string; author?: string; anchor?: NativeAnchor; savedVersion?: { diskVersion: number; docSha256: string } }
export type Request = Action & { key: string; expectedRevision: number }
type Context = {
  data: LiveState | null; error: string; busy: boolean; pending: boolean; notice: string
  nativeSessionId: string; native: NativeSaveState | null; attach: (frame: HostWindow) => () => void
  refresh: () => Promise<void>; act: (action: Action) => Promise<boolean>; retry: () => Promise<boolean>
  save: () => Promise<NativeSaveState | undefined>; download: () => Promise<void>; bindAnchor: () => Promise<void>
  engine: (input: Record<string, unknown>) => Promise<Record<string, unknown>>
  workflow: (input?: Record<string, unknown>) => Promise<unknown>
  complete: (id: string, reason: string) => Promise<void>; notify: (message: string) => void
}
export const LiveContext = createContext<Context | null>(null)
export const useLive = () => { const value = useContext(LiveContext); if (!value) throw Error('Missing integration provider'); return value }
