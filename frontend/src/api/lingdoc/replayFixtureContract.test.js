import {createRequire} from 'node:module'
import {fileURLToPath} from 'node:url'
const require=createRequire(import.meta.url),assert=require('node:assert/strict'),test=require('node:test'),fs=require('node:fs'),path=require('node:path'),ts=require('typescript'),Ajv=require('ajv')
const folder=path.dirname(fileURLToPath(import.meta.url)),root=path.resolve(folder,'../../../..')
const spec=JSON.parse(fs.readFileSync(path.join(root,'docs/08-本轮实施方案/contracts/openapi.json'),'utf8'))
const operation=spec.paths['/template-catalog/replay-fixtures']?.get
const ajv=new Ajv({strict:false,validateFormats:false})
const schema=()=>({...operation.responses['200'].content['application/json'].schema,components:spec.components})
const fixture={fixture_id:'demo-source-preflight-positive',rule_id:'source',scene:'preflight',polarity:'positive',target_ref:'project',ruleset:{ruleset_id:'ruleset-demo',version:'1'},template_id:'template-demo',template_version:'1'}
const envelope=data=>({data,request_id:'contract-example',meta:{replayed:false,refresh_required:false}})
test('read-only fixture catalog path publishes metadata and the standard error envelope',()=>{
 assert.equal(operation.operationId,'listReplayFixtures');assert.equal(operation.requestBody,undefined)
 assert.ok(operation.responses['401']);assert.ok(operation.responses['403']);assert.ok(operation.responses['503'])
 assert.deepEqual(spec.security,[{bearerAuth:[]}])
 const http=fs.readFileSync(path.join(root,'internal/lingdoc/workspace/g4_full_http.go'),'utf8')
 assert.ok(http.includes('catalog.GET("/replay-fixtures", h.listReplayFixtures)'))
})
test('metadata envelope accepts exact pins and an explicitly empty catalog',()=>{
 const validate=ajv.compile(schema());assert.equal(validate(envelope([fixture])),true,JSON.stringify(validate.errors));assert.equal(validate(envelope([])),true)
})
test('missing pins, unknown polarity and fixture source payload are not contract metadata',()=>{
 const validate=ajv.compile(schema())
 const missing=structuredClone(fixture);delete missing.ruleset.version
 assert.equal(validate(envelope([missing])),false)
 assert.equal(validate(envelope([{...fixture,polarity:'unchecked'}])),false)
 assert.equal(validate(envelope([{...fixture,input:{secretSourceText:'must stay server side'}}])),false)
 assert.equal(validate({data:[fixture]}),false)
})
test('actual TypeScript client uses the published GET without a mutation or idempotency key',async()=>{
 const calls=[],box={exports:{}};const client=path.join(folder,'templateGovernance.ts')
 const js=ts.transpileModule(fs.readFileSync(client,'utf8'),{fileName:client,compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
 const expected=envelope([fixture]);new Function('require','module','exports',js)(id=>{assert.equal(id,'@/utils/request');return {get:(...args)=>{calls.push(args);return Promise.resolve(expected)},post:()=>assert.fail('Catalog listing must not write')}},box,box.exports)
 assert.deepEqual(await box.exports.listReplayFixtures(),expected);assert.deepEqual(calls,[['/api/v1/lingdoc/template-catalog/replay-fixtures']])
})
