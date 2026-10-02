import { Laptop, Layers, Smartphone } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Button, Pill } from "../components/ui";
import { t } from "../i18n";
import { desktopOS, deviceDetails, deviceLabel } from "../lib/devices";
import { ago, dateShort, time } from "../lib/format";
import { request } from "./net";
import type { Device, Info } from "./types";

function deviceName(d: Device): string {
  return d.shared ? t("sub.sharedPlace") : deviceLabel(d, t("sub.device"));
}

/** The subscriber's own devices: each holds a place; one may be unbound a day. */
export function Devices({ info, subURL, reload }: { info: Info; subURL: string; reload: () => Promise<void> }) {
  const [confirm, setConfirm] = useState<number | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  // The button that opened the confirmation: focus goes back to it, not to the page's top.
  const opener = useRef<number | null>(null);
  const list = info.devices ?? [];
  const full = info.device_limit > 0 && list.length >= info.device_limit;
  const wait = info.unbind_after && new Date(info.unbind_after).getTime() > Date.now() ? info.unbind_after : "";

  useEffect(() => {
    if (confirm !== null || opener.current === null) return;
    // Cancelled: the button is back, give it the focus again.
    document.getElementById(`unbind-${opener.current}`)?.focus();
    opener.current = null;
  }, [confirm]);

  const ask = (id: number) => {
    opener.current = id;
    setConfirm(id);
  };

  const unbind = async (id: number) => {
    setBusy(true);
    setError("");
    try {
      const r = await request(`${subURL}/devices/${id}/unbind`, { method: "POST" });
      if (r.status === 429) {
        const b = (await r.json().catch(() => ({}))) as { unbind_after?: string };
        setError(b.unbind_after ? t("sub.unbindAfter", { date: dateShort(b.unbind_after), time: time(b.unbind_after) }) : t("sub.unbindFailed"));
      } else if (!r.ok) {
        setError(t("sub.unbindFailed"));
      }
      // The device is gone from the list (or the answer says why not): focus the heading
      // once the list is back, a button that no longer exists cannot keep it.
      opener.current = null;
      setConfirm(null);
      await reload();
      document.getElementById("devices-title")?.focus();
    } catch {
      setError(t("sub.unbindFailed"));
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="glass rounded-3xl p-4" aria-labelledby="devices-title">
      <div className="mb-3 flex items-center justify-between gap-2">
        <h2 id="devices-title" tabIndex={-1} className="text-[15px] font-semibold">
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
            const meta = deviceDetails(d, !d.shared);
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
                    <Button id={`unbind-${d.id}`} size="sm" disabled={busy || !!wait} onClick={() => ask(d.id)} aria-label={t("sub.unbindLabel", { name })}>
                      {t("sub.unbind")}
                    </Button>
                  )}
                </div>
                {confirm === d.id ? (
                  <div className="mt-2 rounded-2xl bg-[var(--hover)] p-3" role="group" aria-label={t("sub.unbindLabel", { name })}>
                    <p className="text-[13px] text-[var(--ink-700)]">{t("sub.unbindWarn")}</p>
                    <div className="mt-2 flex justify-end gap-2">
                      <Button variant="ghost" size="sm" disabled={busy} autoFocus onClick={() => setConfirm(null)}>
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
