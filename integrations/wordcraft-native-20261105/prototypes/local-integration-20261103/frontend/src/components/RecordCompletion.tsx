import { useState } from 'react'
import { useLive } from '../lib/live-context'
import { InlineNotice } from './ReviewPrimitives'
import { decisionLabels } from '../data/scenario'
export function RecordCompletion({ id }: { id: string }) {
  const live = useLive()
  const record = live.data!.records[id]
  const [reason, setReason] = useState('')
  const history = !!record.history?.length && <details className="comment-edit"><summary>查看处理记录（{record.history.length}）</summary><ol>{record.history.map((entry, index) => <li key={index}><small>{entry.at ? new Date(entry.at).toLocaleString() : ''}</small><p>{entry.type === 'decide' && entry.decision ? decisionLabels[entry.decision] : entry.type === 'complete' ? `确认处理完成 · 保存版本 v${entry.resultVersion?.diskVersion}` : entry.type === 'anchor' ? '人工绑定正文选区' : entry.type === 'comment' ? '录入人工意见' : '登记资料更新'}</p>{(entry.reason || entry.text) && <p>{entry.reason || entry.text}</p>}</li>)}</ol></details>
  if (record.progress === 'completed') return <><InlineNotice>{record.resultVersion ? `已登记完成，关联正文保存版本 v${record.resultVersion.diskVersion}。${record.completionReason || ''}` : `已确认无需调整。${record.reason}`}</InlineNotice>{history}</>
  if (!record.decision || record.decision === 'defer') return null
  if (id === 'R3' || record.decision === 'update_plan' || record.decision === 'adjust_both') return <InlineNotice tone="warning">已记录你的处理方向；图源或研究方案版本尚未接通，暂不能登记完成。</InlineNotice>
  return <><div className="record-completion"><label>实际处理说明<textarea rows={2} value={reason} onChange={event => setReason(event.target.value)} placeholder="说明你修改了什么，保存后登记结果" /></label><button className="button secondary full" disabled={live.busy || live.native?.phase !== 'saved' || !reason.trim()} onClick={() => void live.complete(id, reason.trim())}>关联已保存版本并登记完成</button>{live.native?.phase !== 'saved' && <p className="muted-copy">请先修改正文并保存。</p>}</div>{history}</>
}
