import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import clsx from "clsx";
import { CalendarPlus, ChevronRight, Plus, Power, RotateCcw, Search, Trash2, X } from "lucide-react";
import { AnimatePresence, motion } from "motion/react";
import { memo, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { errorText, type Tariff, type User } from "../../api/client";
import { userActions, useTariffs, useUserMutation, useUsers } from "../../api/hooks";
import { Confirm } from "../../components/overlay";
import { QueryBoundary } from "../../components/query";
import { useToast } from "../../components/toast";
import { Avatar, Bar, Button, EmptyState, PageHeader, Skeleton, StatePill } from "../../components/ui";
import { t, useLocale } from "../../i18n";
import { bytes, dateShort, expiryText, num } from "../../lib/format";
import { useMediaQuery } from "../../lib/media";
import { USER_STATES } from "../search";
import { CreateUserDrawer } from "./user-create";
import { UserDrawer } from "./user-drawer";

export function UsersPage() {
  const search = useSearch({ from: "/_app/users" });
  const navigate = useNavigate({ from: "/users" });
  const [q, setQ] = useState(search.q);
  // The last search text this page itself put into the URL: a different one in the URL
  // came from outside (Back, a link, the reset button) and replaces what is typed.
  const written = useRef(search.q);
  const users = useUsers({ state: search.state, q: search.q });
  const tariffs = useTariffs();
  const [selected, setSelected] = useState<ReadonlySet<number>>(new Set());
  const narrow = useMediaQuery("(max-width: 767px)");

  useEffect(() => {
    if (search.q === written.current) return;
    written.current = search.q;
    setQ(search.q);
  }, [search.q]);

  useEffect(() => {
    if (q === written.current) return;
    const timer = window.setTimeout(() => {
      written.current = q;
      void navigate({ search: (s) => ({ ...s, q }), replace: true });
    }, 250);
    return () => window.clearTimeout(timer);
  }, [q, navigate]);

  const tariffById = useMemo(() => new Map((tariffs.data ?? []).map((tr) => [tr.id, tr])), [tariffs.data]);
  const counts = users.data?.counts;
  const items = useMemo(() => users.data?.items ?? [], [users.data]);
  // What a bulk action touches is what the admin sees ticked: a row that left the list (a
  // search, another filter, a user deleted elsewhere) drops out of the selection with it.
  const chosen = useMemo(() => items.filter((u) => selected.has(u.id)), [items, selected]);
  const allSelected = items.length > 0 && chosen.length === items.length;
  const openUser = useCallback((id?: number) => void navigate({ search: (s) => ({ ...s, user: id, create: undefined }) }), [navigate]);
  const toggle = useCallback(
    (id: number) =>
      setSelected((s) => {
        const n = new Set(s);
        if (n.has(id)) n.delete(id);
        else n.add(id);
        return n;
      }),
    [],
  );
  const clear = useCallback(() => setSelected(new Set()), []);

  return (
    <>
      <PageHeader
        title={t("nav.users")}
        sub={counts ? t("users.subtitle", { n: counts.all, active: num(counts.active) }) : "…"}
        actions={
          <Button variant="primary" onClick={() => void navigate({ search: (s) => ({ ...s, create: true, user: undefined }) })}>
            <Plus size={18} aria-hidden />
            <span className="max-[760px]:hidden">{t("dashboard.newUser")}</span>
          </Button>
        }
      />
      <div className="reveal flex flex-col items-stretch justify-between gap-3 lg:flex-row lg:items-center">
        <div className="-mx-3 flex gap-2 overflow-x-auto px-3 pb-1 lg:mx-0 lg:flex-wrap lg:overflow-visible lg:px-0 lg:pb-0" role="group" aria-label={t("users.filter")}>
          {USER_STATES.map((f) => (
            <button
              key={f}
              type="button"
              className="chip shrink-0"
              aria-pressed={search.state === f}
              onClick={() => {
                clear();
                void navigate({ search: (s) => ({ ...s, state: f }) });
              }}
            >
              {t(`users.filters.${f}`)}
              {counts ? <span className="chip-count num">{num(counts[f])}</span> : null}
            </button>
          ))}
        </div>
        <label className="search-field">
          <Search size={16} aria-hidden />
          <input type="search" placeholder={t("users.searchPlaceholder")} value={q} onChange={(e) => setQ(e.target.value)} aria-label={t("users.searchLabel")} />
        </label>
      </div>
      <section className="card glass reveal overflow-hidden !p-2 md:!pb-0" style={{ "--i": 1 } as React.CSSProperties} aria-label={t("users.listLabel")} aria-busy={users.isPlaceholderData}>
        <QueryBoundary query={users} pending={<TableSkeleton />} title={t("users.loadFailed")}>
          {(data) =>
            data.counts.all === 0 ? (
              <EmptyState title={t("users.emptyTitle")} text={t("users.emptyText")}>
                <Button variant="primary" onClick={() => void navigate({ search: (s) => ({ ...s, create: true }) })}>
                  <Plus size={18} aria-hidden /> {t("dashboard.newUser")}
                </Button>
              </EmptyState>
            ) : data.items.length === 0 ? (
              <EmptyState search title={t("users.notFoundTitle")} text={search.q ? t("users.notFoundQuery", { q: search.q }) : t("users.notFoundGroup")}>
                <Button
                  onClick={() => {
                    written.current = "";
                    setQ("");
                    void navigate({ search: { state: "all", q: "" } });
                  }}
                >
                  {t("users.resetFilter")}
                </Button>
              </EmptyState>
            ) : (
              <>
                {narrow ? (
                  <div className="flex flex-col gap-2">
                    {data.items.map((u) => (
                      <UserCard key={u.id} u={u} tariff={u.tariff_id ? tariffById.get(u.tariff_id) : undefined} />
                    ))}
                  </div>
                ) : (
                  <table className="utable">
                    <thead>
                      <tr>
                        <th className="w-10">
                          <input type="checkbox" className="check" checked={allSelected} aria-label={t("users.selectAll")} onChange={() => setSelected(allSelected ? new Set() : new Set(data.items.map((u) => u.id)))} />
                        </th>
                        <th>{t("users.colUser")}</th>
                        <th>{t("users.colTariff")}</th>
                        <th>{t("users.colTraffic")}</th>
                        <th>{t("users.colExpiry")}</th>
                        <th>{t("users.colDevices")}</th>
                        <th>{t("users.colStatus")}</th>
                        <th className="w-12">
                          <span className="sr-only">{t("common.open")}</span>
                        </th>
                      </tr>
                    </thead>
                    <tbody>
                      {data.items.map((u) => (
                        <UserRow key={u.id} u={u} tariff={u.tariff_id ? tariffById.get(u.tariff_id) : undefined} selected={selected.has(u.id)} onToggle={toggle} onOpen={openUser} />
                      ))}
                    </tbody>
                  </table>
                )}
                <div className="flex items-center justify-between gap-3 border-t border-[var(--hairline)] p-3 text-[13px] text-[var(--ink-500)]">
                  <span>{t("users.shown", { n: num(data.items.length), total: num(data.total) })}</span>
                  <span className="max-md:hidden">{t("users.keyboardHint")}</span>
                </div>
              </>
            )
          }
        </QueryBoundary>
      </section>

      <BulkBar chosen={chosen} clear={clear} />
      <CreateUserDrawer
        open={!!search.create}
        onOpenChange={(v) => void navigate({ search: (s) => ({ ...s, create: v ? true : undefined }) })}
        onCreated={(id) => void navigate({ search: (s) => ({ ...s, create: undefined, user: id }) })}
      />
      <UserDrawer id={search.user} onClose={() => openUser(undefined)} />
    </>
  );
}

function Usage({ u }: { u: User }) {
  const used = u.used_up + u.used_down;
  if (u.traffic_limit == null) {
    return (
      <div className="usage inf min-w-[160px]">
        <div className="usage-txt">
          <span>
            <b className="num">{bytes(used)}</b>
          </span>
          <span>{t("users.unlimited")}</span>
        </div>
        <Bar pct={100} />
      </div>
    );
  }
  const pct = u.traffic_limit > 0 ? (used / u.traffic_limit) * 100 : 100;
  return (
    <div className={clsx("usage min-w-[160px]", pct >= 100 ? "bad" : pct >= 85 && "warn")}>
      <div className="usage-txt">
        <span>
          <b className="num">{bytes(used)}</b> {t("users.of", { total: bytes(u.traffic_limit) })}
        </span>
        <span className="num">{Math.min(100, Math.round(pct))}%</span>
      </div>
      <Bar pct={pct} label={t("users.colTraffic")} />
    </div>
  );
}

function Expiry({ u }: { u: User }) {
  const e = expiryText(u.expires_at);
  return (
    <>
      <div className="font-medium">{u.expires_at ? t("users.until", { date: dateShort(u.expires_at) }) : t("time.forever")}</div>
      {u.expires_at ? <div className={clsx("exp-days", e.tone)}>{e.text}</div> : null}
    </>
  );
}

// Rows are memoized: a poll hands back the same objects for users that did not change, and
// a tick in one checkbox then redraws that one row. Text is read at render time, so a row
// subscribes to the language itself.
const UserRow = memo(function UserRow({ u, tariff, selected, onToggle, onOpen }: { u: User; tariff?: Tariff; selected: boolean; onToggle: (id: number) => void; onOpen: (id: number) => void }) {
  useLocale();
  const devices = u.online_ips.length;
  // The row is clickable for the mouse; the keyboard and screen readers use the link in
  // the name cell and the checkbox, each a control of its own.
  return (
    <tr className={selected ? "sel" : undefined} onClick={() => onOpen(u.id)}>
      <td onClick={(e) => e.stopPropagation()}>
        <input type="checkbox" className="check" checked={selected} onChange={() => onToggle(u.id)} aria-label={t("users.select", { name: u.name })} />
      </td>
      <td>
        <div className="flex min-w-[200px] items-center gap-3">
          <Avatar name={u.name} seed={u.id} />
          <div className="min-w-0">
            <div className="truncate font-medium">
              <Link from="/users" to="/users" search={(s) => ({ ...s, user: u.id, create: undefined })} className="row-link" onClick={(e) => e.stopPropagation()}>
                {u.name}
              </Link>
              {u.online ? <span className="online-dot" title={t("users.onlineNow")} role="img" aria-label={t("users.onlineNow")} /> : null}
            </div>
            <div className="mt-0.5 flex flex-wrap items-center gap-1.5 text-xs text-[var(--ink-500)]">
              {u.contact ? <span>{u.contact}</span> : null}
              {u.tags.map((tag) => (
                <span key={tag} className="tag">
                  {tag}
                </span>
              ))}
            </div>
          </div>
        </div>
      </td>
      <td>
        <div className="font-medium">{tariff?.name ?? "—"}</div>
        <div className="mt-0.5 text-xs text-[var(--ink-500)]">{u.traffic_limit != null ? bytes(u.traffic_limit) : t("users.unlimited")}</div>
      </td>
      <td>
        <Usage u={u} />
      </td>
      <td>
        <Expiry u={u} />
      </td>
      <td>
        <span className={clsx("num font-medium", u.device_limit != null && devices >= u.device_limit && devices > 0 && "text-[var(--honey-600)]")}>
          {devices}
          <small className="font-normal text-[var(--ink-500)]"> {t("users.of", { total: u.device_limit ?? "∞" })}</small>
        </span>
      </td>
      <td>
        <StatePill state={u.state} />
      </td>
      <td className="text-right">
        <span className="icon-btn" aria-hidden>
          <ChevronRight size={18} />
        </span>
      </td>
    </tr>
  );
});

const UserCard = memo(function UserCard({ u, tariff }: { u: User; tariff?: Tariff }) {
  useLocale();
  const e = expiryText(u.expires_at);
  return (
    <Link from="/users" to="/users" search={(s) => ({ ...s, user: u.id, create: undefined })} className="panel-soft cv-auto grid w-full grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-3 p-3 text-left">
      <Avatar name={u.name} seed={u.id} />
      <div className="min-w-0">
        <div className="truncate font-medium">
          {u.name}
          {u.online ? <span className="online-dot" role="img" aria-label={t("users.onlineNow")} /> : null}
        </div>
        <div className="truncate text-xs text-[var(--ink-500)]">
          {tariff?.name ?? t("users.noTariff")} · {e.text}
        </div>
      </div>
      <StatePill state={u.state} />
      <div className="col-span-3">
        <Usage u={u} />
      </div>
    </Link>
  );
});

function TableSkeleton() {
  return (
    <div role="status" aria-busy aria-label={t("users.loadingList")}>
      {Array.from({ length: 8 }, (_, i) => (
        <div key={i} className="grid grid-cols-[40px_2fr_1fr_1.4fr_1fr_0.8fr_0.9fr] items-center gap-3 border-t border-[var(--hairline)] px-3 py-4 first:border-t-0">
          <Skeleton style={{ width: 18, height: 18, borderRadius: 6 }} />
          <div className="flex items-center gap-3">
            <Skeleton style={{ width: 36, height: 36, borderRadius: "50%" }} />
            <div className="flex flex-1 flex-col gap-2">
              <Skeleton style={{ width: "70%" }} />
              <Skeleton style={{ width: "40%" }} />
            </div>
          </div>
          <Skeleton style={{ width: "60%" }} />
          <Skeleton />
          <Skeleton style={{ width: "50%" }} />
          <Skeleton style={{ width: "40%" }} />
          <Skeleton style={{ width: "70%", height: 24, borderRadius: 12 }} />
        </div>
      ))}
    </div>
  );
}

type BulkAction = "extend" | "reset" | "disable" | "enable" | "delete";

/** How many names the delete confirmation lists before it says "…". */
const NAMES_SHOWN = 5;

function BulkBar({ chosen, clear }: { chosen: User[]; clear: () => void }) {
  const toast = useToast();
  const bulk = useUserMutation(userActions.bulk);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const n = chosen.length;
  const busy = bulk.isPending;
  const running = (action: BulkAction) => busy && bulk.variables?.action === action;
  const run = (action: BulkAction) =>
    bulk.mutate(
      { ids: chosen.map((u) => u.id), action },
      {
        onSuccess: (r) => {
          toast.ok(t(`users.bulkDone.${action}`, { n: r.affected }));
          clear();
          setConfirmDelete(false);
        },
        onError: (e) => toast.error(errorText(e)),
      },
    );
  // The confirmation of a delete has nothing to confirm once the selection is gone.
  useEffect(() => {
    if (n === 0) setConfirmDelete(false);
  }, [n]);
  const names = chosen
    .slice(0, NAMES_SHOWN)
    .map((u) => u.name)
    .join(", ");
  return (
    <>
      <AnimatePresence>
        {n > 0 ? (
          <motion.div
            className="bulk-bar glass-strong"
            role="region"
            aria-label={t("users.bulkLabel")}
            initial={{ opacity: 0, y: 24, x: "-50%" }}
            animate={{ opacity: 1, y: 0, x: "-50%" }}
            exit={{ opacity: 0, y: 24, x: "-50%" }}
            transition={{ type: "spring", stiffness: 420, damping: 32 }}
          >
            <span className="num mr-2 font-semibold whitespace-nowrap">{t("users.selected", { n })}</span>
            <Button size="sm" loading={running("extend")} disabled={busy} onClick={() => run("extend")} title={t("users.extendPeriodHint")}>
              <CalendarPlus size={16} aria-hidden />
              <span className="max-sm:hidden">{t("users.extendPeriod")}</span>
            </Button>
            <Button size="sm" loading={running("reset")} disabled={busy} onClick={() => run("reset")} aria-label={t("users.resetTraffic")}>
              <RotateCcw size={16} aria-hidden />
              <span className="max-sm:hidden">{t("users.resetTraffic")}</span>
            </Button>
            <Button size="sm" variant="danger" loading={running("disable")} disabled={busy} onClick={() => run("disable")} aria-label={t("users.disable")}>
              <Power size={16} aria-hidden />
              <span className="max-sm:hidden">{t("users.disable")}</span>
            </Button>
            <Button size="sm" variant="danger" disabled={busy} onClick={() => setConfirmDelete(true)} aria-label={t("users.deleteSelected")}>
              <Trash2 size={16} aria-hidden />
            </Button>
            <button type="button" className="icon-btn" aria-label={t("users.clearSelection")} disabled={busy} onClick={clear}>
              <X size={16} />
            </button>
          </motion.div>
        ) : null}
      </AnimatePresence>
      <Confirm
        open={confirmDelete && n > 0}
        onOpenChange={setConfirmDelete}
        title={t("users.deleteTitle", { n })}
        text={`${t("users.deleteText")} ${t("users.deleteWho", { names: n > NAMES_SHOWN ? `${names}…` : names })}`}
        confirm={t("common.delete")}
        danger
        loading={running("delete")}
        onConfirm={() => run("delete")}
      />
    </>
  );
}
