// Owns only explicit uploaded bytes and their project copies. Never reads a client path.
import {createHash,randomUUID} from 'node:crypto';
import {mkdir,readFile,writeFile,rename,lstat} from 'node:fs/promises';
import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
const exec=promisify(execFile), here=path.dirname(fileURLToPath(import.meta.url));
const hash=x=>createHash('sha256').update(x).digest('hex');
const clone=x=>structuredClone(x);
const error=(message,status=400,extra={})=>Object.assign(new Error(message),{status,...extra});
const MAX_BYTES=10*1024*1024;
// Only our fixed messages may cross the UI boundary. Parser/OS/upstream errors
// can contain paths or commands, so keep their full diagnostics in the manifest.
const PUBLIC_ERRORS=new Set([
 '解析产物路径不是普通文件，停止写入','资料文本包含空字节或超过 2 MiB 阅读上限',
 '文本编码无法识别，请另存为 UTF-8 或带 BOM 的 UTF-16 后重试',
 'DOCX 展开内容超过安全读取上限','DOCX 条目展开比例或大小超限','文件不是有效 DOCX',
 'DOCX XML 不允许 DTD 或实体声明','DOCX 缺少正文','加密 PDF 暂不支持，请先提供可阅读副本',
 'PDF 预览支持 1–32 页，原文件已保留','PDF 页面尺寸超限','PDF 页面渲染超过本轮预览上限，原文件已保留',
 '资料超过 10 MiB','保留副本校验失败；不会覆盖原件。','解析文本校验失败。',
 '资料解析超时，原文件已保留。','资料解析失败，原文件已保留。',
 '资料检索接入失败，原文件已保留，可稍后重试。',
 'PDF 无法解析，文件可能损坏；原文件已保留。','DOCX 无法解析，文件可能损坏；原文件已保留。'
]);
const publicError=(message,record)=>!message?'':PUBLIC_ERRORS.has(message)?message:
 record?.status==='uploading'?'资料检索接入失败，原文件已保留，可稍后重试。':
 ['pdf','docx'].includes(record?.extension)?`${record.extension.toUpperCase()} 无法解析，文件可能损坏；原文件已保留。`:'资料解析失败，原文件已保留。';

export async function createImportStore({directory,sources,pythonBinary='D:/python3.10/python.exe'}) {
  if(!sources?.uploadText)throw Error('Go text upload adapter required');
  const root=path.resolve(directory,'imported-materials'), manifest=path.join(root,'imports.json');
  await mkdir(root,{recursive:true});
  if((await lstat(root)).isSymbolicLink())throw Error('Import root cannot be a link');
  let db={schema:1,records:[],operations:{}};
  try{db=JSON.parse(await readFile(manifest,'utf8'))}catch(e){if(e.code!=='ENOENT')throw e;}
  if(db.schema!==1||!Array.isArray(db.records)||!db.operations||db.records.some(r=>!/^imp-[a-f0-9-]{36}$/.test(r.id)||!['txt','md','pdf','docx'].includes(r.extension)))throw Error('Invalid import manifest');
  const save=async()=>{const temp=manifest+'.'+randomUUID()+'.tmp';await writeFile(temp,JSON.stringify(db,null,2),{flag:'wx'});await rename(temp,manifest);};
  const safeRecord=r=>{const value=clone(r);delete value.diagnostic;value.error=publicError(value.error,value);return value;};
  const directoryFor=r=>path.join(root,r.id);
  const originalFor=r=>path.join(directoryFor(r),'original.'+r.extension);
  async function checkDirectory(r){if((await lstat(directoryFor(r))).isSymbolicLink())throw Error('Imported material directory cannot be a link');}
  async function readOwned(file){const info=await lstat(file);if(!info.isFile()||info.isSymbolicLink())throw Error('Imported material file cannot be a link');return readFile(file);}
  let serial=Promise.resolve();
  async function perform(input){
    const {name,base64,key,folder='前期研究材料'}=input||{};
    if(typeof name!=='string'||!name.trim()||name.length>180||/[\x00-\x1f\\/]/.test(name)||name==='.'||name==='..')throw error('请选择有效文件名，不接受磁盘路径。');
    const extension=path.extname(name).slice(1).toLowerCase();
    if(!['txt','md','pdf','docx'].includes(extension))throw error('仅支持 TXT、MD、PDF 和 DOCX。');
    if(typeof folder!=='string'||folder.length>256||/[\x00-\x1f]/.test(folder))throw error('资料分类无效。');
    if(typeof key!=='string'||!/^[a-zA-Z0-9_-]{8,128}$/.test(key))throw error('缺少可重试的导入操作编号。');
    if(typeof base64!=='string'||base64.length>4*Math.ceil(MAX_BYTES/3)||base64.length%4||/[^A-Za-z0-9+/=]/.test(base64))throw error('上传内容无效或超过 10 MiB。');
    const bytes=Buffer.from(base64,'base64');
    if(!bytes.length||bytes.length>MAX_BYTES||bytes.toString('base64')!==base64)throw error('文件为空或上传内容无效。');
    const sha=hash(bytes), fingerprint=hash(JSON.stringify({name,sha,folder}));
    const old=db.operations[key];
    if(old&&old.fingerprint!==fingerprint)throw error('同一操作编号不能用于不同资料。',409);
    let record=old?db.records.find(r=>r.id===old.id):db.records.find(r=>r.name===name&&r.originalSha256===sha);
    if(!record){
      if(db.records.length>=500||Object.keys(db.operations).length>=2000)throw error('资料导入记录达到本轮上限。',409);
      const sameNameCount=db.records.filter(r=>r.name===name).length;
      record={id:'imp-'+randomUUID(),name,displayTitle:sameNameCount?`${name}（独立导入 ${sameNameCount+1}）`:name,folder,extension,originalSha256:sha,originalBytes:bytes.length,version:'1',status:'copying',searchable:false,createdAt:new Date().toISOString()};
      await mkdir(directoryFor(record));
      await writeFile(originalFor(record),bytes,{flag:'wx'});
      record.status='copied';db.records.push(record);
    }
    db.operations[key]={id:record.id,fingerprint};await save();
    try{
      await checkDirectory(record);
      if(hash(await readOwned(originalFor(record)))!==record.originalSha256)throw Error('保留副本校验失败；不会覆盖原件。');
      if(['ready','readable_only'].includes(record.status))return safeRecord(record);
      record.status='extracting';record.error='';await save();
      const derived=path.join(directoryFor(record),'derived');
      await mkdir(derived,{recursive:true});if((await lstat(derived)).isSymbolicLink())throw Error('Derived directory cannot be a link');
      try{await exec(pythonBinary,[path.join(here,'extract-source.py'),originalFor(record),derived,extension],{windowsHide:true,timeout:45000,maxBuffer:512*1024,encoding:'utf8',env:{...process.env,PYTHONIOENCODING:'utf-8'}})}
      catch(e){let detail;try{detail=JSON.parse(e.stdout.trim()).error}catch{}throw Object.assign(Error(detail||(e.killed?'资料解析超时，原文件已保留。':'资料解析失败，原文件已保留。')),{diagnostic:{message:e.message,stdout:e.stdout,stderr:e.stderr,detail}});}
      const extractionBytes=await readOwned(path.join(derived,'extraction.json'));
      const extracted=JSON.parse(extractionBytes.toString('utf8'));
      const text=(await readOwned(path.join(derived,'text.txt'))).toString('utf8');
      if(hash(text)!==extracted.textSha256)throw Error('解析文本校验失败。');
      record.rendering=extracted.rendering;record.extractedTextSha256=extracted.textSha256;record.extractionSha256=hash(extractionBytes);
      record.notice=extracted.notice||'';record.limitations=extracted.limitations||[];
      if(!extracted.searchable){record.status='readable_only';record.sourceId=record.id;record.searchable=false;await save();return safeRecord(record);}
      record.status='uploading';await save();
      const uploadTitle=name.replace(/\.[^.]+$/,'').slice(0,150).replace(/[\uD800-\uDBFF]$/,'')+'-资料文本.txt';
      const uploaded=await sources.uploadText({title:uploadTitle,text,key:'import-'+record.id});
      if(!uploaded.sourceId||!uploaded.asset?.id||!uploaded.knowledgeId||typeof uploaded.document?.text!=='string')throw Error('资料检索返回的绑定信息不完整。');
      const shared=db.records.find(r=>r.id!==record.id&&r.originalSha256!==record.originalSha256&&(r.assetId===uploaded.asset.id||r.sourceId===uploaded.sourceId));
      if(shared){
        Object.assign(record,{sourceId:record.id,knowledgeId:uploaded.knowledgeId,status:'readable_only',searchable:false,duplicateOf:shared.id,error:'',notice:'该原件与已有资料的检索文本相同，服务复用了已有检索身份。本副本仍可阅读，但尚未建立独立资料版本，因此暂不加入问答，旧引用保持原指向。'});
        await save();return safeRecord(record);
      }
      Object.assign(record,{sourceId:uploaded.sourceId,assetId:uploaded.asset.id,knowledgeId:uploaded.knowledgeId,indexedTextSha256:hash(uploaded.document.text),sourceQuotedTextHash:uploaded.source?.quoted_text_hash||'',status:'ready',searchable:true,error:''});
      await save();return safeRecord(record);
    }catch(e){
      if(e.code==='SOURCE_ORIGINAL_UNVERIFIABLE'&&record.status==='uploading'){
        Object.assign(record,{status:'readable_only',sourceId:record.id,searchable:false,knowledgeId:e.knowledgeId,unavailableAssetId:e.assetId,error:'',notice:'原文核验未完成，暂不可用于问答。本地完整副本仍可阅读；不会绕过来源校验。',diagnostic:{at:new Date().toISOString(),phase:'uploading',code:e.code,message:e.message}});
        await save();return safeRecord(record);
      }
      const message=publicError(e.message,record);record.diagnostic={at:new Date().toISOString(),phase:record.status,...(e.diagnostic||{message:e.message})};record.status='failed';record.searchable=false;record.error=message;record.knowledgeId=e.knowledgeId||record.knowledgeId;await save();throw error(message,422,{importId:record.id,record:safeRecord(record)});}
  }
  return {
    importFile(input){const next=serial.then(()=>perform(input));serial=next.catch(()=>{});return next;},
    list(){return db.records.map(safeRecord);},
    async resolve(sourceId){
      const record=db.records.find(r=>r.sourceId===sourceId||r.id===sourceId);
      if(!record||!['ready','readable_only'].includes(record.status))throw error('资料尚未可读，请查看导入记录。',404);
      await checkDirectory(record);
      const originalPath=originalFor(record);
      if(hash(await readOwned(originalPath))!==record.originalSha256)throw error('保留原件已变化，停止展示错误版本。',409);
      const derived=path.join(directoryFor(record),'derived');
      if((await lstat(derived)).isSymbolicLink())throw Error('Derived directory cannot be a link');
      const extractionBytes=await readOwned(path.join(derived,'extraction.json'));
      if(hash(extractionBytes)!==record.extractionSha256)throw error('资料解析记录已变化，停止展示错误版本。',409);
      const extraction=JSON.parse(extractionBytes.toString('utf8'));
      let payload;
      if(record.extension==='pdf'){
        const pages=[];
        for(const [i,page]of extraction.pages.entries()){
          if(page.file!==`page-${i+1}.png`)throw Error('Invalid derived page path');
          const bytes=await readOwned(path.join(derived,page.file));if(hash(bytes)!==page.sha256)throw error('资料页图已变化，停止展示错误版本。',409);pages.push(bytes.toString('base64'));
        }
        payload={op:'source.open',sourceId:record.sourceId,title:record.displayTitle||record.name,version:record.version,pages};
      }else{
        const text=(await readOwned(path.join(derived,'text.txt'))).toString('utf8');if(hash(text)!==record.extractedTextSha256)throw error('资料文本已变化，停止展示错误版本。',409);
        payload={op:'source.openText',sourceId:record.sourceId,title:(record.displayTitle||record.name)+(record.extension==='docx'?'（解析文本）':''),version:record.version,text};
      }
      return {record:safeRecord(record),originalPath,payload,pageTexts:extraction.pageTexts||[]};
    }
  };
}
