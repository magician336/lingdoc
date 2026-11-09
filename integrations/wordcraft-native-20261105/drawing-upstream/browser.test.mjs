import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import path from 'node:path';
import {access, mkdtemp, rm} from 'node:fs/promises';
import {createRequire} from 'node:module';
import {fileURLToPath} from 'node:url';
import {inflateRawSync} from 'node:zlib';
import {startDrawingUpstream} from './launch.mjs';
import {sourceDirectory} from './patch.mjs';
import {createDrawingHTTP} from '../prototypes/local-integration-20261103/native-reading/drawing-http.mjs';
import {readDrawing} from '../prototypes/local-integration-20261103/native-reading/drawing-png.mjs';

const chrome = 'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe';
const present = async file => { try { await access(file); return true; } catch { return false; } };
const xml = '<mxfile><diagram id="test-page" name="Page-1"><mxGraphModel><root><mxCell id="0"/><mxCell id="1" parent="0"/><mxCell id="test-cell" value="LingDoc smoke" vertex="1" parent="1"><mxGeometry x="40" y="40" width="140" height="60" as="geometry"/></mxCell></root></mxGraphModel></diagram></mxfile>';
function containsCell(source) {
  if (source.includes('test-cell')) return true;
  for (const match of source.matchAll(/<diagram\b[^>]*>([A-Za-z0-9+/=\s]+)<\/diagram>/g)) {
    try {
      const decoded = inflateRawSync(Buffer.from(match[1].replace(/\s/g,''),'base64'), {maxOutputLength:1024*1024}).toString('utf8');
      if (decodeURIComponent(decoded).includes('test-cell')) return true;
    } catch { /* Try the next diagram. */ }
  }
  return false;
}
const messages = [{id:'smoke-message',role:'user',parts:[{type:'text',text:'Preserve this drawing conversation'}]}];

test('real upstream drawing can save and restore portable source', {timeout:240000}, async t => {
  if (!await present(path.join(sourceDirectory,'.next','standalone','server.js'))) return t.skip('Upstream standalone build is unavailable');
  if (process.platform !== 'win32' || !await present(chrome)) return t.skip('Windows Chrome is unavailable');
  const require = createRequire(path.join(sourceDirectory,'package.json'));
  const {chromium} = require('@playwright/test');
  const runtime = path.resolve(path.dirname(fileURLToPath(import.meta.url)),'runtime');
  const directory = await mkdtemp(path.join(runtime,'browser-smoke-'));
  let server, upstream, browser, page; const diagnostics = [];
  const oldExternal = process.env.LINGDOC_DRAWING_ORIGIN;
  delete process.env.LINGDOC_DRAWING_ORIGIN;
  try {
    let handler;
    server = http.createServer(async (req,res) => {
      const host = `127.0.0.1:${server.address().port}`;
      if (req.headers.host !== host || req.headers['sec-fetch-site'] === 'cross-site' ||
          (req.headers.origin && req.headers.origin !== `http://${host}`)) { res.writeHead(403); return res.end(); }
      if (!handler) { res.writeHead(503); return res.end(); }
      try { if (!await handler(req,res,new URL(req.url,`http://${host}`))) {res.writeHead(404);res.end();} }
      catch {if (!res.headersSent) res.writeHead(500);res.end();}
    });
    await new Promise((resolve,reject) => {server.once('error',reject);server.listen(0,'127.0.0.1',resolve);});
    console.error('smoke: parent server listening');
    const parentOrigin = `http://127.0.0.1:${server.address().port}`;
    upstream = await startDrawingUpstream({parentOrigin,configPath:null,directory});
    console.error('smoke: upstream started');
    assert.equal(upstream.aiConfigured,false);
    handler = await createDrawingHTTP({directory:path.join(directory,'store'),token:'smoke-token',drawingOrigin:upstream.origin});
    browser = await chromium.launchPersistentContext(path.join(directory,'chrome-profile'),{executablePath:chrome,headless:true});
    console.error('smoke: Chrome launched');
    page = browser.pages()[0] || await browser.newPage();
    page.on('console', msg => { if (msg.type() === 'error' || msg.type() === 'warning') diagnostics.push(`console ${msg.type()}: ${msg.text()}`); });
    page.on('pageerror', error => diagnostics.push(`pageerror: ${error.message}`));
    page.on('response', response => { if (response.status() >= 400) diagnostics.push(`HTTP ${response.status()} ${response.url()}`); });
    page.on('dialog', dialog => dialog.accept());
    await page.goto(parentOrigin+'/native-reading/drawing');
    console.error('smoke: parent page loaded');
    await page.locator('#save').waitFor({state:'visible'});
    await page.waitForFunction(() => !document.getElementById('save').disabled, null, {timeout:45000});
    console.error('smoke: drawing ready');
    // The message originates from the configured parent window, and the real
    // bridge waits for draw.io and validates the exact source/origin.
    const loaded = await page.evaluate(({origin, xml, messages}) => new Promise((resolve,reject) => {
      const timer = setTimeout(() => reject(new Error('Drawing load timed out')),30000);
      const frame = document.getElementById('editor');
      const onMessage = event => {
        if (event.source !== frame.contentWindow || event.origin !== origin || event.data?.requestId !== 'smoke-load') return;
        window.removeEventListener('message',onMessage); clearTimeout(timer); resolve(event.data);
      };
      window.addEventListener('message',onMessage);
      frame.contentWindow.postMessage({channel:'lingdoc-drawing',type:'load',requestId:'smoke-load',xml,messages,title:'Smoke drawing'},origin);
    }), {origin:upstream.origin,xml,messages});
    assert.equal(loaded.loaded,true,JSON.stringify(loaded));
    console.error('smoke: XML and messages loaded');
    await page.locator('#title').fill('Smoke drawing');
    const firstResponse = page.waitForResponse(response => response.url().endsWith('/drawing/save') && response.request().method()==='POST', {timeout:60000});
    await page.locator('#save').click();
    const first = await firstResponse; console.error('smoke: first save response',first.status()); assert.equal(first.status(),200,await first.text());
    const firstRecord = await first.json();
    await page.waitForFunction(() => [...document.querySelector('#saved').options].some(option => option.value));
    assert.ok(containsCell(firstRecord.xml)); assert.deepEqual(firstRecord.messages,messages);
    assert.deepEqual(readDrawing(firstRecord.embeddedPng),{id:firstRecord.id,revision:firstRecord.revision,title:firstRecord.title,xml:firstRecord.xml,messages:firstRecord.messages});
    await page.locator('#new').click();
    await page.waitForFunction(() => document.querySelector('#status').textContent.includes('已新建图稿'));
    await page.locator('#saved').selectOption(firstRecord.id);
    await page.locator('#open').click();
    await page.waitForFunction(() => document.querySelector('#status').textContent.includes('已打开保存的图稿'));
    const secondResponse = page.waitForResponse(response => response.url().endsWith('/drawing/save') && response.request().method()==='POST', {timeout:60000});
    await page.locator('#save').click();
    const second = await secondResponse; console.error('smoke: second save response',second.status()); assert.equal(second.status(),200,await second.text());
    const restored = await second.json();
    assert.equal(restored.id,firstRecord.id); assert.equal(restored.revision,2);
    assert.ok(containsCell(restored.xml)); assert.deepEqual(restored.messages,messages);
    assert.deepEqual(readDrawing(restored.embeddedPng),{id:restored.id,revision:restored.revision,title:restored.title,xml:restored.xml,messages:restored.messages});
  } catch (error) {
    if (page) {
      const screenshot = path.join(runtime,'browser-smoke-failure.png');
      await page.screenshot({path:screenshot,fullPage:true}).catch(() => {});
      const status = await page.locator('#status').textContent().catch(() => 'unavailable');
      diagnostics.push(`status: ${status}`, `frames: ${page.frames().map(frame => frame.url()).join(', ')}`, `screenshot: ${screenshot}`);
    }
    console.error(diagnostics.join('\n'));
    throw error;
  } finally {
    if (browser) await browser.close();
    if (upstream) await upstream.stop();
    if (server) await new Promise(resolve=>server.close(resolve));
    if (oldExternal === undefined) delete process.env.LINGDOC_DRAWING_ORIGIN; else process.env.LINGDOC_DRAWING_ORIGIN=oldExternal;
    const relative = path.relative(runtime,path.resolve(directory));
    assert.ok(relative && !relative.startsWith('..'+path.sep) && relative !== '..' && !path.isAbsolute(relative), 'Temporary directory must remain inside runtime');
    await rm(directory,{recursive:true,force:true});
  }
});
