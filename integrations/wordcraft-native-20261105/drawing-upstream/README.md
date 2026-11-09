# LingDoc drawing adapter

This adapter pins Next AI Draw.io to `04b1de949f9f2f14971bf2e26f4a7fd8cc392626`. Its complete source, npm dependencies, bundled draw.io release and generated application live in the ignored `runtime/` directory. Tracked files contain the small LingDoc overlay, bootstrap, launcher and contract tests.

## Prepare the application

Run from the LingDoc repository root with Node 22+ and Git available.

```powershell
node integrations/wordcraft-native-20261105/drawing-upstream/prepare.mjs --build
```

Preparation fetches the exact upstream commit into `runtime/source`, checks the Git remote and commit, checks the original Git blob hashes of every patch target, applies the overlay, runs `npm ci --ignore-scripts` with the upstream lockfile, and runs the upstream build. Fresh caches set local `core.autocrlf=false` before checkout. Existing CRLF checkouts are accepted only when CRLF-to-LF normalization reproduces the exact pinned blob hash. The upstream prebuild fetches and verifies its pinned draw.io v32.0.2 release. The standalone output receives the public assets and Next static assets it needs.

The layout overlay uses installed sans-serif and monospace fonts through the existing `--font-latin` and `--font-mono` CSS variables. The existing language-specific Chinese and Japanese fallback stacks remain in effect. The build no longer fetches Instrument Sans or JetBrains Mono from Google Fonts. Interface font metrics follow the fonts installed on the user's system.

`--install` prepares and installs dependencies without building. With no flags the command only fetches and applies the overlay. A failed network fetch can be retried. Existing differing source is rejected. Overlay files can update when their current bytes exactly match their recorded owned hash. Intent markers and replacements are atomic, and an interrupted update retains the previous hash until completion. User edits and unrecorded files are rejected. Keep a modified cache for inspection and prepare a separate fresh cache when upstream changes.

The bootstrap does not fetch floating revisions, rewrite the upstream lockfile, or vendor the entire source into LingDoc's tracked tree. Preserve LICENSE, NOTICE and bundled dependency notices when distributing the generated application.

## Launcher

The native-reading gateway imports the following function.

```js
import { startDrawingUpstream } from '../../../drawing-upstream/launch.mjs';

const drawing = await startDrawingUpstream({
  parentOrigin: 'http://127.0.0.1:7212',
  configPath: process.env.LINGDOC_LOCAL_AI_CONFIG,
  directory: drawingRuntimeDirectory,
});
// drawing.origin is the iframe origin. Always await drawing.stop() on shutdown.
```

The launcher binds the standalone Next server to an allocated `127.0.0.1` port and verifies a per-run identity before reporting readiness. Missing build artifacts produce an actionable preparation error. The document application can catch that error and retain its existing functionality.

`LINGDOC_DRAWING_ORIGIN` can select an already-operated exact loopback HTTP origin. That external process must run this overlay and be configured for the parent origin. Its lifecycle belongs to its operator. Credentials are never forwarded to an external origin.

The local configuration schema is the existing LingDoc schema from `internal/container/lingdoc_local_ai.go` in the UPDF Go candidate. Only the supported `deepseek-flash` model is accepted. The adapter reads the authorized absolute file server-side and passes its API credential only in the drawing child process environment. Upstream's DeepSeek provider uses the fixed `https://api.deepseek.com/v1` endpoint. The system encryption key is validated and remains unused by the drawing application. Credentials are absent from URLs, command arguments, browser messages, bootstrap output and drawing metadata.

This launch adapter reuses the local configuration directly. Provider calls use upstream's SDK rather than Go's registered chat service. The upstream model selector's browser overrides are removed at the server boundary so drawing chat keeps the configured service. Automatic tool-error retry, truncation continuation and provider transport retry are disabled. Explicit upstream retry controls remain available. Child diagnostics are not forwarded because upstream tool-repair logging may contain diagram contents. Debug payload logging and Langfuse credentials are disabled for this child.

## Parent-window contract

The parent opens its dedicated drawing page and places the returned drawing origin in an iframe. Set the iframe referrer policy to `origin`. The overlay permits the exact configured parent origin and its `localhost`/`127.0.0.1` alias on the same port. It verifies both `event.source === window.parent` and exact origin, then locks to the origin that sent the first accepted request. CSP `frame-ancestors` permits those parent origins and `'self'` for the nested bundled draw.io frame.

All messages are structured objects with `channel: 'lingdoc-drawing'`.

| Direction | Type | Fields |
| --- | --- | --- |
| Child to parent | `ready` | Sent after chat restore and draw.io readiness |
| Parent to child | `load` | `requestId`, `xml`, optional `messages`, `title`, `prompt` |
| Parent to child | `prompt` | `requestId`, `prompt`. Prefills the composer and preserves diagram and chat |
| Parent to child | `feature` | `requestId`, `feature`: `paper`, `animated`, `aws`, `flowchart` or `creative` |
| Parent to child | `export` | `requestId`, optional `skipEmpty: true` |
| Child to parent | `result` | Matching `requestId`, `loaded: true`, `prompted: true`, `selected: true` with `feature`, or the export fields below |
| Child to parent | `error` | Matching `requestId`, `error` |

Request IDs contain 1–128 ASCII letters, digits, underscores or hyphens. Only one bridge operation runs at once. A parent RPC timeout of at least 45 seconds covers the export sequence. User text is prefilled for explicit submission.

LingDoc opens directly on the canvas, including on phones and after New Chat. The pinned UI store keeps the upstream welcome page dismissed. Empty pages fit and center within the available editor area, at no more than 100% zoom; nonempty drawings retain upstream view handling. Feature navigation adds the selected creation instructions to the composer and opens/focuses it. Switching features replaces only the prefix previously added by the bridge and keeps user text and attachments. It neither submits a model request nor loads bundled example files. Paper-to-diagram uses the user's uploaded paper or pasted text. Generation still uses the existing, explicitly submitted chat flow and configured provider.

A load receipt waits for the canvas to finish loading and for the chat hook's visible messages to match the restored conversation. This prevents an immediate export from reading the prior conversation during the upstream SDK's throttled state update. Failure to observe that state within five seconds returns a recoverable error.

An export returns full multipage `xml`, current-page PNG data URL `png`, sanitized `messages` and `title`. It requests full XML before and after PNG export and rejects the save if the XML changed. It never takes the PNG export's potentially page-scoped XML as the authoritative source. Export is refused during model generation or session switching. XML is capped at 1 MiB of UTF-8, messages at 1 MiB of UTF-8 and 1000 entries, PNG data URL at 5 MiB, and prefilled text at 12500 JavaScript characters. The parent independently validates its own byte and image limits.

With `skipEmpty: true`, export returns `empty: true` without PNG only when every decoded page contains solely draw.io's two structural root cells and the latest chat is empty. Unknown data and decoding failures use normal export. Busy, leaving and pending-load guards run again after awaiting XML. The parent uses this option only for an unsaved canvas; clearing an existing drawing still saves that change.

The parent owns explicit Save, Insert and Update controls and the atomic saved drawing revision. Selecting a library drawing automatically saves the current canvas before loading the captured destination. Save/export failure prevents loading; controls and the iframe are locked during switching. Selecting the current drawing does nothing. The child exports state and never writes the WordCraft document. Restore includes the diagram and chat messages. Upstream's transient historical version strip and per-message regeneration snapshots remain browser-local in this release.

## Validation

Run the dependency-free checks and contract suite from the repository root.

```powershell
node --check integrations/wordcraft-native-20261105/drawing-upstream/launch.mjs
node --check integrations/wordcraft-native-20261105/drawing-upstream/prepare.mjs
node --test integrations/wordcraft-native-20261105/drawing-upstream/integration.test.mjs
```

The official-source patch contract uses `runtime/contract-fixture` or `LINGDOC_DRAWING_TEST_SOURCE`. It needs the exact pinned patch targets and `contexts/diagram-context.tsx`. Use a pristine checkout of the pinned upstream commit for that source path. Strict bridge checking also needs TypeScript and React/Node type declarations in the ignored `runtime/typecheck/node_modules` directory. The test extracts the actual hook member signatures from the official source. Its Next middleware declarations are test contracts, so this check does not replace the complete upstream build.

During implementation the six official files were fetched through the GitHub file API at the pinned revision. The current test suite passed 10 tests with zero skipped tests. It covered exact patch contracts, CRLF checkout normalization, repeat application, interrupted patch and overlay-update recovery, changed-source rejection, nested-frame CSP, local-font layout integration, server model mapping, missing-build behavior, strict TypeScript bridge checking, and executable bridge origin/load/prompt/export/busy/UTF-8 paths. It also covered conservative empty-page detection and waiting for the SDK's throttled message snapshot before acknowledging restore. No live provider request was made.

The subsequent canvas-entry/navigation update adds pinned `stores/ui-store.ts` and `lib/drawio/editor-bridge.ts` targets. Its contract suite passed 13/13 tests, including the entry policy, empty-page fit with page scale/viewport changes, preserved nonempty zoom, and feature input/attachment preservation without submission. Complete TypeScript checking and production build passed. The updated browser suite passed 4/4 tests in both Chrome and Edge: no welcome screen on entry or new drawings, fitted empty sheets at four sizes, the existing eight-size workspace checks, all five feature selections, real text attachment retention, zero model requests, and preserved drawing content/chat. Generated blank-canvas screenshots are `runtime/drawing-blank-{1920,390}.png`. Real provider generation remains outside this UI verification.

The pinned dependency installation, draw.io resource download, complete TypeScript check and Next production build passed on 2026-11-09. The build reports four upstream dynamic-filesystem tracing warnings; distribution size has not been optimized. The launcher was started and stopped successfully without model credentials. Native WordCraft integration still requires its separately documented Rust/WASM and WSL toolchain. Real model calls and native insertion/undo/DOCX round trips remain unverified.

After building, the optional Windows Chrome smoke test exercises the actual iframe and draw.io application with a temporary browser profile and drawing store. It does not call a model. Without the standalone build or Windows Chrome, it skips rather than claiming a pass.

The browser suite also checks eight viewport sizes, including narrow and short windows, and appends an extension-like body host to ensure it cannot create another application grid row. It checks visible save/library controls, full-height embedded workspace, chat input bounds and mobile canvas/chat switching. Desktop and mobile screenshots are written under the ignored `runtime/drawing-layout-{1920,390}.png` paths. Both Chrome and Edge passed this layout regression and the save/restore flow on 2026-11-09. To run with another installed Windows Chromium browser, set `LINGDOC_DRAWING_BROWSER` to its executable path before running the same command.

On 2026-11-09 the updated real-browser test passed in 24.6 seconds with zero skips. It loaded XML and chat into the actual canvas, saved, created a new drawing, reopened directly from a blank canvas without an empty save, saved revision 2, and verified editable XML and chat inside both exported PNG files. It then edited a second drawing's XML, title and conversation, switched away to trigger autosave, and switched back to verify complete source and chat restoration. This covers the drawing application; it does not verify native WordCraft insertion or document saving.

```powershell
node --test integrations/wordcraft-native-20261105/drawing-upstream/browser.test.mjs
```
