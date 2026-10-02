import type { Dict, Loaders } from "./index";

// The subscription page reads a few sections of the dictionaries (see SUB_SECTIONS in
// vite.config.ts, which builds these two modules): the other 95% would only be download.
// A key of another section shows up as the key itself, and in `pnpm dev` as a warning.
export const subDicts: Loaders<Partial<Dict>> = {
  ru: () => import("virtual:i18n-sub-ru"),
  en: () => import("virtual:i18n-sub-en"),
};
