import {deflateSync, inflateSync} from 'node:zlib';
import {validateDrawing, fail} from './drawing-validation.mjs';
const signature = Buffer.from('89504e470d0a1a0a','hex');
const keyword = Buffer.from('LingDocDrawing\0', 'ascii');
const limit = 4 * 1024 * 1024;
const table = Array.from({length:256}, (_, n) => { for (let i=0;i<8;i++) n = (n & 1) ? 0xedb88320 ^ (n >>> 1) : n >>> 1; return n >>> 0; });
function crc32(data) { let crc = 0xffffffff; for (const value of data) crc = table[(crc ^ value) & 255] ^ (crc >>> 8); return (crc ^ 0xffffffff) >>> 0; }
function chunk(type, data) { const name = Buffer.from(type, 'ascii'); const out = Buffer.alloc(12 + data.length); out.writeUInt32BE(data.length,0); name.copy(out,4); data.copy(out,8); out.writeUInt32BE(crc32(out.subarray(4,8+data.length)),8+data.length); return out; }
function parse(input) {
  if (typeof input !== 'string' || input.length > Math.ceil(limit / 3)*4 || (!/^[A-Za-z0-9+/]*={0,2}$/.test(input) || Buffer.from(input,'base64').toString('base64') !== input)) throw fail('VALIDATION', 'Invalid PNG encoding');
  const data = Buffer.from(input,'base64'); if (data.length > limit || data.length < 8 || !data.subarray(0,8).equals(signature)) throw fail('VALIDATION', 'Invalid PNG signature');
  const chunks=[]; let offset=8; let ended=false;
  while (offset < data.length) {
    if (data.length-offset < 12) throw fail('VALIDATION', 'Truncated PNG chunk');
    const length = data.readUInt32BE(offset); if (length > limit || length > data.length-offset-12) throw fail('VALIDATION', 'Invalid PNG chunk length');
    const type = data.toString('ascii',offset+4,offset+8); if (!/^[A-Za-z]{4}$/.test(type)) throw fail('VALIDATION', 'Invalid PNG chunk type');
    const end=offset+12+length; if (crc32(data.subarray(offset+4,end-4)) !== data.readUInt32BE(end-4)) throw fail('VALIDATION', 'Invalid PNG CRC');
    if ((!chunks.length && (type !== 'IHDR' || length !== 13)) || (type === 'IHDR' && chunks.length) || (type === 'IEND' && length !== 0)) throw fail('VALIDATION', 'Invalid PNG structure');
    chunks.push({type, data:data.subarray(offset+8,end-4), raw:data.subarray(offset,end)}); offset=end;
    if (type === 'IEND') { ended=true; break; }
  }
  if (!ended || offset !== data.length || !chunks.some(c=>c.type === 'IDAT')) throw fail('VALIDATION', 'Incomplete PNG');
  return chunks;
}
export function validatePng(pngBase64) { parse(pngBase64); }
const own = c => c.type === 'tEXt' && c.data.subarray(0,keyword.length).equals(keyword);
export function embedDrawing(pngBase64, record) {
  const metadata = validateDrawing(record);
  const chunks = parse(pngBase64);
  const payload = Buffer.concat([keyword, Buffer.from(deflateSync(Buffer.from(JSON.stringify(metadata))).toString('base64'),'ascii')]);
  const result = Buffer.concat([signature, ...chunks.flatMap(c => own(c) ? [] : c.type === 'IEND' ? [chunk('tEXt',payload),c.raw] : [c.raw])]);
  if (result.length > limit) throw fail('VALIDATION', 'PNG too large');
  return result.toString('base64');
}
export function readDrawing(pngBase64) {
  const matches = parse(pngBase64).filter(own); if (!matches.length) return null;
  if (matches.length !== 1) throw fail('VALIDATION', 'Duplicate drawing metadata');
  const encoded = matches[0].data.subarray(keyword.length).toString('ascii');
  if ((!/^[A-Za-z0-9+/]*={0,2}$/.test(encoded) || Buffer.from(encoded,'base64').toString('base64') !== encoded)) throw fail('VALIDATION', 'Invalid drawing metadata');
  try {
    const raw = inflateSync(Buffer.from(encoded,'base64'),{maxOutputLength:2*1024*1024});
    return validateDrawing(JSON.parse(raw.toString('utf8')));
  } catch { throw fail('VALIDATION', 'Invalid drawing metadata'); }
}
