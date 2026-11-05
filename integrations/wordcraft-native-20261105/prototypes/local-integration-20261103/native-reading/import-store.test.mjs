import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp,mkdir,readFile,writeFile} from 'node:fs/promises';
import {createHash} from 'node:crypto';
import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {createImportStore} from './import-store.mjs';
const exec=promisify(execFile), here=path.dirname(fileURLToPath(import.meta.url));
const artifacts=path.join(here,'artifacts');await mkdir(artifacts,{recursive:true});
const root=await mkdtemp(path.join(artifacts,'import-tests-'));
const hash=x=>createHash('sha256').update(x).digest('hex');
const input=(name,text,key='operation-0001')=>({name,base64:Buffer.from(text).toString('base64'),key});
async function setup(name,options={}){
 const directory=path.join(root,name);let calls=[];
 const sources={uploadText:async value=>{calls.push(value);if(options.fail&&calls.length===1)throw Object.assign(Error('Go temporarily unavailable'),{knowledgeId:'preserved-upload'});const n=options.sameIdentity?1:calls.length;return {sourceId:'source-'+n,asset:{id:'asset-'+n},knowledgeId:'knowledge-'+n,document:{text:value.text},source:{quoted_text_hash:hash(value.text)}};}};
 return {store:await createImportStore({directory,sources}),sources,calls,directory};
}

test('TXT copy remains readable, dedup is stable, and no model adapter exists',async()=>{
 const {store,calls}=await setup('txt');const text='第一段\r\n第二段 🧭';
 const a=await store.importFile(input('访谈.txt',text));
 assert.equal(a.status,'ready');assert.equal(a.indexedTextSha256,hash(text));assert.equal(calls.length,1);
 const resolved=await store.resolve(a.sourceId);
 assert.equal((await readFile(resolved.originalPath)).toString(),text);assert.equal(resolved.payload.text,text);
 assert(!JSON.stringify(store.list()).includes('originalPath'));
 const again=await store.importFile(input('访谈.txt',text,'operation-0002'));
 assert.equal(again.id,a.id);assert.equal(calls.length,1);
 await assert.rejects(store.importFile(input('访谈.txt','changed','operation-0001')),/操作编号/);
 const changed=await store.importFile(input('访谈.txt','changed','operation-0003'));
 assert.notEqual(changed.id,a.id);assert.notEqual(changed.displayTitle,a.displayTitle);assert.equal((await store.resolve(a.sourceId)).payload.text,text);
});

test('MD stays literal and path/base64/size inputs are rejected before creating records',async()=>{
 const {store}=await setup('validation');
 for(const name of ['../secret.txt','D:\\secret.txt','nested/a.md','bad.exe'])await assert.rejects(store.importFile(input(name,'x')));
 await assert.rejects(store.importFile({name:'x.txt',base64:'@@@@',key:'operation-0001'}));
 await assert.rejects(store.importFile({name:'x.txt',base64:'A'.repeat(4*Math.ceil(10*1024*1024/3)+4),key:'operation-0001'}));
 assert.equal(store.list().length,0);
 const text='# Title\n<script>doNotExecute()</script>\n[link](file:///secret)';
 const result=await store.importFile(input('notes.md',text));assert.equal((await store.resolve(result.id)).payload.text,text);
});

test('upstream failure keeps the original and explicit retry resumes the same record',async()=>{
 const {store,sources,calls,directory}=await setup('retry',{fail:true});
 await assert.rejects(store.importFile(input('retry.txt','keep original')),/检索接入失败/);
 const failed=store.list()[0];assert.equal(failed.status,'failed');assert.equal(failed.knowledgeId,'preserved-upload');
 const reopened=await createImportStore({directory,sources});
 const ready=await reopened.importFile(input('retry.txt','keep original'));
 assert.equal(ready.id,failed.id);assert.equal(ready.status,'ready');assert.equal(calls.length,2);
 assert.equal((await readFile((await reopened.resolve(ready.id)).originalPath)).toString(),'keep original');
});

test('invalid parsing is visible and never discards the uploaded copy',async()=>{
 const {store}=await setup('invalid-pdf');
 let importId;try{await store.importFile(input('broken.pdf','not a PDF'))}catch(e){importId=e.importId;}
 assert(importId);const record=store.list()[0];assert.equal(record.status,'failed');assert(record.error);
 await assert.rejects(store.importFile(input('broken.pdf','not a PDF')));
 assert.equal(store.list().length,1);assert.equal(store.list()[0].id,importId);
});

test('parser diagnostics remain internal and legacy errors are projected safely without rewriting history',async()=>{
 const {store,directory,sources}=await setup('safe-errors');
 let thrown;try{await store.importFile(input('broken.pdf','not a PDF'))}catch(e){thrown=e;}
 assert.match(thrown.message,/PDF 无法解析/);
 assert(!JSON.stringify(thrown.record).includes(directory));assert(!('diagnostic' in thrown.record));
 const manifest=path.join(directory,'imported-materials','imports.json');
 const saved=JSON.parse(await readFile(manifest,'utf8'));
 assert(saved.records[0].diagnostic.message);assert(saved.records[0].diagnostic.detail);
 saved.records[0].error="Failed to open file 'D:\\private\\secret.pdf'";
 await writeFile(manifest,JSON.stringify(saved));
 const before=await readFile(manifest,'utf8');
 const reopened=await createImportStore({directory,sources});
 assert.match(reopened.list()[0].error,/PDF 无法解析/);
 assert(!JSON.stringify(reopened.list()).includes('secret.pdf'));
 assert(!('diagnostic' in reopened.list()[0]));
 assert.equal(await readFile(manifest,'utf8'),before);
});

const fixtureDir=path.join(root,'fixtures');await mkdir(fixtureDir);
await exec('D:/python3.10/python.exe',['-c',String.raw`
import fitz,zipfile,sys
from pathlib import Path
p=Path(sys.argv[1])
d=fitz.open()
for i in range(2):
 page=d.new_page();page.insert_text((50,50),'Source page '+str(i+1))
d.save(p/'text.pdf');d.close()
d=fitz.open();d.new_page();d.save(p/'scanned.pdf');d.close()
d=fitz.open()
for i in range(33):d.new_page()
d.save(p/'too-long.pdf');d.close()
with zipfile.ZipFile(p/'text.docx','w',zipfile.ZIP_DEFLATED) as z:
 z.writestr('[Content_Types].xml','<Types/>')
 z.writestr('word/document.xml','<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Before</w:t></w:r></w:p><w:tbl><w:tr><w:tc><w:p><w:r><w:t>Cell</w:t></w:r></w:p></w:tc></w:tr></w:tbl><w:p><w:r><w:t>After</w:t></w:r></w:p></w:body></w:document>')
with zipfile.ZipFile(p/'bomb.docx','w',zipfile.ZIP_DEFLATED) as z:
 z.writestr('[Content_Types].xml','<Types/>');z.writestr('word/document.xml','0'*(2*1024*1024))
`,fixtureDir],{windowsHide:true,timeout:30000,maxBuffer:1024*1024});

test('PDF preserves bytes, renders every page, and uploads real extracted text',async()=>{
 const {store,calls}=await setup('pdf');const bytes=await readFile(path.join(fixtureDir,'text.pdf'));
 const record=await store.importFile(input('source.pdf',bytes));const result=await store.resolve(record.id);
 assert.equal(record.originalSha256,hash(bytes));assert.equal(result.payload.op,'source.open');assert.equal(result.payload.pages.length,2);
 assert.equal(result.pageTexts.length,2);assert(result.pageTexts[0].text.includes('Source page 1'));assert(calls[0].text.includes('第 2 页'));
});

test('scanned PDF is readable-only without fake search text or Go upload',async()=>{
 const {store,calls}=await setup('scanned');const record=await store.importFile(input('scan.pdf',await readFile(path.join(fixtureDir,'scanned.pdf'))));
 assert.equal(record.status,'readable_only');assert.equal(record.searchable,false);assert.equal(calls.length,0);
 assert.equal((await store.resolve(record.id)).payload.pages.length,1);
});

test('DOCX extraction retains paragraph/table order and is explicitly non-facsimile',async()=>{
 const {store}=await setup('docx');const record=await store.importFile(input('source.docx',await readFile(path.join(fixtureDir,'text.docx'))));
 const result=await store.resolve(record.id);assert.equal(result.payload.text,'Before\nCell\nAfter');assert(result.payload.title.includes('解析文本'));
 assert.equal(record.rendering,'docx-extracted-text');assert(record.limitations.length);
});

test('PDF page limits and DOCX expansion limits retain failed import records',async()=>{
 const {store}=await setup('limits');
 await assert.rejects(store.importFile(input('long.pdf',await readFile(path.join(fixtureDir,'too-long.pdf')))),/32/);
 await assert.rejects(store.importFile(input('bomb.docx',await readFile(path.join(fixtureDir,'bomb.docx')),'operation-0002')),/比例|大小/);
 assert.equal(store.list().length,2);assert(store.list().every(r=>r.status==='failed'));
});

test('changed preserved bytes are rejected instead of silently showing a new version',async()=>{
 const {store}=await setup('tamper');const record=await store.importFile(input('old.txt','original'));const resolved=await store.resolve(record.id);
 await writeFile(resolved.originalPath,'changed');await assert.rejects(store.resolve(record.id),/原件已变化/);
});

test('deduplicated import revalidates retained original instead of reporting corrupted copy ready',async()=>{
 const {store,calls}=await setup('tampered-dedup');
 const record=await store.importFile(input('notes.txt','verified original'));
 const resolved=await store.resolve(record.id);
 await writeFile(resolved.originalPath,'external replacement');
 await assert.rejects(store.importFile(input('notes.txt','verified original','operation-0002')),/保留副本校验失败/);
 assert.equal(store.list().length,1);
 assert.equal(store.list()[0].status,'failed');
 assert.equal(store.list()[0].searchable,false);
 assert.equal(calls.length,1,'failed retained-copy validation must not upload again');
 assert.equal(await readFile(resolved.originalPath,'utf8'),'external replacement','do not overwrite evidence or originals during retry');
});

test('different originals sharing Go identity remain separate readable copies without stealing old citations',async()=>{
 const {store}=await setup('shared-identity',{sameIdentity:true});const firstBytes=await readFile(path.join(fixtureDir,'text.pdf'));
 const first=await store.importFile(input('paper.pdf',firstBytes));
 const second=await store.importFile(input('paper.pdf',Buffer.concat([firstBytes,Buffer.from('\n% retained separate copy\n')]),'operation-0002'));
 assert.notEqual(first.originalSha256,second.originalSha256);assert.equal(second.status,'readable_only');assert.equal(second.searchable,false);
 assert.equal(second.sourceId,second.id);assert.equal(second.duplicateOf,first.id);assert(second.notice.includes('未建立独立'));
 assert.equal((await store.resolve(first.sourceId)).record.id,first.id);assert.equal((await store.resolve(second.sourceId)).record.id,second.id);
 assert.equal(second.assetId,undefined);assert.notEqual(first.displayTitle,second.displayTitle);
});


test('only post-upload original verification failure may preserve readable local copy',async()=>{
 for(const [label,failure,readable] of [
  ['verified-upload',Object.assign(Error('Original unavailable'),{code:'SOURCE_ORIGINAL_UNVERIFIABLE',knowledgeId:'k-upload',assetId:'a-upload'}),true],
  ['auth',Object.assign(Error('Unauthorized'),{status:401}),false],
  ['generic-forbidden',Object.assign(Error('Forbidden'),{status:403,code:'forbidden'}),false],
  ['network',Error('Network failed'),false]
 ]){
  const directory=path.join(root,'boundary-'+label);
  const store=await createImportStore({directory,sources:{uploadText:async()=>{throw failure}}});
  if(readable){const result=await store.importFile(input('notes.md','![image](https://invalid.test/a.png)'));assert.equal(result.status,'readable_only');assert.equal(result.searchable,false);assert.equal(result.unavailableAssetId,'a-upload');assert.match((await store.resolve(result.id)).payload.text,/!\[image\]/);}
  else {await assert.rejects(()=>store.importFile(input('notes.md','original')));assert.equal(store.list()[0].status,'failed');await assert.rejects(()=>store.resolve(store.list()[0].id));}
 }
});
