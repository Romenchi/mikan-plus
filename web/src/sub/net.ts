import { safeHref } from "../lib/url";

// The same subscription URL serves the config to apps; the page only links to it. In
// Telegram the page runs as the bot's Mini App at /<sub path>/tg: the subscription then
// comes from Telegram's sign-in instead of the address.
export const pageURL = location.origin + location.pathname.replace(/\/$/, "");
export const tgMode = /\/tg$/.test(pageURL);
export const subRoot = pageURL.replace(/\/tg$/, "");
// Telegram's launch data after the #: the Mini App's sign-in.
export const initData = new URLSearchParams(location.hash.slice(1)).get("tgWebAppData") ?? "";

/** The token a subscription URL ends with. */
export const tokenOf = (url: string) => url.slice(url.lastIndexOf("/") + 1);

const TIMEOUT_MS = 15_000;

/**
 * fetch that never hangs: a request that does not answer in 15 s fails like any other
 * (the page then shows its error instead of skeletons forever), and `signal` cancels it.
 */
export function request(url: string, init: RequestInit & { signal?: AbortSignal } = {}): Promise<Response> {
  const ctl = new AbortController();
  const timer = window.setTimeout(() => ctl.abort(), TIMEOUT_MS);
  init.signal?.addEventListener("abort", () => ctl.abort(), { once: true });
  return fetch(url, { cache: "no-store", ...init, signal: ctl.signal }).finally(() => window.clearTimeout(timer));
}

export function json<T>(r: Response): Promise<T> {
  return r.ok ? (r.json() as Promise<T>) : Promise.reject(new Error(String(r.status)));
}

type TelegramProxy = { postEvent?: (type: string, data: string) => void };

/** Telegram's Mini App bridge: to the app's web view, or to Telegram Web around the frame. */
export function tgEvent(type: string, data: Record<string, unknown> | "" = "") {
  const w = window as Window & { TelegramWebviewProxy?: TelegramProxy };
  if (w.TelegramWebviewProxy?.postEvent) w.TelegramWebviewProxy.postEvent(type, JSON.stringify(data));
  else if (window.parent !== window) window.parent.postMessage(JSON.stringify({ eventType: type, eventData: data }), "https://web.telegram.org");
}

/** Opens a link outside the Mini App: Telegram links in Telegram, the rest in the browser. */
export function openOutside(url: string) {
  const tme = /^https:\/\/t\.me(\/.*)$/.exec(url);
  if (tme) tgEvent("web_app_open_tg_link", { path_full: tme[1] });
  else if (safeHref(url, { tg: true })) tgEvent("web_app_open_link", { url });
}

/** In Telegram a link leaves the Mini App through the bridge; elsewhere it is a plain link. */
export function outside(url: string) {
  return tgMode
    ? {
        onClick: (e: React.MouseEvent) => {
          e.preventDefault();
          openOutside(url);
        },
      }
    : {};
}
