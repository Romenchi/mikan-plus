import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { Link, useBlocker, useNavigate, useSearch } from "@tanstack/react-router";
import { ArrowDown, ArrowUp, Bell, Bot, Globe, LayoutList, Link2, Megaphone, Network, Plus, PlugZap, Send, Shield, Trash2, TriangleAlert } from "lucide-react";
import { useMemo, useState, type FormEvent } from "react";
import { api, ApiError, errorText, unwrap, type Schemas } from "../../api/client";
import { qk, useNodes, useSettings } from "../../api/hooks";
import { useDraft } from "../../lib/draft";
import { TELEGRAM_TABS } from "../search";
import { ago, num } from "../../lib/format";
import { Confirm } from "../../components/overlay";
import { QueryBoundary } from "../../components/query";
import { Columns, Tabs } from "../../components/tabs";
import { useToast } from "../../components/toast";
import { Bar, Button, Field, PageHeader, Pill, Segmented, Skeleton } from "../../components/ui";
import { Switch } from "../../components/switch";
import { t, tMaybe, useLocale } from "../../i18n";

type View = Schemas["TelegramView"];
type Config = Schemas["Config"];
type MenuButton = Schemas["MenuButton"];
type TextKey = keyof Schemas["Texts"];

const TAB_ICONS = { connect: PlugZap, menu: LayoutList, notify: Bell, broadcast: Megaphone } as const;

function useTelegram() {
  return useQuery({
    queryKey: qk.telegram,
    queryFn: ({ signal }) => unwrap(api.GET("/api/v1/telegram", { signal })),
    // A broadcast in progress moves every second; otherwise little changes.
    refetchInterval: (q) => (q.state.data?.broadcast?.active ? 2_000 : 10_000),
  });
}

function usePatchTelegram() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: Schemas["PatchTelegramInputBody"]) => unwrap(api.PATCH("/api/v1/telegram", { body })),
    onSuccess: (v) => qc.setQueryData(qk.telegram, v),
  });
}

/**
 * The bot in four sections: connecting it, its menu and texts, notifications and options,
 * broadcasts. Menu, texts and options are one draft saved together from the bar below,
 * whichever section they were changed in.
 */
export function TelegramPage() {
  const tg = useTelegram();
  return (
    <>
      <PageHeader title={t("nav.telegram")} sub={t("telegram.subtitle")} />
      <QueryBoundary
        query={tg}
        pending={
          <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_360px]">
            <Skeleton style={{ height: 320, borderRadius: 20 }} />
            <Skeleton style={{ height: 420, borderRadius: 20 }} />
          </div>
        }
        wrap={(state) => <section className="card glass">{state}</section>}
      >
        {(v) => <TelegramBody v={v} />}
      </QueryBoundary>
    </>
  );
}

function TelegramBody({ v }: { v: View }) {
  const { tab } = useSearch({ from: "/_app/telegram" });
  const navigate = useNavigate({ from: "/telegram" });
  const patch = usePatchTelegram();
  const toast = useToast();
  // The draft follows the server's copy while untouched and keeps the edits when the copy
  // changes under it (a poll, the bot saved from another session).
  const { draft, setDraft, dirty, reset } = useDraft(v.config);
  const save = () =>
    patch.mutate(
      { config: draft },
      {
        onSuccess: (r) => {
          setDraft(r.config);
          toast.ok(t("telegram.saved"));
        },
        onError: (e) => toast.error(errorText(e)),
      },
    );
  // Leaving the page drops the draft: ask first. Switching the section stays on the page.
  const leave = useBlocker({ shouldBlockFn: ({ current, next }) => dirty && current.pathname !== next.pathname, enableBeforeUnload: () => dirty, withResolver: true });

  return (
    <>
      <Tabs
        id="telegram"
        label={t("telegram.sections")}
        tabs={TELEGRAM_TABS.map((id) => ({ id, label: t(`telegram.tabs.${id}`), icon: TAB_ICONS[id] }))}
        value={tab}
        onChange={(next) => void navigate({ search: { tab: next }, replace: true })}
      >
        {tab === "connect" ? (
          <Columns
            wide="left"
            left={
              <>
                <ConnectCard v={v} />
                <RouteCard v={v} />
              </>
            }
            right={<Preview draft={draft} v={v} />}
          />
        ) : tab === "menu" ? (
          <Columns
            wide="left"
            left={
              <>
                <MenuCard draft={draft} setDraft={setDraft} />
                <TextsCard draft={draft} setDraft={setDraft} defaults={v.defaults} />
              </>
            }
            right={<Preview draft={draft} v={v} />}
          />
        ) : tab === "notify" ? (
          <Columns wide="left" left={<OptionsCard draft={draft} setDraft={setDraft} v={v} />} right={<Preview draft={draft} v={v} />} />
        ) : (
          <div className="max-w-3xl">
            <BroadcastCard v={v} />
          </div>
        )}
      </Tabs>
      <AnimatePresence>
        {dirty ? (
          <motion.div
            className="bulk-bar glass-strong"
            role="region"
            aria-label={t("telegram.unsaved")}
            initial={{ opacity: 0, y: 24, x: "-50%" }}
            animate={{ opacity: 1, y: 0, x: "-50%" }}
            exit={{ opacity: 0, y: 24, x: "-50%" }}
            transition={{ type: "spring", stiffness: 420, damping: 32 }}
          >
            <span className="text-[13px] font-medium">{t("telegram.unsaved")}</span>
            <Button variant="ghost" size="sm" onClick={reset}>
              {t("telegram.discard")}
            </Button>
            <Button variant="primary" size="sm" loading={patch.isPending} onClick={save}>
              {t("common.save")}
            </Button>
          </motion.div>
        ) : null}
      </AnimatePresence>
      <Confirm
        open={leave.status === "blocked"}
        onOpenChange={(open) => !open && leave.reset?.()}
        title={t("telegram.leaveTitle")}
        text={t("telegram.leaveText")}
        confirm={t("telegram.leaveConfirm")}
        danger
        onConfirm={() => leave.proceed?.()}
      />
    </>
  );
}

function statusOf(v: View): { tone: "ok" | "warn" | "bad" | "off"; text: string } {
  if (!v.enabled) return { tone: "off", text: t("telegram.off") };
  if (v.running && !v.error) return { tone: "ok", text: t("telegram.running") };
  if (v.running) return { tone: "warn", text: t("telegram.reconnecting") };
  if (!v.error) return { tone: "off", text: t("telegram.starting") };
  return { tone: "bad", text: t("telegram.stopped") };
}

// Cards rise in turn, as on the other tabs.
const rise = (i: number) => ({ className: "card glass reveal", style: { "--i": i } as React.CSSProperties });
// Menu buttons slide to their new place when moved, shown or hidden.
const slide = { type: "spring", stiffness: 520, damping: 40 } as const;

function ConnectCard({ v }: { v: View }) {
  const patch = usePatchTelegram();
  const toast = useToast();
  const [token, setToken] = useState("");
  const [editing, setEditing] = useState(false);
  const [removing, setRemoving] = useState(false);
  const [error, setError] = useState("");
  const submit = (e: FormEvent) => {
    e.preventDefault();
    setError("");
    patch.mutate(
      { token: token.trim(), enabled: true },
      {
        onSuccess: (r) => {
          setToken("");
          setEditing(false);
          toast.ok(t("telegram.connected", { name: r.bot?.username ?? "" }));
        },
        onError: (err) => {
          if (err instanceof ApiError && Object.keys(err.fields).length) setError(Object.values(err.fields)[0] ?? "");
          else setError(errorText(err));
        },
      },
    );
  };
  const st = statusOf(v);
  const showForm = !v.token_set || editing;
  return (
    <section {...rise(0)}>
      <div className="card-head">
        <div>
          <h2 className="card-title">{t("telegram.connect")}</h2>
          <div className="card-sub">{t("telegram.connectSub")}</div>
        </div>
        {v.token_set ? (
          <Switch checked={v.enabled} label={t("telegram.enabled")} disabled={patch.isPending} onChange={(on) => patch.mutate({ enabled: on }, { onError: (e) => toast.error(errorText(e)) })} />
        ) : null}
      </div>
      {v.token_set && v.bot ? (
        <div className="panel-soft flex items-center gap-3 p-3">
          <span className="grid h-10 w-10 place-items-center rounded-xl bg-[var(--hover)] text-[var(--ink-700)]" aria-hidden>
            <Bot size={20} />
          </span>
          <div className="min-w-0 flex-1">
            <a className="font-semibold text-[var(--ink-900)] hover:underline" href={`https://t.me/${encodeURIComponent(v.bot.username)}`} target="_blank" rel="noreferrer noopener">
              @{v.bot.username}
            </a>
            <div className="truncate text-xs text-[var(--ink-500)]">{v.bot.name}</div>
          </div>
          <Pill tone={st.tone}>{st.text}</Pill>
        </div>
      ) : null}
      {v.error && v.enabled ? (
        <p className="mt-3 text-[13px] text-[var(--berry-600)]" role="alert">
          {tMaybe(`telegram.err.${v.error}`) ?? v.error}
        </p>
      ) : null}
      {v.token_set ? (
        <p className="mt-3 text-xs text-[var(--ink-500)]">
          {t("telegram.stats", { linked: v.linked, accounts: v.accounts })}
          {" · "}
          {v.mini_app_url ? t("telegram.miniAppOn") : t("telegram.miniAppNoCert")}
        </p>
      ) : null}
      {showForm ? (
        <form onSubmit={submit} className="mt-4" noValidate>
          {!v.token_set ? (
            <ol className="mb-4 flex list-decimal flex-col gap-1 pl-5 text-[13px] text-[var(--ink-600)]">
              <li>{t("telegram.step1")}</li>
              <li>{t("telegram.step2")}</li>
            </ol>
          ) : null}
          <Field label={t("telegram.token")} htmlFor="tg-token" hint={t("telegram.tokenHint")} error={error}>
            <input id="tg-token" className="input mono" type="password" autoComplete="off" spellCheck={false} value={token} onChange={(e) => setToken(e.target.value)} placeholder="123456789:AAH…" aria-invalid={!!error} />
          </Field>
          <div className="flex flex-wrap gap-2">
            <Button variant="primary" type="submit" loading={patch.isPending} disabled={!token.trim()}>
              <Link2 size={16} aria-hidden /> {t("telegram.connectButton")}
            </Button>
            {editing ? (
              <Button variant="ghost" onClick={() => setEditing(false)}>
                {t("common.cancel")}
              </Button>
            ) : null}
          </div>
        </form>
      ) : (
        <div className="mt-4 flex flex-wrap gap-2">
          <Button size="sm" onClick={() => setEditing(true)}>
            {t("telegram.changeToken")}
          </Button>
          <Button size="sm" variant="danger" onClick={() => setRemoving(true)}>
            <Trash2 size={16} aria-hidden /> {t("telegram.removeToken")}
          </Button>
        </div>
      )}
      <Confirm
        open={removing}
        onOpenChange={setRemoving}
        title={t("telegram.removeTitle")}
        text={t("telegram.removeText")}
        confirm={t("telegram.removeToken")}
        danger
        loading={patch.isPending}
        onConfirm={() => patch.mutate({ token: "" }, { onSuccess: () => setRemoving(false), onError: (e) => toast.error(errorText(e)) })}
      />
    </section>
  );
}

type RouteMode = Schemas["TelegramRoute"]["mode"];
const ROUTE_ICON = { direct: Globe, node: Network, proxy: Shield } as const;

// A node opens the tunnel to Telegram since 0.4.2; dev builds and nodes not heard from yet
// are given the benefit of the doubt (the check on saving tells).
function tunnels(version?: string): boolean {
  const m = /^(\d+)\.(\d+)\.(\d+)/.exec(version ?? "");
  if (!m) return true;
  const [a, b, c] = [Number(m[1]), Number(m[2]), Number(m[3])];
  return a > 0 || b > 4 || (b === 4 && c >= 2);
}

// How the bot reaches Telegram: straight, through a node of the panel or a proxy — for a
// server where Telegram is blocked.
function RouteCard({ v }: { v: View }) {
  const patch = usePatchTelegram();
  const toast = useToast();
  const nodes = useNodes();
  const saved = v.route;
  const {
    draft: { mode, nodeId },
    setDraft: setRoute,
  } = useDraft<{ mode: RouteMode; nodeId: number }>({ mode: saved.mode, nodeId: saved.node_id ?? 0 });
  const setMode = (m: RouteMode) => setRoute((d) => ({ ...d, mode: m }));
  const setNodeId = (n: number) => setRoute((d) => ({ ...d, nodeId: n }));
  const [proxy, setProxy] = useState("");
  const [error, setError] = useState("");
  const remote = (nodes.data ?? []).filter((n) => !n.local);
  const nodeName = (id?: number) => remote.find((n) => n.id === id)?.name ?? `#${id}`;
  const now =
    saved.mode === "node"
      ? t("telegram.routeNowNode", { name: nodeName(saved.node_id) })
      : saved.mode === "proxy"
        ? t("telegram.routeNowProxy", { proxy: saved.proxy ?? "" })
        : t("telegram.routeNowDirect");
  const changed = mode !== saved.mode || (mode === "node" && nodeId !== (saved.node_id ?? 0)) || (mode === "proxy" && proxy.trim() !== "");
  const ready = mode === "direct" || (mode === "node" && nodeId > 0) || (mode === "proxy" && (proxy.trim() !== "" || !!saved.proxy));
  const submit = (e: FormEvent) => {
    e.preventDefault();
    setError("");
    const route: Schemas["PatchTelegramInputBody"]["route"] =
      mode === "node" ? { mode, node_id: nodeId } : mode === "proxy" ? { mode, ...(proxy.trim() ? { proxy: proxy.trim() } : {}) } : { mode };
    patch.mutate(
      { route },
      {
        onSuccess: (r) => {
          setProxy("");
          const how = r.route.mode === "node" ? nodeName(r.route.node_id) : r.route.mode === "proxy" ? (r.route.proxy ?? "") : t("telegram.routeDirectShort");
          toast.ok(t("telegram.routeSaved", { how }));
        },
        onError: (err) => setError(err instanceof ApiError && Object.keys(err.fields).length ? (Object.values(err.fields)[0] ?? "") : errorText(err)),
      },
    );
  };
  const Icon = ROUTE_ICON[saved.mode];
  return (
    <section {...rise(1)}>
      <div className="card-head">
        <div>
          <h2 className="card-title">{t("telegram.routeTitle")}</h2>
          <div className="card-sub">{t("telegram.routeSub")}</div>
        </div>
      </div>
      <div className="panel-soft mb-4 flex items-center gap-3 p-3">
        <span className="grid h-10 w-10 shrink-0 place-items-center rounded-xl bg-[var(--hover)] text-[var(--ink-700)]" aria-hidden>
          <Icon size={20} />
        </span>
        <div className="min-w-0">
          <div className="text-xs text-[var(--ink-500)]">{t("telegram.routeNow")}</div>
          <div className="truncate text-[13px] font-semibold">{now}</div>
        </div>
      </div>
      <form onSubmit={submit} noValidate>
        <div className="mb-4">
          <Segmented
            value={mode}
            onChange={(m) => {
              setMode(m);
              setError("");
            }}
            label={t("telegram.routeTitle")}
            options={[
              { value: "direct", label: t("telegram.routeDirect") },
              { value: "node", label: t("telegram.routeNode") },
              { value: "proxy", label: t("telegram.routeProxy") },
            ]}
          />
        </div>
        {mode === "direct" ? <p className="mb-4 text-xs text-[var(--ink-500)]">{t("telegram.routeDirectHint")}</p> : null}
        {mode === "node" ? (
          nodes.isPending ? (
            <Skeleton style={{ height: 40, borderRadius: 12, maxWidth: 320 }} />
          ) : remote.length === 0 ? (
            <div className="banner warn mb-4 flex-wrap" role="status">
              <span className="min-w-0 flex-1">{t("telegram.routeNoNodes")}</span>
              <Link to="/nodes" className="btn btn-glass btn-sm">
                {t("telegram.routeOpenNodes")}
              </Link>
            </div>
          ) : (
            <Field label={t("telegram.routeNodeLabel")} htmlFor="tg-route-node" hint={t("telegram.routeNodeHint")} error={error}>
              <select id="tg-route-node" className="input max-w-[320px]" value={nodeId} onChange={(e) => setNodeId(Number(e.target.value))} aria-invalid={!!error}>
                <option value={0} disabled>
                  {t("telegram.routeNodePick")}
                </option>
                {remote.map((n) => (
                  <option key={n.id} value={n.id} disabled={!tunnels(n.version)}>
                    {tunnels(n.version) ? n.name : t("telegram.routeNodeOld", { name: n.name })}
                  </option>
                ))}
              </select>
            </Field>
          )
        ) : null}
        {mode === "proxy" ? (
          <Field label={t("telegram.routeProxyLabel")} htmlFor="tg-route-proxy" hint={saved.proxy ? t("telegram.routeProxyKeep", { proxy: saved.proxy }) : t("telegram.routeProxyHint")} error={error}>
            <input
              id="tg-route-proxy"
              className="input mono"
              type="text"
              autoComplete="off"
              spellCheck={false}
              value={proxy}
              onChange={(e) => setProxy(e.target.value)}
              placeholder="socks5://user:pass@203.0.113.5:1080"
              maxLength={512}
              aria-invalid={!!error}
            />
          </Field>
        ) : null}
        {error && mode === "direct" ? (
          <p className="mb-3 text-xs text-[var(--berry-600)]" role="alert">
            {error}
          </p>
        ) : null}
        {mode === "node" && !nodes.isPending && remote.length === 0 ? null : (
          <Button variant="primary" type="submit" loading={patch.isPending} disabled={!changed || !ready}>
            {mode === "direct" ? t("common.save") : t("telegram.routeSave")}
          </Button>
        )}
      </form>
    </section>
  );
}

const ACTIONS = ["sub", "devices", "connect", "renew", "support", "app"] as const;

function actionLabel(a: string): string {
  return tMaybe(`telegram.action.${a}`) ?? a;
}

function MenuCard({ draft, setDraft }: { draft: Config; setDraft: (c: Config) => void }) {
  const set = (i: number, patch: Partial<MenuButton>) => setDraft({ ...draft, buttons: draft.buttons.map((b, j) => (j === i ? { ...b, ...patch } : b)) });
  const move = (i: number, d: -1 | 1) => {
    const list = [...draft.buttons];
    [list[i], list[i + d]] = [list[i + d]!, list[i]!];
    setDraft({ ...draft, buttons: list });
  };
  const add = (action: "url" | "page") => {
    setDraft({
      ...draft,
      buttons: [...draft.buttons, { id: `c${Date.now().toString(36)}`, action, label: action === "url" ? t("telegram.newLink") : t("telegram.newPage"), on: true, row: false, url: action === "url" ? "https://" : undefined, text: action === "page" ? "" : undefined }],
    });
  };
  const custom = (b: MenuButton) => !ACTIONS.includes(b.action as (typeof ACTIONS)[number]);
  const reduce = useReducedMotion();
  return (
    <section {...rise(1)}>
      <div className="card-head">
        <div>
          <h2 className="card-title">{t("telegram.menu")}</h2>
          <div className="card-sub">{t("telegram.menuSub")}</div>
        </div>
      </div>
      <ul className="flex flex-col gap-2">
        <AnimatePresence initial={false}>
          {draft.buttons.map((b, i) => (
            <motion.li
              key={b.id}
              className="panel-soft p-3"
              layout={!reduce}
              initial={reduce ? false : { opacity: 0, scale: 0.96 }}
              animate={{ opacity: 1, scale: 1 }}
              exit={reduce ? { opacity: 0 } : { opacity: 0, scale: 0.96 }}
              transition={slide}
            >
              <div className="flex items-center gap-2">
                <Switch checked={b.on} label={t("telegram.showButton", { name: b.label })} onChange={(on) => set(i, { on })} />
                <input className="input h-10 min-w-0 flex-1" value={b.label} maxLength={40} onChange={(e) => set(i, { label: e.target.value })} aria-label={t("telegram.buttonLabel")} />
                <button type="button" className="icon-btn" disabled={i === 0} onClick={() => move(i, -1)} aria-label={t("telegram.moveUp")}>
                  <ArrowUp size={16} />
                </button>
                <button type="button" className="icon-btn" disabled={i === draft.buttons.length - 1} onClick={() => move(i, 1)} aria-label={t("telegram.moveDown")}>
                  <ArrowDown size={16} />
                </button>
                {custom(b) ? (
                  <button type="button" className="icon-btn" onClick={() => setDraft({ ...draft, buttons: draft.buttons.filter((_, j) => j !== i) })} aria-label={t("telegram.deleteButton", { name: b.label })}>
                    <Trash2 size={16} />
                  </button>
                ) : null}
              </div>
              <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-[var(--ink-500)]">
                <span>{actionLabel(b.action)}</span>
                {i > 0 ? (
                  <label className="flex items-center gap-1.5">
                    <input type="checkbox" className="check" checked={b.row} onChange={(e) => set(i, { row: e.target.checked })} /> {t("telegram.sameRow")}
                  </label>
                ) : null}
              </div>
              {b.action === "url" ? <input className="input mono mt-2" value={b.url ?? ""} onChange={(e) => set(i, { url: e.target.value })} placeholder="https://… / tg://…" aria-label={t("telegram.buttonUrl")} /> : null}
              {b.action === "page" ? (
                <textarea className="input mt-2" value={b.text ?? ""} maxLength={3000} onChange={(e) => set(i, { text: e.target.value })} placeholder={t("telegram.pagePlaceholder")} aria-label={t("telegram.pageText")} />
              ) : null}
            </motion.li>
          ))}
        </AnimatePresence>
      </ul>
      <div className="mt-3 flex flex-wrap gap-2">
        <button type="button" className="chip-btn" onClick={() => add("url")} disabled={draft.buttons.length >= 20}>
          <Plus size={14} className="mr-1 inline" aria-hidden />
          {t("telegram.addLink")}
        </button>
        <button type="button" className="chip-btn" onClick={() => add("page")} disabled={draft.buttons.length >= 20}>
          <Plus size={14} className="mr-1 inline" aria-hidden />
          {t("telegram.addPage")}
        </button>
      </div>
    </section>
  );
}

const TEXTS: { key: TextKey; label: string }[] = [
  { key: "welcome", label: "telegram.text.welcome" },
  { key: "main", label: "telegram.text.main" },
  { key: "renew", label: "telegram.text.renew" },
  { key: "expiring", label: "telegram.text.expiring" },
  { key: "expired", label: "telegram.text.expired" },
  { key: "traffic_90", label: "telegram.text.traffic_90" },
  { key: "traffic_end", label: "telegram.text.traffic_end" },
];

function TextsCard({ draft, setDraft, defaults }: { draft: Config; setDraft: (c: Config) => void; defaults: Schemas["Texts"] }) {
  return (
    <section {...rise(2)}>
      <div className="card-head">
        <div>
          <h2 className="card-title">{t("telegram.texts")}</h2>
          <div className="card-sub">{t("telegram.textsSub")}</div>
        </div>
      </div>
      <Field label={t("telegram.lang")} hint={t("telegram.langHint")}>
        <Segmented
          label={t("telegram.lang")}
          value={draft.lang}
          onChange={(lang) => setDraft({ ...draft, lang })}
          options={[
            { value: "ru", label: "Русский" },
            { value: "en", label: "English" },
          ]}
        />
      </Field>
      {TEXTS.map(({ key, label }) => (
        <Field key={key} label={tMaybe(label) ?? key} htmlFor={`tg-${key}`}>
          <textarea id={`tg-${key}`} className="input" rows={key === "main" || key === "welcome" ? 5 : 2} maxLength={3000} value={draft.texts[key]} placeholder={defaults[key]} onChange={(e) => setDraft({ ...draft, texts: { ...draft.texts, [key]: e.target.value } })} />
        </Field>
      ))}
      <p className="text-xs text-[var(--ink-500)]">{t("telegram.variables")}</p>
    </section>
  );
}

const NOTICES: (keyof Schemas["Notify"])[] = ["expire_3d", "expire_1d", "expired", "traffic_90", "traffic_100"];

function OptionsCard({ draft, setDraft, v }: { draft: Config; setDraft: (c: Config) => void; v: View }) {
  const row = (title: string, sub: string, on: boolean, change: (v: boolean) => void) => (
    <li key={title} className="flex items-start justify-between gap-4 py-3">
      <div className="min-w-0">
        <div className="text-[13px] font-medium">{title}</div>
        <div className="mt-1 text-xs text-[var(--ink-500)]">{sub}</div>
      </div>
      <Switch checked={on} label={title} onChange={change} />
    </li>
  );
  return (
    <section {...rise(3)}>
      <div className="card-head">
        <div>
          <h2 className="card-title">{t("telegram.options")}</h2>
          <div className="card-sub">{t("telegram.optionsSub")}</div>
        </div>
      </div>
      <ul className="row-list">
        {row(t("telegram.miniApp"), v.mini_app_url ? t("telegram.miniAppSub") : t("telegram.miniAppNoCert"), draft.mini_app, (on) => setDraft({ ...draft, mini_app: on }))}
        {row(t("telegram.cleanChat"), t("telegram.cleanChatSub"), draft.clean_chat, (on) => setDraft({ ...draft, clean_chat: on }))}
        {row(t("telegram.quietNight"), t("telegram.quietNightSub"), draft.quiet_night, (on) => setDraft({ ...draft, quiet_night: on }))}
        {NOTICES.map((k) => row(t(`telegram.notice.${k}`), t("telegram.noticeSub"), draft.notify[k], (on) => setDraft({ ...draft, notify: { ...draft.notify, [k]: on } })))}
      </ul>
    </section>
  );
}

function BroadcastCard({ v }: { v: View }) {
  const toast = useToast();
  const qc = useQueryClient();
  const [text, setText] = useState("");
  const [confirm, setConfirm] = useState(false);
  const send = useMutation({
    mutationFn: () => unwrap(api.POST("/api/v1/telegram/broadcast", { body: { text } })),
    onSuccess: (r) => {
      toast.ok(t("telegram.broadcastSent", { n: r.queued }));
      setText("");
      setConfirm(false);
      void qc.invalidateQueries({ queryKey: qk.telegram });
    },
    onError: (e) => toast.error(errorText(e)),
  });
  const busy = !!v.broadcast?.active;
  const ready = v.running && v.accounts > 0 && !busy;
  return (
    <section {...rise(4)}>
      <div className="card-head">
        <div>
          <h2 className="card-title">{t("telegram.broadcast")}</h2>
          <div className="card-sub">{t("telegram.broadcastSub", { n: v.accounts })}</div>
        </div>
      </div>
      <textarea className="input" rows={4} maxLength={3500} value={text} onChange={(e) => setText(e.target.value)} placeholder={t("telegram.broadcastPlaceholder")} aria-label={t("telegram.broadcast")} disabled={!v.running} />
      <div className="mt-3 flex flex-wrap items-center gap-3">
        <Button variant="primary" disabled={!ready || !text.trim()} onClick={() => setConfirm(true)}>
          <Send size={16} aria-hidden /> {t("telegram.broadcastButton")}
        </Button>
        <span className="text-xs text-[var(--ink-500)]">{busy ? t("telegram.broadcasting") : !v.running ? t("telegram.broadcastOff") : t("telegram.broadcastHint")}</span>
      </div>
      {v.broadcast && <BroadcastProgress b={v.broadcast} />}
      <Confirm
        open={confirm}
        onOpenChange={setConfirm}
        title={t("telegram.broadcastTitle", { n: v.accounts })}
        text={t("telegram.broadcastText")}
        confirm={t("telegram.broadcastButton")}
        loading={send.isPending}
        onConfirm={() => send.mutate()}
      />
    </section>
  );
}

/** How far the last broadcast went; the bot sends up to 20 a second. */
function BroadcastProgress({ b }: { b: Schemas["TelegramBroadcast"] }) {
  const done = b.sent + b.failed;
  const sec = Math.ceil((b.total - done) / 20);
  const eta = sec < 60 ? t("telegram.broadcastEtaSec", { n: Math.max(1, sec) }) : t("telegram.broadcastEtaMin", { n: Math.ceil(sec / 60) });
  return (
    <div className="mt-4 rounded-2xl border border-[var(--hairline)] bg-[var(--glass-strong)] p-3" aria-live="polite">
      <div className="flex items-baseline justify-between gap-3 text-[13px]">
        <span className="font-medium">{b.active ? t("telegram.broadcastGoing") : t("telegram.broadcastLast", { when: ago(new Date(b.started * 1000).toISOString()) })}</span>
        <span className="tabular-nums text-[var(--ink-500)]">{t("telegram.broadcastCount", { done: num(b.sent), total: num(b.total) })}</span>
      </div>
      <div className="mt-2" role="progressbar" aria-label={t("telegram.broadcast")} aria-valuemin={0} aria-valuemax={b.total} aria-valuenow={done}>
        <Bar pct={b.total ? (done / b.total) * 100 : 100} />
      </div>
      {(b.active || b.failed > 0) && (
        <div className="mt-2 flex flex-wrap gap-x-3 gap-y-1 text-xs text-[var(--ink-500)]">
          {b.active && <span>{eta}</span>}
          {b.failed > 0 && <span>{t("telegram.broadcastFailed", { n: num(b.failed) })}</span>}
        </div>
      )}
    </div>
  );
}

/** The main menu as a subscriber sees it in Telegram, with sample data. */
function Preview({ draft, v }: { draft: Config; v: View }) {
  const settings = useSettings();
  const brand = settings.data?.brand || "VPN";
  const support = !!settings.data?.support_url;
  const locale = useLocale();
  const sample: Record<string, string> = useMemo(
    () => ({
      brand,
      name: t("telegram.sample.name"),
      state: t("telegram.sample.state"),
      term: t("telegram.sample.term"),
      traffic: t("telegram.sample.traffic"),
      devices: t("telegram.sample.devices"),
      until: t("telegram.sample.until"),
      days: t("telegram.sample.days"),
      used: t("telegram.sample.used"),
      left: t("telegram.sample.left"),
      limit: t("telegram.sample.limit"),
      reset: t("telegram.sample.reset"),
    }),
    // The sample texts are translated: they change with the language.
    [brand, locale],
  );
  const text = (draft.texts.main || v.defaults.main).replace(/\{(\w+)\}/g, (m, k: string) => sample[k] ?? m);
  const reduce = useReducedMotion();
  const rows: MenuButton[][] = [];
  for (const b of draft.buttons) {
    if (!b.on || (b.action === "support" && !support) || (b.action === "app" && !(draft.mini_app && v.mini_app_url))) continue;
    const last = rows[rows.length - 1];
    if (b.row && last && last.length < 3) last.push(b);
    else rows.push([b]);
  }
  return (
    <section {...rise(1)} aria-label={t("telegram.preview")}>
      <div className="card-head">
        <div>
          <h2 className="card-title">{t("telegram.preview")}</h2>
          <div className="card-sub">{t("telegram.previewSub")}</div>
        </div>
      </div>
      <div className="tg-chat">
        <div className="tg-bubble">{text}</div>
        <motion.div className="tg-keyboard" layout={!reduce} transition={slide}>
          <AnimatePresence initial={false} mode="popLayout">
            {rows.map((r) => (
              <motion.div key={r[0]!.id} className="tg-row" layout={!reduce} transition={slide}>
                <AnimatePresence initial={false} mode="popLayout">
                  {r.map((b) => (
                    <motion.span
                      key={b.id}
                      className="tg-btn"
                      layout={!reduce}
                      initial={reduce ? false : { opacity: 0, scale: 0.85 }}
                      animate={{ opacity: 1, scale: 1 }}
                      exit={reduce ? { opacity: 0 } : { opacity: 0, scale: 0.85 }}
                      transition={slide}
                    >
                      {b.label}
                    </motion.span>
                  ))}
                </AnimatePresence>
              </motion.div>
            ))}
          </AnimatePresence>
        </motion.div>
      </div>
      {!v.mini_app_url && draft.buttons.some((b) => b.action === "app" && b.on) ? (
        <p className="mt-3 flex items-start gap-2 text-xs text-[var(--ink-500)]">
          <TriangleAlert size={14} className="mt-0.5 shrink-0 text-[var(--honey-600)]" aria-hidden /> {t("telegram.miniAppNoCert")}
        </p>
      ) : null}
    </section>
  );
}
