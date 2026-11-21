// Modified for LingDoc. Runtime-only public bridge metadata contains no credentials.
export const dynamic = "force-dynamic"

export function GET() {
    const parentOrigin = process.env.LINGDOC_DRAWING_PARENT_ORIGIN
    if (!parentOrigin) return Response.json({ error: "Unavailable" }, { status: 503 })
    const alias = new URL(parentOrigin)
    alias.hostname = alias.hostname === "localhost" ? "127.0.0.1" : "localhost"
    return Response.json({ parentOrigins: [parentOrigin, alias.origin], runtimeId: process.env.LINGDOC_DRAWING_RUNTIME_ID },
        { headers: { "Cache-Control": "no-store" } })
}
