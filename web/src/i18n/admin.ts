import type { Loaders } from "./index";

// The admin panel reads every section. `Loaders` makes the compiler check en.json against
// ru.json's shape: a key one file lacks fails the build.
export const adminDicts: Loaders = {
  ru: () => import("./ru.json"),
  en: () => import("./en.json"),
};
