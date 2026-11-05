import test from 'node:test';
import assert from 'node:assert/strict';
import {createGoSources} from './go-sources.mjs';

test('text imports explicitly disable all supported generative process overrides',async()=>{
 let form;
 const fetchImpl=async(url,options)=>{
  const route=new URL(url).pathname;let data;
  if(route.endsWith('/bootstrap'))data={projectId:'p1',knowledgeBaseId:'kb1',localOnly:true};
  else if(route.endsWith('/knowledge/file')){form=options.body;data={id:'k1',parse_status:'completed'};}
  else if(route.endsWith('/assets'))data={id:'a1',knowledge_id:'k1'};
  else if(route==='/api/v1/chunks/k1')data=[{id:'s1',chunk_type:'text'}];
  else if(route.endsWith('/sources/s1/document'))data={source:{id:'s1',asset_id:'a1',status:'available'},text:'研究资料',segments:[]};
  else throw Error('Unexpected request '+route);
  return {ok:true,status:200,json:async()=>({data})};
 };
 const sources=createGoSources({goOrigin:'http://127.0.0.1:12345',origin:'http://127.0.0.1:12346',fetchImpl});
 await sources.uploadText({title:'资料.txt',text:'研究资料',key:'import-test-001'});
 assert.equal(form.get('enable_multimodel'),'false');
 assert.deepEqual(JSON.parse(form.get('process_config')),{enable_multimodel:false,question_generation_config:{enabled:false},graph_enabled:false,extract_config:{enabled:false}});
 assert.equal(await form.get('file').text(),'研究资料');
});


test('only source-read denial after successful local upload and binding receives readonly classification',async()=>{
 for(const [status,code,expected] of [[403,'source_access_denied','SOURCE_ORIGINAL_UNVERIFIABLE'],[403,'forbidden','forbidden'],[401,'unauthorized','unauthorized'],[500,'server_error','server_error']]){
  const adapter=createGoSources({goOrigin:'http://127.0.0.1:12345',origin:'http://127.0.0.1:12346',fetchImpl:async(url,opts)=>{
   const p=new URL(url).pathname;
   if(p.endsWith('/document'))return {ok:false,status,json:async()=>({error:{code,message:'read failed'}})};
   let data=p.endsWith('/bootstrap')?{projectId:'p1',knowledgeBaseId:'kb1',localOnly:true}:p.endsWith('/knowledge/file')?{id:'k1',parse_status:'completed'}:p.endsWith('/assets')?{id:'a1',knowledge_id:'k1'}:[{id:'s1',chunk_type:'text'}];
   return {ok:true,status:200,json:async()=>({data})};
  }});
  await assert.rejects(()=>adapter.uploadText({title:'x.txt',text:'body',key:'boundary-001'}),e=>e.code===expected&&e.knowledgeId==='k1'&&e.assetId==='a1');
 }
});
