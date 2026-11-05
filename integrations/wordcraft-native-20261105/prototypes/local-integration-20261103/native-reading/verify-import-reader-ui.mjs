// Read-only UI recheck using retained format verification imports; no uploads/models.
import assert from 'node:assert/strict';
import {mkdir,readFile,writeFile} from 'node:fs/promises';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {browserClient} from '../browser-client.mjs';
const here=path.dirname(fileURLToPath(import.meta.url));
const prior=JSON.parse(await readFile(path.join(here,'artifacts','import-formats-2026-11-04T10-58-28.778Z-8716','verification.json'),'utf8'));
const out=path.join(here,'artifacts','import-reader-ui-'+new Date().toISOString().replaceAll(':','-')+'-'+process.pid);
await mkdir(out,{recursive:true});
let browser;const result={passed:false,imports:0,modelRequests:0,readers:[]};
try{
 browser=await browserClient(path.join(out,'edge'));const {send,evaluate:e,wait}=browser;
 await send('Page.navigate',{url:'http://localhost:7214/project'});
 await wait(`window.nativeReadingReady?.phase==='ready'&&!!window.nativeWorkspace`,120000);
 const state=await e('await nativeWorkspace.state()'),body=await e("await wordcraftHost.engine({op:'status'})");
 const failed=state.imports.find(r=>r.id===prior.failedImport.id);assert(failed&&failed.status==='failed');
 assert.match(failed.error,/PDF 无法解析/);assert(!failed.error.includes('D:'));assert(!failed.diagnostic);
 for(const record of prior.imports){
  await e(`await nativeWorkspace.openSource(${JSON.stringify(record.sourceId)},undefined,${JSON.stringify(record.version)})`);
  await wait(`(await wordcraftHost.engine({op:'source.status'})).sourceId===${JSON.stringify(record.sourceId)}`,10000);
  await new Promise(r=>setTimeout(r,400));
  const status=await e("await wordcraftHost.engine({op:'source.status'})");
  const width=await e('innerWidth');
  assert(status.bodyTabRect&&status.bodyTabRect.x<width*0.5,'resource panel must leave most of the window for central reading');
  assert.deepEqual(await e("await wordcraftHost.engine({op:'status'})"),body);
  result.readers.push({extension:record.extension,status,viewportWidth:width});
  await browser.screenshot(path.join(out,record.extension+'-reader.png'));
 }
 const after=await e('await nativeWorkspace.state()');assert.deepEqual(after.messages,state.messages);assert.deepEqual(after.imports,state.imports);
 assert.equal(browser.errors.length,0);result.passed=true;
}catch(error){result.failure=error.stack;process.exitCode=1;if(browser)await browser.screenshot(path.join(out,'failure.png'));}
finally{if(browser)await browser.close();await writeFile(path.join(out,'verification.json'),JSON.stringify(result,null,2));console.log(JSON.stringify({passed:result.passed,out,failure:result.failure}));}
