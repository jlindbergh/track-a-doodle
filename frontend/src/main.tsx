import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { setWorkerUrl } from 'maplibre-gl';
import maplibreWorkerUrl from 'maplibre-gl/dist/maplibre-gl-worker.mjs?url';
import 'maplibre-gl/dist/maplibre-gl.css';
import './styles.css';
import App from './App';
import AuthGate from './components/AuthGate';
import ConfigProvider from './components/ConfigProvider';

// MapLibre derives its worker URL from its own module URL, which breaks once
// Vite bundles it. Point it at the worker file explicitly.
setWorkerUrl(maplibreWorkerUrl);

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ConfigProvider>
      <AuthGate>
        <App />
      </AuthGate>
    </ConfigProvider>
  </StrictMode>,
);
