import {createRequire} from 'node:module'
import {fileURLToPath} from 'node:url'
const require=createRequire(import.meta.url)
const assert=require('node:assert/strict'),test=require('node:test'),fs=require('node:fs'),path=require('node:path'),ts=require('typescript')
const folder=path.dirname(fileURLToPath(import.meta.url))
const source=fs.readFileSync(path.join(folder,'Workspace.vue'),'utf8')
const start=source.indexOf('async function showCandidateSource(')
const end=source.indexOf('\nasync function refreshSource(',start)
assert.ok(start>=0&&end>start,'Compile the actual product handler, not a duplicate implementation')
const js=ts.transpileModule(source.slice(start,end),{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.CommonJS}}).outputText
function harness({denied=false,switchDuringContext=false,busy=false}={}){
 const calls=[],state={project:{value:{id:'project-a'}},busy:{value:busy},contextBusy:{value:false},errorMessage:{value:''},sources:{value:[]},inspTab:{value:'ai'},inspOpen:{value:false},sourceListElement:{value:{scrollIntoView:()=>calls.push('scroll')}}}
 const mocks={...state,getSource:async(id,sourceID)=>{calls.push(['protected-read',id,sourceID]);if(denied)throw Error('source_access_denied');return {data:{id:sourceID,asset_revision:2}}},loadSourceContext:async()=>{calls.push('context');if(switchDuringContext)state.project.value={id:'project-b'}},nextTick:async()=>calls.push('paint'),failure:e=>calls.push(['failure',e.message])}
 const handler=new Function(...Object.keys(mocks),js+'\nreturn showCandidateSource;')(...Object.values(mocks))
 return {state,calls,handler}
}
test('candidate source click reveals the source pane after successful current protected reads',async()=>{
 const h=harness();await h.handler('source-a')
 assert.equal(h.state.inspTab.value,'sources');assert.equal(h.state.inspOpen.value,true)
 assert.deepEqual(h.calls.slice(0,2),[['protected-read','project-a','source-a'],'context'])
 assert.equal(h.calls.at(-1),'scroll');assert.equal(h.state.busy.value,false)
 assert.equal(h.state.sources.value[0].asset_revision,2)
})
test('denied current source reads do not reveal cached source content or switch panes',async()=>{
 const h=harness({denied:true});await h.handler('source-a')
 assert.equal(h.state.inspTab.value,'ai');assert.equal(h.state.inspOpen.value,false)
 assert.deepEqual(h.state.sources.value,[]);assert.deepEqual(h.calls.at(-1),['failure','source_access_denied'])
 assert.equal(h.state.busy.value,false)
})
test('a late source-context response from a previous project does not navigate the new project',async()=>{
 const h=harness({switchDuringContext:true});await h.handler('source-a')
 assert.equal(h.state.inspTab.value,'ai');assert.equal(h.state.inspOpen.value,false)
 assert.equal(h.calls.includes('scroll'),false);assert.equal(h.state.busy.value,false)
})
test('busy source operations do not start a duplicate read or switch the writing pane',async()=>{
 const h=harness({busy:true});await h.handler('source-a')
 assert.deepEqual(h.calls,[]);assert.equal(h.state.inspTab.value,'ai')
})
