import test from 'node:test'
import assert from 'node:assert/strict'
import {copyMatchesFormal,committedRevision,currentAnchorRange} from './documentState'
const formal={body_markdown:'中文😀正文',source_ids:['s2','s1'],review_items:[{id:'r',statement:'待核',source_ids:['s1'],anchor:{status:'located',start_utf16:0,end_utf16:2,quote_markdown:'中文',working_copy_revision:3,chapter_version_id:'v'}}],citation_usages:[],current_version_id:'v'}
const copy={...structuredClone(formal),current_version_id:undefined,base_chapter_version_id:'v',working_copy_revision:4}
copy.review_items[0].anchor.working_copy_revision=4
test('actual commit revision shift alone does not leave a false draft difference',()=>{assert.equal(copyMatchesFormal(copy,formal),true);assert.equal(committedRevision(copy,formal),4)})
test('semantic anchor status, quote, version and statement changes remain draft changes',()=>{
 for(const field of ['status','quote_markdown','chapter_version_id'] as const){const c=structuredClone(copy);c.review_items[0].anchor[field]='changed';assert.equal(copyMatchesFormal(c,formal),false)}
 const c=structuredClone(copy);c.review_items[0].statement='新事实';assert.equal(copyMatchesFormal(c,formal),false)
})
test('persisted working copy is never treated as formal just because it has a revision',()=>{const c={...copy,body_markdown:'未提交稿'};assert.equal(committedRevision(c,formal),0);assert.equal(committedRevision(copy,{...formal,current_version_id:null}),0)})
test('located anchor requires exact current slice and never falls back to the first repeated quote',()=>{
 assert.deepEqual(currentAnchorRange('甲乙甲乙',{status:'located',start_utf16:2,end_utf16:4,quote_markdown:'甲乙'}),{from:2,to:4,quote:'甲乙'})
 assert.equal(currentAnchorRange('已经编辑',{status:'located',start_utf16:0,end_utf16:2,quote_markdown:'甲乙'}),null)
 assert.equal(currentAnchorRange('甲乙',{status:'stale',start_utf16:0,end_utf16:2,quote_markdown:'甲乙'}),null)
})
