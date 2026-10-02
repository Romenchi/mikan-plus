// Dictionaries cut down for the subscription page, made by the plugin in vite.config.ts.
declare module "virtual:i18n-sub-ru" {
  const dict: Partial<typeof import("./i18n/ru.json")>;
  export default dict;
}
declare module "virtual:i18n-sub-en" {
  const dict: Partial<typeof import("./i18n/ru.json")>;
  export default dict;
}
