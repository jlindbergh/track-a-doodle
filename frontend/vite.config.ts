import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig({
  plugins: [react()],
  // Pre-bundle the map libraries at startup rather than on first page load.
  optimizeDeps: {
    include: ['maplibre-gl', 'react-map-gl/maplibre'],
  },
  server: {
    host: true,
    port: 5173,
    // In Docker on macOS, edits in the bind-mounted source don't raise file
    // events inside the container, so poll for changes instead.
    watch: process.env.VITE_USE_POLLING ? { usePolling: true, interval: 300 } : undefined,
    proxy: {
      // The backend owns all data access; the browser never talks to Martin.
      '/api': process.env.API_PROXY_TARGET ?? 'http://localhost:8080',
    },
  },
});
