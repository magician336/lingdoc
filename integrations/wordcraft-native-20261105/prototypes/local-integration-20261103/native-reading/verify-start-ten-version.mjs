import {createHash} from 'node:crypto';
import {readFile,writeFile} from 'node:fs/promises';
import assert from 'node:assert/strict';
const sha=b=>createHash('sha256').update(b).digest('hex');
const root='candidates/lingdoc-native-workspace-20261027';const origin='http://localhost:7227';
const response=await fetch(origin+'/wordcraft_bg.wasm');assert(response.ok);const remote=Buffer.from(await response.arrayBuffer()),local=await readFile(root+'/build/out/wordcraft_bg.wasm');assert.equal(sha(remote),sha(local));
const js=await (await fetch(origin+'/native-reading/load-source.js')).text();const stripped=js.slice(js.indexOf('\n')+1),source=await readFile('prototypes/local-integration-20261103/native-reading/load-source.js','utf8');assert.equal(stripped,source);
const endpoints=await Promise.all(['http://localhost:7227/','http://127.0.0.1:7227/'].map(async url=>({url,status:(await fetch(url)).status})));for(const e of endpoints)assert.equal(e.status,200);
const result={passed:true,origin,wasmSha256:sha(local),hostScriptSha256:sha(source),endpoints};await writeFile('evidence/native-source-workspace-20261104/start-ten-live-version.json',JSON.stringify(result,null,2));console.log(JSON.stringify(result));
