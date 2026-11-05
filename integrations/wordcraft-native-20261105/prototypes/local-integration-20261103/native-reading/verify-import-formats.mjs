// Real local import/Go verification; generated fixtures only, no model calls or body edits.
import assert from 'node:assert/strict';
import {mkdir,readFile,writeFile} from 'node:fs/promises';
import {createHash,randomUUID} from 'node:crypto';
import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {browserClient} from '../browser-client.mjs';
const here=path.dirname(fileURLToPath(import.meta.url)), stamp=new Date().toISOString().replaceAll(':','-')+'-'+process.pid;
const out=path.join(here,'artifacts','import-formats-'+stamp), sha=x=>createHash('sha256').update(x).digest('hex');
const result={passed:false,origin:'http://localhost:7214',scope:'real Go import of generated PDF/DOCX/MD, preserved-copy hashes and invalid PDF retention',modelRequests:0,checks:[],imports:[]};
await mkdir(out,{recursive:true});
const trial=JSON.parse(await readFile(path.join(here,'runtime','start-import','trial.json'),'utf8'));
assert.equal(trial.port,7214);assert.equal(path.resolve(trial.directory),path.join(here,'runtime','start-import'));
const files={pdf:`generated-${stamp}.pdf`,docx:`generated-${stamp}.docx`,md:`generated-${stamp}.md`,bad:`broken-${stamp}.pdf`};
await promisify(execFile)('D:/python3.10/python.exe',['-c',String.raw`
import fitz,zipfile,sys
from pathlib import Path
root=Path(sys.argv[1]);pdf,docx,marker=sys.argv[2:]
d=fitz.open()
for number in range(1,3):
 p=d.new_page();p.insert_text((50,60),f'Generated import test {marker} - page {number}');p.insert_text((50,90),'Original PDF text layer; no model generation.')
d.save(root/pdf);d.close()
with zipfile.ZipFile(root/docx,'w',zipfile.ZIP_DEFLATED) as z:
 z.writestr('[Content_Types].xml','<Types/>')
 z.writestr('word/document.xml','<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Before '+marker+'</w:t></w:r></w:p><w:tbl><w:tr><w:tc><w:p><w:r><w:t>Table cell</w:t></w:r></w:p></w:tc></w:tr></w:tbl><w:p><w:r><w:t>After</w:t></w:r></w:p></w:body></w:document>')
`,out,files.pdf,files.docx,stamp],{windowsHide:true,timeout:30000,maxBuffer:1024*1024});
const md=`# Generated material ${stamp}\n\nLiteral **Markdown** and [a link](https://example.invalid/not-fetched).\n`;
await writeFile(path.join(out,files.md),md);await writeFile(path.join(out,files.bad),'This is deliberately not a PDF. '+stamp);
let browser;
try{
 browser=await browserClient(path.join(out,'edge'));
 const {evaluate:e,send,wait}=browser;
 const ready=()=>wait(`window.nativeReadingReady?.phase==='ready'&&!!window.nativeWorkspace`,120000);
 const passed=label=>{result.checks.push(label);console.log('PASS '+label)};
 await send('Page.navigate',{url:result.origin+'/'});await ready();
 await e(`(()=>{window.formatApi=async(route,input)=>{const r=await fetch('/native-reading/workspace/'+route,{method:input===undefined?'GET':'POST',headers:{'Content-Type':'application/json','X-Workspace-Token':NATIVE_WORKSPACE_TOKEN},body:input===undefined?undefined:JSON.stringify(input)});return {status:r.status,value:await r.json()}};return true})()`);
 const initial=await e(`await nativeWorkspace.state()`);assert(!initial.busy&&!initial.importBusy);
 const initialBody=await e(`await wordcraftHost.engine({op:'status'})`);
 for(const kind of ['pdf','docx','md']){
   const bytes=await readFile(path.join(out,files[kind]));
   const request={name:files[kind],base64:bytes.toString('base64'),key:'formats-'+randomUUID(),folder:'前期研究材料',projectId:initial.projectId};
   const response=await e(`await formatApi('import',${JSON.stringify(request)})`);
   assert.equal(response.status,200,JSON.stringify(response.value));
   const record=response.value.record;assert.equal(record.status,'ready');assert(record.searchable&&record.assetId&&record.sourceId&&record.knowledgeId);
   assert.equal(record.originalSha256,sha(bytes));assert.equal(record.indexedTextSha256.length,64);
   const copy=path.join(trial.directory,'imports','imported-materials',record.id,'original.'+kind);
   assert(/^imp-[a-f0-9-]{36}$/.test(record.id));assert.equal(sha(await readFile(copy)),sha(bytes));
   const source=await e(`await formatApi('source',${JSON.stringify({sourceId:record.sourceId,version:record.version})})`);assert.equal(source.status,200,JSON.stringify(source.value));
   const payload=source.value.payload;
   if(kind==='pdf'){
     assert.equal(payload.op,'source.open');assert.equal(payload.pages.length,2);
     const pageFacts=payload.pages.map((p,i)=>{const png=Buffer.from(p,'base64');assert.equal(png.subarray(0,8).toString('hex'),'89504e470d0a1a0a');return {page:i+1,sha256:sha(png),width:png.readUInt32BE(16),height:png.readUInt32BE(20)}});
     assert(pageFacts.every(p=>p.width>0&&p.height>0));result.pdfPages=pageFacts;
   }else if(kind==='docx'){
     assert.equal(payload.op,'source.openText');assert(payload.title.includes('解析文本'));assert.equal(payload.text,`Before ${stamp}\nTable cell\nAfter`);
   }else{assert.equal(payload.op,'source.openText');assert.equal(payload.text,md);}
   result.imports.push({...record,fixture:path.join(out,files[kind]),copy});
   passed(`${kind.toUpperCase()} real Go binding, source payload and preserved original SHA-256`);
 }
 const badBytes=await readFile(path.join(out,files.bad));
 const bad=await e(`await formatApi('import',${JSON.stringify({name:files.bad,base64:badBytes.toString('base64'),key:'formats-'+randomUUID(),projectId:initial.projectId})})`);
 assert.equal(bad.status,422);const afterBad=await e(`await nativeWorkspace.state()`);const failed=afterBad.imports.find(r=>r.name===files.bad);
 assert(failed&&failed.status==='failed'&&failed.error);assert.equal(failed.originalSha256,sha(badBytes));
 assert.equal(sha(await readFile(path.join(trial.directory,'imports','imported-materials',failed.id,'original.pdf'))),sha(badBytes));
 result.failedImport=failed;passed('invalid PDF remains as a visible failed record with its original bytes retained');
 assert.deepEqual(await e(`await wordcraftHost.engine({op:'status'})`),initialBody);passed('imports do not modify the ordinary native document');
 assert.equal(JSON.stringify(afterBad.messages),JSON.stringify(initial.messages));assert.equal(afterBad.currentSessionId,initial.currentSessionId);passed('imports and extraction leave conversation/history unchanged; no model action was submitted');
 for(const record of result.imports)assert(afterBad.sources.some(s=>s.id===record.sourceId));passed('all three successful imports appear in the project source list');
 await send('Page.navigate',{url:result.origin+'/project'});await ready();
 const body=await e(`await wordcraftHost.engine({op:'status'})`);
 for(const [index,record]of result.imports.entries()){
   await e(`await nativeWorkspace.openSource(${JSON.stringify(record.sourceId)},undefined,${JSON.stringify(record.version)})`);
   const status=await e(`await wordcraftHost.engine({op:'source.status'})`);assert.equal(status.sourceId,record.sourceId);assert.equal(status.mode,'source');
   if(index===0){assert.equal(status.rendering,'host-rendered-png');assert.equal(status.pageCount,2)}else{assert.equal(status.rendering,'plain-text');assert.equal(status.page,null)}
   assert.deepEqual(await e(`await wordcraftHost.engine({op:'status'})`),body);
   await browser.screenshot(path.join(out,`0${index+1}-${record.extension}-native-reader.png`));
 }
 passed('native PDF, DOCX extracted text and MD reading preserve project document identity/revision/dirty');
 const final=await e(`await nativeWorkspace.state()`);assert.equal(JSON.stringify(final.messages),JSON.stringify(initial.messages));
 result.errors=browser.errors;assert.equal(browser.errors.length,0);result.passed=true;
}catch(error){result.failure=error.stack||error.message;process.exitCode=1;if(browser)try{await browser.screenshot(path.join(out,'failure.png'))}catch{}}
finally{if(browser)try{await browser.close()}catch(error){result.cleanupFailure=error.message;result.passed=false;process.exitCode=1}await writeFile(path.join(out,'verification.json'),JSON.stringify(result,null,2));console.log(JSON.stringify({passed:result.passed,checks:result.checks.length,out,failure:result.failure}));}
