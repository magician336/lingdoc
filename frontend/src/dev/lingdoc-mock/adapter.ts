import type { AxiosAdapter, AxiosResponse, InternalAxiosRequestConfig } from 'axios'

interface Options {
  enabled: boolean
  origin?: string
  status: 200 | 422
  example: string
  fetcher?: typeof fetch
}

/** Dev-only transport for the EXISTING request.ts interceptors, not a second DTO layer.
 * Never forwards auth/cookies/tenant headers or falls back to the configured API.
 * 401 is deliberately not simulated here: that would exercise real token refresh.
 */
export function createContractMockAdapter(options: Options): AxiosAdapter {
  if (!options.enabled) throw new Error('Contract Mock is development-only')
  const origin = new URL(options.origin ?? 'http://127.0.0.1:4010')
  if (origin.protocol !== 'http:' || origin.hostname !== '127.0.0.1' || !origin.port ||
      origin.pathname !== '/' || origin.username || origin.password || origin.search || origin.hash) {
    throw new Error('Contract Mock requires an explicit 127.0.0.1 HTTP origin')
  }
  if (![200, 422].includes(options.status) || !/^[a-z][a-z0-9_]*$/.test(options.example)) {
    throw new Error('Choose a supported explicit mock example')
  }
  const fetcher = options.fetcher ?? fetch
  return async (config: InternalAxiosRequestConfig) => {
    const path = config.url ?? ''
    if (!/^\/api\/v1\/lingdoc\/[A-Za-z0-9_~.%/-]+$/.test(path) ||
        decodeURIComponent(path).split('/').some(part => part === '..' || part === '.') || /%2f|%5c/i.test(path)) {
      throw new Error('Only LingDoc relative paths are allowed; no fallback')
    }
    const headers: Record<string, string> = {
      'X-LingDoc-Mock-Status': String(options.status),
      'X-LingDoc-Mock-Example': options.example,
    }
    for (const [key, value] of Object.entries(config.headers ?? {})) {
      if (['content-type', 'idempotency-key', 'x-request-id'].includes(key.toLowerCase()) && value != null) {
        headers[key] = String(value)
      }
    }
    const abort = new AbortController()
    const cancel = () => abort.abort()
    if (config.signal?.aborted) cancel()
    config.signal?.addEventListener?.('abort', cancel)
    const timer = setTimeout(cancel, 10_000)
    try {
      const method = (config.method ?? 'get').toUpperCase()
      if (!['GET', 'POST', 'PUT', 'PATCH', 'DELETE'].includes(method)) throw new Error('Unsupported mock method')
      const response = await fetcher(origin.origin + path, {
        method, headers, credentials: 'omit', redirect: 'error', cache: 'no-store', signal: abort.signal,
        body: method === 'GET' ? undefined : typeof config.data === 'string' ? config.data :
          config.data == null ? undefined : JSON.stringify(config.data),
      })
      if (response.headers.get('X-LingDoc-Mode') !== 'mock' || response.status === 401) {
        // No response property: request.ts must NOT try authentication recovery.
        throw new Error('Untrusted mock endpoint or unsupported auth simulation')
      }
      const result: AxiosResponse = {
        data: await response.json(), status: response.status, statusText: response.statusText,
        headers: Object.fromEntries(response.headers.entries()), config,
      }
      if (!response.ok || response.headers.get('X-LingDoc-Contract-Checked') !== 'true') {
        throw Object.assign(new Error('Mock example rejected'), { config, response: result })
      }
      return result
    } finally {
      clearTimeout(timer)
      config.signal?.removeEventListener?.('abort', cancel)
    }
  }
}
