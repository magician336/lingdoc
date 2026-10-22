import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
const require = createRequire(import.meta.url)
const __filename = fileURLToPath(import.meta.url)
const __dirname = require('node:path').dirname(__filename)

// Actual Vue script/template and the actual typed API module. HTTP is an
// explicit synthetic fixture; no browser, live backend or model call happens.
const test=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),Module=require('node:module')
const frontend=path.resolve(__dirname,'../../..'),load=Module.createRequire(path.join(frontend,'package.json')),ts=load('typescript'),compiler=load('@vue/compiler-sfc'),vue=load('vue'),{renderToString}=load('@vue/server-renderer')
let fixture,nonce=0
function request(method,url,body,config){
 fixture.requests.push({method,url,body:body&&JSON.parse(JSON.stringify(body)),config:config&&JSON.parse(JSON.stringify(config))})
 return Promise.resolve({data:[],meta:{replayed:false,refresh_required:false}})
}
function compile(filename,source,resolve){const m=new Module(filename);m.paths=Module._nodeModulePaths(frontend);m.require=resolve||load;m._compile(ts.transpileModule(source??fs.readFileSync(filename,'utf8'),{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022,esModuleInterop:true}}).outputText,filename);return m.exports}
const api=compile(path.join(frontend,'src/api/lingdoc/writingAction.ts'),undefined,id=>id==='@/utils/request'?{get:(url,c)=>request('GET',url,undefined,c)}:load(id))
const source=path.join(__dirname,'GenerationActionPanel.vue'),parsed=compiler.parse(fs.readFileSync(source,'utf8'));assert.deepEqual(parsed.errors,[])
const script=compiler.compileScript(parsed.descriptor,{id:'g7-action-panel-test'}),template=compiler.compileTemplate({source:parsed.descriptor.template.content,filename:source,id:'g7-action-panel-test',compilerOptions:{bindingMetadata:script.bindings},isTS:true});assert.deepEqual(template.errors,[])
const panel=compile(source,script.content,id=>id==='@/api/lingdoc/writingAction'?api:load(id)).default;panel.render=compile(source+'.template',template.code).render

// The action table fixture is the known-good table from the development plan:
// rewrite runs on a selection, takes a length and a tone, produces prose and
// may be adopted at the three targets.  Continue runs on a selection too and
// takes the same two output parameters.  Compress also runs on a selection but
// takes a ratio (a continuous dimension: min/max, no value list) and a tone.
function actionTable(){
 return [{
  action:'rewrite',label:'改写',scopes:['section'],
  output_params:[
   {name:'length',values:['short','medium','long'],min:0,max:0},
   {name:'tone',values:['neutral','formal','academic','plain'],min:0,max:0},
  ],
  produce:'prose',
  targets:['working_copy_partial','working_copy_whole','formal_version_whole'],
 },{
  action:'continue',label:'续写',scopes:['section'],
  output_params:[
   {name:'length',values:['short','medium','long'],min:0,max:0},
   {name:'tone',values:['neutral','formal','academic','plain'],min:0,max:0},
  ],
  produce:'prose',
  targets:['working_copy_partial','working_copy_whole','formal_version_whole'],
 },{
  action:'compress',label:'压缩',scopes:['section'],
  output_params:[
   {name:'ratio',values:[],min:10,max:90},
   {name:'tone',values:['neutral','formal','academic','plain'],min:0,max:0},
  ],
  produce:'prose',
  targets:['working_copy_partial','working_copy_whole','formal_version_whole'],
 }]
}
async function render(overrides){
 const props=vue.reactive({projectId:'p1',scope:'section',action:'rewrite',actionTable:actionTable(),...overrides})
 const events=[];let controller
 const component={...panel,setup(input,context){controller=panel.setup(input,context);return controller}}
 const html=await renderToString(vue.createSSRApp(component,{...props,onSubmit:payload=>events.push(payload)}))
 return {html,events,controller,props}
}

test('the panel shows the action, the allowed scope, the output parameters, the produce shape and the adoption targets before the run',async()=>{
 const {html,fixtureRequests}=await render()
 assert.match(html,/动作/);assert.match(html,/改写/);assert.match(html,/rewrite/)
 assert.match(html,/允许的目标范围/);assert.match(html,/选段/)
 assert.match(html,/可用的产出参数/);assert.match(html,/length/);assert.match(html,/tone/);assert.match(html,/short/);assert.match(html,/formal/)
 assert.match(html,/产出形状/);assert.match(html,/候选正文/)
 assert.match(html,/允许的采纳目标/);assert.match(html,/工作副本·部分/);assert.match(html,/正式版本·整段/)
})

test('the panel sends no request while an output parameter value is outside the closed value set',async()=>{
 const {events,controller}=await render()
 controller.chosen.value={length:'huge',tone:'formal'}
 controller.submit()
 assert.equal(events.length,0)
})

test('the panel sends no request while an output parameter is still missing',async()=>{
 const {events,controller}=await render()
 controller.chosen.value={length:'short'}
 controller.submit()
 assert.equal(events.length,0)
})

test('the panel emits the action, the scope and the output parameters once they are legal',async()=>{
 const {events,controller}=await render()
 controller.chosen.value={length:'short',tone:'formal'}
 controller.note.value='别动第二段'
 controller.submit()
 assert.equal(events.length,1)
 assert.equal(events[0].action,'rewrite')
 assert.equal(events[0].scope,'section')
 assert.deepEqual(events[0].output_params,{length:'short',tone:'formal'})
 assert.equal(events[0].instruction,'别动第二段')
})

test('the panel renders the action table the server gave instead of a table of its own',async()=>{
 const synthetic=[{action:'synthetic-action',label:'合成动作',scopes:['chapter'],output_params:[],produce:'outline',targets:['working_copy_whole']}]
 const {html}=await render({action:'synthetic-action',actionTable:synthetic})
 assert.match(html,/合成动作/);assert.match(html,/章节骨架/);assert.match(html,/工作副本·整段/)
 assert(!html.includes('改写'))
})

test('the panel shows the continue action with its own output parameters before the run',async()=>{
 const {html,events,controller}=await render({action:'continue'})
 assert.match(html,/续写/);assert.match(html,/continue/)
 assert.match(html,/选段/);assert.match(html,/候选正文/)
 assert.match(html,/length/);assert.match(html,/tone/)
 controller.chosen.value={length:'medium',tone:'academic'}
 controller.submit()
 assert.equal(events.length,1)
 assert.equal(events[0].action,'continue')
 assert.deepEqual(events[0].output_params,{length:'medium',tone:'academic'})
})

test('the panel shows the compress action with its ratio interval and tone before the run',async()=>{
 const {html,events,controller}=await render({action:'compress'})
 assert.match(html,/压缩/);assert.match(html,/compress/)
 assert.match(html,/选段/);assert.match(html,/候选正文/)
 assert.match(html,/ratio/);assert.match(html,/tone/)
 // The ratio is a continuous dimension: the interval is visible before the run.
 assert.match(html,/min="10"/);assert.match(html,/max="90"/)
 controller.ratio.value=40
 controller.chosen.value={tone:'formal'}
 controller.submit()
 assert.equal(events.length,1)
 assert.equal(events[0].action,'compress')
 assert.deepEqual(events[0].output_params,{ratio:40,tone:'formal'})
})

test('the panel sends no request while the ratio is outside the closed interval',async()=>{
 const {events,controller}=await render({action:'compress'})
 controller.ratio.value=95
 controller.chosen.value={tone:'formal'}
 controller.submit()
 assert.equal(events.length,0)
})

test('the panel sends no request while the ratio is still missing',async()=>{
 const {events,controller}=await render({action:'compress'})
 controller.chosen.value={tone:'formal'}
 controller.submit()
 assert.equal(events.length,0)
})
