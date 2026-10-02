import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import clsx from "clsx";
import { ChevronRight, RefreshCw } from "lucide-react";
import { useState, type FormEvent } from "react";
import { api, errorText, unwrap, type Schemas } from "../../../api/client";
import { qk, usePaymentSettings, useUpdates } from "../../../api/hooks";
import { Confirm } from "../../../components/overlay";
import { StaleNotice } from "../../../components/query";
import { useToast } from "../../../components/toast";
import { Button, ErrorState, Field, Pill, Skeleton } from "../../../components/ui";
import { Switch } from "../../../components/switch";
import { getLocale, LOCALES, t } from "../../../i18n";
import { useDraft } from "../../../lib/draft";
import { fieldErrors } from "../../../lib/fields";
import { ago } from "../../../lib/format";
import { useSaveSettings } from "./shared";

export function ServerCard({ s }: { s: Schemas["SettingsView"] }) {
  const save = useSaveSettings();
  // Saving another card replaces `s`: what is typed here stays.
  const { draft: form, setDraft: setForm } = useDraft({ public_host: s.public_host, domain: s.domain, quiet_hour_utc: String(s.quiet_hour_utc) });
  const errors = fieldErrors(save.error);
  const submit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate({ public_host: form.public_host, domain: form.domain, quiet_hour_utc: Number(form.quiet_hour_utc) });
  };
  const set = (k: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement>) => setForm((f) => ({ ...f, [k]: e.target.value }));
  return (
    <section className="card glass reveal">
      <form onSubmit={submit} noValidate>
        <div className="card-head">
          <h2 className="card-title">{t("settings.server")}</h2>
        </div>
        <Field label={t("settings.host")} htmlFor="s-host" hint={t("settings.hostHint")} error={errors.public_host}>
          <input id="s-host" className="input mono" value={form.public_host} onChange={set("public_host")} aria-invalid={!!errors.public_host} />
        </Field>
        <Field label={t("settings.domain")} htmlFor="s-domain" hint={t("settings.domainHint")} error={errors.domain}>
          <input id="s-domain" className="input mono" value={form.domain} onChange={set("domain")} placeholder="vpn.example.com" aria-invalid={!!errors.domain} />
        </Field>
        <Field label={t("settings.quietHour")} htmlFor="s-quiet" hint={t("settings.quietHourHint")}>
          <input id="s-quiet" className="input max-w-[100px]" inputMode="numeric" value={form.quiet_hour_utc} onChange={set("quiet_hour_utc")} />
        </Field>
        <Button type="submit" variant="primary" loading={save.isPending}>
          {t("common.save")}
        </Button>
      </form>
    </section>
  );
}

/** What visitors get until they pick a language; the header's switch is this browser's own. */
export function LanguageCard({ s }: { s: Schemas["SettingsView"] }) {
  const save = useSaveSettings();
  const options = [
    { id: "auto", label: t("settings.langAuto") },
    ...LOCALES.map((l) => ({ id: l.id, label: l.label, lang: l.id })),
  ] as const;
  const current = (save.isPending && save.variables.default_lang) || s.default_lang;
  return (
    <section className="card glass reveal" style={{ "--i": 2 } as React.CSSProperties}>
      <div className="card-head">
        <div>
          <h2 className="card-title">{t("settings.lang")}</h2>
          <div className="card-sub">{t("settings.langSub")}</div>
        </div>
      </div>
      <div className="grid gap-2 sm:grid-cols-3" role="radiogroup" aria-label={t("settings.lang")} aria-busy={save.isPending}>
        {options.map((o) => (
          <button
            key={o.id}
            type="button"
            role="radio"
            aria-checked={current === o.id}
            className="opt"
            lang={"lang" in o ? o.lang : undefined}
            disabled={save.isPending}
            onClick={() => o.id !== s.default_lang && save.mutate({ default_lang: o.id })}
          >
            <span className="font-semibold">{o.label}</span>
          </button>
        ))}
      </div>
      <p className="mt-3 text-xs text-[var(--ink-500)]">{t("settings.langNote")}</p>
    </section>
  );
}

/** Global switches of the automatic fixes; each connection can opt out in its settings. */
export function AutoCard({ s }: { s: Schemas["SettingsView"] }) {
  const save = useSaveSettings();
  const rows = [
    { key: "auto_port", title: t("settings.autoPort"), sub: t("settings.autoPortSub"), on: s.auto_port },
    { key: "auto_sni", title: t("settings.autoSni"), sub: t("settings.autoSniSub"), on: s.auto_sni },
  ] as const;
  return (
    <section className="card glass reveal" style={{ "--i": 3 } as React.CSSProperties}>
      <div className="card-head">
        <div>
          <h2 className="card-title">{t("settings.auto")}</h2>
          <div className="card-sub">{t("settings.autoSub")}</div>
        </div>
      </div>
      <ul className="row-list">
        {rows.map((r) => (
          <li key={r.key} className="flex items-start justify-between gap-4 py-3">
            <div className="min-w-0">
              <div className="text-[13px] font-medium">{r.title}</div>
              <div className="mt-1 text-xs text-[var(--ink-500)]">{r.sub}</div>
            </div>
            <Switch checked={r.on} label={r.title} disabled={save.isPending} onChange={(v) => save.mutate({ [r.key]: v })} />
          </li>
        ))}
      </ul>
      <p className="mt-3 text-xs text-[var(--ink-500)]">{t("settings.autoNote")}</p>
    </section>
  );
}

// The switch for selling at all. Off, Payments leaves the menu; the page stays reachable
// from here for the history.
export function SalesCard() {
  const qc = useQueryClient();
  const toast = useToast();
  const ps = usePaymentSettings();
  const save = useMutation({
    mutationFn: (enabled: boolean) => unwrap(api.PATCH("/api/v1/payments/settings", { body: { enabled } })),
    onSuccess: (v) => {
      qc.setQueryData(qk.paymentSettings, v);
      toast.ok(v.enabled ? t("settings.salesOnToast") : t("settings.salesOffToast"));
    },
    onError: (e) => toast.error(errorText(e)),
  });
  const on = ps.data?.enabled === true;
  return (
    <section className="card glass reveal" style={{ "--i": 5 } as React.CSSProperties}>
      <div className="card-head">
        <div>
          <h2 className="card-title">{t("settings.sales")}</h2>
          <div className="card-sub">{t("settings.salesSub")}</div>
        </div>
        {ps.isPending ? (
          <Skeleton style={{ width: 40, height: 24, borderRadius: 12 }} />
        ) : ps.isError && !ps.data ? null : (
          <Switch checked={on} label={t("settings.sales")} disabled={save.isPending} onChange={(v) => save.mutate(v)} />
        )}
      </div>
      {ps.isError && !ps.data ? (
        <ErrorState text={errorText(ps.error)} onRetry={() => void ps.refetch()} />
      ) : ps.data ? (
        <div className="flex flex-wrap items-center justify-between gap-3">
          <p className="min-w-0 flex-1 text-xs text-[var(--ink-500)]">{on ? t("settings.salesOnNote") : t("settings.salesOffNote")}</p>
          <Link to="/payments" className="btn btn-glass btn-sm">
            {t("settings.openPayments")} <ChevronRight size={16} aria-hidden />
          </Link>
        </div>
      ) : null}
    </section>
  );
}

/** The release the panel runs, the newest one and the host updater's last run. */
export function UpdatesCard() {
  const u = useUpdates();
  const qc = useQueryClient();
  const toast = useToast();
  const [confirm, setConfirm] = useState(false);
  const put = (d: Schemas["UpdatesView"]) => qc.setQueryData(qk.updates, d);
  const fail = (e: unknown) => toast.error(errorText(e));
  const check = useMutation({ mutationFn: () => unwrap(api.POST("/api/v1/updates/check")), onSuccess: put, onError: fail });
  const auto = useMutation({
    mutationFn: (v: boolean) => unwrap(api.PATCH("/api/v1/updates", { body: { auto: v } })),
    onSuccess: (d) => {
      put(d);
      toast.ok(t("settings.saved"));
    },
    onError: fail,
  });
  const request = useMutation({
    mutationFn: () => unwrap(api.POST("/api/v1/updates/request")),
    onSuccess: (d) => {
      put(d);
      setConfirm(false);
    },
    onError: fail,
  });
  const v = u.data;
  if (!v) {
    if (!u.isError) return <Skeleton style={{ height: 180, borderRadius: 20 }} />;
    return (
      <section className="card glass">
        <ErrorState text={errorText(u.error)} onRetry={() => void u.refetch()} />
      </section>
    );
  }
  const notes = v.notes[getLocale()] || v.notes.en || "";
  const running = v.host?.state === "running";
  const waiting = v.requested_at > 0 || running;
  const stale = v.requested_at > 0 && !running && Date.now() / 1000 - v.requested_at > 120;
  const lastAt = v.host?.at ? ago(v.host.at) : "";
  return (
    <section id="updates" className="card glass reveal">
      {u.isError ? <StaleNotice onRetry={() => void u.refetch()} retrying={u.isFetching} /> : null}
      <div className="card-head">
        <div>
          <h2 className="card-title">{t("settings.updates")}</h2>
          <div className="card-sub">{t("settings.updatesSub")}</div>
        </div>
        <Button size="sm" loading={check.isPending} onClick={() => check.mutate()}>
          <RefreshCw size={16} aria-hidden /> {t("settings.updatesCheck")}
        </Button>
      </div>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <span className="font-semibold">mikan {v.current}</span>
        {v.available ? (
          <Pill tone="warn">{t("settings.updatesOut", { v: v.latest })}</Pill>
        ) : v.latest ? (
          <Pill tone="ok">{t("settings.updatesLatest")}</Pill>
        ) : (
          <Pill tone="off">{v.error === "no_release" ? t("settings.updatesNoRelease") : v.checked_at ? t("settings.updatesUnknown") : t("settings.updatesNever")}</Pill>
        )}
        {v.checked_at > 0 ? <span className="text-xs text-[var(--ink-500)]">{t("settings.updatesChecked", { ago: ago(new Date(v.checked_at * 1000).toISOString()) })}</span> : null}
      </div>
      {v.error && v.error !== "no_release" ? <p className="mt-2 text-xs text-[var(--berry-600)]">{t("settings.updatesError", { e: v.error })}</p> : null}
      {v.available && notes ? (
        <div className="panel-soft mt-4 p-3">
          <div className="mb-2 text-xs font-semibold text-[var(--ink-500)]">{t("settings.updatesChanges", { v: v.latest })}</div>
          <ul className="notes">
            {notes
              .split("\n")
              .filter((l) => l.trim())
              .map((l, i) => (
                <li key={i}>{l.replace(/^[-*]\s*/, "")}</li>
              ))}
          </ul>
        </div>
      ) : null}
      {v.available || waiting ? (
        <div className="mt-4 flex flex-wrap items-center gap-3">
          <Button variant="primary" loading={request.isPending || (waiting && !stale)} disabled={waiting} onClick={() => setConfirm(true)}>
            {waiting ? t("settings.updatesWaiting") : t("settings.updatesNow", { v: v.latest })}
          </Button>
        </div>
      ) : null}
      {waiting && !stale ? <p className="mt-2 text-xs text-[var(--ink-500)]">{t("settings.updatesWaitingText")}</p> : null}
      {stale ? <p className="mt-2 text-xs text-[var(--honey-600)]">{t("settings.updatesStale")}</p> : null}
      {v.host && !waiting ? (
        <p className={clsx("mt-3 text-xs", v.host.state === "failed" ? "text-[var(--berry-600)]" : "text-[var(--ink-500)]")}>
          {v.host.state === "failed"
            ? t("settings.updatesLastFailed", { v: v.host.version || v.latest, from: v.host.from, e: v.host.error.split("\n")[0] ?? "" })
            : t("settings.updatesLastOk", { v: v.host.version, from: v.host.from, ago: lastAt })}
        </p>
      ) : null}
      <ul className="row-list mt-3">
        <li className="flex items-start justify-between gap-4 py-3">
          <div className="min-w-0">
            <div className="text-[13px] font-medium">{t("settings.updatesAuto")}</div>
            <div className="mt-1 text-xs text-[var(--ink-500)]">{t("settings.updatesAutoSub")}</div>
          </div>
          <Switch checked={v.auto} label={t("settings.updatesAuto")} disabled={auto.isPending} onChange={(on) => auto.mutate(on)} />
        </li>
      </ul>
      <Confirm
        open={confirm}
        onOpenChange={setConfirm}
        title={t("settings.updatesConfirmTitle", { v: v.latest })}
        text={t("settings.updatesConfirmText")}
        confirm={t("settings.updatesConfirm")}
        loading={request.isPending}
        onConfirm={() => request.mutate()}
      />
    </section>
  );
}
