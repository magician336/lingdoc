// Local-only client for the existing Go material service. No model calls and no
// direct database writes. The caller persists operation keys and returned IDs.
import { createHash } from 'node:crypto';

const sha256 = value => createHash('sha256').update(value).digest('hex');
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
const segment = value => {
  if (typeof value !== 'string' || !value || value.length > 160 || /[\u0000-\u001f/\\]/.test(value)) throw new Error('Invalid Go object ID');
  return encodeURIComponent(value);
};
function loopback(value, name) {
  const url = new URL(value);
  if (url.protocol !== 'http:' || !['127.0.0.1', 'localhost'].includes(url.hostname) || !url.port || url.origin !== value) throw new Error(`${name} must be an exact loopback HTTP origin`);
  return value;
}

export function createGoSources({ goOrigin, runtimeId, origin, fetchImpl = fetch, timeoutMs = 90_000 }) {
  goOrigin = loopback(goOrigin, 'goOrigin');
  origin = loopback(origin, 'origin');
  let currentBootstrap;
  const uploads = new Map();
  async function request(route, { method = 'GET', body, key, duplicateFile = false } = {}) {
    const headers = { Accept: 'application/json', Origin: origin };
    if (key) headers['Idempotency-Key'] = key;
    if (body !== undefined && !(body instanceof FormData)) {
      headers['Content-Type'] = 'application/json';
      body = JSON.stringify(body);
    }
    const response = await fetchImpl(goOrigin + route, { method, headers, body, signal: AbortSignal.timeout(20_000), redirect: 'error' });
    let value;
    try { value = await response.json(); } catch { throw Object.assign(new Error('Go returned an unreadable response'), { status: response.status }); }
    if (!response.ok || value.success === false) {
      if (duplicateFile && response.status === 409 && value.code === 'duplicate_file' && value.data?.id) return value.data;
      const form = value.error && typeof value.error === 'object' ? value.error : value;
      throw Object.assign(new Error(form.message || 'Go material request failed'), { status: response.status, code: form.code || (typeof value.error === 'string' ? value.error : undefined), requestId: value.request_id });
    }
    return Object.hasOwn(value, 'data') ? value.data : value;
  }
  async function bootstrap() {
    if (runtimeId) {
      const health = await request('/healthz');
      if (!health.ok || health.runtimeId !== runtimeId) throw new Error('Go runtime identity changed; reconnect explicitly');
    }
    const value = await request('/api/local/research/bootstrap');
    if (!value.projectId || !value.knowledgeBaseId || !value.localOnly) throw new Error('Go research bootstrap is unavailable');
    currentBootstrap = value;
    return value;
  }
  const boot = async () => currentBootstrap || bootstrap();
  const projectPath = async () => `/api/v1/lingdoc/projects/${segment((await boot()).projectId)}`;
  async function readSource(sourceId) {
    const document = await request(`${await projectPath()}/sources/${segment(sourceId)}/document`);
    if (document.source?.status !== 'available' || typeof document.text !== 'string' || !Array.isArray(document.segments)) throw new Error('Source original is unavailable or stale');
    return { ...document, contentSha256: sha256(document.text) };
  }
  async function finishUpload(knowledge, inputHash, key) {
    const deadline = Date.now() + timeoutMs;
    while (knowledge.parse_status !== 'completed') {
      if (['failed', 'cancelled'].includes(knowledge.parse_status)) throw Object.assign(new Error('资料解析失败，原上传记录已保留'), { code: 'SOURCE_PARSE_FAILED', knowledgeId: knowledge.id });
      if (Date.now() >= deadline) throw Object.assign(new Error('资料仍在解析，上传已保留，可稍后继续读取'), { code: 'SOURCE_PARSE_PENDING', knowledgeId: knowledge.id });
      await sleep(350);
      knowledge = await request(`/api/v1/knowledge/${segment(knowledge.id)}`);
    }
    const asset = await request(`${await projectPath()}/assets`, { method: 'POST', body: { knowledge_id: knowledge.id }, key: `local-bind-${sha256(key).slice(0, 48)}` });
    if (!asset.id || asset.knowledge_id !== knowledge.id) throw new Error('Go returned an inconsistent project material binding');
    const chunks = await request(`/api/v1/chunks/${segment(knowledge.id)}?page=1&page_size=100`);
    const chunk = Array.isArray(chunks) && chunks.find(item => item.id && (!item.chunk_type || item.chunk_type === 'text'));
    if (!chunk) throw Object.assign(new Error('已上传的资料没有可读原文段落'), { code: 'SOURCE_TEXT_UNAVAILABLE', knowledgeId: knowledge.id, assetId: asset.id });
    let document;
    try { document = await readSource(chunk.id); }
    catch (error) { throw Object.assign(error, {knowledgeId:knowledge.id,assetId:asset.id,sourceId:chunk.id,...(error.status===403&&error.code==='source_access_denied'?{code:'SOURCE_ORIGINAL_UNVERIFIABLE',originalCode:error.code}:{})}); }
    if (document.source.asset_id !== asset.id) throw new Error('Source does not belong to the bound material');
    return { projectId: (await boot()).projectId, knowledgeId: knowledge.id, asset, sourceId: document.source.id, source: document.source, document, uploadedTextSha256: inputHash };
  }
  async function uploadText({ title, text, key }) {
    if (typeof title !== 'string' || !title.trim() || title.length > 180 || /[\u0000-\u001f/\\]/.test(title)) throw new Error('请提供不含路径的资料标题');
    if (typeof text !== 'string' || !text.trim() || Buffer.byteLength(text) > 8 * 1024 * 1024) throw new Error('文本资料不能为空且不得超过 8 MiB');
    if (typeof key !== 'string' || key.length < 8 || key.length > 128) throw new Error('请保留一个 8–128 字符的上传操作键用于重试');
    const fingerprint = sha256(JSON.stringify({ title, text }));
    const existing = uploads.get(key);
    if (existing && existing.fingerprint !== fingerprint) throw Object.assign(new Error('同一操作键不能用于不同资料'), { status: 409, code: 'IDEMPOTENCY_CONFLICT' });
    if (existing?.promise) return existing.promise;
    const entry = existing || { fingerprint };
    const promise = (async () => {
      if (!entry.knowledge) {
        const info = await boot();
        const form = new FormData();
        form.set('file', new Blob([text], { type: 'text/plain;charset=utf-8' }), title.replace(/\.(txt|md)$/i, '') + '.txt');
        form.set('metadata', JSON.stringify({ local_operation_key: key, local_content_sha256: sha256(text) }));
        form.set('enable_multimodel', 'false');
        // KnowledgeProcessOverrides: explicit per-file opt-out survives KB defaults.
        form.set('process_config', JSON.stringify({enable_multimodel:false,question_generation_config:{enabled:false},graph_enabled:false,extract_config:{enabled:false}}));
        // Existing Go upload deduplicates content and returns 409 duplicate_file
        // with its original knowledge object. This is not a new Go idempotency API.
        entry.knowledge = await request(`/api/v1/knowledge-bases/${segment(info.knowledgeBaseId)}/knowledge/file`, { method: 'POST', body: form, duplicateFile: true });
        if (!entry.knowledge?.id) throw new Error('Go did not return the uploaded material ID');
      }
      return finishUpload(entry.knowledge, sha256(text), key);
    })();
    entry.promise = promise;
    uploads.set(key, entry);
    try { return await promise; } catch (error) { entry.promise = null; throw error; }
  }
  return { bootstrap, uploadText, readSource, listAssets: async () => request(`${await projectPath()}/assets`), listDependencies: async () => request(`${await projectPath()}/dependencies`) };
}
