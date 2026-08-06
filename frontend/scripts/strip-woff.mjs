import { readdirSync, rmSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

// WebView2 always supports woff2, so the woff fallbacks @fontsource emits are
// dead weight in the desktop bundle. The app only runs inside the WebView, so
// stripping them after the build is safe and saves ~80KB.
const assetsDir = join(dirname(fileURLToPath(import.meta.url)), '..', 'dist', 'assets');

let removed = 0;
for (const entry of readdirSync(assetsDir)) {
  if (entry.endsWith('.woff')) {
    rmSync(join(assetsDir, entry));
    removed += 1;
  }
}

console.log(`Removed ${removed} woff fallback font(s) from dist/assets.`);
