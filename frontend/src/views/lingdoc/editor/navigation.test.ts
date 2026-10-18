import test from 'node:test'
import assert from 'node:assert/strict'
import {ChapterSaveQueue} from './saveQueue'
test('explicit navigation waits for the actual in-flight save and does not send a subsequently discarded draft',async()=>{
 let release!: (value:{ok:true;newWorkingCopyRevision:number})=>void
 const sent:string[]=[]
 const q=new ChapterSaveQueue(async(_ctx,body)=>{sent.push(body);return await new Promise<{ok:true;newWorkingCopyRevision:number}>(r=>release=r)},10000)
 q.setBaseline('c',4);q.markDirty('c','A')
 const flush=q.flushAll();await Promise.resolve();q.markDirty('c','B')
 const pause=q.pauseAndWait();release({ok:true,newWorkingCopyRevision:5})
 await pause;await flush
 assert.deepEqual(sent,['A']);assert.equal(q.state('c'),'dirty')
 q.dispose()
})
