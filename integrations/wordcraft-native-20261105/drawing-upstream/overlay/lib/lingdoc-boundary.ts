// Modified for LingDoc. This process serves one exact loopback origin.
import type { NextRequest } from "next/server"
import { NextResponse } from "next/server"

export function lingdocContinue(headers?: Headers): NextResponse {
    const response = headers ? NextResponse.next({ request: { headers } }) : NextResponse.next()
    const parent = new URL(process.env.LINGDOC_DRAWING_PARENT_ORIGIN || "http://127.0.0.1:1")
    const alias = new URL(parent)
    alias.hostname = alias.hostname === "localhost" ? "127.0.0.1" : "localhost"
    response.headers.set("Content-Security-Policy", `frame-ancestors ${parent.origin} ${alias.origin}`)
    response.headers.set("X-Content-Type-Options", "nosniff")
    return response
}

export function lingdocBoundary(request: NextRequest): NextResponse | null {
    const self = process.env.LINGDOC_DRAWING_SELF_ORIGIN
    if (!self) return NextResponse.json({ error: "Unavailable" }, { status: 503 })
    const expected = new URL(self)
    if (request.headers.get("host") !== expected.host ||
        request.headers.get("sec-fetch-site") === "cross-site") {
        // A parent iframe navigation is the one permitted cross-site request.
        const parent = process.env.LINGDOC_DRAWING_PARENT_ORIGIN
        const referrer = request.headers.get("referer")
        let fromParent = false
        try {
            const alias = new URL(parent || "")
            alias.hostname = alias.hostname === "localhost" ? "127.0.0.1" : "localhost"
            fromParent = !!referrer && [parent, alias.origin].includes(new URL(referrer).origin)
        } catch {}
        if (request.headers.get("host") !== expected.host || request.method !== "GET" ||
            request.headers.get("sec-fetch-dest") !== "iframe" || !fromParent)
            return NextResponse.json({ error: "Forbidden" }, { status: 403 })
    }
    const origin = request.headers.get("origin")
    if ((origin && origin !== self) ||
        (!["GET", "HEAD", "OPTIONS"].includes(request.method) && origin !== self))
        return NextResponse.json({ error: "Forbidden" }, { status: 403 })
    if (request.nextUrl.pathname.startsWith("/api/admin/"))
        return NextResponse.json({ error: "Not found" }, { status: 404 })
    return null
}
