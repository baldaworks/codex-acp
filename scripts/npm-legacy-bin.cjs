#!/usr/bin/env node
'use strict';

// Keep the old executable name while sharing the generated native-binary shim.
const fs = require('node:fs');
const path = require('node:path');
const manifestPath = path.join(__dirname, '../.omnidist/codex-acp/npm/@normahq/codex-acp-bridge/package.json');
const manifest = JSON.parse(fs.readFileSync(manifestPath, 'utf8'));
if (manifest.name !== '@normahq/codex-acp-bridge' || manifest.bin?.['codex-acp'] !== 'codex-acp.js') {
  throw new Error('Unexpected generated legacy alias manifest');
}
manifest.bin['codex-acp-bridge'] = manifest.bin['codex-acp'];
manifest.deprecated = 'Deprecated: migrate to codex-acp. Install with npm install -g codex-acp or run npx codex-acp. Legacy compatibility remains available.';
fs.writeFileSync(manifestPath, JSON.stringify(manifest, null, 2) + '\n');
