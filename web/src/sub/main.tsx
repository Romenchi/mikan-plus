import "../styles/app.css";
import { Check, Copy, Laptop, Layers, LifeBuoy, QrCode, Send, Smartphone } from "lucide-react";
import { motion } from "motion/react";
import { StrictMode, useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { Atmosphere } from "../components/atmosphere";
import { LangSwitch } from "../components/lang";
import { Button, Pill, QR, Ring, Skeleton } from "../components/ui";
import { t, useLocale } from "../i18n";
import { ago, appName, bytes, dateLong, dateShort, days, daysUntil, time } from "../lib/format";
import { initTheme } from "../lib/theme";
import { loadShop, Shop, type ShopData } from "./shop";

initTheme();

type Info = {
  name: string;
  brand: string;
  support_url?: string;
  state: "active" | "expiring" | "limited" | "expired" | "disabled";
  used_up: number;
  used_down: number;
  limit?: number;
  expires_at?: string;
  resets_at?: string;
  device_limit: number;
  protocols: string[];
  binding: boolean;
  devices?: Device[];
  unbind_after?: string;
  telegram?: string;
};

type Device = { id: number; os: string; os_version: string; model: string; app: string; shared: boolean; created_at: string; last_seen: string };

type Platform = "ios" | "android" | "windows" | "macos";

// The same subscription URL serves the config to apps; the page only links to it. In
// Telegram the page runs as the bot's Mini App at /<sub path>/tg: the subscription then
// comes from Telegram's sign-in instead of the address.
const pageURL = location.origin + location.pathname.replace(/\/$/, "");
const tgMode = /\/tg$/.test(pageURL);
const subRoot = pageURL.replace(/\/tg$/, "");
// Telegram's launch data after the #: the Mini App's sign-in.
const initData = new URLSearchParams(location.hash.slice(1)).get("tgWebAppData") ?? "";

type TelegramProxy = { postEvent?: (type: string, data: string) => void };

/** Telegram's Mini App bridge: to the app's web view, or to Telegram Web around the frame. */
function tgEvent(type: string, data: Record<string, unknown> | "" = "") {
  const w = window as Window & { TelegramWebviewProxy?: TelegramProxy };
  if (w.TelegramWebviewProxy?.postEvent) w.TelegramWebviewProxy.postEvent(type, JSON.stringify(data));
  else if (window.parent !== window) window.parent.postMessage(JSON.stringify({ eventType: type, eventData: data }), "https://web.telegram.org");
}

/** Opens a link outside the Mini App: Telegram links in Telegram, the rest in the browser. */
function openOutside(url: string) {
  const tme = /^https:\/\/t\.me(\/.*)$/.exec(url);
  if (tme) tgEvent("web_app_open_tg_link", { path_full: tme[1] });
  else tgEvent("web_app_open_link", { url });
}

/** In Telegram a link leaves the Mini App through the bridge; elsewhere it is a plain link. */
function outside(url: string) {
  return tgMode
    ? {
        onClick: (e: React.MouseEvent) => {
          e.preventDefault();
          openOutside(url);
        },
      }
    : {};
}

type App = { name: string; note: "easiest" | "free" | "stable" | "openSource" | "modern" | "bestWindows" | "tun" | "oneButton"; link: (url: string, brand: string) => string };
const enc = encodeURIComponent;
const clash = (url: string, brand: string) => `clash://install-config?url=${enc(url)}&name=${enc(brand)}`;
const APPS: Record<Platform, App[]> = {
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

function detect(): Platform {
  const ua = navigator.userAgent;
  if (/iPhone|iPad|iPod/.test(ua)) return "ios";
  if (/Android/.test(ua)) return "android";
  if (/Mac OS X/.test(ua)) return "macos";
  if (/Windows/.test(ua)) return "windows";
  return "android";
}

type TgSub = { token: string; name: string };

function SubPage() {
  const [info, setInfo] = useState<Info | null>(null);
  const [failed, setFailed] = useState(false);
  const [platform, setPlatform] = useState<Platform>(detect);
  const [qr, setQr] = useState(false);
  const [copied, setCopied] = useState(false);
  const [subURL, setSubURL] = useState(tgMode ? "" : pageURL);
  const [tg, setTg] = useState<{ state: "loading" | "none" | "failed" | "ok"; subs: TgSub[] }>({ state: tgMode ? "loading" : "ok", subs: [] });
  const [shop, setShop] = useState<ShopData | null>(null);

  const load = (url = subURL) =>
    fetch(url + "/info", { cache: "no-store" })
      .then((r) => (r.ok ? r.json() : Promise.reject(new Error(String(r.status)))))
      .then((d: Info) => {
        setInfo(d);
        document.title = d.brand;
      });

  // The Mini App signs in with the launch data Telegram puts after the #.
  const session = () =>
    fetch(subRoot + "/tg/session", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ init_data: initData }), cache: "no-store" })
      .then((r) => (r.ok ? r.json() : Promise.reject(new Error(String(r.status)))))
      .then((d: { subs: TgSub[] }) => {
        if (d.subs.length === 0) {
          setTg({ state: "none", subs: [] });
          return;
        }
        setTg({ state: "ok", subs: d.subs });
        // A new subscription bought here shows at once; the one on screen stays otherwise.
        setSubURL((cur) => (cur && d.subs.some((s) => cur.endsWith("/" + s.token)) ? cur : subRoot + "/" + d.subs[0]!.token));
      })
      .catch(() => setTg({ state: "failed", subs: [] }));
  const refresh = () => {
    void session();
    if (subURL) void load(subURL).catch(() => undefined);
  };
  useEffect(() => {
    if (!tgMode) return;
    tgEvent("web_app_ready");
    tgEvent("web_app_expand");
    void session();
    loadShop(subRoot, initData)
      .then(setShop)
      .catch(() => setShop(null));
  }, []);

  // Telegram tells the Mini App when its payment sheet closes; a paid one is applied by
  // the panel within seconds.
  useEffect(() => {
    if (!tgMode) return;
    let timer = 0;
    const onEvent = (type: string, data: unknown) => {
      if (type === "invoice_closed" && (data as { status?: string } | null)?.status === "paid") {
        window.clearTimeout(timer);
        timer = window.setTimeout(refresh, 2000);
      }
    };
    const w = window as Window & { Telegram?: { WebView?: { receiveEvent?: (type: string, data: unknown) => void } } };
    w.Telegram ??= {};
    w.Telegram.WebView ??= {};
    const prev = w.Telegram.WebView.receiveEvent;
    w.Telegram.WebView.receiveEvent = (type, data) => {
      prev?.(type, data);
      onEvent(type, data);
    };
    const onMessage = (e: MessageEvent) => {
      if (e.origin !== "https://web.telegram.org" || typeof e.data !== "string") return;
      try {
        const m = JSON.parse(e.data) as { eventType?: string; eventData?: unknown };
        if (m.eventType) onEvent(m.eventType, m.eventData);
      } catch {
        // not Telegram's
      }
    };
    window.addEventListener("message", onMessage);
    return () => {
      window.removeEventListener("message", onMessage);
      window.clearTimeout(timer);
      if (w.Telegram?.WebView) w.Telegram.WebView.receiveEvent = prev;
    };
  }, [subURL]);

  const shopFor = (token: string, title: string) =>
    shop && shop.offers.length > 0 ? (
      <Shop data={shop} subRoot={subRoot} initData={initData} token={token} title={title} openInvoice={(slug) => tgEvent("web_app_open_invoice", { slug })} openLink={openOutside} onRefresh={refresh} />
    ) : null;

  useEffect(() => {
    if (subURL) load(subURL).catch(() => setFailed(true));
  }, [subURL]);

  // The Mini App sends an app's "Add" to the browser as #open=<app>: open it right away.
  useEffect(() => {
    const want = new URLSearchParams(location.hash.slice(1)).get("open");
    if (tgMode || !want || !info) return;
    const app = Object.values(APPS)
      .flat()
      .find((a) => a.name === want);
    if (app) location.href = app.link(subURL, info.brand);
  }, [info]);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(subURL);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 2000);
    } catch {
      setQr(true);
    }
  };

  if (tg.state === "none" && shop?.allow_new && shop.offers.length > 0) {
    return (
      <Shell>
        <section className="glass rounded-3xl p-6 text-center">
          <h1 className="font-display text-xl font-medium">{t("sub.tgNoSubTitle")}</h1>
          <p className="mt-2 text-[13px] text-[var(--ink-500)]">{t("sub.tgNoSubText")}</p>
        </section>
        {shopFor("", t("sub.shopNew"))}
      </Shell>
    );
  }
  if (tg.state === "none" || tg.state === "failed") {
    return (
      <Shell>
        <section className="glass rounded-3xl p-6 text-center">
          <h1 className="font-display text-xl font-medium">{tg.state === "none" ? t("sub.tgNoSubTitle") : t("sub.loadFailed")}</h1>
          <p className="mt-2 text-[13px] text-[var(--ink-500)]">{tg.state === "none" ? t("sub.tgNoSubText") : t("sub.tgFailed")}</p>
        </section>
      </Shell>
    );
  }
  if (failed) {
    return (
      <Shell>
        <section className="glass rounded-3xl p-6 text-center">
          <h1 className="font-display text-xl font-medium">{t("sub.loadFailed")}</h1>
          <p className="mt-2 text-[13px] text-[var(--ink-500)]">{t("sub.loadFailedText")}</p>
        </section>
      </Shell>
    );
  }
  if (!info) {
    return (
      <Shell>
        {tgMode ? (
          <p className="px-1 text-[13px] text-[var(--ink-500)]" role="status">
            {t("sub.tgLoading")}
          </p>
        ) : null}
        <Skeleton style={{ height: 96, borderRadius: 24 }} />
        <Skeleton style={{ height: 140, borderRadius: 24 }} />
        <Skeleton style={{ height: 220, borderRadius: 24 }} />
      </Shell>
    );
  }

  const used = info.used_up + info.used_down;
  const left = info.limit != null ? Math.max(0, info.limit - used) : null;
  const pct = info.limit ? (used / info.limit) * 100 : 0;
  const d = info.expires_at ? daysUntil(info.expires_at) : null;
  const firstName = info.name.split(/\s+/)[0] ?? "";
  const tone = ({ active: "ok", expiring: "warn", limited: "bad", expired: "bad", disabled: "off" } as const)[info.state];
  const [leftValue, leftUnit] = left != null ? bytes(left).split(" ") : ["∞", ""];

  return (
    <Shell brand={info.brand}>
      {tg.subs.length > 1 ? (
        <div className="flex gap-1 overflow-x-auto rounded-[14px] bg-[var(--hover)] p-1" role="group" aria-label={t("sub.link")}>
          {tg.subs.map((s) => (
            <button
              key={s.token}
              type="button"
              aria-pressed={subURL.endsWith("/" + s.token)}
              onClick={() => setSubURL(subRoot + "/" + s.token)}
              className="h-8 shrink-0 rounded-[10px] px-3 text-xs font-semibold text-[var(--ink-600)] aria-pressed:bg-white aria-pressed:text-[var(--ink-900)] aria-pressed:shadow-sm"
            >
              {s.name}
            </button>
          ))}
        </div>
      ) : null}
      <motion.section className="glass rounded-3xl p-4" initial={{ opacity: 0, y: 8 }} animate={{ opacity: 1, y: 0 }}>
        <h1 className="font-display text-xl leading-7 font-medium tracking-tight">{t(`sub.status.${info.state}`, { name: firstName })}</h1>
        <div className="mt-2 flex items-center justify-between gap-2 text-[13px] text-[var(--ink-600)]">
          <span>{info.expires_at ? t("sub.until", { date: dateLong(info.expires_at) }) : t("sub.forever")}</span>
          {d !== null && d >= 0 ? <Pill tone={tone}>{days(d)}</Pill> : null}
        </div>
        {(info.state === "expired" || info.state === "limited" || info.state === "disabled") && info.support_url && !(tgMode && shop?.offers.length) ? (
          <a className="btn btn-primary btn-block mt-4" href={info.support_url} target="_blank" rel="noreferrer noopener" {...outside(info.support_url)}>
            {t("sub.renew")}
          </a>
        ) : null}
      </motion.section>

      {tgMode ? shopFor(subURL.slice(subURL.lastIndexOf("/") + 1), t("sub.shop")) : null}

      <section className="glass grid grid-cols-[104px_1fr] items-center gap-4 rounded-3xl p-4">
        <Ring size={104} pct={info.limit != null ? pct : 100} label={leftValue} sub={left != null ? t("sub.left", { unit: leftUnit ?? "" }) : t("users.unlimited")} />
        <div className="flex flex-col gap-2 text-xs text-[var(--ink-500)]">
          <div>
            {t("userDrawer.used")}
            <b className="num block text-base font-medium text-[var(--ink-900)]">{info.limit != null ? `${bytes(used)} ${t("users.of", { total: bytes(info.limit) })}` : bytes(used)}</b>
          </div>
          {info.resets_at ? (
            <div>
              {t("sub.resets")}
              <b className="block text-base font-medium text-[var(--ink-900)]">{dateShort(info.resets_at)}</b>
            </div>
          ) : null}
          {info.device_limit ? (
            <div>
              {t("sub.devices")}
              <b className="num block text-base font-medium text-[var(--ink-900)]">{t("sub.upTo", { n: info.device_limit })}</b>
            </div>
          ) : null}
        </div>
      </section>

      {info.binding || info.devices?.length ? <Devices info={info} subURL={subURL} reload={() => load()} /> : null}

      <section className="glass rounded-3xl p-4">
        <h2 className="mb-3 text-[15px] font-semibold">{t("sub.connect")}</h2>
        <div className="mb-3 flex gap-1 rounded-[14px] bg-[var(--hover)] p-1" role="group" aria-label={t("sub.platform")}>
          {(
            [
              ["ios", "iPhone"],
              ["android", "Android"],
              ["windows", "Windows"],
              ["macos", "Mac"],
            ] as const
          ).map(([k, l]) => (
            <button
              key={k}
              type="button"
              aria-pressed={platform === k}
              onClick={() => setPlatform(k)}
              className="h-8 flex-1 rounded-[10px] text-xs font-semibold text-[var(--ink-600)] aria-pressed:bg-white aria-pressed:text-[var(--ink-900)] aria-pressed:shadow-sm"
            >
              {l}
            </button>
          ))}
        </div>
        <div className="row-list">
          {APPS[platform].map((a, i) => (
            <div key={a.name} className="grid grid-cols-[40px_minmax(0,1fr)_auto] items-center gap-3 py-2">
              <span className="font-display grid h-10 w-10 place-items-center rounded-xl border border-[var(--hairline)] bg-white text-sm font-semibold text-[var(--ink-700)]" aria-hidden>
                {a.name[0]}
              </span>
              <div className="min-w-0">
                <div className="text-sm font-semibold">{a.name}</div>
                <div className="text-xs text-[var(--ink-500)]">
                  {i === 0 ? <span className="font-medium text-[var(--mikan-700)]">{t("sub.recommended")} · </span> : null}
                  {t(`sub.notes.${a.note}`)}
                </div>
              </div>
              <a
                className={i === 0 ? "btn btn-primary btn-sm" : "btn btn-glass btn-sm"}
                href={a.link(subURL, info.brand)}
                title={tgMode ? t("sub.tgBrowser") : undefined}
                {...outside(subURL + "#open=" + enc(a.name))}
              >
                {t("common.add")}
              </a>
            </div>
          ))}
        </div>
      </section>

      <section className="glass rounded-3xl p-4">
        <h2 className="mb-3 text-[15px] font-semibold">{t("sub.howTo")}</h2>
        <ol className="flex flex-col gap-3 text-[13px] text-[var(--ink-600)]">
          {([1, 2, 3] as const).map((n) => (
            <li key={n} className="grid grid-cols-[28px_1fr] items-start gap-3">
              <span className="font-display grid h-7 w-7 place-items-center rounded-full border border-[var(--hairline)] bg-white text-xs font-semibold">{n}</span>
              <div>
                <b className="block font-semibold text-[var(--ink-900)]">{t(`sub.steps.${n}.title`)}</b>
                {t(`sub.steps.${n}.text`)}
              </div>
            </li>
          ))}
        </ol>
      </section>

      <section className="glass rounded-3xl p-4">
        <h2 className="mb-3 text-[15px] font-semibold">{t("sub.link")}</h2>
        <div className="link-field">
          <span className="mono">{subURL}</span>
          <button type="button" className="icon-btn" onClick={copy} aria-label={t("common.copyLink")}>
            {copied ? <Check size={18} className="text-[var(--leaf-500)]" /> : <Copy size={18} />}
          </button>
        </div>
        <button type="button" className="btn btn-glass btn-sm mt-2" onClick={() => setQr((v) => !v)} aria-expanded={qr}>
          <QrCode size={16} aria-hidden /> {t("sub.qrOther")}
        </button>
        {qr ? (
          <div className="mt-3 flex justify-center">
            <QR value={subURL} size={200} />
          </div>
        ) : null}
      </section>

      {info.telegram && !tgMode ? (
        <a className="btn btn-glass btn-block h-12 rounded-2xl" href={info.telegram} target="_blank" rel="noreferrer noopener">
          <Send size={18} aria-hidden /> {t("sub.openTelegram")}
        </a>
      ) : null}
      {info.support_url ? (
        <a className="btn btn-glass btn-block h-12 rounded-2xl" href={info.support_url} target="_blank" rel="noreferrer noopener" {...outside(info.support_url)}>
          <LifeBuoy size={18} aria-hidden /> {t("sub.support")}
        </a>
      ) : null}
    </Shell>
  );
}

const desktopOS = /windows|mac|linux|darwin/i;

function deviceName(d: Device): string {
  if (d.shared) return t("sub.sharedPlace");
  return d.model || [d.os, d.os_version].filter(Boolean).join(" ") || appName(d.app) || t("userDrawer.device");
}

/** The subscriber's own devices: each holds a place; one may be unbound a day. */
function Devices({ info, subURL, reload }: { info: Info; subURL: string; reload: () => Promise<void> }) {
  const [confirm, setConfirm] = useState<number | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const list = info.devices ?? [];
  const full = info.device_limit > 0 && list.length >= info.device_limit;
  const wait = info.unbind_after && new Date(info.unbind_after).getTime() > Date.now() ? info.unbind_after : "";

  const unbind = async (id: number) => {
    setBusy(true);
    setError("");
    try {
      const r = await fetch(`${subURL}/devices/${id}/unbind`, { method: "POST", cache: "no-store" });
      if (r.status === 429) {
        const b = (await r.json().catch(() => ({}))) as { unbind_after?: string };
        setError(b.unbind_after ? t("sub.unbindAfter", { date: dateShort(b.unbind_after), time: time(b.unbind_after) }) : t("sub.unbindFailed"));
      } else if (!r.ok) {
        setError(t("sub.unbindFailed"));
      }
      setConfirm(null);
      await reload();
    } catch {
      setError(t("sub.unbindFailed"));
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="glass rounded-3xl p-4" aria-labelledby="devices-title">
      <div className="mb-3 flex items-center justify-between gap-2">
        <h2 id="devices-title" className="text-[15px] font-semibold">
          {t("sub.devicesTitle")}
        </h2>
        {info.device_limit > 0 ? <Pill tone={full ? "warn" : "ok"}>{t("sub.devicesCount", { n: list.length, limit: info.device_limit })}</Pill> : null}
      </div>
      {full ? <p className="mb-3 rounded-2xl bg-[var(--hover)] p-3 text-[13px] text-[var(--ink-700)]">{t("sub.devicesFull")}</p> : null}
      {list.length === 0 ? (
        <p className="text-[13px] text-[var(--ink-500)]">{t("sub.devicesEmpty")}</p>
      ) : (
        <ul className="row-list">
          {list.map((d) => {
            const name = deviceName(d);
            const system = !d.shared && d.model ? [d.os, d.os_version].filter(Boolean).join(" ") : "";
            const meta = [system, appName(d.app)].filter(Boolean).join(" · ");
            return (
              <li key={d.id} className="py-2">
                <div className="grid grid-cols-[40px_minmax(0,1fr)_auto] items-center gap-3">
                  <span className="grid h-10 w-10 place-items-center rounded-xl border border-[var(--hairline)] bg-white text-[var(--ink-600)]" aria-hidden>
                    {d.shared ? <Layers size={18} /> : desktopOS.test(d.os) ? <Laptop size={18} /> : <Smartphone size={18} />}
                  </span>
                  <div className="min-w-0">
                    <div className="truncate text-sm font-semibold">{name}</div>
                    <div className="text-xs break-words text-[var(--ink-500)]">
                      {meta ? `${meta} · ` : ""}
                      {ago(d.last_seen)}
                    </div>
                  </div>
                  {confirm === d.id ? null : (
                    <Button size="sm" disabled={busy || !!wait} onClick={() => setConfirm(d.id)} aria-label={t("sub.unbindLabel", { name })}>
                      {t("sub.unbind")}
                    </Button>
                  )}
                </div>
                {confirm === d.id ? (
                  <div className="mt-2 rounded-2xl bg-[var(--hover)] p-3" role="group" aria-label={t("sub.unbindLabel", { name })}>
                    <p className="text-[13px] text-[var(--ink-700)]">{t("sub.unbindWarn")}</p>
                    <div className="mt-2 flex justify-end gap-2">
                      <Button variant="ghost" size="sm" disabled={busy} onClick={() => setConfirm(null)}>
                        {t("common.cancel")}
                      </Button>
                      <Button variant="danger-solid" size="sm" loading={busy} onClick={() => void unbind(d.id)}>
                        {t("sub.unbindYes")}
                      </Button>
                    </div>
                  </div>
                ) : null}
              </li>
            );
          })}
        </ul>
      )}
      {error ? (
        <p className="mt-2 text-[13px] text-[var(--berry-600)]" role="alert">
          {error}
        </p>
      ) : wait ? (
        <p className="mt-2 text-xs text-[var(--ink-500)]">{t("sub.unbindAfter", { date: dateShort(wait), time: time(wait) })}</p>
      ) : null}
      <p className="mt-2 text-xs text-[var(--ink-500)]">{t("sub.devicesNote")}</p>
    </section>
  );
}

function Shell({ brand, children }: { brand?: string; children: React.ReactNode }) {
  return (
    <main className="mx-auto flex max-w-[440px] flex-col gap-3 px-4 pt-6 pb-10">
      <div className="flex items-center gap-2 px-1 pb-1">
        <span className="font-display grid h-7 w-7 place-items-center rounded-[9px] bg-[var(--ink-900)] text-[13px] font-semibold text-white">{(brand ?? "V")[0]}</span>
        <span className="font-display text-[15px] font-semibold tracking-tight">{brand ?? ""}</span>
        <LangSwitch className="ml-auto" />
      </div>
      {children}
    </main>
  );
}

function Root() {
  const locale = useLocale();
  return <SubPage key={locale} />;
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <Atmosphere />
    <Root />
  </StrictMode>,
);
