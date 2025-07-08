import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react';

const buildTimestamp = new Date().getTime();

// https://vite.dev/config/
export default defineConfig(({ mode }) => {
  const isDev = mode === 'development';
  return {
    plugins: [react()],
    server: {
      port: 3015,
      strictPort: true,
      hmr: {
        clientPort: 3015,
      },
      headers: {
        'Access-Control-Allow-Headers': 'X-Requested-With, content-type, Authorization, sentry-trace, baggage',
      },
    },
    build: {
      rollupOptions: {
        input: {
          "panel-glouton-main": 'src/index.ts',
        },
        output: {
          entryFileNames: `js/[name].js`,
        },
      },
    },
    define: {
      __DEV__: isDev,
      buildTimestamp: JSON.stringify(buildTimestamp),
    },
    resolve: {
      extensions: ['.js', '.jsx', '.ts', '.tsx'],
    },
    optimizeDeps: {
      include: [
        'jsvat',
        'd3-array',
        'd3-brush',
        'd3-chord',
        'd3-delaunay',
        'd3-force',
        'd3-hierarchy',
        'd3-polygon',
        'd3-random',
        'd3-scale',
        'd3-selection',
        'd3-shape',
        'd3-zoom',
        'delaunator',
        'internmap',
      ],
    },
  }
})