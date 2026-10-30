// SPDX-License-Identifier: MIT

// Writes a minimal package.json into each dist target so Node resolves
// the module system correctly. The root package.json declares
// "type": "module"; the CommonJS output under dist/cjs needs a local
// package.json that overrides "type" to "commonjs", and the ESM output
// under dist/esm restates "type": "module" so it stays resolvable when
// the package is consumed from a CommonJS-default project.

import { mkdirSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = dirname(dirname(fileURLToPath(import.meta.url)));

for (const [dir, type] of [
  ['dist/esm', 'module'],
  ['dist/cjs', 'commonjs'],
]) {
  const target = join(root, dir);
  mkdirSync(target, { recursive: true });
  writeFileSync(join(target, 'package.json'), JSON.stringify({ type }, null, 2) + '\n');
}
