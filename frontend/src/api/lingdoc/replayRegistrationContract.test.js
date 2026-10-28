import {createRequire} from 'node:module'
import {fileURLToPath} from 'node:url'
const require=createRequire(import.meta.url),assert=require('node:assert/strict'),test=require('node:test'),fs=require('node:fs'),path=require('node:path'),ts=require('typescript'),Ajv=require('ajv')
const folder=path.dirname(fileURLToPath(import.meta.url)),root=path.resolve(folder,'../../../..')
const spec=JSON.parse(fs.readFileSync(path.join(root,'docs/08-本轮实施方案/contracts/openapi.json'),'utf8'))
const registrationPath='/template-catalog/profiles/{templateId}/versions/{version}/replay-fixtures'
const coveragePath='/template-catalog/profiles/{templateId}/versions/{version}/replay-coverage'
const registration=spec.paths[registrationPath]?.post
const coverage=spec.paths[coveragePath]?.get
const ajv=new Ajv({strict:false,validateFormats:false})
const withComponents=schema=>({...schema,components:spec.components})
const envelope=data=>({data,request_id:'contract-example',meta:{replayed:false,refresh_required:false}})
const fixtureInput={fixture_id:'org-goal-present-1-write_check-positive-1',rule_id:'goal-present',scene:'write_check',polarity:'positive',target_ref:'field/goal',input:{project_id:'replay-synthetic',target_version:'1',fields:{goal:'已确认研究目标'}},expected_status:'PASS',review_reference:'review-record-2026-001'}
const coverageView={template_id:'template-demo',version:'3',content_hash:'sha:content',policy_hash:'sha:policy',complete:false,
 entries:[{rule_id:'goal-present',scene:'write_check',positive_covered:true,negative_covered:false,declared_positive_example_refs:['ex-1'],declared_negative_example_refs:[],unsatisfied_declared_example_refs:['ex-1']}],
 unsatisfied_declared_example_refs:['ex-1']}

test('registration is a manager POST with replay evidence family and the shared coverage readback is a read-only GET',()=>{
 assert.equal(registration.operationId,'registerReplayFixtures');assert.equal(coverage.operationId,'readReplayCoverage')
 assert.equal(coverage.requestBody,undefined)
 for (const code of ['400','401','404','409','422','503']) { assert.ok(registration.responses[code],`registration ${code}`); assert.ok(coverage.responses[code],`coverage ${code}`) }
 assert.ok(registration.responses['201']);assert.ok(coverage.responses['200'])
 assert.equal(registration.requestBody.content['application/json'].schema.$ref,'#/components/schemas/RegisterReplayFixturesInput')
 const routes=fs.readFileSync(path.join(root,'internal/lingdoc/workspace/template_governance_routes.go'),'utf8')
 assert.ok(routes.includes('catalog.POST("/profiles/:templateId/versions/:version/replay-fixtures", h.registerReplayFixtures)'))
 assert.ok(routes.includes('catalog.GET("/profiles/:templateId/versions/:version/replay-coverage", h.replayCoverage)'))
})

test('registration body validates against the published schema and rejects client-declared pins',()=>{
 const validate=ajv.compile(withComponents(registration.requestBody.content['application/json'].schema))
 const body={template_id:'template-demo',version:'3',reason:'补齐反例',fixtures:[fixtureInput]}
 assert.equal(validate(body),true,JSON.stringify(validate.errors))
 const claimedPin=structuredClone(body);claimedPin.fixtures[0].content_hash='sha:client-claimed'
 assert.equal(validate(claimedPin),false,'client-declared hashes must not be part of the contract')
 const badStatus=structuredClone(body);badStatus.fixtures[0].expected_status='MAYBE'
 assert.equal(validate(badStatus),false)
 const emptyBatch=structuredClone(body);emptyBatch.fixtures=[]
 assert.equal(validate(emptyBatch),false)
 const missingReference=structuredClone(body);delete missingReference.fixtures[0].review_reference
 assert.equal(validate(missingReference),false,'review_reference is mandatory per fixture')
})

test('coverage readback response carries the matrix, the declared-ref satisfaction and the server pins',()=>{
 const validate=ajv.compile(withComponents(coverage.responses['200'].content['application/json'].schema))
 assert.equal(validate(envelope(coverageView)),true,JSON.stringify(validate.errors))
 const noMatrix=structuredClone(envelope(coverageView));delete noMatrix.data.unsatisfied_declared_example_refs
 assert.equal(validate(noMatrix),false)
})

test('actual TypeScript client posts the batch with an Idempotency-Key and reads coverage without one',async()=>{
 const calls=[],box={exports:{}};const client=path.join(folder,'templateGovernance.ts')
 const js=ts.transpileModule(fs.readFileSync(client,'utf8'),{fileName:client,compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
 new Function('require','module','exports',js)(id=>{assert.equal(id,'@/utils/request');return {
  get:(...args)=>{calls.push(['GET',...args]);return Promise.resolve(envelope(coverageView))},
  post:(...args)=>{calls.push(['POST',...args]);return Promise.resolve(envelope({template_id:'template-demo',version:'3',content_hash:'sha:content',policy_hash:'sha:policy',registered:[],coverage:{complete:false,entries:[],unsatisfied_declared_example_refs:[]}}))}
 }},box,box.exports)
 const input={template_id:'template-demo',version:'3',fixtures:[fixtureInput],reason:'补齐反例'}
 await box.exports.registerReplayFixtures(input,'idem-key-1')
 assert.deepEqual(calls[0],['POST','/api/v1/lingdoc/template-catalog/profiles/template-demo/versions/3/replay-fixtures',input,{headers:{'Idempotency-Key':'idem-key-1'}}])
 await box.exports.readReplayCoverage('template-demo','3')
 assert.deepEqual(calls[1],['GET','/api/v1/lingdoc/template-catalog/profiles/template-demo/versions/3/replay-coverage'])
})
