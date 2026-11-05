> Current stage: [STAGE-V1.md](STAGE-V1.md). The snapshot notes below describe the original import; use the stage entry for current versions and acceptance.

# WordCraft V1 — 2026-11-05 source snapshot

This folder freezes the locally verified native WordCraft/LingDoc candidate. It does not replace the Magician repository root application. Start here: older README files inside the snapshot describe earlier stages.

## Scope

Native Home/New/Open and normal document editing remain available without a project. The project adds copied source imports, version-bound reading tabs/groups, explicit split reading, and source-grounded AI conversations with visible removable context and citations. This version has one current project and one editable primary manuscript; it is not a multi-project or multi-editor-session release. No automatic manuscript writing, opinion decisions, or figure generation is promised.

## Source map

- `candidates/lingdoc-native-workspace-20261027/engine`: native Rust/egui workspace, assets, licenses and locked dependencies.
- The candidate's `scripts`, `build-s0.ps1`, and `ds`: build/runtime and document-save host.
- `candidates/updf-workspace-r1-20261026/go-src`: matching Go service and document reader, preserved separately from repository-root Go.
- `prototypes/local-integration-20261103/native-reading`: native entry, project resources, import/reading and conversation bridges.
- `prototypes/local-integration-20261103/frontend/dist`: required existing startup static assets; not a replacement for the native editing canvas.
- `SOURCE-MANIFEST.json`: 3622 exported files, exact byte hashes, original source HEAD and verified WASM hash. The source includes uncommitted product work; original HEAD alone does not reproduce it.

## Build / run prerequisites

This is source preservation, not a portable executable release. The current launcher targets Windows + WSL. Use a short checkout path on Windows: deeply nested test/runtime paths exceed Python's Windows path limits. Do not change Windows global policy merely to run this archive.

Prepare Node, Windows Python with PyMuPDF (the current importer defaults to `D:/python3.10/python.exe`), WSL Go and module cache, and the Python reader requirements in `go-src/docreader/requirements-local.txt`. WSL defaults to `LingDoc-Beta-R29-Ubuntu2404`; inspect `scripts/local-environment.mjs` for environment overrides and offline cache defaults.

Native build requires Rust 1.95, wasm32 target, wasm-bindgen 0.2.129, C++ build tools and locked external craft-pdf dependency. Inspect `build-s0.ps1` and its fixed local tool paths before running. Generate `build/out/wordcraft.js` and `wordcraft_bg.wasm`. Tools, caches, system fonts and compiled WASM are not committed.

Build the matching Go executable from the saved Go directory with `go build -tags sqlite_fts5 -o /tmp/lingdoc-local-source-ai ./cmd/lingdoc-local` in WSL. Set `LINGDOC_RESEARCH_GO_BINARY` if using another location. Supply AI configuration privately using the runtime's existing configuration mechanism; no credentials are included.

Once prerequisites and native outputs exist, from this snapshot folder use `node prototypes/local-integration-20261103/native-reading/start.mjs --port 7227 --directory <new-runtime-directory>`. Use a free port and a short, new runtime directory; do not overwrite the existing live trial's data. A fresh clone full build/start was not performed for this archival delivery.

## Verification and limitations

Original working copy: JS 61/61, native Backstage 34/34, native workspace 54/54; final start-page browser 22/22, visual 5/5, ordinary-document 6/6, project-boundary 5/5. Final native build succeeded. Original detailed evidence remains in `evidence/native-source-workspace-20261104` of the development worktree.

Snapshot checks: 3622 source hashes match; independent static secret/data review found no blocking item. Workspace tests pass 29/29 in this archive. Import and load-source tests pass 32/32 using identical files in a short directory. Initial deep-path run was 50/61 and remains recorded locally; unrestricted execution did not fix that path limitation. An attempted combined short-directory test omitted workspace parent dependencies; workspace tests were therefore verified separately in the complete archive.

Real AI: three actual responses were verified on the preceding 7226 build. Its full browser run did not pass because of a later scroll stall; a subsequent UI-only continuation passed 8 checks with no new model calls. The final 7227 start-page acceptance did not call the model again. Do not describe all AI browser checks or all CI as passed. Full Go/native CI was not rerun. Scanned PDF has no OCR; DOCX source reading is extracted text, not a second editable WordCraft document.

## Preservation

The exact verified WASM and JS are also frozen in the local release archive, alongside this manifest. Runtime databases, imported user files, model credentials, browser profiles and caches are excluded from Git. Historical tests and examples are source fixtures, not live user data. The unrelated local `verify-first-version.mjs` edit is excluded: that file is copied from original HEAD.
