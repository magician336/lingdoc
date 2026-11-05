// Real Go/model probe using only the explicitly fictional acceptance material.
// It does not apply a native edit and cannot establish native acceptance.
import {mkdir,writeFile} from 'node:fs/promises';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {startIntegration} from './server.mjs';
const here=path.dirname(fileURLToPath(import.meta.url));
const directory=path.join(here,'artifacts','workflow-model',new Date().toISOString().replace(/[:.]/g,'-'));
await mkdir(directory,{recursive:true});
process.env.LINGDOC_LOCAL_AI_CONFIG ||= path.resolve(here,'../../candidates/lingdoc-native-workspace-20261027/runtime/ai-private/local-ai.json');
let app;const report={passed:false,scope:'real Go + real model, fictional fixture only; no native edit',startedAt:new Date().toISOString()};
try {
  app=await startIntegration({directory:path.join(directory,'runtime'),port:7223});
  const config=await fetch(app.origin+'/integration/config').then(r=>r.json());
  const state=await fetch(app.origin+'/integration/state',{headers:{'X-Integration-Token':config.token}}).then(r=>r.json());
  const input={type:'generate',key:'real-model-fixture-0001',expectedChainRevision:state.revision,target:{documentId:1,revision:0,anchor:{path:[0],off:0},focus:{path:[0],off:22},text:'据此了解全市老年人使用数字公共服务的情况。',hostSessionId:'isolated-model-probe'},instruction:'仅将概括范围收窄为所选社区，不新增任何事实或数字。只输出修改后的完整一句话。',sourceVersionIds:['v1'],includeComment:true,contextConfirmed:true};
  if(process.argv.includes('--no-sources')) {input.sourceVersionIds=[];input.includeComment=false;input.instruction='将全市改为所选社区，其他文字不变。只输出这一句话。';report.variant='minimal no-source/no-comment';}
  const response=await fetch(app.origin+'/integration/workflow',{method:'POST',headers:{Origin:app.origin,'Content-Type':'application/json','X-Integration-Token':config.token},body:JSON.stringify(input),signal:AbortSignal.timeout(120000)});
  const result=await response.json();
  report.httpStatus=response.status;
  if(!response.ok)throw Error(result.error||'workflow failed');
  const candidate=result.candidate;
  if(candidate.status!=='proposed'||candidate.modelReceipt?.runMode!=='real'||!candidate.replacement)throw Error('No real proposed candidate');
  report.candidate=candidate;report.passed=true;
} catch(error) {report.error=error.message;process.exitCode=1;}
finally {await app?.stop();report.finishedAt=new Date().toISOString();await writeFile(path.join(directory,'report.json'),JSON.stringify(report,null,2));console.log(JSON.stringify({passed:report.passed,error:report.error,report:path.join(directory,'report.json')}));}
