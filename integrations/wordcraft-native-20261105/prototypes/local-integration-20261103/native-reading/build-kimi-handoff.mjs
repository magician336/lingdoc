// Explicit source snapshot. Default is a dry plan; never copies directories.
import {readFile,writeFile,mkdir,lstat,copyFile} from 'node:fs/promises';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {createHash} from 'node:crypto';
import {execFileSync} from 'node:child_process';
const here=path.dirname(fileURLToPath(import.meta.url)),root=path.resolve(here,'../../..');
const native='prototypes/local-integration-20261103/native-reading/';
const engine='candidates/lingdoc-native-workspace-20261027/engine/';
const allowlist=[
 ...['project_workspace.rs','source_workspace.rs','backstage.rs','lib.rs','chrome.rs','ribbon.rs','ribbon_fit.rs','canvas.rs','theme.rs','keys.rs'].map(n=>engine+'crates/ui-egui/src/'+n),
 ...['web.rs','host_bridge.rs'].map(n=>engine+'apps/wordcraft-web/src/'+n),
 ...['start.mjs','load-source.js','standalone.js','workspace-service.mjs','workspace-service.test.mjs','import-store.mjs','import-store.test.mjs','extract-source.py','verify.mjs','verify-workspace.mjs','verify-workspace-state.mjs','verify-start-page.mjs','verify-standalone.mjs','verify-source-race.mjs','verify-layout.mjs','verify-layout-foundation.mjs','verify-markdown-reader.mjs','verify-import-reader-ui.mjs','verify-import-formats.mjs','verify-document-boundary.mjs','KIMI-CONTRACT.md','build-kimi-handoff.mjs'].map(n=>native+n),
 ...['go-sources.mjs','go-sources.test.mjs','chain-store.mjs','server.mjs','workflow-store.mjs','workflow-model.mjs','seed-document.py'].map(n=>'prototypes/local-integration-20261103/'+n),
];
const sha=b=>createHash('sha256').update(b).digest('hex');
const args=process.argv.slice(2),options={screenshots:[]};
for(let i=0;i<args.length;i++){
 const key=args[i];if(key==='--build'){options.build=true;continue;}
 if(!['--output','--baseline','--start-here','--design','--screenshot'].includes(key)||!args[i+1])throw Error('Unknown or incomplete argument '+key);
 const value=args[++i];if(key==='--screenshot')options.screenshots.push(value);else options[key.slice(2)]=value;
}
const forbidden=/(^|[\\/])(runtime|ai-private|node_modules|target|browser-profile|browserprofile|\.git)([\\/]|$)|\.(db|sqlite|sqlite3)$/i;
async function checkedFile(input){
 const file=path.resolve(input);if(forbidden.test(file))throw Error('Prohibited input path');
 let cursor=file;while(cursor!==path.dirname(cursor)){const stat=await lstat(cursor);if(stat.isSymbolicLink())throw Error('Linked inputs are not allowed');cursor=path.dirname(cursor);}
 if(!(await lstat(file)).isFile())throw Error('Input must be a file');return file;
}
const files=[];
for(const relative of allowlist)files.push({source:await checkedFile(path.join(root,relative)),target:'source/'+relative});
for(const [flag,target]of [['start-here','START-HERE.md'],['design','DESIGN.md']])if(options[flag]){
 if(path.extname(options[flag]).toLowerCase()!=='.md')throw Error('Design inputs must be Markdown');
 files.push({source:await checkedFile(options[flag]),target});
}
for(const [i,input]of options.screenshots.entries()){
 if(!['.png','.jpg','.jpeg'].includes(path.extname(input).toLowerCase()))throw Error('Screenshot must be PNG or JPEG');
 files.push({source:await checkedFile(input),target:`screenshots/${String(i+1).padStart(2,'0')}${path.extname(input).toLowerCase()}`});
}
if(!options.build){console.log(JSON.stringify({mode:'plan',files:files.map(f=>f.target),generated:['CURRENT.json','verify-handoff.mjs','synthetic-state.json','index.html'],note:'No files copied. Build requires baseline, output, START-HERE, DESIGN and approved screenshots.'},null,2));process.exit(0);}
if(!options.output||!options.baseline||!options['start-here']||!options.design||!options.screenshots.length)throw Error('Build requires --output --baseline --start-here --design and --screenshot');
if(!/^[A-Za-z0-9_-]{8,96}$/.test(options.baseline))throw Error('Invalid baseline ID');
const output=path.resolve(options.output);if(forbidden.test(output)||path.extname(output))throw Error('Output must be a fresh directory without extension');
// Refuse overwriting either package or archive. Explicit filenames only.
for(const file of [output,output+'.zip']){try{await lstat(file);throw Error('Output already exists');}catch(e){if(e.code!=='ENOENT')throw e;}}
await mkdir(output,{recursive:false});
for(const file of files){const dest=path.join(output,file.target);await mkdir(path.dirname(dest),{recursive:true});await copyFile(file.source,dest);}
const synthetic={synthetic:true,notice:'人工合成示例，不是运行状态或模型输出',stateRevision:1,projectId:'mock-project',projectTitle:'示例研究任务',documentTitle:'示例申请书.docx',sources:[{id:'mock-source',title:'示例资料.txt',version:'1',folder:'参考文献',searchable:true}],sessions:[{id:'mock-chat',title:'示例会话'}],currentSessionId:'mock-chat',messages:[{id:'mock-message',role:'assistant',text:'示例回答，仅用于布局。',citations:[{sourceId:'mock-source',title:'示例资料.txt',version:'1',quote:'示例原文，仅用于布局。'}],runMode:'mock'}],contextSources:[],imports:[],importBusy:false,importError:'',busy:false,error:'',contextNote:'示例：默认检索当前项目资料，不读取正文。'};
await writeFile(path.join(output,'synthetic-state.json'),JSON.stringify(synthetic,null,2));
const verify=`import {readFile,readdir} from 'node:fs/promises';
import {createHash} from 'node:crypto';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
const root=path.dirname(fileURLToPath(import.meta.url));
const current=JSON.parse(await readFile(path.join(root,'CURRENT.json'),'utf8'));
async function walk(dir=''){let result=[];for(const e of await readdir(path.join(root,dir),{withFileTypes:true})){if(e.isSymbolicLink())throw Error('Linked file refused');const p=path.posix.join(dir,e.name);result.push(...(e.isDirectory()?await walk(p):[p]));}return result;}
const actual=(await walk()).filter(p=>p!=='CURRENT.json').sort(), expected=current.files.map(f=>f.path).sort();
if(JSON.stringify(actual)!==JSON.stringify(expected))throw Error('Package file set mismatch');
for(const f of current.files){if(f.path.startsWith('/')||f.path.split('/').includes('..'))throw Error('Invalid package path');const b=await readFile(path.join(root,f.path));if(b.length!==f.bytes||createHash('sha256').update(b).digest('hex')!==f.sha256)throw Error('Hash mismatch: '+f.path);}
console.log(JSON.stringify({baselineId:current.baselineId,verified:true,fileCount:expected.length,note:'File integrity only; no runtime or model validation.'}));
`;
await writeFile(path.join(output,'verify-handoff.mjs'),verify);
const screenshots=files.filter(f=>f.target.startsWith('screenshots/'));
const gallery='<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>LingDoc 设计交接截图</title><style>body{max-width:1400px;margin:24px auto;padding:0 20px;font:16px system-ui;background:#f4f5f7;color:#20242c}img{max-width:100%;border:1px solid #ccd2db}figure{margin:24px 0}figcaption{margin:8px 0}</style><h1>LingDoc 原生设计交接</h1><p>截图仅为视觉证据。请先阅读 START-HERE.md 与 DESIGN.md，并运行 node verify-handoff.mjs 校验本包。</p>'+screenshots.map((f,i)=>'<figure><figcaption>截图 '+(i+1)+'</figcaption><a href="'+f.target+'"><img loading="lazy" src="'+f.target+'" alt="原生界面截图 '+(i+1)+'"></a></figure>').join('')+'</html>';
await writeFile(path.join(output,'index.html'),gallery);
const entries=[];
for(const target of [...files.map(f=>f.target),'synthetic-state.json','verify-handoff.mjs','index.html']){const b=await readFile(path.join(output,target));entries.push({path:target,bytes:b.length,sha256:sha(b)});}
let head='unknown';try{head=execFileSync('git',['rev-parse','HEAD'],{cwd:root,encoding:'utf8',windowsHide:true}).trim();}catch{}
await writeFile(path.join(output,'CURRENT.json'),JSON.stringify({baselineId:options.baseline,builtAt:new Date().toISOString(),gitHead:head,sourceMode:'working-tree-including-uncommitted',scope:'selected-source-design-handoff-not-full-build',files:entries},null,2));
execFileSync(process.execPath,[path.join(output,'verify-handoff.mjs')],{stdio:'inherit',windowsHide:true});
// Archive only exact manifest entries; no recursive traversal or shell interpolation.
const zipScript="import json,pathlib,sys,zipfile\np=pathlib.Path(sys.argv[1]);m=json.loads((p/'CURRENT.json').read_text(encoding='utf-8'))\nwith zipfile.ZipFile(str(p)+'.zip','x',zipfile.ZIP_DEFLATED) as z:\n for n in ['CURRENT.json']+[f['path'] for f in m['files']]: z.write(p/n,n)\n";
execFileSync('python',['-c',zipScript,output],{stdio:'inherit',windowsHide:true});
console.log(JSON.stringify({output,zip:output+'.zip',zipSha256:sha(await readFile(output+'.zip')),baselineId:options.baseline}));
