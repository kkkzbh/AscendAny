# AscendAny Pintia Exporter

Chrome/Edge Manifest V3 extension prototype for exporting the current Pintia problem set as an offline AscendAny import unit.

The extension is intentionally browser-side only:

- Pintia login stays in the browser.
- The extension exports a JSON bundle.
- Local AscendAny imports only the exported bundle.

## Install for development

1. Open `chrome://extensions`.
2. Enable Developer mode.
3. Load unpacked.
4. Select this directory:

```text
tools/pintia-exporter-extension
```

## Use

1. Open a Pintia problem set page, for example:

```text
https://pintia.cn/problem-sets/<problemSetId>/...
```

2. Click the AscendAny Pintia Exporter extension.
3. Click Export.
4. Keep the opened progress tab alive until the download starts. You can switch focus away from it.
5. Save the generated `AscendAny-Pintia-<exam-title>-<problemSetId>-<timestamp>.json`.

## Current status

This extension now uses a current-tab route warm-up flow. When export starts, the popup opens a persistent progress tab. The background service worker navigates the original Pintia tab through the required routes, collects data from each route, restores the original URL, validates completeness, and downloads a single JSON bundle.

The export intentionally interrupts the Pintia tab during collection. The progress tab can be left in the background, but closing it may stop the export.

The exported schema is documented in:

```text
doc/Pintia浏览器导出插件设计.md
```
