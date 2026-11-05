import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import type { NativeAdapter, NativeSaveState } from '../../../native-adapter.js'
import { LiveContext, type LiveState, type Request, type Action, type NativeAnchor, type HostWindow } from './live-context'
export function LiveProvider({ children }: { children: ReactNode }) {
  const [data, setData] = useState<LiveState | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [pending, setPending] = useState(false)
  const [notice, setNotice] = useState('')
  const [native, setNative] = useState<NativeSaveState | null>(null)
  const config = useRef<Promise<{ token: string }> | null>(null)
  const latest = useRef<LiveState | null>(null)
  const request = useRef<Request | null>(null)
  const executing = useRef(false)
  const adapter = useRef<NativeAdapter | null>(null)
  const host = useRef<HostWindow | null>(null)
  const boundEngineHost = useRef<HostWindow['wordcraftHost']>(undefined)
  const nativeSessionId = useRef(crypto.randomUUID()).current
  const notify = useCallback((message: string) => setNotice(message), [])
  const getConfig = useCallback(() => {
    if (!config.current) config.current = fetch('/integration/config').then(async response => {
      if (!response.ok) throw Error('无法读取本地集成配置')
      return response.json() as Promise<{ token: string }>
    }).catch(error => { config.current = null; throw error })
    return config.current
  }, [])
  const load = useCallback(async () => {
    const settings = await getConfig()
    const response = await fetch('/integration/state', { headers: { 'X-Integration-Token': settings.token } })
    const value = await response.json()
    if (!response.ok) throw Error(value.message || value.error || '项目读取失败')
    if (!latest.current || value.revision >= latest.current.revision) { latest.current = value; setData(value) }
    setError('')
  }, [getConfig])
  const refresh = useCallback(async () => { try { await load() } catch (error) { setError(error instanceof Error ? error.message : '项目读取失败') } }, [load])
  useEffect(() => { void refresh() }, [refresh])
  const send = useCallback(async (body: Request): Promise<boolean> => {
    if (executing.current) return false
    executing.current = true; setBusy(true); request.current = body; setPending(true)
    try {
      const settings = await getConfig()
      const response = await fetch('/integration/actions', { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-Integration-Token': settings.token }, body: JSON.stringify(body) })
      const value = await response.json()
      if (!response.ok) {
        if (response.status === 409) { request.current = null; setPending(false); await load(); throw Error(`${value.message || value.error || '项目记录已变化'}；已读回最新记录，请核对后重试。`) }
        if (response.status >= 400 && response.status < 500) { request.current = null; setPending(false) }
        throw Error(value.message || value.error || `写入失败 (${response.status})`)
      }
      latest.current = value; setData(value); setError(''); request.current = null; setPending(false)
      notify('已保存到本地项目。刷新或重新打开后可以继续查看。'); return true
    } catch (error) { setError(error instanceof Error ? error.message : '结果未知，请沿用原请求重试'); return false }
    finally { executing.current = false; setBusy(false) }
  }, [getConfig, load, notify])
  const act = useCallback(async (action: Action) => {
    if (!latest.current) return false
    if (request.current) { notify('上一次写入结果尚未确认，请先重试原请求。'); return false }
    return send({ ...action, key: crypto.randomUUID(), expectedRevision: latest.current.revision })
  }, [notify, send])
  const retry = useCallback(() => request.current ? send(request.current) : Promise.resolve(false), [send])
  const attach = useCallback((frame: HostWindow) => {
    let active = true; let current: NativeAdapter | null = null; let loading = false
    host.current = frame
    const moduleURL = '/integration/native-adapter.js'
    const poll = async () => {
      if (!active || loading || !frame.wordcraftHost) return
      loading = true
      try {
        if (!current) {
          const module = await import(/* @vite-ignore */ moduleURL) as { createNativeAdapter: (options: { hostWindow: Window; onState: (state: NativeSaveState) => void }) => NativeAdapter }
          if (!active) return
          current = module.createNativeAdapter({ hostWindow: frame, onState: value => { if (active) setNative(value) } }); adapter.current = current
          boundEngineHost.current = frame.wordcraftHost
        }
        await current.refresh()
      } catch (error) { if (active) setError(error instanceof Error ? error.message : '原生状态读取失败') }
      finally { loading = false }
    }
    void poll(); const timer = window.setInterval(() => void poll(), 1200)
    return () => { active = false; window.clearInterval(timer); current?.dispose(); if (adapter.current === current) adapter.current = null }
  }, [])
  const save = useCallback(async () => {
    try { if (!adapter.current) throw Error('WordCraft 尚未就绪，请稍候'); await adapter.current.save(); await adapter.current.refresh(); notify(adapter.current.getState().phase === 'saved' ? '当前文稿已保存并核对读回。' : '此次保存已结束，当前仍有新编辑需要保存。'); return adapter.current.getState() }
    catch (error) { setError(error instanceof Error ? error.message : '保存失败，正文已保留') }
  }, [notify])
  const download = useCallback(async () => {
    try {
      if (!adapter.current || !host.current?.wordcraftHost) throw Error('WordCraft 尚未就绪')
      await adapter.current.save(); const state = await adapter.current.refresh()
      if (state.phase !== 'saved' || !state.saved) throw Error('当前文稿仍有未保存内容，请完成保存再导出')
      const version = await host.current.wordcraftHost.api('version', { diskVersion: state.saved.diskVersion })
      const bytes = Uint8Array.from(atob(version.data), c => c.charCodeAt(0))
      const digest = Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', bytes))).map(x => x.toString(16).padStart(2, '0')).join('')
      if (digest !== state.saved.sha256 || version.sha256 !== digest) throw Error('导出副本摘要校验失败')
      const url = URL.createObjectURL(new Blob([bytes], { type: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document' }))
      const link = document.createElement('a'); link.href = url; link.download = `社区老年人数字公共服务研究-v${state.saved.goDiskVersion}.docx`; link.click(); window.setTimeout(() => URL.revokeObjectURL(url), 30000)
      notify('已下载真实保存版本的 DOCX 副本；任务完成情况仍按各条决定记录。')
    } catch (error) { setError(error instanceof Error ? error.message : '导出失败') }
  }, [notify])
  const bindAnchor = useCallback(async () => {
    try {
      if (!host.current?.wordcraftHost) throw Error('原生宿主尚未就绪')
      const value = await host.current.wordcraftHost.engine({ op: 'selection' }) as { selection: NativeAnchor['selection']; state: { documentId: number; revision: number } }
      if (!value.selection?.text?.trim()) throw Error('请先在原生正文中框选意见对应文字，再点“绑定当前选区”。')
      await act({ type: 'anchor', id: 'C01', anchor: { hostSessionId: nativeSessionId, ...value.state, selection: value.selection, excerpt: value.selection.text } })
    } catch (error) { setError(error instanceof Error ? error.message : '绑定失败') }
  }, [act, nativeSessionId])
  const complete = useCallback(async (id: string, reason: string) => {
    try {
      const state = await adapter.current?.refresh()
      if (state?.phase !== 'saved' || !state.saved) throw Error('请先修改原生正文并保存，再登记处理完成。')
      await act({ type: 'complete', id, reason, savedVersion: { diskVersion: state.saved.goDiskVersion, docSha256: state.saved.sha256 } })
    } catch (error) { setError(error instanceof Error ? error.message : '登记失败') }
  }, [act])
  const engine = useCallback(async (input: Record<string, unknown>) => {
    if (!host.current?.wordcraftHost) throw Error('WordCraft 尚未就绪')
    if (!boundEngineHost.current || boundEngineHost.current !== host.current.wordcraftHost) throw Error('原生宿主已重新打开，请刷新页面后重新选择正文。旧候选不能跨宿主执行。')
    return host.current.wordcraftHost.engine(input)
  }, [])
  const workflow = useCallback(async (input?: Record<string, unknown>) => {
    const settings = await getConfig()
    const response = await fetch('/integration/workflow', { method: input ? 'POST' : 'GET', headers: { 'Content-Type': 'application/json', 'X-Integration-Token': settings.token }, body: input ? JSON.stringify(input) : undefined })
    const value = await response.json()
    if (!response.ok) throw Error(value.error || '修改任务请求失败')
    return value
  }, [getConfig])
  return <LiveContext.Provider value={{ data, error, busy, pending, notice, nativeSessionId, native, attach, refresh, act, retry, save, download, bindAnchor, complete, notify, engine, workflow }}>{children}</LiveContext.Provider>
}
