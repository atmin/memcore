# Vendored files of the window

`vendor/` holds six files of other projects for the page of `memcore ui`. `//go:embed` puts them into the executable, and the server serves them from its own origin, under `/static/vendor/`. memcore never fetches them at run time. So the page:
- needs no CDN;
- works offline;
- loads only scripts that its Content Security Policy allows, `script-src 'self'`.

`static_test.go::TestVendorHashes` checks each file against the SHA-256 below.

## `vendor/marked.umd.js`

marked turns markdown into HTML. The page loads it with a plain `<script>` tag, which sets `globalThis.marked`.

- **Version:** 18.1.3
- **License:** MIT
- **Source:** `https://cdn.jsdelivr.net/npm/marked@18.1.3/lib/marked.umd.js`
- **SHA-256:** `c34a221dc89e91833a88822e7717b8a0cf27118f8c9a8779dffb48a6be31f97a`

## `vendor/purify.min.js`

DOMPurify removes scripts, handlers and styles from the HTML of a document and from the SVG of a diagram. It sets `globalThis.DOMPurify`.

- **Version:** 3.4.17
- **License:** MPL-2.0 or Apache-2.0
- **Source:** `https://cdn.jsdelivr.net/npm/dompurify@3.4.17/dist/purify.min.js`
- **SHA-256:** `cd37898f9782adf54820c413191b5691803f0f137c799d6edcfd38523b4c2c31`

## `vendor/pele.global.js`

pele renders a Mermaid diagram to SVG, with no dependency. It comes from `github.com/obsidianmd/pele`, and sets `globalThis.pele`. The page calls `pele.render(source)` and sanitizes the `svg` of the result.

- **Version:** 1.13.0
- **License:** MIT
- **Source:** `https://cdn.jsdelivr.net/npm/pele@1.13.0/dist/pele.global.js`
- **SHA-256:** `0b0a46c57a5a6971670c09f5ec4066705bd78505f90fd11adf6260325efa0910`

## `vendor/preact.umd.js`

Preact renders the views of the page from components, and keeps their state across updates. The page loads it with a plain `<script>` tag, which sets `globalThis.preact`. Version 10 is the last one with a UMD build: Preact 11 ships only ES modules, which need an import map.

- **Version:** 10.29.8
- **License:** MIT
- **Source:** `https://cdn.jsdelivr.net/npm/preact@10.29.8/dist/preact.umd.js`
- **SHA-256:** `134b77bc803fa38661dc1b1e44e96eb0bb6a1a00edbb96d34dcde421b2e80b06`

## `vendor/hooks.umd.js`

The hooks of Preact give a component its state and its effects. The file reads `globalThis.preact`, so the page loads it after `preact.umd.js`. It sets `globalThis.preactHooks`.

- **Version:** 10.29.8
- **License:** MIT
- **Source:** `https://cdn.jsdelivr.net/npm/preact@10.29.8/hooks/dist/hooks.umd.js`
- **SHA-256:** `5c29238e5dc99df306d7f7fff038591a397cfcfabb59f81fbdef43d670aa0566`

## `vendor/htm.umd.js`

htm turns tagged templates into calls of Preact, at run time and with no eval. So the page needs no build step. It sets `globalThis.htm`.

- **Version:** 3.1.1
- **License:** Apache-2.0
- **Source:** `https://cdn.jsdelivr.net/npm/htm@3.1.1/dist/htm.umd.js`
- **SHA-256:** `7a31776e04bd4afde0d4308177d26f377716fcf7e4bd70be590746d6aa594f08`

## Update a file

Download the new version over the old file, and hash it:

```sh
curl -fsSL "https://cdn.jsdelivr.net/npm/pele@<version>/dist/pele.global.js" \
  -o internal/ui/static/vendor/pele.global.js
shasum -a 256 internal/ui/static/vendor/pele.global.js
```

Change the version, the source and the SHA-256 in this file in the same commit.
