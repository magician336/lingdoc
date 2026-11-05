import init,* as bindings from './wordcraft.js';
window.wasmBindings=bindings;
try {
 await init({module_or_path:'./wordcraft_bg.wasm'});
 const installed=[];
 for(const font of window.WORDCRAFT_HOST_CONFIG.fonts){
  const response=await fetch('/api/font',{method:'POST',headers:{'Content-Type':'application/json','X-Host-Token':window.WORDCRAFT_HOST_CONFIG.token},body:JSON.stringify({name:font.name})});
  if(!response.ok)throw Error('Configured local font failed: '+response.status);
  const bytes=new Uint8Array(await response.arrayBuffer());
  const digest=Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256',bytes))).map(x=>x.toString(16).padStart(2,'0')).join('');
  if(bytes.length!==font.bytes||digest!==font.sha256)throw Error('Configured local font hash/length mismatch');
  const receipt=JSON.parse(bindings.host_install_font(font.name,bytes));
  if(receipt.sha256!==digest||receipt.sampleMissing.length)throw Error('Engine font install or glyph coverage failed');
  installed.push(receipt);
 }
 window.WORDCRAFT_FONT_READY={ok:true,installed};
}catch(error){window.WORDCRAFT_FONT_READY={ok:false,error:error.message};throw error;}
