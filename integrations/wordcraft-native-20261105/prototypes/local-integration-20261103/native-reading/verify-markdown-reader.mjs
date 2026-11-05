// One explicit fictional fixture, no model calls; screenshots retain first failures.
import assert from 'node:assert/strict';
import {mkdir,writeFile} from 'node:fs/promises';
import {randomUUID} from 'node:crypto';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {browserClient} from '../browser-client.mjs';
const here=path.dirname(fileURLToPath(import.meta.url));
const out=path.join(here,'artifacts','markdown-reader-'+new Date().toISOString().replaceAll(':','-')+'-'+process.pid);
await mkdir(out,{recursive:true});
const text=`# 社区数字服务研究：访谈整理

> 虚构演示材料：以下访谈对象、记录与判断均为测试构造，不代表真实调研。

## 阅读任务

本次整理关注居民办理社区事务时遇到的困难，帮助研究者核对问题描述与原始记录之间的关系。

## 访谈观察

- 居民希望在线预约后能够清楚看到下一步办理要求。
- 工作人员希望减少重复填写，并保留人工核验环节。
- 对不熟悉手机操作的居民，应保留线下办理途径。

## 待核对内容

1. 数字服务是否覆盖现场办理的全部事项。
2. 是否需要提供纸质材料，以及由谁确认。

## 安全显示样例

以下是原文中的链接和 HTML 字样，仅用于验证阅读器不执行、不请求网络：

[演示链接](https://lingdoc-reader-test.invalid/link)
![演示图片](https://lingdoc-reader-test.invalid/image.png)
<img src="https://lingdoc-reader-test.invalid/html.png">
<script>window.lingdocUnsafeMarkdownExecuted=true</script>

\x60\x60\x60text
# 这行是代码块里的字面原文
<script>这是文字，不是执行指令</script>
\x60\x60\x60
`;
await writeFile(path.join(out,'访谈整理.md'),text);
let browser;const result={passed:false,modelRequests:0,origin:'http://localhost:7215',fixture:'访谈整理.md'};
try{
 browser=await browserClient(path.join(out,'edge'));const {send,evaluate:e,wait}=browser;
 await send('Page.navigate',{url:result.origin+'/project'});
 await wait(`window.nativeReadingReady?.phase==='ready'&&!!window.nativeWorkspace`,120000);
 const before=await e("await wordcraftHost.engine({op:'status'})"),state=await e('await nativeWorkspace.state()');
 const request={name:'访谈整理.md',base64:Buffer.from(text).toString('base64'),key:'markdown-'+randomUUID(),folder:'前期研究材料',projectId:state.projectId};
 const imported=await e(`await (async()=>{const r=await fetch('/native-reading/workspace/import',{method:'POST',headers:{'Content-Type':'application/json','X-Workspace-Token':NATIVE_WORKSPACE_TOKEN},body:JSON.stringify(${JSON.stringify(request)})});return {status:r.status,value:await r.json()}})()`);
 assert.equal(imported.status,200,JSON.stringify(imported.value));const record=imported.value.record;
 assert.equal(record.status,'readable_only');assert.equal(record.searchable,false);result.importId=record.id;
 const api=async(route,input)=>e(`await (async()=>{const r=await fetch('/native-reading/workspace/${route}',{method:'POST',headers:{'Content-Type':'application/json','X-Workspace-Token':NATIVE_WORKSPACE_TOKEN},body:JSON.stringify(${JSON.stringify(input)})});return {status:r.status,value:await r.json()}})()`);
 const rejected=await api('action',{kind:'addContext',sourceId:record.sourceId,version:record.version,key:'md-context-'+randomUUID(),expectedSessionId:state.currentSessionId});
 assert.equal(rejected.status,409);assert.match(rejected.value.error,/仅支持阅读/);
 const original=await api('source',{sourceId:record.sourceId,version:record.version});assert.equal(original.status,200);assert.equal(original.value.payload.text,text);
 result.contextRejected=true;result.exactOriginal=true;
 await e(`await nativeWorkspace.openSource(${JSON.stringify(record.sourceId)},undefined,${JSON.stringify(record.version)})`);
 await wait(`(await wordcraftHost.engine({op:'source.status'})).sourceId===${JSON.stringify(record.sourceId)}`,10000);
 await new Promise(r=>setTimeout(r,400));
 let status=await e("await wordcraftHost.engine({op:'source.status'})");assert.equal(status.markdownRaw,false);
 await browser.screenshot(path.join(out,'01-markdown-reading.png'));
 for(const expected of [true,false]){
  const r=status.markdownToggleRect;assert(r&&r.width>0,'native Markdown toggle rect required');
  const x=r.x+r.width/2,y=r.y+r.height/2;
  await send('Input.dispatchMouseEvent',{type:'mousePressed',x,y,button:'left',clickCount:1});
  await send('Input.dispatchMouseEvent',{type:'mouseReleased',x,y,button:'left',clickCount:1});
  await wait(`(await wordcraftHost.engine({op:'source.status'})).markdownRaw===${expected}`,10000);
  status=await e("await wordcraftHost.engine({op:'source.status'})");
  assert.deepEqual(await e("await wordcraftHost.engine({op:'status'})"),before);
  await browser.screenshot(path.join(out,expected?'02-markdown-original.png':'03-markdown-reading-return.png'));
 }
 result.resourceUrls=await e("performance.getEntriesByType('resource').map(x=>x.name).filter(x=>x.includes('lingdoc-reader-test.invalid'))");
 assert.deepEqual(result.resourceUrls,[]);assert.equal(await e('Boolean(window.lingdocUnsafeMarkdownExecuted)'),false);
 const after=await e('await nativeWorkspace.state()');assert.deepEqual(after.messages,state.messages);
 assert.equal(browser.errors.length,0);result.passed=true;
}catch(error){result.failure=error.stack;process.exitCode=1;if(browser)await browser.screenshot(path.join(out,'failure.png'));}
finally{if(browser)await browser.close();await writeFile(path.join(out,'verification.json'),JSON.stringify(result,null,2));console.log(JSON.stringify({passed:result.passed,out,failure:result.failure}));}
