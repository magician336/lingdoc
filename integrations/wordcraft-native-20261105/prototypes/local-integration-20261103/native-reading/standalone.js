// Ordinary WordCraft documents have browser-native save services, never the
// project's DS/Go save coordinator. This page does not fetch project DOCX bytes.
const sleep=ms=>new Promise(resolve=>setTimeout(resolve,ms));
async function engine(request) {
  const reply=JSON.parse(await window.wasmBindings.host_request(JSON.stringify(request)));
  if(!reply.ok)throw Error(reply.error);
  return reply.result;
}
window.wordcraftHost={engine,standalone:true};
for(let i=0;i<600;i++) {
  if(window.WORDCRAFT_FONT_READY?.error)throw Error(window.WORDCRAFT_FONT_READY.error);
  if(window.WORDCRAFT_FONT_READY?.ok&&window.wasmBindings?.host_request) {
    try {await engine({op:'status'});window.STANDALONE_READY=true;break;}catch{}
  }
  await sleep(100);
}
if(!window.STANDALONE_READY)throw Error('原生编辑器尚未准备好。');
