import {readFile,writeFile,mkdir} from 'node:fs/promises';
import path from 'node:path';
if(!process.argv.includes('--run-diagnostic'))throw Error('Explicit diagnostic flag required; sends one synthetic request');
const root=process.cwd();
const log=await readFile(path.join(root,'prototypes/local-integration-20261103/native-reading/runtime/ai-qa-20261105/integration/logs/f5dda27a-bac9-4b12-99cd-8bfa0770935d/go.log'),'utf8');
const row=log.split(/\r?\n/).find(x=>x.startsWith('{"max_tokens"')&&x.includes('研究说明中'));
if(!row)throw Error('synthetic request missing');
const body=JSON.parse(row);if(body.messages.some(x=>/api_key|Bearer /.test(x.content)))throw Error('refuse unexpected input');
const cfg=JSON.parse(await readFile(path.join(root,'candidates/lingdoc-native-workspace-20261027/runtime/ai-private/local-ai.json'),'utf8'));
if(body.model!==cfg.model)throw Error('model mismatch');
const out=path.join(root,'evidence/native-source-workspace-20261104/ai-diagnostic-20261105');await mkdir(out,{recursive:true});
const r=await fetch('https://api.deepseek.com/v1/chat/completions',{method:'POST',headers:{'Content-Type':'application/json',Authorization:'Bearer '+cfg.api_key},body:JSON.stringify(body),signal:AbortSignal.timeout(65000)});
if(!r.ok){await writeFile(path.join(out,'result.json'),JSON.stringify({status:r.status}));throw Error('provider HTTP '+r.status);}
const result=await r.json();const content=result.choices?.[0]?.message?.content;
let answer;try{answer=JSON.parse(content)}catch{}
const context=JSON.parse(body.messages[1].content).context;
const checks=answer?.citations?.map(c=>{const s=context.source_refs.find(s=>s.source_id===c.source_id);return {sourceKnown:!!s,quote:c.quote,literal:!!s?.quoted_text.includes(c.quote),whitespaceEquivalent:!!s?.quoted_text.replace(/\s/g,'').includes(c.quote.replace(/\s/g,''))}});
await writeFile(path.join(out,'result.json'),JSON.stringify({status:r.status,model:result.model,finishReason:result.choices?.[0]?.finish_reason,content,checks,usage:result.usage},null,2));
console.log(JSON.stringify({status:r.status,finishReason:result.choices?.[0]?.finish_reason,content,checks}));
