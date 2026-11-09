import {createHash, randomUUID} from 'node:crypto';
import {mkdir, readFile, writeFile, rename, unlink} from 'node:fs/promises';
import path from 'node:path';
import {embedDrawing, validatePng} from './drawing-png.mjs';
import {validateDrawing, fail, idPattern} from './drawing-validation.mjs';

export function createDrawingStore({directory, write = writeFile}) {
  if (typeof directory !== 'string' || !directory) throw fail('VALIDATION', 'Directory required');
  const file = path.join(directory, 'drawings.json');
  let queue = Promise.resolve();
  const serialized = fn => { const result = queue.then(fn); queue = result.catch(() => {}); return result; };
  async function load() {
    try { const db = JSON.parse(await readFile(file, 'utf8')); if (db.version !== 1 || !Array.isArray(db.records) || !Array.isArray(db.keys)) throw new Error('Invalid drawing database'); return db; }
    catch (err) { if (err.code === 'ENOENT') return {version:1,records:[],keys:[]}; throw err; }
  }
  return {
    list: () => serialized(async () => { const db = await load(); const latest = new Map(); for (const record of db.records) latest.set(record.id, record); return [...latest.values()]; }),
    get: id => serialized(async () => { if (typeof id !== 'string' || !idPattern.test(id)) throw fail('VALIDATION', 'Invalid id'); const db = await load(); return db.records.findLast(record => record.id === id) || null; }),
    save: input => serialized(async () => {
      if (!input || typeof input.key !== 'string' || !idPattern.test(input.key) || !Number.isSafeInteger(input.expectedRevision) || input.expectedRevision < 0 || (input.id !== undefined && (typeof input.id !== 'string' || !idPattern.test(input.id)))) throw fail('VALIDATION', 'Invalid save request');
      const id = input.id || randomUUID();
      const metadata = validateDrawing({id, revision:Math.max(1,input.expectedRevision + 1), title:input.title, xml:input.xml, messages:input.messages});

      try { validatePng(input.png); } catch { throw fail('VALIDATION', 'Invalid PNG'); }
      const fingerprint = createHash('sha256').update(JSON.stringify({id:input.id ?? null, expectedRevision:input.expectedRevision, title:metadata.title, xml:metadata.xml, messages:metadata.messages, png:input.png})).digest('hex');
      const db = await load();
      const previous = db.keys.find(item => item.key === input.key);
      if (previous) { if (previous.fingerprint !== fingerprint) throw fail('CONFLICT', 'Idempotency key reused'); const record = db.records.find(record => record.id === previous.id && record.revision === previous.revision); return {...record, embeddedPng:embedDrawing(record.png, record)}; }
      const current = db.records.findLast(record => record.id === id);
      if ((!current && input.expectedRevision !== 0) || (current && current.revision !== input.expectedRevision)) throw fail('CONFLICT', 'Drawing revision changed');
      if (!current && input.id && input.expectedRevision !== 0) throw fail('NOT_FOUND', 'Drawing not found');
      const record = {...metadata, png:input.png};
      let embeddedPng; try { embeddedPng = embedDrawing(record.png, record); } catch { throw fail('VALIDATION', 'PNG with drawing metadata is too large or invalid'); }
      db.records.push(record); db.keys.push({key:input.key, fingerprint, id, revision:record.revision});
      await mkdir(directory, {recursive:true});
      const temp = file + '.' + randomUUID() + '.tmp';
      try { await write(temp, JSON.stringify(db), {flag:'wx'}); await rename(temp, file); }
      catch (err) { await unlink(temp).catch(() => {}); throw err; }
      return {...record, embeddedPng};
    })
  };
}
