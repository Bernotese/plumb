/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** "1" builds the UI against the mock API (T-006); never in a release build. */
  readonly VITE_PLUMB_MOCK?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
