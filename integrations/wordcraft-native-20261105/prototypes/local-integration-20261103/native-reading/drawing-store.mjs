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
    try { const db = JSON.parse(await readFile(file, 'utf8')); if (db.version !== 1 || !Array.isArray(db.records) || !Array.isArray(db.keys) || (db.tombstones !== undefined && !Array.isArray(db.tombstones))) throw new Error('Invalid drawing database'); db.tombstones ??= []; return db; }
    catch (err) { if (err.code === 'ENOENT') return {version:1,records:[],keys:[],tombstones:[]}; throw err; }
  }
  async function commit(db) {
    await mkdir(directory, {recursive:true});
    const temp = file + '.' + randomUUID() + '.tmp';
    try { await write(temp, JSON.stringify(db), {flag:'wx'}); await rename(temp, file); }
    catch (err) { await unlink(temp).catch(() => {}); throw err; }
  }
  return {
    list: () => serialized(async () => { const db = await load(); const latest = new Map(); for (const record of db.records) latest.set(record.id, record); return [...latest.values()]; }),
    get: id => serialized(async () => { if (typeof id !== 'string' || !idPattern.test(id)) throw fail('VALIDATION', 'Invalid id'); const db = await load(); return db.records.findLast(record => record.id === id) || null; }),
    history: id => serialized(async () => {
      if (typeof id !== 'string' || !idPattern.test(id)) throw fail('VALIDATION', 'Invalid id');
      const db = await load();
      return db.records.filter(record => record.id === id).map(({id,revision,title}) => ({id,revision,title})).reverse();
    }),
    version: (id, revision) => serialized(async () => {
      if (typeof id !== 'string' || !idPattern.test(id) || !Number.isSafeInteger(revision) || revision < 1) throw fail('VALIDATION', 'Invalid version');
      const db = await load();
      return {record:db.records.find(record => record.id === id && record.revision === revision) || null,
        headRevision:db.records.findLast(record => record.id === id)?.revision || 0};
    }),
    save: input => serialized(async () => {
      if (!input || typeof input.key !== 'string' || !idPattern.test(input.key) || !Number.isSafeInteger(input.expectedRevision) || input.expectedRevision < 0 || (input.id !== undefined && (typeof input.id !== 'string' || !idPattern.test(input.id)))) throw fail('VALIDATION', 'Invalid save request');
      const id = input.id || randomUUID();
      const metadata = validateDrawing({id, revision:Math.max(1,input.expectedRevision + 1), title:input.title, xml:input.xml, messages:input.messages});

      try { validatePng(input.png); } catch { throw fail('VALIDATION', 'Invalid PNG'); }
      const fingerprint = createHash('sha256').update(JSON.stringify({id:input.id ?? null, expectedRevision:input.expectedRevision, title:metadata.title, xml:metadata.xml, messages:metadata.messages, png:input.png})).digest('hex');
      const db = await load();
      const previous = db.keys.find(item => item.key === input.key);
      if (previous) { if (previous.operation || previous.fingerprint !== fingerprint) throw fail('CONFLICT', 'Idempotency key reused'); const record = db.records.find(record => record.id === previous.id && record.revision === previous.revision); if (!record) throw fail('CONFLICT', 'Drawing was deleted'); return {...record, embeddedPng:embedDrawing(record.png, record)}; }
      if (db.tombstones.includes(id)) throw fail('CONFLICT', 'Drawing was deleted');
      const current = db.records.findLast(record => record.id === id);
      if ((!current && input.expectedRevision !== 0) || (current && current.revision !== input.expectedRevision)) throw fail('CONFLICT', 'Drawing revision changed');
      if (!current && input.id && input.expectedRevision !== 0) throw fail('NOT_FOUND', 'Drawing not found');
      // Save receipts survive version deletion. Their immutable revision numbers
      // are the high-water mark: a deleted version must never acquire new content.
      const highWater = db.keys.reduce((max, receipt) => receipt.id === id && !receipt.operation ? Math.max(max, receipt.revision) : max, current?.revision || 0);
      if (!Number.isSafeInteger(highWater + 1)) throw fail('VALIDATION', 'Revision limit reached');
      const record = {...metadata, revision:highWater + 1, png:input.png};
      let embeddedPng; try { embeddedPng = embedDrawing(record.png, record); } catch { throw fail('VALIDATION', 'PNG with drawing metadata is too large or invalid'); }
      db.records.push(record); db.keys.push({key:input.key, fingerprint, id, revision:record.revision});
      await commit(db);
      return {...record, embeddedPng};
    }),
    deleteVersion: input => serialized(async () => {
      if (!input || typeof input.id !== 'string' || !idPattern.test(input.id) || typeof input.key !== 'string' || !idPattern.test(input.key) ||
          !Number.isSafeInteger(input.revision) || input.revision < 1) throw fail('VALIDATION', 'Invalid version deletion');
      const fingerprint = createHash('sha256').update(JSON.stringify({operation:'delete-version',id:input.id,revision:input.revision})).digest('hex');
      const db = await load();
      const previous = db.keys.find(item => item.key === input.key);
      if (previous) {
        if (previous.operation !== 'delete-version' || previous.fingerprint !== fingerprint) throw fail('CONFLICT', 'Idempotency key reused');
        return previous.result;
      }
      const versions = db.records.filter(record => record.id === input.id);
      if (!versions.some(record => record.revision === input.revision)) throw fail('NOT_FOUND', 'Drawing version not found');
      const remaining = versions.filter(record => record.revision !== input.revision);
      const result = {id:input.id,revision:input.revision,deleted:true,remaining:remaining.length,
        previousHeadRevision:versions.at(-1).revision,headRevision:remaining.at(-1)?.revision || 0};
      db.records = db.records.filter(record => record.id !== input.id || record.revision !== input.revision);
      if (!remaining.length && !db.tombstones.includes(input.id)) db.tombstones.push(input.id);
      db.keys.push({key:input.key,operation:'delete-version',fingerprint,result});
      await commit(db);
      return result;
    }),
    delete: input => serialized(async () => {
      if (!input || typeof input.id !== 'string' || !idPattern.test(input.id) ||
          typeof input.key !== 'string' || !idPattern.test(input.key) ||
          !Number.isSafeInteger(input.expectedRevision) || input.expectedRevision < 1) {
        throw fail('VALIDATION', 'Invalid delete request');
      }
      const fingerprint = createHash('sha256').update(JSON.stringify({operation:'delete', id:input.id, expectedRevision:input.expectedRevision})).digest('hex');
      const db = await load();
      const previous = db.keys.find(item => item.key === input.key);
      if (previous) {
        if (previous.operation !== 'delete' || previous.fingerprint !== fingerprint) throw fail('CONFLICT', 'Idempotency key reused');
        return {id:previous.id, revision:previous.revision, deleted:true};
      }
      const current = db.records.findLast(record => record.id === input.id);
      if (!current) throw fail('NOT_FOUND', 'Drawing not found');
      if (current.revision !== input.expectedRevision) throw fail('CONFLICT', 'Drawing revision changed');
      db.records = db.records.filter(record => record.id !== input.id);
      db.tombstones.push(input.id);
      db.keys.push({key:input.key, operation:'delete', fingerprint, id:input.id, revision:current.revision});
      await commit(db);
      return {id:input.id, revision:current.revision, deleted:true};
    })
  };
}
