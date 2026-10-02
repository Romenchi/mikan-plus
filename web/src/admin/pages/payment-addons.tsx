import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Copy, ExternalLink, Plus, Trash2 } from "lucide-react";
import { useState, type FormEvent } from "react";
import { api, errorText, unwrap, type Schemas } from "../../api/client";
import { qk } from "../../api/hooks";
import { Confirm, Drawer } from "../../components/overlay";
import { useToast } from "../../components/toast";
import { QueryBoundary } from "../../components/query";
import { Switch } from "../../components/switch";
import { Button, EmptyState, ErrorState, Field, Pill, Skeleton, Spinner } from "../../components/ui";
import { getLocale, t } from "../../i18n";
import { useCopy } from "../../lib/copy";
import { useDraft } from "../../lib/draft";
import { fieldErrors } from "../../lib/fields";
import { safeHref } from "../../lib/url";

type Addons = Schemas["AddonsView"];
type Addon = Schemas["AddonView"];
type Entry = Schemas["AddonCatalogEntry"];

/** The marketplace: followed every few seconds while the server works on a request. */
export function useAddons() {
  return useQuery({
    queryKey: qk.addons,
    queryFn: ({ signal }) => unwrap(api.GET("/api/v1/addons", { signal })),
    refetchInterval: (q) => (q.state.data?.pending ? 3_000 : false),
  });
}

/** A text the adapter gives per language, in the panel's. */
export function localized(m: Record<string, string> | undefined, fallback: string): string {
  if (!m) return fallback;
  return m[getLocale()] || m.en || Object.values(m)[0] || fallback;
}

/** "addon:yookassa" as the admin knows it: the adapter's own name. */
export function addonName(id: string, data: Addons | undefined): string {
  const a = data?.installed.find((x) => x.id === id) ?? data?.catalog.find((x) => x.id === id);
  return localized(a?.name, KNOWN_NAMES[id] ?? id);
}

// The two that were built in until 0.4.3, named even while the catalog is out of reach.
const KNOWN_NAMES: Record<string, string> = { yookassa: "ЮKassa", cryptobot: "CryptoBot" };

export function AddonsCard({ selling }: { selling: boolean }) {
  const q = useAddons();
  return (
    <QueryBoundary query={q} pending={<Skeleton style={{ height: 200, borderRadius: 20 }} />} wrap={(state) => <section className="card glass">{state}</section>}>
      {(d) => <Addons d={d} selling={selling} onRetry={() => void q.refetch()} retrying={q.isFetching} />}
    </QueryBoundary>
  );
}

function Addons({ d, selling, onRetry, retrying }: { d: Addons; selling: boolean; onRetry: () => void; retrying: boolean }) {
  const [shop, setShop] = useState(false);
  const busy = !!d.pending;
  const last = d.last && !busy && d.last.state === "failed" ? d.last : null;
  return (
    <section className="card glass reveal" style={{ "--i": 2 } as React.CSSProperties} aria-busy={busy}>
      <div className="card-head">
        <div className="min-w-0">
          <h2 className="card-title">{t("addons.title")}</h2>
          <div className="card-sub">{t("addons.sub")}</div>
        </div>
        <Button size="sm" onClick={() => setShop(true)} disabled={!d.supported}>
          <Plus size={16} aria-hidden /> {t("addons.add")}
        </Button>
      </div>
      {!d.supported ? (
        <div className="banner warn mb-4" role="status">
          {t("addons.unsupported")}
        </div>
      ) : null}
      {d.pending ? (
        <div className="banner info mb-4" role="status">
          <Spinner />
          <span className="min-w-0 flex-1">{t(d.pending.action === "remove" ? "addons.removing" : "addons.installing", { name: addonName(d.pending.id, d) })}</span>
        </div>
      ) : null}
      {last ? (
        <div className="banner err mb-4" role="alert">
          <span className="min-w-0 flex-1 break-words">{t(last.action === "remove" ? "addons.removeFailed" : "addons.installFailed", { name: addonName(last.id, d), error: last.error ?? "" })}</span>
        </div>
      ) : null}
      {d.installed.length === 0 ? (
        <EmptyState title={t("addons.empty")} text={t("addons.emptyText")} />
      ) : (
        d.installed.map((a) => <AddonBlock key={a.id} a={a} busy={busy} selling={selling} />)
      )}
      <CatalogDrawer open={shop} onOpenChange={setShop} data={d} onRetry={onRetry} retrying={retrying} />
    </section>
  );
}

type Values = Record<string, string | boolean>;

function initial(a: Addon): Values {
  const v: Values = {};
  for (const f of a.settings) {
    if (f.type === "bool") v[f.key] = f.value === true;
    else v[f.key] = f.secret ? "" : typeof f.value === "string" ? f.value : "";
  }
  return v;
}

function AddonBlock({ a, busy, selling }: { a: Addon; busy: boolean; selling: boolean }) {
  const qc = useQueryClient();
  const toast = useToast();
  // The secrets in the draft start empty (the API never sends them) and are cleared after a
  // save, so a saved form is not dirty.
  const { draft, setDraft } = useDraft({ enabled: a.enabled, values: initial(a) });
  const { enabled, values } = draft;
  const setEnabled = (on: boolean) => setDraft((d) => ({ ...d, enabled: on }));
  const setValues = (fn: (v: Values) => Values) => setDraft((d) => ({ ...d, values: fn(d.values) }));
  const [remove, setRemove] = useState(false);
  const name = localized(a.name, a.id);
  const save = useMutation({
    mutationFn: (body: Schemas["PatchAddonInputBody"]) => unwrap(api.PATCH("/api/v1/addons/{id}", { params: { path: { id: a.id } }, body })),
    onSuccess: (v) => {
      qc.setQueryData(qk.addons, v);
      void qc.invalidateQueries({ queryKey: qk.paymentSettings });
      const now = v.installed.find((x) => x.id === a.id);
      if (now) setDraft({ enabled: now.enabled, values: initial(now) });
      toast.ok(t("addons.saved", { name }));
    },
  });
  const drop = useMutation({
    mutationFn: () => unwrap(api.POST("/api/v1/addons/{id}/remove", { params: { path: { id: a.id } } })),
    onSuccess: (v) => {
      qc.setQueryData(qk.addons, v);
      setRemove(false);
      toast.ok(t("addons.requested"));
    },
    onError: (e) => toast.error(errorText(e)),
  });
  const errors = fieldErrors(save.error);
  const general = save.error && !Object.keys(errors).some((k) => k.startsWith("settings.")) ? errors.settings || errorText(save.error) : "";
  const submit = (e: FormEvent) => {
    e.preventDefault();
    const settings: Record<string, unknown> = {};
    for (const f of a.settings) {
      const v = values[f.key];
      if (f.secret && v === "") continue; // empty keeps the saved secret
      settings[f.key] = typeof v === "string" ? v.trim() : v;
    }
    save.mutate({ enabled, settings });
  };
  const failed = a.status === "failed";
  return (
    <form className="mb-4 border-t border-[var(--hairline)] pt-4 first-of-type:border-t-0 first-of-type:pt-0" onSubmit={submit} noValidate aria-label={name}>
      <div className="mb-3 flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2 text-[13px] font-semibold">
            {name}
            <span className="num text-xs font-normal text-[var(--ink-500)]">v{a.version}</span>
            {failed ? <Pill tone="bad">{t("addons.failed")}</Pill> : enabled && selling ? <Pill tone={a.available ? "ok" : "warn"}>{a.available ? t("payments.live") : t("payments.notReady")}</Pill> : null}
          </div>
          {failed && a.error ? <div className="mt-1 break-words text-xs text-[var(--berry-600)]">{a.error}</div> : null}
          {a.info_error ? <div className="mt-1 text-xs text-[var(--honey-600)]">{t("addons.unreachable")}</div> : null}
        </div>
        <Switch checked={enabled} onChange={setEnabled} label={name} disabled={failed || !!a.info_error} />
      </div>
      {general ? (
        <div className="banner err mb-3" role="alert">
          {general}
        </div>
      ) : null}
      {enabled && !a.info_error ? (
        <>
          {localized(a.help, "") ? <p className="mb-3 text-xs text-[var(--ink-500)]">{localized(a.help, "")}</p> : null}
          <div className="grid gap-x-3 sm:grid-cols-2">
            {a.settings.map((f) => {
              const id = `addon-${a.id}-${f.key}`;
              const label = localized(f.label, f.key);
              const err = errors[`settings.${f.key}`];
              if (f.type === "bool")
                return (
                  <label key={f.key} className="mb-3 flex items-center gap-2 text-[13px] sm:col-span-2">
                    <input type="checkbox" className="check" checked={values[f.key] === true} onChange={(e) => setValues((v) => ({ ...v, [f.key]: e.target.checked }))} /> {label}
                  </label>
                );
              return (
                <Field key={f.key} label={label} htmlFor={id} error={err}>
                  <input
                    id={id}
                    className="input mono"
                    type={f.secret ? "password" : "text"}
                    value={String(values[f.key] ?? "")}
                    onChange={(e) => setValues((v) => ({ ...v, [f.key]: e.target.value }))}
                    placeholder={f.secret && f.set ? t("payments.keySaved") : ""}
                    autoComplete={f.secret ? "new-password" : "off"}
                    aria-invalid={!!err}
                    required={f.required}
                  />
                </Field>
              );
            })}
          </div>
          <Webhook label={t("addons.webhook")} url={a.webhook_url} />
        </>
      ) : null}
      <div className="mt-3 flex flex-wrap items-center gap-2">
        {!failed && !a.info_error && (enabled || a.enabled) ? (
          <Button type="submit" size="sm" variant="primary" loading={save.isPending}>
            {t("common.save")}
          </Button>
        ) : null}
        <Button type="button" size="sm" variant="danger" onClick={() => setRemove(true)} disabled={busy}>
          <Trash2 size={16} aria-hidden /> {t("addons.remove")}
        </Button>
      </div>
      <Confirm
        open={remove}
        onOpenChange={setRemove}
        title={t("addons.removeTitle", { name })}
        text={t("addons.removeText")}
        confirm={t("addons.remove")}
        danger
        loading={drop.isPending}
        onConfirm={() => drop.mutate()}
      />
    </form>
  );
}

function CatalogDrawer({ open, onOpenChange, data, onRetry, retrying }: { open: boolean; onOpenChange: (v: boolean) => void; data: Addons; onRetry: () => void; retrying: boolean }) {
  const qc = useQueryClient();
  const toast = useToast();
  const install = useMutation({
    mutationFn: (id: string) => unwrap(api.POST("/api/v1/addons/{id}/install", { params: { path: { id } } })),
    onSuccess: (v) => {
      qc.setQueryData(qk.addons, v);
      toast.ok(t("addons.requested"));
      onOpenChange(false);
    },
    onError: (e) => toast.error(errorText(e)),
  });
  return (
    <Drawer open={open} onOpenChange={onOpenChange} title={t("addons.catalog")} meta={t("addons.catalogMeta")}>
      {data.catalog_error ? (
        <ErrorState text={t("errors.api.catalog_unavailable")} onRetry={retrying ? undefined : onRetry} />
      ) : data.catalog.length === 0 ? (
        <EmptyState title={t("addons.catalogEmpty")} text={t("addons.catalogEmptyText")} />
      ) : (
        <ul className="row-list">
          {data.catalog.map((e) => (
            <CatalogRow key={e.id} e={e} busy={!!data.pending} loading={install.isPending && install.variables === e.id} onInstall={() => install.mutate(e.id)} />
          ))}
        </ul>
      )}
    </Drawer>
  );
}

function CatalogRow({ e, busy, loading, onInstall }: { e: Entry; busy: boolean; loading: boolean; onInstall: () => void }) {
  return (
    <li className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3 py-3">
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-2 text-[13px] font-semibold">
          {localized(e.name, e.id)}
          <span className="num text-xs font-normal text-[var(--ink-500)]">v{e.version}</span>
        </div>
        <div className="mt-1 text-xs text-[var(--ink-500)]">{localized(e.description, "")}</div>
        {safeHref(e.homepage) ? (
          <a className="link-btn mt-1 inline-flex items-center gap-1 text-xs" href={safeHref(e.homepage)} target="_blank" rel="noopener noreferrer">
            {t("addons.homepage")} <ExternalLink size={12} aria-hidden />
          </a>
        ) : null}
      </div>
      {e.installed && !e.update ? (
        <Pill tone="ok">{t("addons.installed")}</Pill>
      ) : (
        <Button size="sm" variant={e.update ? "glass" : "primary"} loading={loading} disabled={busy} onClick={onInstall}>
          {e.update ? t("addons.update") : t("addons.install")}
        </Button>
      )}
    </li>
  );
}

export function Webhook({ label, url }: { label: string; url: string }) {
  const copy = useCopy();
  if (!url) return <p className="text-xs text-[var(--ink-500)]">{t("payments.webhookNoHost")}</p>;
  return (
    <div>
      <div className="mb-1 text-xs text-[var(--ink-500)]">{label}</div>
      <div className="link-field">
        <span className="mono">{url}</span>
        <button type="button" className="icon-btn" onClick={() => void copy(url, t("payments.webhookCopied"))} aria-label={t("payments.copyWebhook")}>
          <Copy size={18} />
        </button>
      </div>
    </div>
  );
}
