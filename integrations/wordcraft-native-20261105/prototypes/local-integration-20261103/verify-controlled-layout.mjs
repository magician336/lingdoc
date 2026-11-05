import { mkdir, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { browserClient } from './browser-client.mjs';
const here=path.dirname(fileURLToPath(import.meta.url));
const out=path.join(here,'artifacts','controlled-layout-'+new Date().toISOString().replaceAll(':','-'));
await mkdir(out,{recursive:true});
const browser=await browserClient(path.join(out,'edge'));
try {
  await browser.send('Page.navigate',{url:'http://127.0.0.1:7211/'});
  await browser.wait(`!!document.querySelector('.top-nav')`);
  await browser.evaluate(`document.querySelector('.top-nav button:nth-child(2)').click()`);
  await browser.wait(`!!document.querySelector('iframe')?.contentWindow?.wordcraftHost`);
  await browser.wait(`(()=>{try{return !!JSON.parse(document.querySelector('iframe').contentDocument.querySelector('#host-log').textContent).opened}catch{return false}})()`,90000);
  await browser.evaluate(`document.querySelector('.right-panel-tabs button:nth-child(2)').click()`);
  await browser.wait(`!!document.querySelector('[data-testid="cw-capture"]')`);
  await browser.screenshot(path.join(out,'writing.png'));
  const result=await browser.evaluate(`({width:innerWidth,bodyWidth:document.body.scrollWidth,iframe:document.querySelector('iframe').getBoundingClientRect().toJSON(),assistant:document.querySelector('.right-panel').getBoundingClientRect().toJSON(),text:document.querySelector('.controlled-writing').innerText})`);
  await writeFile(path.join(out,'layout.json'),JSON.stringify({...result,scope:'read-only layout; not functionality acceptance'},null,2));
  console.log(out);
} finally { await browser.close(); }
