// Records exact local source/build identity; this is not a release or test gate.
import { readFile, readdir, writeFile } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
const here=path.dirname(fileURLToPath(import.meta.url));
const repo=path.resolve(here,'../..');
const native='candidates/lingdoc-native-workspace-20261027';
const files=['server.mjs','native-adapter.js','chain-store.mjs','go-sources.mjs','workflow-store.mjs','workflow-model.mjs','frontend/src/lib/live.tsx','frontend/src/lib/live-context.ts','frontend/src/components/ControlledAssistant.tsx','frontend/src/components/NativeHost.tsx','frontend/src/components/WritingWorkspace.tsx','frontend/src/components/ReviewDelivery.tsx','frontend/src/components/ReviewPrimitives.tsx','frontend/src/components/ProjectOverview.tsx','frontend/src/App.tsx','frontend/src/index.css','frontend/dist/index.html','verify-controlled.mjs','Start-ControlledTrial.ps1','Stop-ControlledTrial.ps1'].map(p=>'prototypes/local-integration-20261103/'+p);
for(const entry of await readdir(path.join(here,'frontend/dist/assets'),{withFileTypes:true})) if(entry.isFile())files.push('prototypes/local-integration-20261103/frontend/dist/assets/'+entry.name);
files.push(...['engine/crates/engine/src/cmd/text.rs','engine/crates/engine/src/session.rs','engine/crates/engine/tests/controlled_replace.rs','engine/apps/wordcraft-web/src/host_bridge.rs','build/out/wordcraft.js','build/out/wordcraft_bg.wasm'].map(p=>native+'/'+p));
async function includeTree(relative){
  for(const entry of await readdir(path.join(repo,relative),{withFileTypes:true})) {
    const file=relative+'/'+entry.name;
    if(entry.isDirectory())await includeTree(file);
    else if(entry.isFile())files.push(file);
  }
}
await includeTree('prototypes/local-integration-20261103/frontend/src');
files.push(...['package.json','frontend/package.json','WORKFLOW-API.md','V1-IMPLEMENTATION.md','受控写作-试用步骤.md','受控写作-前端接续说明.md','START-HERE.md','verify-gateway.mjs','snapshot-controlled.mjs'].map(p=>'prototypes/local-integration-20261103/'+p));
const records=[];
for(const file of [...new Set(files)].sort()){const bytes=await readFile(path.join(repo,file));records.push({file,bytes:bytes.length,sha256:createHash('sha256').update(bytes).digest('hex')});}
const manifest={componentVersion:'controlled-writing-0.3.0',designBaselineId:'LD-V1-20261103-R1',observedAt:new Date().toISOString(),sourceHead:'5269e58f561bcb973d63a7623f2d076e8d943c8e',uncommitted:true,scope:'local code and artifacts only; not proof of test pass, cloud synchronization, merge or release',files:records};
await writeFile(path.join(here,'CONTROLLED-INTEGRATION.json'),JSON.stringify(manifest,null,2)+'\n');
console.log(JSON.stringify({files:records.length,componentVersion:manifest.componentVersion}));
