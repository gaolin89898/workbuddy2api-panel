import { defineConfig } from 'vite';
import vue from '@vitejs/plugin-vue';
import { fileURLToPath } from 'node:url';

export default defineConfig(({ mode }) => ({
  plugins: [vue()],
  base: mode === 'standalone' ? './' : '/panel/',
  server: {
    port: 5173,
    strictPort: false,
    proxy: { '/panel/api': 'http://127.0.0.1:7863' },
  },
  build: {
    assetsInlineLimit: 16384,
    outDir: fileURLToPath(new URL(mode === 'standalone' ? './dist' : '../internal/panel/accounts_dist', import.meta.url)),
    emptyOutDir: true,
    rollupOptions: {
      output: {
        entryFileNames: 'accounts.js',
        chunkFileNames: 'chunks/[name]-[hash].js',
        assetFileNames: '[name][extname]',
      },
    },
  },
}));
