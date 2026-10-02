import { t } from "../i18n";

// Mirrors proto.Fingerprints: the uTLS profiles mihomo, Xray and sing-box all accept.
export const FINGERPRINTS = ["chrome", "firefox", "safari", "ios", "android", "edge", "360", "qq", "random", "randomized"] as const;

const BROWSERS: Record<string, string> = {
  chrome: "Chrome",
  firefox: "Firefox",
  safari: "Safari (macOS)",
  ios: "Safari (iOS)",
  android: "Android (OkHttp)",
  edge: "Edge",
  "360": "360 Browser",
  qq: "QQ Browser",
};

export const isKnownFingerprint = (fp: string) => (FINGERPRINTS as readonly string[]).includes(fp);

/** Mirrors proto.ValidFingerprint: a known profile or an own one of the right shape. */
export const validFingerprint = (fp: string) => isKnownFingerprint(fp) || /^[a-z0-9_]{1,32}$/.test(fp);

/** What the select shows: a browser's name, what a random profile does, or the own value. */
export function fingerprintLabel(fp: string): string {
  if (fp === "random") return t("settings.fpRandom");
  if (fp === "randomized") return t("settings.fpRandomized");
  return BROWSERS[fp] ?? fp;
}
