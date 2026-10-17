import assert from 'node:assert/strict'
import test from 'node:test'
import {getSchema} from '@tiptap/core'
import StarterKit from '@tiptap/starter-kit'
import {Node} from '@tiptap/pm/model'
import {SourceRef,RawBlock,ChapterTitle} from './nodes'
import {chaptersToDocJson} from './docJson'
import {markdownRangeToPm,savedMarkdownOf} from './pmSerialize'
import {sortChaptersByTemplate} from './documentState'
const schema=getSchema([StarterKit.configure({heading:{levels:[2,3,4]}}),SourceRef,RawBlock,ChapterTitle])
const doc=(body:string)=>Node.fromJSON(schema,chaptersToDocJson([{id:'c1',title:'第一章',body_markdown:body}]) as never)
test('review range maps full bold delimiters onto the exact visible text',()=>{
 const d=doc('前文 **载入正文** 后文'),md=savedMarkdownOf(d,'c1')!,at=md.indexOf('**载入正文**')
 const r=markdownRangeToPm(d,'c1',at,at+8,'**载入正文**')
 assert.ok(r);assert.equal(d.textBetween(r.from,r.to),'载入正文')
})
test('duplicate review quotes preserve the specified second range instead of guessing',()=>{
 const d=doc('目标句，前文目标句。'),md=savedMarkdownOf(d,'c1')!,at=md.lastIndexOf('目标句')
 const r=markdownRangeToPm(d,'c1',at,at+3,'目标句')!
 const first=markdownRangeToPm(d,'c1',0,3,'目标句')!
 assert.ok(r.from>first.from);assert.equal(d.textBetween(r.from,r.to),'目标句')
 assert.equal(markdownRangeToPm(d,'c1',at,at+3,'旧目标'),null)
})
test('review boundaries reject partial source atoms and surrogate halves',()=>{
 const d=doc('甲[[source:s1]]😀乙'),md=savedMarkdownOf(d,'c1')!,atom=md.indexOf('[[source:s1]]'),emoji=md.indexOf('😀')
 assert.equal(markdownRangeToPm(d,'c1',atom+1,atom+13,md.slice(atom+1,atom+13)),null)
 assert.equal(markdownRangeToPm(d,'c1',emoji+1,emoji+2,md.slice(emoji+1,emoji+2)),null)
 assert.ok(markdownRangeToPm(d,'c1',atom,atom+13,'[[source:s1]]'))
})
test('document order follows actual template order and preserves unknown chapter fallback',()=>{
 const original=[{section_id:'question',id:'q'},{section_id:'method',id:'m'},{section_id:'old',id:'old'}]
 assert.deepEqual(sortChaptersByTemplate(original,[{section_id:'question',order:20},{section_id:'method',order:10}]).map(c=>c.id),['m','q','old'])
 assert.deepEqual(original.map(c=>c.id),['q','m','old'])
})
