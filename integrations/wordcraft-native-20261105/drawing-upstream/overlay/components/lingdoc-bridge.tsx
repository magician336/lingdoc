"use client"

// Modified for LingDoc. The parent owns authoritative drawing versions.
import { useEffect, useRef } from "react"
import { useChatEngine } from "@/components/chat/chat-engine"
import { useDiagram } from "@/contexts/diagram-context"
import { sanitizeMessages } from "@/lib/session-storage"
import { modelOfPage, parseXml } from "@/lib/diagram-pages"
import { hasCells } from "@/packages/mcp-server/src/pages.ts"
import { centerEmptyPage } from "@/lib/drawio/editor-bridge"
import { useUiStore } from "@/stores/ui-store"

const CHANNEL = "lingdoc-drawing"
const MAX_XML = 1024 * 1024
const MAX_MESSAGES = 1024 * 1024
const bytes = (value: string) => new TextEncoder().encode(value).byteLength
const FEATURE_PROMPTS = {
    paper: "请根据我上传或粘贴在聊天中的实际论文及补充说明，绘制可编辑的研究概览图，梳理研究问题、方法、实验与结论。若尚未提供论文内容，请先提醒我使用聊天区的＋上传论文或粘贴论文正文，不使用内置示例或编造论文内容。",
    animated: "请根据我提供的内容和当前图稿，绘制可编辑的动态连接线示意图，用 draw.io 的流动连接线展示过程或数据方向。保留相关的已有元素，缺少必要信息时先向我确认。",
    aws: "请根据我提供的系统需求和当前图稿，绘制可编辑的 AWS 架构图，标注服务、数据流、边界及必要的安全关系。不假设我尚未提供的部署需求，信息不足时先向我确认。",
    flowchart: "请根据我描述的流程和当前图稿，绘制可编辑的流程图，清楚区分开始、结束、处理步骤和判断分支。保留我的业务含义，缺少必要步骤时先向我确认。",
    creative: "请根据我提供的主题、用途和风格要求，使用可编辑的图形与连接线完成创意绘画。保留当前图稿中相关的已有内容，尚未给出主题时先向我询问。",
} as const

function isDefinitelyEmptyDiagram(xml: string): boolean {
    try {
        const doc = parseXml(xml)
        if (!doc || doc.doctype) return false
        const file = doc.documentElement
        let models: Array<Element | null>
        if (file.nodeName === "mxGraphModel") models = [file]
        else if (file.nodeName === "mxfile") {
            const pages = Array.from(file.children)
            if (!pages.length || pages.some(page => page.nodeName !== "diagram")) return false
            models = pages.map(modelOfPage)
        } else return false
        return models.every(model => {
            if (!model || model.nodeName !== "mxGraphModel" ||
                model.hasAttribute("backgroundImage") || model.hasAttribute("background") ||
                model.children.length !== 1 || model.children[0].nodeName !== "root" ||
                hasCells(new XMLSerializer().serializeToString(model))) return false
            const root = model.children[0]
            const cells = Array.from(root.children)
            // Only draw.io's two structural root cells prove an empty page.
            // Missing roots, unknown elements, labels and custom data are saved.
            return !root.textContent?.trim() && cells.length === 2 &&
                cells.every(cell => cell.nodeName === "mxCell" && !cell.children.length &&
                    Array.from(cell.attributes).every(attribute => ["id", "parent"].includes(attribute.name))) &&
                cells.some(cell => cell.getAttribute("id") === "0" && !cell.hasAttribute("parent")) &&
                cells.some(cell => cell.getAttribute("id") === "1" && cell.getAttribute("parent") === "0")
        })
    } catch { return false }
}

export function LingDocBridge() {
    const engine = useChatEngine()
    const diagram = useDiagram()
    const current = useRef({ engine, diagram })
    const featurePrefix = useRef("")
    current.current = { engine, diagram }
    const ready = engine.isRestored && diagram.isDrawioReady

    useEffect(() => {
        if (!ready || window.parent === window) return
        const abort = new AbortController()
        let dispose = () => {}
        let active = true
        void (async () => {
            const response = await fetch("/api/lingdoc-bridge-config", {
                cache: "no-store", signal: abort.signal,
            })
            if (!response.ok) throw new Error("Drawing bridge configuration unavailable")
            const { parentOrigins } = await response.json()
            if (!Array.isArray(parentOrigins) || parentOrigins.length !== 2 ||
                !parentOrigins.every((origin: string) => {
                    const parsed = new URL(origin)
                    return parsed.origin === origin && parsed.protocol === "http:" &&
                        ["127.0.0.1", "localhost"].includes(parsed.hostname)
                })) throw new Error("Invalid drawing parent origin")
            if (!active) return
            let parentOrigin: string | null = null
            const send = (value: Record<string, unknown>) => {
                for (const origin of parentOrigin ? [parentOrigin] : parentOrigins)
                    window.parent.postMessage({ channel: CHANNEL, ...value }, origin)
            }
            let pending = false
            const receive = async (event: MessageEvent) => {
                if (event.source !== window.parent || !parentOrigins.includes(event.origin) ||
                    (parentOrigin && event.origin !== parentOrigin)) return
                const input = event.data
                if (!input || input.channel !== CHANNEL ||
                    !["load", "export", "prompt", "feature"].includes(input.type) ||
                    typeof input.requestId !== "string" ||
                    !/^[a-zA-Z0-9_-]{1,128}$/.test(input.requestId)) return
                parentOrigin = event.origin
                if (pending) {
                    send({ type: "error", requestId: input.requestId, error: "绘图操作尚未完成。" })
                    return
                }
                pending = true
                try {
                    const { engine, diagram } = current.current
                    if (engine.isBusy || engine.isLeaving) throw new Error("请等待当前绘图回答完成或停止回答。")
                    if (input.type === "feature") {
                        if (typeof input.feature !== "string" || !Object.hasOwn(FEATURE_PROMPTS, input.feature))
                            throw new Error("绘图功能无效。")
                        if (diagram.hasPendingLoad()) throw new Error("画布正在加载，请稍候再选择功能。")
                        const prefix = FEATURE_PROMPTS[input.feature as keyof typeof FEATURE_PROMPTS] + "\n\n"
                        const existing = engine.input
                        const content = featurePrefix.current && existing.startsWith(featurePrefix.current)
                            ? existing.slice(featurePrefix.current.length) : existing
                        const next = prefix + content
                        if (next.length > 12500) throw new Error("当前输入加上功能提示后超过限制，请先整理输入内容。")
                        engine.setInput(next)
                        featurePrefix.current = prefix
                        useUiStore.getState().focusComposer()
                        send({ type: "result", requestId: input.requestId, selected: true, feature: input.feature })
                    } else if (input.type === "prompt") {
                        if (typeof input.prompt !== "string" || input.prompt.length > 12500)
                            throw new Error("选中文字为空或超过限制。")
                        engine.setInput(input.prompt)
                        send({ type: "result", requestId: input.requestId, prompted: true })
                    } else if (input.type === "load") {
                        if (typeof input.xml !== "string" || bytes(input.xml) > MAX_XML)
                            throw new Error("图稿 XML 为空或超过限制。")
                        const rawMessages = input.messages ?? []
                        if (!Array.isArray(rawMessages) || rawMessages.length > 1000 ||
                            bytes(JSON.stringify(rawMessages)) > MAX_MESSAGES)
                            throw new Error("绘图会话超过限制。")
                        const messages = sanitizeMessages(rawMessages)
                        if (messages.length !== rawMessages.length) throw new Error("绘图会话格式无效。")
                        if (input.xml) {
                            const error = diagram.loadDiagram(input.xml)
                            if (error) throw new Error("图稿 XML 未通过校验。")
                        } else diagram.clearDiagram()
                        engine.loadedMessageIdsRef.current = new Set(messages.map(message => message.id))
                        engine.setMessages(messages as Parameters<typeof engine.setMessages>[0])
                        if (typeof input.title === "string") await engine.renameSession(input.title.slice(0, 100))
                        if (typeof input.prompt === "string") engine.setInput(input.prompt.slice(0, 12500))
                        useUiStore.getState().setHeroDismissed(true)
                        // useChat throttles the exposed message snapshot. A load
                        // receipt must observe the restored state, so an immediate
                        // export cannot save the previous drawing's conversation.
                        const expectedMessages = JSON.stringify(messages)
                        const deadline = Date.now() + 5000
                        while (active) {
                            const restored = current.current
                            if (restored.engine.isBusy || restored.engine.isLeaving)
                                throw new Error("绘图状态已变化，请稍候再打开图稿。")
                            if (!restored.diagram.hasPendingLoad() &&
                                JSON.stringify(sanitizeMessages(restored.engine.messages)) === expectedMessages) break
                            if (Date.now() >= deadline) throw new Error("绘图会话尚未完整恢复，请重新打开图稿。")
                            await new Promise<void>(resolve => setTimeout(resolve, 16))
                        }
                        if (!active) throw new Error("绘图连接已变化，请重新打开图稿。")
                        centerEmptyPage()
                        send({ type: "result", requestId: input.requestId, loaded: true })
                    } else {
                        if (diagram.hasPendingLoad()) throw new Error("画布正在加载，请稍候再保存。")
                        // Export the complete multipage file independently of the
                        // PNG's currentPage setting, then verify it did not change.
                        const source = await diagram.requestExport({ format: "xml" }, 10000)
                        if (!source?.xml) throw new Error("完整图稿导出失败，请重试。")
                        const exported = current.current
                        if (exported.engine.isBusy || exported.engine.isLeaving || exported.diagram.hasPendingLoad())
                            throw new Error("画布状态已变化，请稍候再保存。")
                        if (input.skipEmpty === true && exported.engine.messages.length === 0 &&
                            isDefinitelyEmptyDiagram(source.xml)) {
                            send({ type: "result", requestId: input.requestId, empty: true })
                            return
                        }
                        const result = await diagram.requestExport({
                            format: "png", border: 20, scale: 1,
                            currentPage: true, background: "#ffffff",
                        }, 20000)
                        const verified = await diagram.requestExport({ format: "xml" }, 10000)
                        const latest = current.current
                        if (latest.engine.isBusy || latest.engine.isLeaving)
                            throw new Error("绘图回答已开始，请在完成后重新保存。")
                        if (verified?.xml !== source.xml) throw new Error("图稿在导出期间发生变化，请重新保存。")
                        if (!result?.data?.startsWith("data:image/png;base64,"))
                            throw new Error("画布导出失败，请重试。")
                        if (bytes(source.xml) > MAX_XML || result.data.length > 5 * 1024 * 1024)
                            throw new Error("图稿或预览图片超过保存限制。")
                        const messages = sanitizeMessages(latest.engine.messages)
                        if (messages.length > 1000 || bytes(JSON.stringify(messages)) > MAX_MESSAGES) throw new Error("绘图会话超过保存限制。")
                        send({ type: "result", requestId: input.requestId,
                            xml: source.xml, png: result.data, messages,
                            title: latest.engine.currentTitle || "未命名图稿" })
                    }
                } catch (error) {
                    send({ type: "error", requestId: input.requestId,
                        error: error instanceof Error ? error.message : "绘图操作失败。" })
                } finally { pending = false }
            }
            window.addEventListener("message", receive)
            dispose = () => window.removeEventListener("message", receive)
            centerEmptyPage()
            send({ type: "ready" })
        })().catch(() => {
            // The parent times out with its own recoverable connection message.
        })
        return () => { active = false; abort.abort(); dispose() }
    }, [ready])
    return null
}
