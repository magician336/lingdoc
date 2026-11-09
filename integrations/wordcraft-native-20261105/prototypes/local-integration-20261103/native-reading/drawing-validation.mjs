export const fail = (code, message) => Object.assign(new Error(message), {code});
const bytes = value => Buffer.byteLength(value, 'utf8');
export const idPattern = /^[A-Za-z0-9_-]{1,128}$/;
export function validateDrawing(record) {
  if (!record || typeof record !== 'object' || Array.isArray(record)) throw fail('VALIDATION', 'Invalid drawing');
  if (typeof record.id !== 'string' || !idPattern.test(record.id) || !Number.isSafeInteger(record.revision) || record.revision < 1) throw fail('VALIDATION', 'Invalid drawing identity');
  if (typeof record.title !== 'string' || bytes(record.title) > 4096) throw fail('VALIDATION', 'Invalid title');
  const xml = record.xml;
  if (typeof xml !== 'string' || bytes(xml) > 1024 * 1024 || !xml.trim() || /<!\s*(?:DOCTYPE|ENTITY)\b/i.test(xml)) throw fail('VALIDATION', 'Invalid XML');
  // Reject malformed nesting and non-XML markup without evaluating external entities.
  const stripped = xml.replace(/<\?[^?]*\?>|<!--[\s\S]*?-->|<!\[CDATA\[[\s\S]*?\]\]>/g, '');
  const tagPattern = /<(?:"[^"]*"|'[^']*'|[^'">])*>/g;
  const tags = stripped.match(tagPattern) || [];
  const stack = []; let roots = 0;
  for (const tag of tags) {
    const match = tag.match(/^<\s*(\/?)\s*([A-Za-z_:][\w:.-]*)(?:\s+[A-Za-z_:][\w:.-]*\s*=\s*(?:"[^"<]*"|'[^'<]*'))*\s*(\/?)>$/);
    if (!match || (match[1] && match[3])) throw fail('VALIDATION', 'Invalid XML structure');
    if (match[1]) { if (stack.pop() !== match[2]) throw fail('VALIDATION', 'Invalid XML nesting'); }
    else { if (!stack.length) { roots++; if (!['mxfile','mxGraphModel'].includes(match[2])) throw fail('VALIDATION', 'Invalid drawing root'); } if (!match[3]) stack.push(match[2]); }
  }
  if (stack.length || roots !== 1 || /[<>]/.test(stripped.replace(tagPattern, ''))) throw fail('VALIDATION', 'Invalid XML structure');
  if (!Array.isArray(record.messages) || record.messages.length > 1000) throw fail('VALIDATION', 'Invalid messages');
  let encoded;
  try { encoded = JSON.stringify({id:record.id, revision:record.revision, title:record.title, xml, messages:record.messages}); } catch { throw fail('VALIDATION', 'Invalid messages'); }
  if (!encoded || bytes(encoded) > 2 * 1024 * 1024) throw fail('VALIDATION', 'Drawing metadata too large');
  return JSON.parse(encoded);
}
