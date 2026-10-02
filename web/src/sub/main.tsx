import "../styles/app.css";
import { Check, Copy, LifeBuoy, QrCode, Send } from "lucide-react";
import { StrictMode, useCallback, useEffect, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import { Atmosphere } from "../components/atmosphere";
import { ErrorBoundary } from "../components/error-boundary";
import { LangSwitch } from "../components/lang";
import { Bar, Button, Pill, QR, Ring, Skeleton } from "../components/ui";
import { initI18n, t, useLocale } from "../i18n";
import { subDicts } from "../i18n/sub";
import { bytes, dateLong, dateShort, days, daysUntil } from "../lib/format";
import { initTheme } from "../lib/theme";
import { safeHref } from "../lib/url";
import { APPS, detect, enc, type Platform } from "./apps";
import { Devices } from "./devices";
import { initData, json, openOutside, outside, pageURL, request, subRoot, tgEvent, tgMode, tokenOf } from "./net";
import { loadShop, Shop, type ShopData } from "./shop";
import type { Info, TgSub } from "./types";

initTheme();

/** Pauses between the reloads after a payment: the panel applies a paid one within seconds. */
const AFTER_PAYMENT_MS = [2_000, 5_000, 10_000];

function SubPage() {
  // Texts are read at render time: follow a language switch without remounting the page
  // (a remount would sign in to the Mini App and fetch everything again).
  useLocale();
  // The info that came for one subscription's address; it is shown only while that address
  // is the one on screen, so a late answer of another subscription never takes its place.
  const [info, setInfo] = useState<{ url: string; data: Info } | null>(null);
  const [failedURL, setFailedURL] = useState<string | null>(null);
  const [platform, setPlatform] = useState<Platform>(detect);
  const [qr, setQr] = useState(false);
  const [copied, setCopied] = useState(false);
  const [subURL, setSubURL] = useState(tgMode ? "" : pageURL);
  const [tg, setTg] = useState<{ state: "loading" | "none" | "failed" | "ok"; subs: TgSub[] }>({ state: tgMode ? "loading" : "ok", subs: [] });
  const [shop, setShop] = useState<ShopData | null>(null);
  const [packages, setPackages] = useState<{ token: string; data: ShopData } | null>(null);
  const current = info && info.url === subURL ? info.data : null;
  const failed = !current && failedURL === subURL;

  const loadInfo = useCallback(async (url: string, signal?: AbortSignal) => {
    const d = await request(url + "/info", { signal }).then((r) => json<Info>(r));
    setInfo({ url, data: d });
    setFailedURL(null);
    document.title = d.brand;
  }, []);

  // The Mini App signs in with the launch data Telegram puts after the #.
  const session = useCallback(
    () =>
      request(subRoot + "/tg/session", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ init_data: initData }) })
        .then((r) => json<{ subs: TgSub[] }>(r))
        .then((d) => {
          if (d.subs.length === 0) {
            setTg({ state: "none", subs: [] });
            return;
          }
          setTg({ state: "ok", subs: d.subs });
          // A new subscription bought here shows at once; the one on screen stays otherwise.
          setSubURL((cur) => (cur && d.subs.some((s) => cur.endsWith("/" + s.token)) ? cur : subRoot + "/" + d.subs[0]!.token));
        })
        // A refresh that fails leaves what is on screen; only a first sign-in shows the error.
        .catch(() => setTg((cur) => (cur.state === "ok" && cur.subs.length ? cur : { state: "failed", subs: [] }))),
    [],
  );

  const token = tokenOf(subURL);
  // What a refresh reloads: the session, the subscription's info and the shop.
  const refresh = () => {
    void session();
    if (subURL) void loadInfo(subURL).catch(() => undefined);
    if (tgMode) {
      loadShop(subRoot, initData)
        .then(setShop)
        .catch(() => undefined);
      if (subURL) {
        loadShop(subRoot, initData, token)
          .then((data) => setPackages({ token, data }))
          .catch(() => undefined);
      }
    }
  };
  const refreshRef = useRef(refresh);
  refreshRef.current = refresh;

  useEffect(() => {
    if (!tgMode) return;
    tgEvent("web_app_ready");
    tgEvent("web_app_expand");
    void session();
    loadShop(subRoot, initData)
      .then(setShop)
      .catch(() => setShop(null));
  }, [session]);

  // Telegram tells the Mini App when its payment sheet closes; a paid one is applied by
  // the panel within seconds, so the page reloads a few times, a little later each time.
  useEffect(() => {
    if (!tgMode) return;
    let timers: number[] = [];
    const onEvent = (type: string, data: unknown) => {
      if (type === "invoice_closed" && (data as { status?: string } | null)?.status === "paid") {
        timers.forEach((id) => window.clearTimeout(id));
        timers = AFTER_PAYMENT_MS.map((ms) => window.setTimeout(() => refreshRef.current(), ms));
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
      timers.forEach((id) => window.clearTimeout(id));
      if (w.Telegram?.WebView) w.Telegram.WebView.receiveEvent = prev;
    };
  }, []);

  // One subscription's info (and, in the Mini App, its traffic packages) at a time: a
  // switch cancels what the previous one still waits for.
  useEffect(() => {
    if (!subURL) return;
    const ctl = new AbortController();
    loadInfo(subURL, ctl.signal).catch(() => {
      if (!ctl.signal.aborted) setFailedURL(subURL);
    });
    if (tgMode) {
      const tok = tokenOf(subURL);
      loadShop(subRoot, initData, tok)
        .then((data) => !ctl.signal.aborted && setPackages({ token: tok, data }))
        .catch(() => !ctl.signal.aborted && setPackages(null));
    }
    return () => ctl.abort();
  }, [subURL, loadInfo]);

  // The Mini App sends an app's "Add" to the browser as #open=<app>: open it once, right
  // away. The mark is dropped from the address, or a reload and every refresh of the info
  // would ask the system to open the app again.
  const launched = useRef(false);
  useEffect(() => {
    if (tgMode || !current || launched.current) return;
    const want = new URLSearchParams(location.hash.slice(1)).get("open");
    if (!want) return;
    launched.current = true;
    history.replaceState(null, "", location.pathname + location.search);
    const app = Object.values(APPS)
      .flat()
      .find((a) => a.name === want);
    if (app) location.href = app.link(subURL, current.brand);
  }, [current, subURL]);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(subURL);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 2000);
    } catch {
      setQr(true);
    }
  };

  const shopFor = (shopToken: string, title: string) =>
    shop && shop.offers.length > 0 ? (
      <Shop
        data={shop}
        offers={shop.offers}
        subRoot={subRoot}
        initData={initData}
        token={shopToken}
        title={title}
        openInvoice={(slug) => tgEvent("web_app_open_invoice", { slug })}
        openLink={openOutside}
        onRefresh={refresh}
      />
    ) : null;
  // Traffic packages are for the subscription on screen.
  const packagesShop =
    packages && packages.data.packages?.length && packages.token === token ? (
      <Shop
        key={token}
        data={packages.data}
        offers={packages.data.packages}
        field="package_id"
        pick={t("sub.packagesPick")}
        subRoot={subRoot}
        initData={initData}
        token={token}
        title={t("sub.packages")}
        openInvoice={(slug) => tgEvent("web_app_open_invoice", { slug })}
        openLink={openOutside}
        onRefresh={refresh}
      />
    ) : null;

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
        <section className="glass rounded-3xl p-6 text-center" role={tg.state === "failed" ? "alert" : undefined}>
          <h1 className="font-display text-xl font-medium">{tg.state === "none" ? t("sub.tgNoSubTitle") : t("sub.loadFailed")}</h1>
          <p className="mt-2 text-[13px] text-[var(--ink-500)]">{tg.state === "none" ? t("sub.tgNoSubText") : t("sub.tgFailed")}</p>
          {tg.state === "failed" ? (
            <Button variant="primary" className="mt-4" onClick={() => void session()}>
              {t("common.retry")}
            </Button>
          ) : null}
        </section>
      </Shell>
    );
  }
  if (failed) {
    return (
      <Shell>
        <section className="glass rounded-3xl p-6 text-center" role="alert">
          <h1 className="font-display text-xl font-medium">{t("sub.loadFailed")}</h1>
          <p className="mt-2 text-[13px] text-[var(--ink-500)]">{t("sub.loadFailedText")}</p>
          <Button
            variant="primary"
            className="mt-4"
            onClick={() => {
              setFailedURL(null);
              loadInfo(subURL).catch(() => setFailedURL(subURL));
            }}
          >
            {t("common.retry")}
          </Button>
        </section>
      </Shell>
    );
  }
  if (!current) {
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

  const used = current.used_up + current.used_down;
  const extra = current.extra ?? 0;
  // Traffic packages are spent after the tariff's traffic: they add to what is left.
  const left = current.limit != null ? Math.max(0, current.limit - used) + extra : null;
  const cap = current.limit != null ? Math.max(current.limit, used) + extra : 0;
  const pct = cap ? (used / cap) * 100 : 0;
  const ofTotal = (limit: number, more: number) => t("sub.of", { total: more > 0 ? t("sub.plusPackages", { limit: bytes(limit), extra: bytes(more) }) : bytes(limit) });
  const d = current.expires_at ? daysUntil(current.expires_at) : null;
  const firstName = current.name.split(/\s+/)[0] ?? "";
  const tone = ({ active: "ok", expiring: "warn", limited: "bad", expired: "bad", disabled: "off" } as const)[current.state];
  const [leftValue, leftUnit] = left != null ? bytes(left).split(" ") : ["∞", ""];
  // Links from the server are followed only when they are http(s) (or Telegram's own).
  const support = safeHref(current.support_url, { tg: true });
  const telegram = safeHref(current.telegram);

  return (
    <Shell brand={current.brand}>
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
      <section className="glass reveal rounded-3xl p-4">
        <h1 className="font-display text-xl leading-7 font-medium tracking-tight">{t(`sub.status.${current.state}`, { name: firstName })}</h1>
        <div className="mt-2 flex items-center justify-between gap-2 text-[13px] text-[var(--ink-600)]">
          <span>{current.expires_at ? t("sub.until", { date: dateLong(current.expires_at) }) : t("sub.forever")}</span>
          {d !== null && d >= 0 ? <Pill tone={tone}>{days(d)}</Pill> : null}
        </div>
        {(current.state === "expired" || current.state === "limited" || current.state === "disabled") && support && !(tgMode && shop?.offers.length) ? (
          <a className="btn btn-primary btn-block mt-4" href={support} target="_blank" rel="noreferrer noopener" {...outside(support)}>
            {t("sub.renew")}
          </a>
        ) : null}
      </section>

      {tgMode ? shopFor(token, t("sub.shop")) : null}
      {tgMode ? packagesShop : null}

      <section className="glass grid grid-cols-[104px_1fr] items-center gap-4 rounded-3xl p-4">
        <Ring size={104} pct={current.limit != null ? pct : 100} label={leftValue} sub={left != null ? t("sub.left", { unit: leftUnit ?? "" }) : t("sub.unlimited")} />
        <div className="flex flex-col gap-2 text-xs text-[var(--ink-500)]">
          <div>
            {t("sub.used")}
            <b className="num block text-base font-medium text-[var(--ink-900)]">{current.limit != null ? `${bytes(used)} ${ofTotal(current.limit, extra)}` : bytes(used)}</b>
          </div>
          {current.resets_at ? (
            <div>
              {t("sub.resets")}
              <b className="block text-base font-medium text-[var(--ink-900)]">{dateShort(current.resets_at)}</b>
            </div>
          ) : null}
          {current.device_limit ? (
            <div>
              {t("sub.devices")}
              <b className="num block text-base font-medium text-[var(--ink-900)]">{t("sub.upTo", { n: current.device_limit })}</b>
            </div>
          ) : null}
        </div>
      </section>

      {current.pools?.length ? (
        <section className="glass rounded-3xl p-4">
          <h2 className="mb-3 text-[15px] font-semibold">{t("sub.pools")}</h2>
          <ul className="flex flex-col gap-3">
            {current.pools.map((p) => (
              <li key={p.name}>
                <div className="mb-1 flex items-center justify-between gap-2 text-[13px]">
                  <span className="font-medium">{p.name}</span>
                  <span className="num text-xs text-[var(--ink-600)]">{p.limit != null ? `${bytes(p.used)} ${ofTotal(p.limit, p.extra ?? 0)}` : bytes(p.used)}</span>
                </div>
                {p.limit != null ? <Bar label={p.name} pct={Math.min(100, (p.used / (Math.max(p.limit, p.used) + (p.extra ?? 0))) * 100)} /> : null}
                {p.limit != null && p.used >= p.limit && !p.extra ? <p className="mt-1 text-xs text-[var(--berry-600)]">{t("sub.poolOut")}</p> : null}
              </li>
            ))}
          </ul>
        </section>
      ) : null}

      {current.binding || current.devices?.length ? <Devices info={current} subURL={subURL} reload={() => loadInfo(subURL).catch(() => undefined)} /> : null}

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
                href={a.link(subURL, current.brand)}
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

      {telegram && !tgMode ? (
        <a className="btn btn-glass btn-block h-12 rounded-2xl" href={telegram} target="_blank" rel="noreferrer noopener">
          <Send size={18} aria-hidden /> {t("sub.openTelegram")}
        </a>
      ) : null}
      {support ? (
        <a className="btn btn-glass btn-block h-12 rounded-2xl" href={support} target="_blank" rel="noreferrer noopener" {...outside(support)}>
          <LifeBuoy size={18} aria-hidden /> {t("sub.support")}
        </a>
      ) : null}
    </Shell>
  );
}

function Shell({ brand, children }: { brand?: string; children: React.ReactNode }) {
  return (
    <main className="calm-glass mx-auto flex max-w-[440px] flex-col gap-3 px-4 pt-[calc(24px+env(safe-area-inset-top))] pb-[calc(40px+env(safe-area-inset-bottom))]">
      <div className="flex items-center gap-2 px-1 pb-1">
        <span className="font-display grid h-7 w-7 place-items-center rounded-[9px] bg-[var(--ink-900)] text-[13px] font-semibold text-white">{(brand ?? "V")[0]}</span>
        <span className="font-display text-[15px] font-semibold tracking-tight">{brand ?? ""}</span>
        <LangSwitch className="ml-auto" />
      </div>
      {children}
    </main>
  );
}

// Dictionaries load before the first render: t() stays synchronous everywhere.
void initI18n(subDicts).then(() => {
  // The tab's title until the subscription says its brand (sub.html's own is Russian).
  document.title = t("sub.pageTitle");
  createRoot(document.getElementById("root")!).render(
    <StrictMode>
      <ErrorBoundary>
        <Atmosphere calm />
        <SubPage />
      </ErrorBoundary>
    </StrictMode>,
  );
});
