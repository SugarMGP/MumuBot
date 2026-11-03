/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_USE_MOCK?: string;
  readonly VITE_MOCK_SEED?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
