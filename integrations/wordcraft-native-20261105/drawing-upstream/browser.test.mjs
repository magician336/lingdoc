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

const browserExecutable = process.env.LINGDOC_DRAWING_BROWSER || 'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe';
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
  if (process.platform !== 'win32' || !await present(browserExecutable)) return t.skip('Configured Windows Chromium browser is unavailable');
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
    browser = await chromium.launchPersistentContext(path.join(directory,'browser-profile'),{executablePath:browserExecutable,headless:true});
    console.error('smoke: browser launched');
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
    const loadCanvas = (xml, messages) => page.evaluate(({origin, xml, messages}) => new Promise((resolve,reject) => {
      const timer = setTimeout(() => reject(new Error('Drawing load timed out')),30000);
      const frame = document.getElementById('editor');
      const onMessage = event => {
        if (event.source !== frame.contentWindow || event.origin !== origin || event.data?.requestId !== 'smoke-load') return;
        window.removeEventListener('message',onMessage); clearTimeout(timer); resolve(event.data);
      };
      window.addEventListener('message',onMessage);
      frame.contentWindow.postMessage({channel:'lingdoc-drawing',type:'load',requestId:'smoke-load',xml,messages,title:'Smoke drawing'},origin);
    }), {origin:upstream.origin,xml,messages});
    const loaded = await loadCanvas(xml, messages);
    assert.equal(loaded.loaded,true,JSON.stringify(loaded));
    console.error('smoke: XML and messages loaded');
    await t.test('workspace fits the viewport even when extensions append body nodes', async () => {
      await page.evaluate(() => {
        // Extension hosts must not become rows in the application's grid.
        const host = document.createElement('div');
        host.id = 'extension-layout-fixture';
        document.body.append(host);
      });
      const app = page.frames().find(frame => frame.url() === upstream.origin + '/zh');
      for (const [width,height] of [[1920,1080],[2560,1440],[1280,720],[1024,768],[768,1024],[390,844],[844,390],[1280,480]]) {
        await page.setViewportSize({width,height});
        await page.waitForFunction(() => Math.abs(document.querySelector('.workspace').getBoundingClientRect().bottom - innerHeight) < 2, null, {timeout:3000});
        const layout = await page.evaluate(() => {
          const rect = selector => document.querySelector(selector).getBoundingClientRect().toJSON();
          return {width:innerWidth,height:innerHeight,scrollWidth:document.documentElement.scrollWidth,canvas:rect('#editor'),save:rect('#save'),picker:rect('#saved')};
        });
        assert.ok(layout.scrollWidth <= width + 1, `No page overflow at ${width}x${height}`);
        assert.ok(layout.canvas.height > height * .5, `Canvas gets usable height at ${width}x${height}`);
        for (const control of [layout.save,layout.picker]) {
          assert.ok(control.x >= 0 && control.right <= width + 1 && control.y >= 0 && control.bottom <= height, 'Primary controls remain visible');
        }
        await app.waitForFunction(() => {
          const workspace = document.querySelector('[data-testid="workspace"]').getBoundingClientRect();
          return Math.abs(workspace.width - innerWidth) < 2 && Math.abs(workspace.height - innerHeight) < 2;
        });
        const inner = await app.evaluate(() => ({width:innerWidth,height:innerHeight,scrollWidth:document.documentElement.scrollWidth}));
        assert.ok(Math.abs(inner.height-layout.canvas.height) < 2 && Math.abs(inner.width-layout.canvas.width) < 2, 'Iframe follows its available space');
        assert.ok(inner.scrollWidth <= inner.width + 1, 'Embedded workspace does not overflow horizontally');
        if (inner.width < 768) await app.getByRole('button',{name:'对话',exact:true}).click();
        await app.getByTestId('chat-input').waitFor({state:'visible'});
        const chat = await app.evaluate(() => ({
          panel:document.querySelector('[data-testid="chat-panel"]').getBoundingClientRect().toJSON(),
          input:document.querySelector('[data-testid="chat-input"]').getBoundingClientRect().toJSON(),
        }));
        for (const rect of [chat.panel,chat.input]) {
          assert.ok(rect.x >= 0 && rect.right <= inner.width + 1 && rect.y >= 0 && rect.bottom <= inner.height + 1, `Chat remains accessible at ${width}x${height}`);
        }
        if (width === 1920 || width === 390) await page.screenshot({path:path.join(runtime,`drawing-layout-${width}.png`),fullPage:true});
        if (inner.width < 768) {
          await app.getByRole('button',{name:'画布',exact:true}).click();
          await app.getByTestId('canvas-stage').waitFor({state:'visible'});
        }
      }
      await page.evaluate(() => document.getElementById('extension-layout-fixture').remove());
      await page.setViewportSize({width:1280,height:720});
    });
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
    const beforeSwitch = await page.locator('#saved option').count();
    await page.locator('#saved').selectOption(firstRecord.id);
    await page.waitForFunction(() => document.querySelector('#status').textContent.includes('已打开保存的图稿'));
    assert.equal(await page.locator('#saved option').count(), beforeSwitch, 'Opening from a blank canvas creates no empty drawing');
    const secondResponse = page.waitForResponse(response => response.url().endsWith('/drawing/save') && response.request().method()==='POST', {timeout:60000});
    await page.locator('#save').click();
    const second = await secondResponse; console.error('smoke: second save response',second.status()); assert.equal(second.status(),200,await second.text());
    const restored = await second.json();
    assert.equal(restored.id,firstRecord.id); assert.equal(restored.revision,2);
    assert.ok(containsCell(restored.xml)); assert.deepEqual(restored.messages,messages);
    assert.deepEqual(readDrawing(restored.embeddedPng),{id:restored.id,revision:restored.revision,title:restored.title,xml:restored.xml,messages:restored.messages});
    await page.waitForFunction(() => !document.querySelector('#new').disabled);
    await page.locator('#new').click();
    await page.waitForFunction(() => document.querySelector('#status').textContent.includes('已新建图稿'));
    assert.equal((await loadCanvas(xml, messages)).loaded, true);
    await page.locator('#title').fill('Second drawing');
    const secondDrawingResponse = page.waitForResponse(response => response.url().endsWith('/drawing/save') && response.request().method() === 'POST');
    await page.locator('#save').click();
    const secondDrawing = await (await secondDrawingResponse).json();
    await page.waitForFunction(() => !document.querySelector('#saved').disabled);
    const editedMessages = [...messages, {id:'edited-message',role:'user',parts:[{type:'text',text:'Unsaved changes before switching'}]}];
    assert.equal((await loadCanvas(xml.replace('LingDoc smoke', 'Unsaved canvas edit'), editedMessages)).loaded, true);
    await page.locator('#title').fill('Second drawing edited');
    const autosaveResponse = page.waitForResponse(response => response.url().endsWith('/drawing/save') && response.request().method() === 'POST');
    await page.locator('#saved').selectOption(firstRecord.id);
    const autosaved = await (await autosaveResponse).json();
    await page.waitForFunction(() => document.querySelector('#status').textContent.includes('原图已自动保存'));
    assert.equal(autosaved.id, secondDrawing.id);
    assert.equal(autosaved.revision, 2);
    assert.equal(autosaved.title, 'Second drawing edited');
    assert.deepEqual(autosaved.messages, editedMessages);
    assert.equal(await page.locator('#saved').inputValue(), firstRecord.id);
    await page.locator('#saved').selectOption(secondDrawing.id);
    await page.waitForFunction(() => !document.querySelector('#saved').disabled);
    assert.equal(await page.locator('#title').inputValue(), 'Second drawing edited');
    const reopenedResponse = page.waitForResponse(response => response.url().endsWith('/drawing/save') && response.request().method() === 'POST');
    await page.locator('#save').click();
    const reopened = await (await reopenedResponse).json();
    assert.equal(reopened.id, secondDrawing.id);
    assert.equal(reopened.xml, autosaved.xml, 'Switching back restores the autosaved canvas');
    assert.deepEqual(reopened.messages, editedMessages);
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
