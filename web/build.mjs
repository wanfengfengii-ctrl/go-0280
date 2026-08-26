// Deterministic build: copies the static workbench source into the Go embed
// directory. No bundler or network install is required, so public tests can use
// the pre-built assets directly.
import { cp, mkdir } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const src = resolve(here, 'src');
const out = resolve(here, '..', 'internal', 'httpapi', 'dist');

await mkdir(out, { recursive: true });
await cp(src, out, { recursive: true });
console.log('built workbench ->', out);
