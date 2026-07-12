/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** Base path for API calls (default "/api"). */
  readonly VITE_API_BASE?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
