import { ArrowUp, Eye, Plus, Send, Sparkles, X } from 'lucide-react'
import { useState } from 'react'
import { comment, sourceVersions } from '../data/scenario'
import { InlineNotice } from './ReviewPrimitives'

type ContextId = 'comment' | 'source-v2'
type Message = { id: number; role: 'user' | 'assistant'; text: string; demo?: boolean }

const contextItems = {
  comment: {
    title: '导师意见对应旧摘录',
    meta: 'D01 · 场景修订 12',
    text: comment.excerpt,
  },
  'source-v2': {
    title: '社区联系记录 v2 · P1',
    meta: sourceVersions.v2.versionId,
    text: sourceVersions.v2.text,
  },
}

export function ContextAssistant({ onCompare }: { onCompare: () => void }) {
  const [contexts, setContexts] = useState<ContextId[]>([])
  const [input, setInput] = useState('')
  const [previewOpen, setPreviewOpen] = useState(false)
  const [waiting, setWaiting] = useState(false)
  const [messages, setMessages] = useState<Message[]>([
    { id: 1, role: 'assistant', text: '我可以协助比较局部表述。只有下方列出的内容会进入演示请求；未授权正文或资料不会被读取。', demo: true },
  ])

  const toggleContext = (id: ContextId) => {
    setContexts((current) => current.includes(id) ? current.filter((item) => item !== id) : [...current, id])
  }

  const send = () => {
    const text = input.trim()
    if (!text || waiting) return
    setMessages((current) => [...current, { id: Date.now(), role: 'user', text }])
    setInput('')
    setWaiting(true)
    window.setTimeout(() => {
      setMessages((current) => [...current, {
        id: Date.now() + 1,
        role: 'assistant',
        text: contexts.length
          ? '基于你显式提供的内容，可以把“概括全市”收窄为“描述所选社区的使用障碍”，并补充社区选择标准。建议仍需你与原稿对照后决定。'
          : '当前没有提供正文或资料。我可以讨论一般写法，但不会据此判断你的原稿。若需要局部建议，请先添加上下文。',
        demo: true,
      }])
      setWaiting(false)
    }, 450)
  }

  return (
    <div className="assistant-panel">
      <div className="assistant-scroll">
        <InlineNotice>本地演示回复 · 未连接模型。下面展示的是实际会随请求发送的上下文，不是无关标签。</InlineNotice>
        <section className="context-section">
          <div className="section-heading compact"><h3>本次上下文</h3><span>{contexts.length} 项</span></div>
          {contexts.length === 0 && <p className="muted-copy">未提供正文或资料。</p>}
          {contexts.map((id) => (
            <div className="context-chip" key={id}>
              <span><strong>{contextItems[id].title}</strong><small>{contextItems[id].meta}</small></span>
              <button className="icon-button small" onClick={() => toggleContext(id)} aria-label={`移除${contextItems[id].title}`}><X size={14} /></button>
            </div>
          ))}
          <div className="context-add-row">
            {(Object.keys(contextItems) as ContextId[]).filter((id) => !contexts.includes(id)).map((id) => (
              <button className="text-button" key={id} onClick={() => toggleContext(id)}><Plus size={14} />{id === 'comment' ? '旧摘录' : '联系记录 v2'}</button>
            ))}
          </div>
          <button className="button secondary full" onClick={() => setPreviewOpen((value) => !value)}>
            <Eye size={15} />{previewOpen ? '收起实际发送内容' : '展开实际发送内容'}
          </button>
          {previewOpen && (
            <div className="request-preview">
              {contexts.length ? contexts.map((id) => <p key={id}><strong>{contextItems[id].title}</strong>：{contextItems[id].text}</p>) : <p>无正文、资料或卡片附件。</p>}
            </div>
          )}
        </section>

        <section className="message-list" aria-label="演示会话">
          {messages.map((message) => (
            <div className={`message message-${message.role}`} key={message.id}>
              <p>{message.text}</p>
              {message.demo && <span>演示</span>}
            </div>
          ))}
          {waiting && <div className="message message-assistant"><p>正在生成本地演示回复…</p></div>}
        </section>
        {messages.some((message) => message.role === 'assistant' && message.text.includes('收窄')) && (
          <button className="suggestion-entry" onClick={onCompare}><Sparkles size={16} />查看建议与原稿对照<ArrowUp size={15} /></button>
        )}
      </div>
      <div className="assistant-composer">
        <label htmlFor="assistant-input">向助手说明需要帮助的局部问题</label>
        <textarea id="assistant-input" value={input} onChange={(event) => setInput(event.target.value)} rows={3} placeholder="例如：如何收窄这段的概括范围？" />
        <div className="composer-footer"><span>发送不会自动采用建议</span><button className="icon-button send" onClick={send} disabled={!input.trim() || waiting} aria-label="发送演示消息"><Send size={17} /></button></div>
      </div>
    </div>
  )
}
