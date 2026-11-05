# Native source reading trial

This directory is a trial harness, not a second writing UI. `/` returns the existing real WordCraft host with a background source loader. The host's save coordinator, DS/Go persistence, font boot and native canvas are unchanged. The outer gateway uses exact loopback hosts and forwards the existing token/origin checks.

Start after the native `source.open`, `source.mode` and `source.status` bridge operations have been built:

```powershell
node prototypes/local-integration-20261103/native-reading/start.mjs
```

Default public port is 7212; upstream integration port is allocated dynamically. The dedicated default runtime is `native-reading/runtime/trial/`. Existing 7210, 7211, 7100 and 7106 are never reused or terminated. A normal process signal stops this harness and its owned runtime; alternatively write `trial.json`'s `runId` to `stop.request` in that same runtime directory. Never kill by port.

The first load waits for the ordinary document open, preloads the fixture through the native source bridge, then selects body mode. It verifies that source loading did not change native document identity/revision/dirty state. A failure is visible instead of being called ready. Check `window.nativeReadingReady` for acceptance diagnostics.

## Source evidence

`fixtures/fictional-research-guide-v1.pdf` is an actual two-page Chinese PDF created for this trial; every page says that it is fictional and not an official requirement. `manifest.json` records its SHA-256, actual PyMuPDF renderer version, page dimensions, extracted text and page image hashes. `source.json` contains the PNG pages loaded by the native reader. Original PDF bytes are retained. Both page images were visually inspected after generation; no clipped text was found.

This tests **raster PDF page viewing**, not PDF text selection, full-text search or precise quote rectangles. Those capabilities are not implied by the PNG page transport.

`prepare_pdf.py` regenerates the fixture explicitly; it is not called during normal startup. Regeneration may change PDF byte identity and must be followed by evidence refresh.

## Validation completed by the harness author

- Python/PyMuPDF 1.24.14 available.
- Two PDF pages rendered and visually inspected.
- `node --check start.mjs` and `node --check load-source.js` passed.
- The persistent service was **not started** by the harness author; native WASM integration and save/undo/read-position acceptance remain to be run after the native bridge build.
