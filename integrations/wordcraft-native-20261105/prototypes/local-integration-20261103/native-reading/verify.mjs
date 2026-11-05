// Real native reader acceptance against an already running, dedicated trial.
// Does not start/stop services or connect to the older 7210/7211 trial.
import assert from 'node:assert/strict';
import { mkdir, writeFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import { browserClient } from '../browser-client.mjs';

const origin = process.env.NATIVE_READING_ORIGIN || 'http://127.0.0.1:7212';
const parsed = new URL(origin);
if (!['localhost', '127.0.0.1'].includes(parsed.hostname) || parsed.protocol !== 'http:' || !['7212','7213','7214','7215','7216','7218','7220','7224','7226'].includes(parsed.port) || !['/','/project'].includes(parsed.pathname)) throw Error('Only the isolated native trials are supported');
const here = path.dirname(fileURLToPath(import.meta.url));
const runId = new Date().toISOString().replaceAll(':', '-') + '-' + process.pid;
const out = path.join(here, 'artifacts', runId);
const result = { runId, origin, passed: false, scope: 'real native source reading; no AI or PDF text search claim', checks: [], errors: [] };
await mkdir(out, { recursive: true });
let browser, applied = false, restored = false;
try {
  browser = await browserClient(path.join(out, 'edge'));
  const { evaluate: e, wait, send } = browser;
  const check = async (expression, label) => { assert(await e(expression), label); result.checks.push(label); console.log('PASS ' + label); };
  await send('Page.navigate', { url: origin });
  await wait(`window.nativeReadingReady?.phase === 'ready' || window.nativeReadingReady?.phase === 'failed'`, 120000);
  await check(`window.nativeReadingReady?.phase === 'ready'`, 'real native source fixture preload succeeds');
  await e(`(()=>{window.h=()=>window.wordcraftHost;window.snap=async()=>({state:await h().engine({op:'status'}),selection:await h().engine({op:'selection'}),paragraphs:(await h().engine({op:'geometry'})).rows.filter(r=>r.area==='body'&&typeof r.text==='string').map(r=>({path:r.path,text:r.text,styles:r.styles}))});window.attempt=async r=>{try{return {accepted:true,value:await h().engine(r)}}catch(error){return {accepted:false,error:error.message}}};return true})()`);
  result.initial = await e(`await snap()`);
  assert.equal(result.initial.state.dirty, false, 'refuse to test a dirty document');
  await check(`!document.querySelector('iframe')&&!document.querySelector('.top-nav')&&!!document.querySelector('#wordcraft_canvas')`, 'trial is the native canvas with no duplicate React workbench');
  await e(`(async()=>{
    window.initialSnap=await snap();
    const rows=(await h().engine({op:'geometry'})).rows;
    const target=rows.find(r=>r.area==='body'&&typeof r.text==='string'&&r.text.startsWith('样本选择：'));
    if(!target)throw Error('Known fictional target paragraph not found; refusing to edit');
    const s=await h().engine({op:'status'});const anchor={story:'body',path:target.path,off:0},focus={...anchor,off:new TextEncoder().encode(target.text).length};
    await h().engine({op:'select',range:{anchor,focus},expectedDocumentId:s.documentId,expectedRevision:s.revision});
    window.testOperation={op:'replaceRange',operationId:'reader-'+crypto.randomUUID(),documentId:s.documentId,revision:s.revision,anchor,focus,expectedText:target.text,text:target.text+'【阅读隔离测试，随后撤销】'};
    await h().engine(testOperation);window.editedSnap=await snap();return true;
  })()`);
  applied = true;
  await check(`editedSnap.state.dirty&&editedSnap.state.documentId===initialSnap.state.documentId`, 'test edit stays in the same unsaved native document');
  await browser.screenshot(path.join(out, '01-body.png'));
  for (const [mode, file] of [['source','02-source'], ['split','03-split'], ['body',null]]) {
    await e(`await h().engine({op:'source.mode',mode:'${mode}'})`);
    await wait(`(await h().engine({op:'source.status'})).mode==='${mode}'`);
    await check(`JSON.stringify(await snap())===JSON.stringify(editedSnap)`, `${mode} view preserves identity, revision, dirty, selection and paragraph styles`);
    if (file) await browser.screenshot(path.join(out, file + '.png'));
  }
  await e(`await h().engine({op:'source.mode',mode:'source'})`);
  await e(`await h().engine({op:'source.page',page:2})`);
  await check(`(await h().engine({op:'source.status'})).page===2&&JSON.stringify(await snap())===JSON.stringify(editedSnap)`, 'page-level source navigation leaves the body unchanged');
  await check(`!(await attempt({op:'source.page',page:99})).accepted&&(await h().engine({op:'source.status'})).page===2`, 'invalid source page keeps the current page');
  await e(`await h().engine({op:'source.page',page:1})`);
  await check(`!(await attempt({op:'insert',text:'SHOULD_NOT_ENTER_BODY',bookmark:editedSnap.selection.selection,expectedDocumentId:editedSnap.state.documentId,expectedRevision:editedSnap.state.revision})).accepted`, 'native insert is refused while the source owns focus');
  await check(`!(await attempt({op:'undoOperation',operationId:testOperation.operationId,documentId:testOperation.documentId,expectedRevision:editedSnap.state.revision})).accepted`, 'native undo is refused while the source owns focus');
  await send('Input.insertText', { text: 'SHOULD_NOT_ENTER_BODY' });
  await send('Input.dispatchKeyEvent', { type: 'rawKeyDown', key: 'z', code: 'KeyZ', windowsVirtualKeyCode: 90, modifiers: 2 });
  await send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'z', code: 'KeyZ', windowsVirtualKeyCode: 90, modifiers: 2 });
  await send('Input.dispatchKeyEvent', { type: 'rawKeyDown', key: 'Backspace', code: 'Backspace', windowsVirtualKeyCode: 8 });
  await send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Backspace', code: 'Backspace', windowsVirtualKeyCode: 8 });
  await e(`new Promise(resolve=>setTimeout(resolve,300))`);
  await check(`JSON.stringify(await snap())===JSON.stringify(editedSnap)`, 'real text, Ctrl+Z and Backspace events in source view do not edit the body');
  // Scroll a real PDF page; preserve its reading offset through a body visit.
  await send('Input.dispatchMouseEvent', { type: 'mouseWheel', x: 1100, y: 650, deltaY: 240, deltaX: 0 });
  await e(`new Promise(resolve=>setTimeout(resolve,300))`);
  await e(`window.readerBefore=await h().engine({op:'source.status'})`);
  await e(`await h().engine({op:'source.mode',mode:'body'})`);
  await e(`await h().engine({op:'source.mode',mode:'source'})`);
  await e(`new Promise(resolve=>setTimeout(resolve,200))`);
  await check(`JSON.stringify((await h().engine({op:'source.status'})).scroll)===JSON.stringify(readerBefore.scroll)`, 'source page reading offset survives switching away and back');
  await e(`await h().engine({op:'source.mode',mode:'body'})`);
  await e(`await h().engine({op:'undoOperation',operationId:testOperation.operationId,documentId:testOperation.documentId,expectedRevision:(await h().engine({op:'status'})).revision})`);
  restored = true;
  await check(`JSON.stringify((await snap()).paragraphs)===JSON.stringify(initialSnap.paragraphs)`, 'returning to the body retains undo history and restores all original paragraph text/styles');
  await e(`await h().engine({op:'source.mode',mode:'source'})`);
  await send('Input.dispatchKeyEvent', { type: 'rawKeyDown', key: 's', code: 'KeyS', windowsVirtualKeyCode: 83, modifiers: 2 });
  await send('Input.dispatchKeyEvent', { type: 'keyUp', key: 's', code: 'KeyS', windowsVirtualKeyCode: 83, modifiers: 2 });
  await wait(`!(await h().engine({op:'status'})).dirty&&h().lastSave?.receipt?.verified===true`, 45000);
  await check(`!(await h().engine({op:'status'})).dirty&&h().lastSave.receipt.verified===true`, 'restored document saves through the existing verified coordinator');
  await check(`(async()=>{const ds=await h().api('version',{diskVersion:h().lastSave.receipt.diskVersion}),go=await h().goApi('versions/latest');return ds.sha256===go.docSha256&&go.docSha256===h().lastSave.receipt.sha256})()`, 'DS readback and Go saved version have the same DOCX digest');
  result.saved = await e(`({receipt:h().lastSave.receipt,version:h().lastSave.version})`);
  await check(`(await h().engine({op:'source.status'})).sourceActive`, 'Ctrl+S saves the restored body while the source retains focus');
  await e(`await h().engine({op:'source.mode',mode:'body'})`);
  await e(`await h().open()`);
  await check(`JSON.stringify((await snap()).paragraphs)===JSON.stringify(initialSnap.paragraphs)&&!(await h().engine({op:'status'})).dirty`, 'reopening saved DOCX reads the restored original content');
  await browser.screenshot(path.join(out, '04-restored-saved.png'));
  result.final = await e(`({document:await snap(),source:await h().engine({op:'source.status'})})`);
  result.errors = browser.errors;
  assert.equal(browser.errors.length, 0, 'no browser exceptions');
  result.passed = true;
} catch (error) {
  result.failure = error.stack || error.message;
  process.exitCode = 1;
  // Recover only this known operation, never arbitrary document history.
  if (browser && applied && !restored) {
    try {
      result.recovery = await browser.evaluate(`(async()=>{await h().engine({op:'source.mode',mode:'body'});const s=await h().engine({op:'status'});const r=await h().engine({op:'undoOperation',operationId:testOperation.operationId,documentId:testOperation.documentId,expectedRevision:s.revision});return {receipt:r,state:await h().engine({op:'status'})}})()`);
    } catch (recoveryError) { result.recoveryFailure = recoveryError.message; }
  }
} finally {
  if (browser) { try { await browser.close(); } catch (error) { result.cleanupFailure = error.message; result.passed = false; process.exitCode = 1; } }
  await writeFile(path.join(out, 'verification.json'), JSON.stringify(result, null, 2));
  console.log(JSON.stringify({ passed: result.passed, checks: result.checks.length, output: out, failure: result.failure }));
}

