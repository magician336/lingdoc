import {mkdir,writeFile} from 'node:fs/promises';
import path from 'node:path';
import {browserClient} from '../browser-client.mjs';
const out=path.resolve('prototypes/local-integration-20261103/native-reading/artifacts/start-final-'+new Date().toISOString().replaceAll(':','-'));
await mkdir(out,{recursive:true});const b=await browserClient(path.join(out,'edge'));let result={passed:false};
try {await b.send('Page.navigate',{url:'http://localhost:7215/'});await b.wait(`window.nativeReadingReady?.phase==='ready'&&!!window.nativeWorkspace`,120000);
await b.screenshot(path.join(out,'01-home.png'));
const click=async key=>{const r=await b.evaluate(`(await wordcraftHost.engine({op:'workspace.status'}))[${JSON.stringify(key)}]`);const x=r.x+r.width/2,y=r.y+r.height/2;await b.send('Input.dispatchMouseEvent',{type:'mousePressed',x,y,button:'left',clickCount:1});await b.send('Input.dispatchMouseEvent',{type:'mouseReleased',x,y,button:'left',clickCount:1});};
await click('projectNavRect');await b.wait(`!!(await wordcraftHost.engine({op:'workspace.status'})).projectCardRect`);await b.screenshot(path.join(out,'02-project-list.png'));
await click('projectCardRect');await b.wait(`!!(await wordcraftHost.engine({op:'workspace.status'})).projectDocumentRect`);await b.evaluate(`new Promise(r=>setTimeout(r,400))`);await b.screenshot(path.join(out,'03-project-details.png'));
result={passed:true,state:await b.evaluate(`await nativeWorkspace.state()`),errors:b.errors};
} finally {await b.close();await writeFile(path.join(out,'capture.json'),JSON.stringify(result,null,2));console.log(out)}
