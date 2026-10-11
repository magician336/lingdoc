import http from 'node:http';
import fs from 'node:fs';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
const root=path.dirname(fileURLToPath(import.meta.url));
const files=new Map([['/','index.html'],['/index.html','index.html'],['/app.css','app.css'],['/app.mjs','app.mjs']]);
const types={'.html':'text/html; charset=utf-8','.css':'text/css; charset=utf-8','.mjs':'text/javascript; charset=utf-8'};
const server=http.createServer((req,res)=>{
 if(req.method!=='GET'&&req.method!=='HEAD'){res.writeHead(405,{'Allow':'GET, HEAD'});res.end();return;}
 let name;try{name=files.get(decodeURIComponent(new URL(req.url,'http://localhost').pathname));}catch{}
 if(!name){res.writeHead(404);res.end('Not found');return;}
 const file=path.join(root,name);
 const stat=fs.lstatSync(file);if(!stat.isFile()||stat.isSymbolicLink()){res.writeHead(403);res.end();return;}
 res.writeHead(200,{'Content-Type':types[path.extname(name)],'Cache-Control':'no-store','X-Content-Type-Options':'nosniff','Content-Security-Policy':"default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'none'; img-src 'self' data:; object-src 'none'; base-uri 'none'; frame-ancestors 'none'",'Referrer-Policy':'no-referrer'});
 if(req.method==='HEAD')res.end();else fs.createReadStream(file).pipe(res);
});
server.on('error',err=>{console.error(err.code);process.exitCode=1;});
server.listen(5194,'127.0.0.1',()=>console.log('LingDoc layout prototype http://127.0.0.1:5194/'));
process.on('SIGTERM',()=>server.close());
process.on('SIGINT',()=>server.close());
