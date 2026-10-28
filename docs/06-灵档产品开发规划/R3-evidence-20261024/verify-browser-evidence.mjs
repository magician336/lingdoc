import {readFile,writeFile} from 'node:fs/promises'
import assert from 'node:assert/strict'
const json=async name=>JSON.parse(await readFile(new URL(name,import.meta.url),'utf8'))
const dom=await json('browser-selected-dom.json'), http=await json('browser-http-captures.json')
const entry=http.captures.find(x=>x.response?.data?.correlation_id===dom.correlation)
assert.ok(entry,'DOM correlation must identify the exact browser response, not a separately generated preview')
const pkg=entry.response.data
assert.equal(entry.status,200)
assert.deepEqual(dom.cards.map(x=>x.id),pkg.card_refs.map(x=>x.id))
const list=http.captures.find(x=>x.path.endsWith('/evidence-cards')&&x.status===200).response.data
const labels={CANDIDATE:'候选',CONFIRMED:'已人工确认',NEEDS_REVIEW:'需复核',INSUFFICIENT:'证据不足',STALE:'已过期',SUPERSEDED:'已被替代'}
for(const [i,ref] of pkg.card_refs.entries()){const card=list.find(c=>c.id===ref.id&&c.version===ref.version);assert.ok(card);assert.equal(dom.cards[i].text,`${ref.id}v${ref.version}${labels[card.status]}`)}
assert.deepEqual(dom.reasons,pkg.selection_reasons)
assert.deepEqual(dom.sources.map(x=>x.id),pkg.source_refs.map(x=>x.source_id))
for(const [i,s] of pkg.source_refs.entries())assert.equal(dom.sources[i].text,`${s.locator||s.source_id}资料版本 v${s.asset_revision}${s.model_use_allowed?'模型使用已许可':'模型使用未许可'}`)
assert.deepEqual(dom.baseline,[`项目版本 v${pkg.project_version} · 规范修订 ${pkg.spec_revision} · 上下文修订 ${pkg.context_revision}`,`章节版本 ${pkg.chapter_version_id||'未关联'} · 任务版本 ${pkg.task_version??'未关联'} · 模板 ${pkg.template_id} v${pkg.template_version}`])
assert.equal(dom.authorization,pkg.authorization_state)
assert.deepEqual(dom.constraints,pkg.output_constraints)
const previews=http.captures.filter(x=>x.path.endsWith('/agent-context'))
assert.ok(previews.length>=5)
assert.deepEqual(previews.slice(0,5).map(x=>x.input.card_refs.length),[0,1,0,1,1])
assert.ok(previews.every(x=>x.input.sensitive_context_confirmed===false))
assert.ok(http.captures.every(x=>[200,409].includes(x.status)))
const suspended=await json('permission-suspend.json'),restored=await json('permission-restore.json')
assert.equal(suspended.read_status,404);assert.equal(suspended.preview_status,404)
assert.equal(restored.read_status,200);assert.equal(restored.preview_status,200)
const ax=async name=>readFile(new URL(name,import.meta.url),'utf8')
for(const name of ['browser-outage-ax.txt','browser-revoked-ax.txt']){const value=await ax(name);assert.match(value,/旧上下文包已清空/);assert.ok(!value.includes(pkg.card_refs[0].id));assert.ok(!value.includes(pkg.correlation_id))}
assert.match(await ax('browser-recovered-ax.txt'),/requires_confirmation/)
assert.match(await ax('browser-restored-conflict-ax.txt'),/版本基线已变/)
assert.match(await ax('browser-restored-empty-ax.txt'),/授权结论：allowed/)
const other=await ax('browser-other-project-ax.txt');assert.match(other,/尚无可加入的证据卡/);assert.ok(!other.includes(pkg.card_refs[0].id))
assert.ok(previews.some(x=>x.status===409&&x.response.error.code==='version_conflict'))
assert.ok(previews.some(x=>x.status===200&&x.response.data.project_id!==pkg.project_id&&x.response.data.card_refs.length===0))
const result={at:new Date().toISOString(),frontend:'a121b8b2',backend:'2f029fcaf0adcf4c9881d6f0d821559814f3cf92',mode:'real browser DOM vs same-request HTTP response, synthetic manual original',result:'PASS',checks:['exact correlation','card ids/versions/status','reasons verbatim/in order','source ids/locators/revisions/permission','all visible version baselines','authorization and output constraints','open/add/remove/add/refresh actual request sequence'],correlation:dom.correlation,project_id:pkg.project_id,sequence:previews.slice(0,5).map(x=>({path:x.path,selected:x.input.card_refs.length,status:x.status})),limitations:['No model or embedding calls','Uploaded-storage-URI anchor adapter failure retained; real manual material path used instead','Screenshot raw controller canvas retained with AX/DOM evidence']}
result.checks.push('actual service outage clears facts and explicit refresh recovers','real member suspension rejects read/preview 404 and removes old package','restore does not silently rebase stale card; 409 then explicit removal recovers','other project has independent empty package')
await writeFile(new URL('browser-comparison.json',import.meta.url),JSON.stringify(result,null,2))
console.log(JSON.stringify(result,null,2))
