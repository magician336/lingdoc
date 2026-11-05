import { FileDown, Save } from 'lucide-react'
import { useEffect, useRef } from 'react'
import type { SaveState, Section } from '../data/scenario'
import { saveStateCopy } from '../data/scenario'
import { useLive, type HostWindow } from '../lib/live-context'
export function NativeHost({ saveState, onSaveStatus, onExport }: { section?: Section; saveState: SaveState; onSaveStatus: () => void; onExport: () => void }) {
  const frame = useRef<HTMLIFrameElement>(null)
  const { attach, save } = useLive()
  useEffect(() => { if (frame.current?.contentWindow) return attach(frame.current.contentWindow as HostWindow) }, [attach])
  return <section className="native-stage live-native-stage" aria-label="WordCraft 原生宿主区域">
    <div className="native-stage-bar"><div><span className="native-host-dot" /><strong>WordCraft</strong><span className="demo-tag">真实文稿</span></div><div className="native-stage-actions"><button className="text-button" onClick={() => void save()}><Save size={15} />保存</button><button className="text-button" onClick={onExport}><FileDown size={15} />导出副本</button></div></div>
    <button className={`save-strip save-${saveState}`} onClick={onSaveStatus}><span>{saveStateCopy[saveState].label}</span><span>查看保存状态</span></button>
    <iframe ref={frame} src="/native.html" title="原生 WordCraft 写作环境" className="live-native-frame" />
  </section>
}
