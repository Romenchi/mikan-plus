// The Mini App's shop: the plans on sale and a way to pay for them. Stars open Telegram's
// own payment sheet; the marketplace's adapters (YooKassa, CryptoBot…) open their pages. The subscription itself is
// issued or renewed by the panel once the provider confirms the payment.
import { Check, RefreshCw } from "lucide-react";
import { useEffect, useState } from "react";
import { Button } from "../components/ui";
import { t, type Key } from "../i18n";
import { rubles } from "../lib/format";

export type Offer = { id: number; name: string; description: string; stars?: number; rub?: number };
export type ShopData = {
  allow_new: boolean;
  providers: { stars: boolean };
  /** Marketplace adapters that take rubles, by the name the buyer sees. */
  addons?: { provider: `addon:${string}`; name: string }[];
  offers: Offer[];
  packages?: Offer[];
};
type Provider = "stars" | `addon:${string}`;

// YooKassa and CryptoBot keep the words buyers knew them by, and come first.
const KNOWN: Record<string, Key> = { "addon:yookassa": "sub.shopCard", "addon:cryptobot": "sub.shopCrypto" };
const rank = (p: string) => (p === "addon:yookassa" ? 0 : p === "addon:cryptobot" ? 1 : 2);

const FAIL: Record<string, Key> = {
  not_for_sale: "sub.shopNotForSale",
  too_many_invoices: "sub.shopTooMany",
  too_many_subs: "sub.shopTooManySubs",
};

/** What the account can buy; with token, the traffic packages of that subscription too. */
export function loadShop(subRoot: string, initData: string, token = ""): Promise<ShopData> {
  return fetch(subRoot + "/tg/shop", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ init_data: initData, token }), cache: "no-store" }).then(
    (r) => (r.ok ? r.json() : Promise.reject(new Error(String(r.status)))),
  );
}

/**
 * Offers and pay buttons: plans (field tariff_id) or traffic packages (package_id). token
 * is the subscription to renew or add traffic to; "" buys a new one. openInvoice/openLink
 * go through the Mini App bridge.
 */
export function Shop({
  data,
  offers,
  field = "tariff_id",
  pick = t("sub.shopPick"),
  subRoot,
  initData,
  token,
  title,
  openInvoice,
  openLink,
  onRefresh,
}: {
  data: ShopData;
  offers: Offer[];
  field?: "tariff_id" | "package_id";
  pick?: string;
  subRoot: string;
  initData: string;
  token: string;
  title: string;
  openInvoice: (slug: string) => void;
  openLink: (url: string) => void;
  onRefresh: () => void;
}) {
  const [picked, setPicked] = useState<number | null>(offers.length === 1 ? offers[0]!.id : null);
  const [busy, setBusy] = useState<Provider | null>(null);
  const [error, setError] = useState("");
  const [opened, setOpened] = useState(false);
  useEffect(() => setError(""), [picked]);
  const offer = offers.find((o) => o.id === picked);
  const failText = (code: string) => (field === "package_id" && code === "not_for_sale" ? t("sub.packageNotForSale") : t(FAIL[code] ?? "sub.shopFail"));

  const pay = async (provider: Provider) => {
    if (!offer) return;
    setBusy(provider);
    setError("");
    try {
      const r = await fetch(subRoot + "/tg/pay", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ init_data: initData, [field]: offer.id, provider, token }),
        cache: "no-store",
      });
      const body = (await r.json().catch(() => ({}))) as { url?: string; code?: string };
      if (!r.ok || !body.url) {
        setError(failText(body.code ?? ""));
        return;
      }
      // A Stars link is https://t.me/$<slug>: Telegram opens its payment sheet for it.
      const slug = /^https:\/\/t\.me\/\$(.+)$/.exec(body.url)?.[1];
      if (provider === "stars" && slug) openInvoice(slug);
      else openLink(body.url);
      setOpened(true);
    } catch {
      setError(t("sub.shopFail"));
    } finally {
      setBusy(null);
    }
  };

  const methods: { id: Provider; label: string; price?: string }[] = [];
  if (offer?.stars && data.providers.stars) methods.push({ id: "stars", label: t("sub.shopStars"), price: `⭐ ${offer.stars}` });
  if (offer?.rub) {
    const adapters = [...(data.addons ?? [])].sort((a, b) => rank(a.provider) - rank(b.provider) || a.provider.localeCompare(b.provider));
    for (const a of adapters) methods.push({ id: a.provider, label: KNOWN[a.provider] ? t(KNOWN[a.provider]!) : a.name, price: rubles(offer.rub) });
  }

  return (
    <section className="glass rounded-3xl p-4" aria-label={title}>
      <h2 className="mb-1 text-[15px] font-semibold">{title}</h2>
      <p className="mb-3 text-xs text-[var(--ink-500)]">{pick}</p>
      <div className="flex flex-col gap-2" role="radiogroup" aria-label={pick}>
        {offers.map((o) => (
          <button key={o.id} type="button" role="radio" aria-checked={picked === o.id} className="opt" onClick={() => setPicked(o.id)}>
            <span className="flex items-center justify-between gap-2">
              <span className="font-semibold">{o.name}</span>
              <span className="num text-[13px] text-[var(--ink-700)]">{o.rub ? rubles(o.rub) : `⭐ ${o.stars}`}</span>
            </span>
            <span className="text-xs text-[var(--ink-500)]">{o.description}</span>
          </button>
        ))}
      </div>
      {offer ? (
        <div className="mt-3 grid gap-2" style={{ gridTemplateColumns: `repeat(${Math.min(3, Math.max(1, methods.length))}, minmax(0, 1fr))` }}>
          {methods.map((m, i) => (
            <Button key={m.id} variant={i === 0 ? "primary" : "glass"} className="flex-col gap-0 py-2" style={{ height: "auto" }} loading={busy === m.id} disabled={!!busy} onClick={() => void pay(m.id)}>
              <span className="text-[13px] font-semibold">{m.price}</span>
              <span className="text-[11px] opacity-80">{m.label}</span>
            </Button>
          ))}
        </div>
      ) : null}
      {error ? (
        <p className="mt-3 text-xs text-[var(--berry-600)]" role="alert">
          {error}
        </p>
      ) : null}
      {opened && !error ? (
        <div className="mt-3 flex items-center justify-between gap-2 text-xs text-[var(--ink-600)]" role="status">
          <span className="flex items-center gap-2">
            <Check size={14} className="shrink-0 text-[var(--leaf-500)]" aria-hidden /> {t("sub.shopDone")}
          </span>
          <Button size="sm" variant="ghost" onClick={onRefresh}>
            <RefreshCw size={14} aria-hidden /> {t("sub.shopRefresh")}
          </Button>
        </div>
      ) : null}
    </section>
  );
}

