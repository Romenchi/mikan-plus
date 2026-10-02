export type Platform = "ios" | "android" | "windows" | "macos";

type App = { name: string; note: "easiest" | "free" | "stable" | "openSource" | "modern" | "bestWindows" | "tun" | "oneButton"; link: (url: string, brand: string) => string };
export const enc = encodeURIComponent;
const clash = (url: string, brand: string) => `clash://install-config?url=${enc(url)}&name=${enc(brand)}`;

export const APPS: Record<Platform, App[]> = {
  ios: [
    { name: "Happ", note: "easiest", link: (u) => `happ://add/${u}` },
    { name: "Streisand", note: "free", link: (u, b) => `streisand://import/${u}#${enc(b)}` },
    { name: "v2RayTun", note: "stable", link: (u) => `v2raytun://import/${u}` },
  ],
  android: [
    { name: "Happ", note: "easiest", link: (u) => `happ://add/${u}` },
    { name: "INCY", note: "modern", link: (u) => `incy://add/${u}` },
    { name: "v2RayTun", note: "stable", link: (u) => `v2raytun://import/${u}` },
    { name: "Hiddify", note: "openSource", link: (u, b) => `hiddify://import/${u}#${enc(b)}` },
  ],
  windows: [
    { name: "Koala Clash", note: "bestWindows", link: (u, b) => `koala-clash://install-config?url=${enc(u)}&name=${enc(b)}` },
    { name: "Hiddify", note: "easiest", link: (u, b) => `hiddify://import/${u}#${enc(b)}` },
    { name: "Clash Verge Rev", note: "tun", link: clash },
  ],
  macos: [
    { name: "Clash Verge Rev", note: "tun", link: clash },
    { name: "Happ", note: "oneButton", link: (u) => `happ://add/${u}` },
    { name: "Hiddify", note: "openSource", link: (u, b) => `hiddify://import/${u}#${enc(b)}` },
  ],
};

export function detect(): Platform {
  const ua = navigator.userAgent;
  if (/iPhone|iPad|iPod/.test(ua)) return "ios";
  if (/Android/.test(ua)) return "android";
  if (/Mac OS X/.test(ua)) return "macos";
  if (/Windows/.test(ua)) return "windows";
  return "android";
}
