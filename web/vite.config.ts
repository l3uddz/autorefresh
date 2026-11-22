import { readdir, readFile, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { gzipSync } from 'node:zlib';
import stylex from '@stylexjs/unplugin';
import { defineConfig } from 'vite';

const outDir = resolve('../internal/web/dist');

export default defineConfig({
  base: './',
  build: {
    outDir,
    emptyOutDir: true,
    rollupOptions: {
      onwarn(warning, warn) {
        // React Server Components directives ("use client") mean nothing in a
        // client-only bundle.
        if (warning.code === 'MODULE_LEVEL_DIRECTIVE' && warning.id?.includes('/node_modules/')) return;
        warn(warning);
      },
    },
  },
  plugins: [
    stylex.vite(),
    {
      name: 'compress-dashboard',
      apply: 'build',
      async closeBundle() {
        let total = 0;
        for (const name of await readdir(outDir, { recursive: true })) {
          if (!/\.(html|js|css)$/.test(name)) continue;
          const file = join(outDir, name);
          const data = gzipSync(await readFile(file));
          await writeFile(file + '.gz', data);
          total += data.length;
        }
        console.log(`Compressed dashboard HTML, JS, and CSS: ${Math.round(total / 1024)} KiB`);
      },
    },
  ],
});
