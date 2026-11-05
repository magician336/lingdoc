import {readFile} from 'node:fs/promises';
import {createHash} from 'node:crypto';
import assert from 'node:assert/strict';
const root=new URL('.',import.meta.url);
const manifest=JSON.parse(await readFile(new URL('CURRENT.json',root),'utf8'));
for(const file of manifest.files){assert(!file.path.includes('..')&&!file.path.includes('/'));const bytes=await readFile(new URL(file.path,root));assert.equal(createHash('sha256').update(bytes).digest('hex'),file.sha256,'Changed file: '+file.path)}
const data=JSON.parse(await readFile(new URL('统一演示场景.json',root),'utf8'));
assert.equal(data.baselineId,manifest.baselineId);
const versions=new Set(data.sourceVersions.map(x=>x.versionId));
const targets=new Set([...data.document.sections.map(x=>x.id),data.figure.id]);
for(const snapshot of data.researchSnapshots)for(const evidence of snapshot.evidence){assert(versions.has(evidence.sourceVersionId));assert(data.sourceVersions.find(x=>x.versionId===evidence.sourceVersionId).paragraphs.some(x=>x.id===evidence.paragraphId))}
for(const dep of data.dependencies){assert(versions.has(dep.from));assert(targets.has(dep.to))}
for(const task of data.reviewTasks){assert.equal(task.changeId,data.change.id);assert(data.dependencies.some(x=>x.id===task.dependencyId&&x.to===task.target));assert(targets.has(task.target))}
assert(versions.has(data.change.fromVersionId)&&versions.has(data.change.toVersionId));
console.log(JSON.stringify({baselineId:manifest.baselineId,files:manifest.files.length,hashes:'pass',scenarioReferences:'pass',implementationVerified:false}));
