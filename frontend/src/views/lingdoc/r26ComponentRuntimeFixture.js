import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import crypto from 'node:crypto'

// Full real SFC + render function on Vue's client renderer. API modules execute
// unchanged through a synthetic request transport; no setup function is exposed
// to tests and no component business logic is copied into this host.
const here=path.dirname(fileURLToPath(import.meta.url))
const frontend=process.env.LINGDOC_R26_FRONTEND_ROOT || path.resolve(here,'../../..')
const require=createRequire(path.join(frontend,'package.json'))
export const Vue=require('vue')
const ts=require('typescript'),SFC=require('@vue/compiler-sfc')
export const response=data=>({data,request_id:'synthetic-r26',meta:{replayed:false,refresh_required:false}})
export const clone=value=>value===undefined?undefined:JSON.parse(JSON.stringify(value))
export const deferred=()=>{let resolve,reject;const promise=new Promise((a,b)=>{resolve=a;reject=b});return {promise,resolve,reject}}
export const flush=async()=>{for(let i=0;i<5;i++){await Vue.nextTick();await new Promise(resolve=>setImmediate(resolve))}}
export function project(id='p',status='active'){
 return {id,name:'Synthetic '+id,status,project_version:7,spec_revision:3,current_context_revision:4,
 spec:{topic:'Saved topic',goal:'Saved goal'},template_id:'template',template_version:'1',template_copy_version:1,
 template_copy:{version:1,ruleset_hash:'synthetic-rules',definition:{id:'template',version:'1',fields:[{field_id:'topic',label:'主题',type:'string'},{field_id:'goal',label:'目标',type:'string'}]}},
 members:[{user_id:'u',role:'owner',status:'active'}]}
}
export const text=node=>node.type==='#comment'?'':(node.text||'')+(node.children||[]).map(text).join('')
export const nodes=node=>[node,...(node.children||[]).flatMap(nodes)]
function element(type){
 return {type,tagName:type.toUpperCase(),children:[],props:{},listeners:{},parent:null,value:'',checked:false,multiple:false,selectedIndex:-1,
  get options(){return this.children.flatMap(n=>n.type==='option'?[n]:n.type==='optgroup'?n.children:[])},
  get selectedOptions(){return this.options.filter(n=>n.selected)},
  getRootNode(){return global.document},getAttribute(k){return this.props[k]},
  addEventListener(k,h){(this.listeners[k]??=[]).push(h)},removeEventListener(k,h){this.listeners[k]=(this.listeners[k]||[]).filter(fn=>fn!==h)},
  dispatchEvent(e){for(const handler of this.listeners[e.type]||[])handler(e);return true},
  showModal(){this.open=true},close(){this.open=false},querySelector(){return null},
  focus(){global.document.activeElement=this},getClientRects(){return [{}]}}
}
const host=Vue.createRenderer({
 createElement:element,createText:text=>({type:'#text',text,children:[],parent:null}),createComment:text=>({type:'#comment',text,children:[],parent:null}),
 setElementText(n,t){n.text=t;n.children=[]},setText(n,t){n.text=t},
 patchProp(n,k,_old,v){n.props[k]=v;if(['value','checked','selected','multiple','disabled','type'].includes(k))n[k]=v;if(k==='value')n._value=v},
 insert(n,p,a=null){if(n.parent){const old=n.parent.children.indexOf(n);if(old>=0)n.parent.children.splice(old,1)}const at=a?p.children.indexOf(a):-1;p.children.splice(at<0?p.children.length:at,0,n);n.parent=p},
 remove(n){if(n.parent){const i=n.parent.children.indexOf(n);if(i>=0)n.parent.children.splice(i,1)}n.parent=null},parentNode:n=>n.parent,
 nextSibling:n=>n.parent?.children[n.parent.children.indexOf(n)+1]||null,
})
const compile=source=>ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022,esModuleInterop:true}}).outputText
export function mountPanel(name,options={}){
 const calls=[],events=[],errors=[],cache=new Map(),auth=Vue.reactive({currentUserId:'u',effectiveTenantId:1})
 const holder=Vue.reactive({project:project(),readonly:false,currentUserId:'u',selectedEvidence:[],...options.props})
 global.document={activeElement:null};global.HTMLElement=class {};global.Document=class {};global.ShadowRoot=class {}
 const send=async(method,url,body,config)=>{
  const call={method,url,body:clone(body),config};calls.push(call)
  const custom=await options.transport?.(call)
  if(custom!==undefined)return custom
  if(method!=='GET')throw new Error('Unexpected synthetic write '+method+' '+url)
  if(url.endsWith('/permissions'))return response({allowed:true,capability:'manage',project_version:holder.project.project_version,context_revision:holder.project.current_context_revision})
  if(url==='/api/v1/lingdoc/projects/'+holder.project.id)return response(clone(holder.project))
  return response(url.endsWith('/releases')?{items:[]}:[])
 }
 const request={get:(url,config)=>send('GET',url,undefined,config),post:(url,body={},config)=>send('POST',url,body,config),postNoBody:(url,config)=>send('POST',url,undefined,config),put:(url,body,config)=>send('PUT',url,body,config),del:(url,body)=>send('DELETE',url,body)}
 function resolveLocal(base){
  const file=[base,base+'.ts',base+'.js',path.join(base,'index.ts'),path.join(base,'index.js')].find(value=>fs.existsSync(value)&&fs.statSync(value).isFile())
  assert.ok(file,'actual local module '+base);return file
 }
 function evaluate(source,file){
  const module={exports:{}}
  const load=id=>{
   if(id==='vue')return Vue
   if(id==='@/stores/auth')return {useAuthStore:()=>auth}
   if(id==='@/utils/request')return request
   if(id.startsWith('@/api/'))return local(resolveLocal(path.join(frontend,'src',id.slice(2))))
   if(id.startsWith('@/composables/'))return local(resolveLocal(path.join(frontend,'src',id.slice(2))))
   if(id.startsWith('.')){
    const resolved=resolveLocal(path.resolve(path.dirname(file),id))
    if(resolved===path.join(frontend,'src/utils/request.ts'))return request
    // A child single-file component is compiled the same way the panel under
    // test is, so a panel that mounts another panel keeps rendering the real
    // component instead of having its source evaluated as plain TypeScript.
    if(resolved.endsWith('.vue'))return {default:compileComponent(resolved)}
    return local(resolved)
   }
   return require(id)
  }
  new Function('require','module','exports','crypto',compile(source))(load,module,module.exports,crypto)
  return module.exports
 }
 function compileComponent(file){
  const parsed=SFC.parse(fs.readFileSync(file,'utf8'),{filename:file})
  assert.deepEqual(parsed.errors,[])
  const script=SFC.compileScript(parsed.descriptor,{id:'r26-'+path.basename(file)})
  const template=SFC.compileTemplate({source:parsed.descriptor.template.content,filename:file,id:'r26-'+path.basename(file),compilerOptions:{bindingMetadata:script.bindings,hoistStatic:false}})
  assert.deepEqual(template.errors,[])
  return {...evaluate(script.content,file).default,render:evaluate(template.code,file).render}
 }
 function local(file){if(!cache.has(file))cache.set(file,evaluate(fs.readFileSync(file,'utf8'),file));return cache.get(file)}
 const filename=path.join(frontend,'src/views/lingdoc',name+'.vue'),parsed=SFC.parse(fs.readFileSync(filename,'utf8'),{filename})
 assert.deepEqual(parsed.errors,[])
 const script=SFC.compileScript(parsed.descriptor,{id:'r26-'+name}),template=SFC.compileTemplate({source:parsed.descriptor.template.content,filename,id:'r26-'+name,compilerOptions:{bindingMetadata:script.bindings,hoistStatic:false}})
 assert.deepEqual(template.errors,[])
 const component={...evaluate(script.content,filename).default,render:evaluate(template.code,filename).render}
 const root={type:'root',children:[]},app=host.createApp({render:()=>Vue.h(component,{...holder,onChanged:()=>events.push('changed'),onDirty:value=>events.push(['dirty',value])})})
 app.config.errorHandler=error=>errors.push(error)
 app.mount(root)
 const find=(predicate,label)=>{const node=nodes(root).find(predicate);assert.ok(node,label);return node}
 const event=(type,target)=>({type,target,currentTarget:target,preventDefault(){},stopPropagation(){}})
 const click=async(label)=>{
  const node=find(n=>n.type==='button'&&text(n).trim()===label,'rendered button '+label)
  assert.ok(!node.props.disabled,'enabled button '+label)
  const handler=node.props.onClick;assert.equal(typeof handler,'function','actual click handler '+label)
  await handler(event('click',node));await flush()
 }
 const field=(label,tag)=>{
  const parent=nodes(root).find(n=>n.type==='label'&&(n.text||'')+n.children.filter(c=>c.type==='#text').map(text).join('')===label)
  return parent&&nodes(parent).find(n=>['input','textarea','select'].includes(n.type)&&(!tag||n.type===tag))
 }
 const edit=async(node,value)=>{
  assert.ok(node,'rendered editable control');node.value=value
  node.dispatchEvent(event('input',node));await flush()
 }
 const select=async(node,value)=>{
  assert.ok(node,'rendered select');assert.equal(node.type,'select')
  assert.ok(node.options.some(o=>o._value===value||o.value===value),'actual option '+value)
  node.options.forEach(o=>{o.selected=(o._value??o.value)===value});node.value=value
  node.dispatchEvent(event('change',node));await flush()
 }
 return {root,holder,auth,calls,events,errors,click,field,edit,select,ui:()=>text(root),nodes:()=>nodes(root),
  find,close(){app.unmount();assert.deepEqual(errors,[],'no Vue/render/transport infrastructure error')}}
}
