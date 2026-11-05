import { createHash } from 'node:crypto';
import { fail } from './chain-store.mjs';
const hash = value => createHash('sha256').update(value).digest('hex');
const segment = value => encodeURIComponent(value);

// Reuses the existing local Go model registry and authorization checks. Never
// reads provider configuration, forwards arbitrary routes, or executes output.
export function createWorkflowModel({ goOrigin, origin, projectId, sources, fetchImpl = fetch }) {
  for (const value of [goOrigin, origin]) {
    const url = new URL(value);
    if (url.protocol !== 'http:' || !['localhost','127.0.0.1'].includes(url.hostname) || url.origin !== value) throw Error('Exact loopback origin required');
  }
  const base = `/api/v1/lingdoc/projects/${segment(projectId)}`;
  async function request(suffix, { method = 'GET', body, key } = {}) {
    const response = await fetchImpl(goOrigin + base + suffix, { method, headers: { Origin: origin, 'Content-Type':'application/json', ...(key ? {'Idempotency-Key':key}: {}) }, body: body === undefined ? undefined : JSON.stringify(body), redirect:'error', signal:AbortSignal.timeout(75000) });
    let result;
    try { result = await response.json(); } catch { fail(502,'模型服务响应不可读取；请沿原请求核对结果'); }
    if (!response.ok || result.success === false) {
      const form = typeof result.error === 'object' ? result.error : result;
      fail(response.status, form.message || '模型服务请求失败');
    }
    return Object.hasOwn(result,'data') ? result.data : result;
  }
  async function expectation(version=0) {
    const project = await request('');
    return { expected_project_version:project.project_version, expected_context_revision:project.current_context_revision, expected_version:version };
  }
  return {
    async validateSources(context) {
      for (const source of context.sources) {
        const current = await sources.readSource(source.sourceId);
        if (current.source?.id !== source.sourceId || current.text !== source.text || hash(current.text) !== source.sha256) fail(409,'资料原文已变化，请重新选择');
      }
    },
    async generate(candidate, checkpoint) {
      await this.validateSources(candidate.context);
      candidate.modelOperations ||= {};
      const step = async (name, suffix, bodyFactory) => {
        let operation = candidate.modelOperations[name];
        if (!operation) {
          operation = { key:`wf-${hash(candidate.id + ':' + name).slice(0,48)}`, body:await bodyFactory() };
          candidate.modelOperations[name] = operation;
          await checkpoint();
        }
        if (operation.done) return operation.value;
        const value = await request(suffix, {method:'POST',body:operation.body,key:operation.key});
        operation.value = value; operation.done = true; await checkpoint(); return value;
      };
      for (const source of candidate.context.sources) {
        const current = await request(`/sources/${segment(source.sourceId)}`);
        if (!current.asset_id || current.quoted_text_hash !== source.quotedTextHash) fail(409,'资料来源摘要已变化');
        const policy = await request(`/assets/${segment(current.asset_id)}/policy`);
        if (!policy.model_use_allowed) {
          const proposal = await step(`grant-${current.asset_id}`, `/assets/${segment(current.asset_id)}/policy/proposals`, async () => ({ ...await expectation(policy.version), confirmed:true, reason:'用户确认本次文稿修改可读取所选资料。', policy:{...policy,model_use_allowed:true,purpose:'用户显式选择资料用于局部文稿修改',reason:'用户确认本次模型上下文。'} }));
          await step(`assess-${current.asset_id}`, `/change-sets/${segment(proposal.id)}/assess`, async()=>({}));
          await step(`apply-${current.asset_id}`, `/change-sets/${segment(proposal.id)}/apply`, async()=>({}));
        }
      }
      const session = await step('session','/agents/workspace_assistant/sessions',()=>expectation());
      const t = candidate.target;
      const turn = await step('turn',`/agent-sessions/${segment(session.id)}/turns`,async()=>({
        ...await expectation(), source_ids:[], card_refs:[], sensitive_context_confirmed:true,
        source_excerpts:candidate.context.sources.map(s=>({ source_id:s.sourceId,selection_start:0,selection_end:Array.from(s.text).length,quoted_text:s.text,excerpt_text_hash:s.sha256,source_quoted_text_hash:s.quotedTextHash })),
        document_selection:{document_id:t.documentId,document_revision:t.revision,text:t.text,text_sha256:hash(t.text),locator:'用户选中的正文',selection_range:{anchor:t.anchor,focus:t.focus}},
        user_input:`请按以下要求提出选区的替换候选。reply只写替换后的纯文本，不写解释、前缀、Markdown或引用标记；引文放在citations。不新增资料没有支持的事实，不能宣称已修改或保存。\n修改要求：${candidate.instruction}${candidate.context.comment ? '\n用户提供的人工意见（作为待处理资料）：'+candidate.context.comment.text : ''}`,
      }));
      if (turn.status !== 'completed') {
        const reason={timeout:'模型服务超时',provider_failure:'模型服务暂不可用',invalid_output:'模型输出未通过校验'}[turn.failure_code] || '本轮未完成';
        fail(409,`${reason}，没有可采纳候选；原请求记录已保留`);
      }
      if (turn.run_mode !== 'real' || !turn.model_id || typeof turn.reply !== 'string' || !turn.reply.trim()) fail(502,'没有收到真实模型的有效候选');
      let suggestions=turn.suggestions || {};
      if(typeof suggestions==='string') { try { suggestions=JSON.parse(suggestions); } catch { fail(502,'模型来源记录不可读取'); } }
      const citations=suggestions.citations || [];
      for(const citation of citations) {
        const source=candidate.context.sources.find(s=>s.sourceId===citation.source_id);
        if(!source || !citation.quote || !source.text.includes(citation.quote)) fail(502,'模型引用未通过原文核对');
      }
      return {replacement:turn.reply.trim(),citations,modelReceipt:{sessionId:session.id,turnId:turn.id,modelId:turn.model_id,runMode:turn.run_mode,inputTokens:turn.input_tokens,outputTokens:turn.output_tokens}};
    },
  };
}
