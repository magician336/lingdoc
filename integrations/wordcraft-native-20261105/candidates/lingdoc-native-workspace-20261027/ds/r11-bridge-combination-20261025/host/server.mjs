// WordCraft fixed host v2 — delivery-v1 host + read-only historical version reads.
//
// Base: <delivery>/candidate/host/server.mjs (that tree is frozen; this is a copy that was
// then changed, so the v1 host keeps serving as the shipped baseline).
//
// v2 changes (everything else is v1 semantics, unchanged):
//   * POST /api/version  {diskVersion:int} -> read-only historical snapshot:
//         {data(base64), sha256, bytes, diskVersion, receipt, savedAt}
//     The file is data/saved-v<N>.docx and its bytes are re-hashed on every call; the digest
//     must equal the receipt recorded for that version. Unknown version -> 404
//     {"error":"unknown disk version"}; invalid parameter -> 400. The call never moves the
//     state.json pointer and never creates/deletes any file.
//   * data/versions.json — durable per-version receipt index. Appended on every successful
//     save (from this round on) and reverse-rebuilt from data/state.json at startup when the
//     index is absent, which is how the pre-existing saved-v1.docx gets indexed.
//   * WORDCRAFT_WEB_DIR selects the served wasm bundle directory (default ../../candidate/web,
//     i.e. <delivery>/candidate/web). The ready line prints that directory plus the sha256 and
//     byte length of both artifacts, so a swapped-in candidate bundle is visible in the log.
//   * WORDCRAFT_DATA_DIR / WORDCRAFT_AUDIT_PATH isolate a test run (defaults: the in-tree
//     data/ directory and <delivery>/logs/host-v2-audit.jsonl).
//   * default port 5321; only the legacy foreign port 5255 stays refused outright.
//     7100/7101 are ordinary candidate preview ports; real occupancy is enforced by listen().
//
// v1 security contract: 127.0.0.1 bind only; exact loopback Host (127.0.0.1 or localhost),
// matching same-Origin + per-process 64-hex X-Host-Token on every /api/* request; fixture sha256 verified
// before the socket opens; 12 fixed fonts verified before the socket opens; explicit static
// allow-list (no request-supplied path is ever resolved); save = fsync+rename+read-back hash+
// atomic state.json, idempotent replay returns the original receipt, 409 on stale diskVersion
// and on save-token identity conflict; every decision appended to the audit JSONL.
import http from 'node:http';
import fs from 'node:fs/promises';
import {appendFile} from 'node:fs/promises';
import path from 'node:path';
import crypto from 'node:crypto';
import {fileURLToPath} from 'node:url';

const ROOT = path.dirname(fileURLToPath(import.meta.url));            // <delivery>/rebuild/host-v2-20261024
const DELIVERY = path.resolve(ROOT, '..', '..');                      // <delivery>
const DATA = process.env.WORDCRAFT_DATA_DIR ? path.resolve(process.env.WORDCRAFT_DATA_DIR) : path.join(ROOT, 'data');
const SITE = path.join(ROOT, 'site');
const RESEARCH_ENABLED = process.env.WORDCRAFT_RESEARCH_ENABLED === '1';
const WEB = process.env.WORDCRAFT_WEB_DIR ? path.resolve(process.env.WORDCRAFT_WEB_DIR) : path.join(DELIVERY, 'candidate', 'web');
const VERSIONS_FILE = path.join(DATA, 'versions.json');
const FONT_DIR = 'C:/Windows/Fonts';
const FIXTURE = path.join(DELIVERY, 'inputs', 'nssfc2026-official-template.docx');
const FIXTURE_SHA256 = 'e6ef31b4b284f509b478071f8be09c76efceab9f32ca8803afdf905eee136181';
const RESERVED_PORTS = new Set([5255]);
const PORT = Number(process.env.WORDCRAFT_HOST_PORT || 5321);
if (!Number.isInteger(PORT) || PORT < 1024 || PORT > 65535) throw Error('invalid port');
if (RESERVED_PORTS.has(PORT)) throw Error(`port ${PORT} belongs to a foreign listening instance and is refused`);
const ORIGIN = `http://127.0.0.1:${PORT}`;
const LOOPBACK_HOST_HEADERS = new Set([`127.0.0.1:${PORT}`, `localhost:${PORT}`]);
const TOKEN = crypto.randomBytes(32).toString('hex');
const AUDIT = process.env.WORDCRAFT_AUDIT_PATH ? path.resolve(process.env.WORDCRAFT_AUDIT_PATH) : path.join(DELIVERY, 'logs', 'host-v2-audit.jsonl');
const GO_ORIGIN = (() => {
	const value = process.env.WORDCRAFT_GO_ORIGIN;
	if (!value) return null;
	const parsed = new URL(value);
	if (parsed.protocol !== 'http:' || !['127.0.0.1', 'localhost'].includes(parsed.hostname) || parsed.origin !== value || !parsed.port) throw Error('WORDCRAFT_GO_ORIGIN must be an exact loopback HTTP origin');
	return parsed.origin;
})();

const sha = b => crypto.createHash('sha256').update(b).digest('hex');

// ---------------------------------------------------------------- served web bundle identity
// Cheap and loud: the served directory and both artifact digests are read before anything else
// happens, so a wrong WORDCRAFT_WEB_DIR fails at startup instead of showing up as a 404 in the
// browser. The digests are reported, not pinned — swapping in an R11 candidate stays the point.
async function webArtifact(name) {
	const file = path.join(WEB, name);
	const bytes = await fs.readFile(file).catch(error => {
		throw Error(`web artifact unreadable at ${file}: ${error.code || error.message}`);
	});
	return {name, file, bytes: bytes.length, sha256: sha(bytes)};
}
const WEB_ARTIFACTS = [await webArtifact('wordcraft.js'), await webArtifact('wordcraft_bg.wasm')];
const WEB_IDENTITY = {dir: WEB, source: process.env.WORDCRAFT_WEB_DIR ? 'WORDCRAFT_WEB_DIR' : 'default', artifacts: WEB_ARTIFACTS};

// ---------------------------------------------------------------- fixed font lock
const FONTS = JSON.parse(await fs.readFile(path.join(ROOT, 'font-manifest.json'), 'utf8'));
if (!Array.isArray(FONTS) || FONTS.length !== 12 || new Set(FONTS.map(f => f.name)).size !== 12 || !FONTS.every(f => /^[a-z0-9_-]+\.(ttf|ttc)$/i.test(f.name) && /^[a-f0-9]{64}$/.test(f.sha256) && Number.isInteger(f.bytes) && f.bytes > 0 && f.bytes <= 32 * 1024 * 1024)) throw Error('invalid fixed font manifest');
async function verifyFixedFonts() {
	const rows = [];
	for (const font of FONTS) {
		let bytes;
		try {
			bytes = await fs.readFile(path.join(FONT_DIR, font.name));
		} catch (error) {
			rows.push({name: font.name, ok: false, reason: `unreadable: ${error.code || error.message}`});
			continue;
		}
		const digest = sha(bytes);
		rows.push({
			name: font.name,
			ok: bytes.length === font.bytes && digest === font.sha256,
			expectedBytes: font.bytes,
			actualBytes: bytes.length,
			expectedSha256: font.sha256,
			actualSha256: digest,
		});
	}
	const failed = rows.filter(row => !row.ok);
	if (failed.length) {
		const error = Error(`fixed font lock failed for ${failed.length}/12 configured local fonts: ${JSON.stringify(failed)}`);
		error.fontLock = rows;
		throw error;
	}
	return rows;
}

// ---------------------------------------------------------------- fixed fixture identity
const fixtureBytes = await fs.readFile(FIXTURE).catch(error => {
	throw Error(`fixed fixture unreadable at ${FIXTURE}: ${error.code || error.message}`);
});
if (sha(fixtureBytes) !== FIXTURE_SHA256) throw Error(`fixed fixture hash mismatch at ${FIXTURE}`);
const FIXTURE_IDENTITY = {path: FIXTURE, sha256: sha(fixtureBytes), bytes: fixtureBytes.length};

let fontRows;
try {
	fontRows = await verifyFixedFonts();
} catch (error) {
	console.error(JSON.stringify({ready: false, pid: process.pid, error: error.message, fontLock: error.fontLock || null}));
	throw error;
}

// ---------------------------------------------------------------- explicit static allow-list
const STATIC = new Map([
	['/', {file: path.join(SITE, 'index.html'), type: 'text/html;charset=utf-8'}],
	['/index.html', {file: path.join(SITE, 'index.html'), type: 'text/html;charset=utf-8'}],
	['/boot.js', {file: path.join(SITE, 'boot.js'), type: 'text/javascript;charset=utf-8'}],
	['/host.js', {file: path.join(SITE, 'host.js'), type: 'text/javascript;charset=utf-8'}],
	['/save-coordinator.js', {file: path.join(SITE, 'save-coordinator.js'), type: 'text/javascript;charset=utf-8'}],
	['/research-panel.js', {file: path.join(SITE, 'research-panel.js'), type: 'text/javascript;charset=utf-8'}],
	['/research-panel.css', {file: path.join(SITE, 'research-panel.css'), type: 'text/css;charset=utf-8'}],
	['/workspace-assistant.js', {file: path.join(SITE, 'workspace-assistant.js'), type: 'text/javascript;charset=utf-8'}],
	['/workspace-assistant-state.js', {file: path.join(SITE, 'workspace-assistant-state.js'), type: 'text/javascript;charset=utf-8'}],
	['/source-document-page.js', {file: path.join(SITE, 'source-document-page.js'), type: 'text/javascript;charset=utf-8'}],
	['/source-document-page.css', {file: path.join(SITE, 'source-document-page.css'), type: 'text/css;charset=utf-8'}],
	['/source-excerpt-state.js', {file: path.join(SITE, 'source-excerpt-state.js'), type: 'text/javascript;charset=utf-8'}],
	// The wasm bundle is served from WEB (WORDCRAFT_WEB_DIR or the frozen candidate/web).
	// Both entries are fixed keys; no request-supplied path is ever resolved.
	['/wordcraft.js', {file: WEB_ARTIFACTS[0].file, type: 'text/javascript;charset=utf-8'}],
	['/wordcraft_bg.wasm', {file: WEB_ARTIFACTS[1].file, type: 'application/wasm'}],
]);

await fs.mkdir(DATA, {recursive: true});

// ---------------------------------------------------------------- disk state (v1 semantics)
let serial = Promise.resolve();
const state = async () => JSON.parse(await fs.readFile(path.join(DATA, 'state.json'), 'utf8').catch(e => e.code === 'ENOENT' ? 'null' : Promise.reject(e)));
const send = (res, status, value) => {
	res.writeHead(status, {'Content-Type': 'application/json;charset=utf-8', 'Cache-Control': 'no-store', 'X-Content-Type-Options': 'nosniff'});
	res.end(JSON.stringify(value));
};
async function body(req) {
	let n = 0;
	const parts = [];
	for await (const p of req) {
		n += p.length;
		if (n > 20 * 1024 * 1024) throw Error('body too large');
		parts.push(p);
	}
	const raw = Buffer.concat(parts).toString('utf8');
	if (!raw.length) return {};
	return JSON.parse(raw);
}

// ---------------------------------------------------------------- per-version receipt index
// Every successful save appends its receipt here; a version read is only answered when the
// snapshot bytes still hash to the recorded digest for that exact version.
const VERSION_FILE_PATTERN = /^saved-v([0-9]{1,9})\.docx$/;
const VERSION_SCHEMA = 'wordcraft-versions-v1';
function normalizeReceipt(value, source) {
	if (!value || typeof value !== 'object' || Array.isArray(value)) throw Error(`invalid receipt in ${source}`);
	const {diskVersion, baseDiskVersion, file, sha256, bytes, documentId, revision, saveToken, savedAt} = value;
	if (!Number.isSafeInteger(diskVersion) || diskVersion < 1) throw Error(`invalid diskVersion in ${source}`);
	const match = typeof file === 'string' ? VERSION_FILE_PATTERN.exec(file) : null;
	if (!match || Number(match[1]) !== diskVersion) throw Error(`invalid version file name in ${source}`);
	if (typeof sha256 !== 'string' || !/^[a-f0-9]{64}$/.test(sha256)) throw Error(`invalid sha256 in ${source}`);
	if (!Number.isSafeInteger(bytes) || bytes < 0) throw Error(`invalid byte count in ${source}`);
	if (!Number.isSafeInteger(documentId) || documentId < 0 || !Number.isSafeInteger(revision) || revision < 0) throw Error(`invalid engine identity in ${source}`);
	if (typeof saveToken !== 'string' || !saveToken.length || saveToken.length > 160) throw Error(`invalid save token in ${source}`);
	return {
		diskVersion,
		baseDiskVersion: Number.isSafeInteger(baseDiskVersion) && baseDiskVersion >= 0 ? baseDiskVersion : diskVersion - 1,
		file,
		sha256,
		bytes,
		documentId,
		revision,
		saveToken,
		verified: value.verified === true,
		savedAt: typeof savedAt === 'string' ? savedAt : null,
	};
}
function versionFilePath(receipt) {
	if (!VERSION_FILE_PATTERN.test(receipt.file)) return null;
	const file = path.join(DATA, receipt.file);
	return path.dirname(file) === path.resolve(DATA) ? file : null;
}
async function persistVersionIndex(index) {
	const entries = [...index.values()].sort((a, b) => a.diskVersion - b.diskVersion);
	const text = JSON.stringify({schema: VERSION_SCHEMA, updatedAt: new Date().toISOString(), entries}, null, 2);
	const tmp = `${VERSIONS_FILE}.${crypto.randomBytes(8).toString('hex')}.tmp`;
	let handle = await fs.open(tmp, 'wx');
	try {
		await handle.writeFile(text);
		await handle.sync();
	} finally {
		await handle.close();
	}
	await fs.rename(tmp, VERSIONS_FILE);
}
async function loadVersionIndex() {
	let loaded = null;
	try {
		const raw = await fs.readFile(VERSIONS_FILE, 'utf8');
		const parsed = JSON.parse(raw);
		if (!parsed || parsed.schema !== VERSION_SCHEMA || !Array.isArray(parsed.entries)) throw Error('invalid versions.json');
		const map = new Map();
		for (const entry of parsed.entries) {
			const receipt = normalizeReceipt(entry, 'versions.json');
			if (map.has(receipt.diskVersion)) throw Error('duplicate disk version in versions.json');
			map.set(receipt.diskVersion, receipt);
		}
		loaded = map;
	} catch (error) {
		if (error.code !== 'ENOENT') throw error;
	}
	const index = loaded || new Map();
	// Reverse rebuild: the current snapshot's own receipt (data/state.json) is authoritative for
	// the version it names, which is how the pre-existing saved-v1.docx becomes readable through
	// /api/version without anyone re-saving it.
	const rebuilt = [];
	const current = await state();
	if (current) {
		const receipt = normalizeReceipt(current, 'state.json');
		if (!index.has(receipt.diskVersion)) {
			index.set(receipt.diskVersion, receipt);
			rebuilt.push({diskVersion: receipt.diskVersion, from: 'state.json'});
		}
	}
	if (!loaded || rebuilt.length) await persistVersionIndex(index);
	return {index, created: !loaded, rebuilt};
}
const {index: versionIndex, created: versionIndexCreated, rebuilt: versionIndexRebuilt} = await loadVersionIndex();
const VERSION_INDEX_STATUS = {file: VERSIONS_FILE, created: versionIndexCreated, rebuilt: versionIndexRebuilt, entries: versionIndex.size};

async function save(payload) {
	const current = await state(), version = current?.diskVersion || 0;
	if (!Array.isArray(payload.bytes) || payload.bytes.length > 4 * 1024 * 1024 || !payload.bytes.every(n => Number.isInteger(n) && n >= 0 && n <= 255)) throw Error('invalid bytes');
	const bytes = Buffer.from(payload.bytes), hash = sha(bytes);
	if (hash !== payload.sha256 || typeof payload.saveToken !== 'string' || !payload.saveToken.length || payload.saveToken.length > 160) throw Error('invalid serialization identity');
	if (!Number.isSafeInteger(payload.documentId) || payload.documentId < 0 || !Number.isSafeInteger(payload.revision) || payload.revision < 0 || !Number.isSafeInteger(payload.expectedDiskVersion) || payload.expectedDiskVersion < 0) throw Error('invalid engine identity');
	// Idempotent retry: the exact same payload returns the original receipt and writes nothing.
	if (current?.saveToken === payload.saveToken && current.sha256 === hash && current.documentId === payload.documentId && current.revision === payload.revision && current.baseDiskVersion === payload.expectedDiskVersion) return current;
	if (current?.saveToken === payload.saveToken) {
		const e = Error('save token identity conflict');
		e.status = 409;
		throw e;
	}
	if (payload.expectedDiskVersion !== version) {
		const e = Error('stale disk version');
		e.status = 409;
		throw e;
	}
	// Never accept a path from the client. Every immutable snapshot and pointer stays here.
	const diskVersion = version + 1, file = `saved-v${diskVersion}.docx`, target = path.join(DATA, file);
	const tmp = path.join(DATA, `${file}.${crypto.randomBytes(8).toString('hex')}.tmp`);
	let handle = await fs.open(tmp, 'wx');
	try {
		await handle.writeFile(bytes);
		await handle.sync();
	} finally {
		await handle.close();
	}
	try {
		await fs.rename(tmp, target);
	} catch (e) {
		await fs.unlink(tmp).catch(() => {});
		throw e;
	}
	const readback = await fs.readFile(target);
	if (sha(readback) !== hash) throw Error('disk verification failed');
	const receipt = {diskVersion, baseDiskVersion: version, file, sha256: hash, bytes: readback.length, documentId: payload.documentId, revision: payload.revision, saveToken: payload.saveToken, verified: true, savedAt: new Date().toISOString()};
	const pointer = path.join(DATA, 'state.tmp.json');
	handle = await fs.open(pointer, 'w');
	try {
		await handle.writeFile(JSON.stringify(receipt, null, 2));
		await handle.sync();
	} finally {
		await handle.close();
	}
	await fs.rename(pointer, path.join(DATA, 'state.json'));
	const persisted = await state();
	if (persisted.sha256 !== hash) throw Error('pointer verification failed');
	// Durable history: append this version's receipt. A failure here is reported as a 500 while
	// the snapshot stays durable, and an exact idempotent replay of the same payload recovers.
	versionIndex.set(receipt.diskVersion, receipt);
	try {
		await persistVersionIndex(versionIndex);
	} catch (error) {
		audit('version_index_failed', {diskVersion: receipt.diskVersion, error: error.message});
		throw error;
	}
	return receipt;
}

function audit(event, record) {
	appendFile(AUDIT, JSON.stringify({at: new Date().toISOString(), pid: process.pid, event, ...record}) + '\n').catch(() => {});
}

const CSS = `default-src 'self' blob: data:; script-src 'self' 'wasm-unsafe-eval'; connect-src 'self'${GO_ORIGIN ? ` ${GO_ORIGIN}` : ''}; style-src 'self' 'unsafe-inline'; img-src 'self' blob: data:; frame-ancestors 'self'`;
const server = http.createServer(async (req, res) => {
	const started = process.hrtime.bigint();
	let auditRecord = {method: req.method, path: req.url, host: req.headers.host || null, origin: req.headers.origin || null};
	try {
		const url = new URL(req.url, ORIGIN);
		auditRecord.path = url.pathname;
		if (!LOOPBACK_HOST_HEADERS.has(req.headers.host)) {
			auditRecord.result = 403; audit('host_header_rejected', auditRecord);
			return send(res, 403, {error: 'invalid host'});
		}
		if (url.pathname.startsWith('/api/')) {
			const tokenOk = req.headers['x-host-token'] === TOKEN;
			auditRecord.tokenMatched = tokenOk;
			// Both loopback aliases are valid preview entry points. Origin must still exactly
			// match the validated request Host, and every API call requires the process token.
			if (req.headers.origin !== `http://${req.headers.host}` || !tokenOk) {
				auditRecord.result = 403; audit('api_origin_or_token_rejected', auditRecord);
				return send(res, 403, {error: 'same-origin token required'});
			}
			if (req.method !== 'POST') {
				auditRecord.result = 405; audit('api_method_rejected', auditRecord);
				return send(res, 405, {error: 'POST required'});
			}
			const payload = await body(req);
			if (url.pathname === '/api/font') {
				const font = FONTS.find(font => font.name === payload.name);
				if (!font) {
					auditRecord.result = 404; audit('font_unknown', auditRecord);
					return send(res, 404, {error: 'unknown configured font'});
				}
				const bytes = await fs.readFile(path.join(FONT_DIR, font.name));
				if (bytes.length !== font.bytes || sha(bytes) !== font.sha256) throw Error('configured local font identity changed');
				auditRecord.result = 200; auditRecord.font = font.name; audit('font_served', auditRecord);
				res.writeHead(200, {'Content-Type': 'application/octet-stream', 'Content-Length': bytes.length, 'Cache-Control': 'no-store', 'Cross-Origin-Resource-Policy': 'same-origin', 'X-Content-Type-Options': 'nosniff'});
				return res.end(bytes);
			}
			if (url.pathname === '/api/version') {
				// Read-only: this branch never touches state.json and never creates or removes a file.
				const requested = payload?.diskVersion;
				if (!Number.isSafeInteger(requested) || requested < 0) {
					auditRecord.result = 400; auditRecord.error = 'invalid diskVersion';
					audit('version_invalid', auditRecord);
					return send(res, 400, {error: 'invalid diskVersion'});
				}
				auditRecord.diskVersion = requested;
				const receipt = requested >= 1 ? versionIndex.get(requested) : undefined;
				if (!receipt) {
					auditRecord.result = 404;
					audit('version_missing', auditRecord);
					return send(res, 404, {error: 'unknown disk version'});
				}
				const file = versionFilePath(receipt);
				if (!file) {
					auditRecord.result = 500; auditRecord.error = 'version receipt is malformed';
					audit('version_hash_mismatch', auditRecord);
					return send(res, 500, {error: 'version receipt is malformed'});
				}
				let bytes;
				try {
					bytes = await fs.readFile(file);
				} catch (error) {
					auditRecord.result = 404; auditRecord.error = `version file unreadable: ${error.code || error.message}`;
					audit('version_missing', auditRecord);
					return send(res, 404, {error: 'unknown disk version'});
				}
				const digest = sha(bytes);
				if (digest !== receipt.sha256 || bytes.length !== receipt.bytes) {
					auditRecord.result = 500; auditRecord.error = 'version hash mismatch';
					auditRecord.actualSha256 = digest; auditRecord.actualBytes = bytes.length;
					audit('version_hash_mismatch', auditRecord);
					return send(res, 500, {error: 'version hash mismatch'});
				}
				const result = {data: bytes.toString('base64'), sha256: digest, bytes: bytes.length, diskVersion: receipt.diskVersion, receipt, savedAt: receipt.savedAt};
				auditRecord.result = 200; auditRecord.sha256 = digest; auditRecord.bytes = bytes.length;
				audit('version_ok', auditRecord);
				return send(res, 200, result);
			}
			if (url.pathname === '/api/save') {
				const operation = serial.then(() => save(payload));
				serial = operation.catch(() => {});
				try {
					const receipt = await operation;
					auditRecord.result = 200; auditRecord.diskVersion = receipt.diskVersion; auditRecord.sha256 = receipt.sha256;
					audit('save_ok', auditRecord);
					return send(res, 200, receipt);
				} catch (error) {
					auditRecord.result = error.status || 500; auditRecord.error = error.message;
					audit('save_failed', auditRecord);
					throw error;
				}
			}
			if (url.pathname === '/api/open') {
				const current = await state();
				const bytes = await fs.readFile(current ? path.join(DATA, current.file) : FIXTURE);
				if (current && sha(bytes) !== current.sha256) throw Error('saved file hash mismatch');
				const result = {data: bytes.toString('base64'), diskVersion: current?.diskVersion || 0, sha256: sha(bytes), receipt: current};
				auditRecord.result = 200; auditRecord.diskVersion = result.diskVersion; auditRecord.sha256 = result.sha256;
				audit('open_ok', auditRecord);
				return send(res, 200, result);
			}
			auditRecord.result = 404; audit('endpoint_unknown', auditRecord);
			return send(res, 404, {error: 'unknown endpoint'});
		}
		if (req.method !== 'GET') {
			auditRecord.result = 405; audit('static_method_rejected', auditRecord);
			return send(res, 405, {error: 'GET required'});
		}
		if (url.pathname === '/host-config.js') {
			auditRecord.result = 200; audit('host_config_served', auditRecord);
			res.writeHead(200, {'Content-Type': 'text/javascript', 'Cache-Control': 'no-store', 'Cross-Origin-Resource-Policy': 'same-origin'});
			return res.end(`window.WORDCRAFT_HOST_CONFIG=${JSON.stringify({token: TOKEN, origin: `http://${req.headers.host}`, goOrigin: GO_ORIGIN, researchEnabled: RESEARCH_ENABLED, fonts: FONTS})};`);
		}
		// No client path is ever resolved: only these exact keys are servable.
		const entry = STATIC.get(url.pathname);
		if (!entry) {
			auditRecord.result = 403; audit('static_path_rejected', auditRecord);
			return send(res, 403, {error: 'path not declared'});
		}
		const bytes = await fs.readFile(entry.file);
		auditRecord.result = 200;
		audit('static_served', auditRecord);
		res.writeHead(200, {'Content-Type': entry.type, 'Cache-Control': 'no-store', 'X-Content-Type-Options': 'nosniff', 'Cross-Origin-Resource-Policy': 'same-origin', 'Content-Security-Policy': CSS});
		return res.end(bytes);
	} catch (error) {
		const status = error.status || 500;
		auditRecord.result = status; auditRecord.error = error.message;
		audit('request_failed', auditRecord);
		return send(res, status, {error: error.message});
	} finally {
		auditRecord.ms = Number(process.hrtime.bigint() - started) / 1e6;
		audit('request_end', auditRecord);
	}
});
server.listen(PORT, '127.0.0.1', () => {
	console.log(JSON.stringify({
		ready: true,
		pid: process.pid,
		origin: ORIGIN,
		bind: '127.0.0.1',
		root: ROOT,
		data: DATA,
		audit: AUDIT,
		fixture: FIXTURE_IDENTITY,
		web: WEB_IDENTITY,
		versions: VERSION_INDEX_STATUS,
		portIsolation: {reservedPortsRefused: [...RESERVED_PORTS].sort()},
		fontLock: {ok: true, count: fontRows.length, names: fontRows.map(r => r.name)},
		scope: 'isolated WordCraft host — delivery v1 host v2 (historical version read)',
	}));
	console.log(JSON.stringify({webArtifacts: WEB_ARTIFACTS.map(a => ({name: a.name, bytes: a.bytes, sha256: a.sha256}))}));
	console.log(JSON.stringify({fontLock: fontRows.map(r => ({name: r.name, ok: r.ok, bytes: r.actualBytes, sha256: r.actualSha256}))}));
});
for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, () => server.close(() => process.exit(0)));
