import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ArrowRightLeft, Cloud, Copy, KeyRound, Pencil, Plus, ShieldCheck, Trash2, Waypoints } from "lucide-react";
import { useEffect, useState, type FormEvent } from "react";
import { api, ApiError, errorText, unwrap, type Schemas } from "../../api/client";
import { qk, useNodes } from "../../api/hooks";
import { CertDrawer, certUntil } from "../../components/cert-drawer";
import { Confirm, Drawer } from "../../components/overlay";
import { QueryBoundary } from "../../components/query";
import { useToast } from "../../components/toast";
import { Bar, Button, EmptyState, Field, PageHeader, Pill, Skeleton } from "../../components/ui";
import { Switch } from "../../components/switch";
import { t, tMaybe } from "../../i18n";
import { useCopy } from "../../lib/copy";
import { bytes, num } from "../../lib/format";
import { CascadeDrawer } from "./node-cascade";
import { RelayDrawer } from "./node-relay";
import { WarpDrawer } from "./node-warp";

type Node = Schemas["NodeInfo"];
type Joined = { name: string; key: string; command: string };

export function nodeLabel(n: Pick<Node, "name" | "local">): string {
  return n.name || (n.local ? t("nodes.localName") : "—");
}

export function NodesPage() {
  const nodes = useNodes();
  const qc = useQueryClient();
  const toast = useToast();
  const [adding, setAdding] = useState(false);
  const [editing, setEditing] = useState<Node | null>(null);
  const [rekeying, setRekeying] = useState<Node | null>(null);
  const [removing, setRemoving] = useState<Node | null>(null);
  const [joined, setJoined] = useState<Joined | null>(null);
  const [warpOf, setWarpOf] = useState<Node | null>(null);
  const [cascadeOf, setCascadeOf] = useState<Node | null>(null);
  const [relayOf, setRelayOf] = useState<Node | null>(null);
  const [certOf, setCertOf] = useState<Node | null>(null);
  const refresh = () => {
    void qc.invalidateQueries({ queryKey: qk.nodes });
    void qc.invalidateQueries({ queryKey: qk.inbounds });
  };
  const rekey = useMutation({
    mutationFn: (id: number) => unwrap(api.POST("/api/v1/nodes/{id}/key", { params: { path: { id } } })),
    onSuccess: (r) => {
      setRekeying(null);
      setJoined({ name: nodeLabel(r.node), key: r.key, command: r.command });
    },
    onSettled: refresh,
    onError: (e) => toast.error(errorText(e)),
  });
  const remove = useMutation({
    mutationFn: (id: number) => unwrap(api.DELETE("/api/v1/nodes/{id}", { params: { path: { id } } })),
    onSuccess: () => {
      toast.ok(t("nodes.deleted"));
      setRemoving(null);
    },
    onSettled: refresh,
    // A node still in use names what goes through it: all of it, not just the first.
    onError: (e) =>
      toast.error(e instanceof ApiError && e.status === 409 && e.detail === "node_in_use" ? `${t("errors.api.node_in_use")} ${e.messages.join("; ")}` : errorText(e)),
  });

  return (
    <>
      <PageHeader
        title={t("nav.nodes")}
        sub={t("nodes.subtitle")}
        actions={
          <Button variant="primary" onClick={() => setAdding(true)}>
            <Plus size={18} aria-hidden />
            <span className="max-[760px]:hidden">{t("nodes.add")}</span>
          </Button>
        }
      />
      <QueryBoundary
        query={nodes}
        pending={
          <div className="grid gap-4 lg:grid-cols-2">
            {[0, 1].map((i) => (
              <Skeleton key={i} style={{ height: 220, borderRadius: 20 }} />
            ))}
          </div>
        }
        wrap={(state) => <section className="card glass">{state}</section>}
      >
        {(list) =>
          list.length === 0 ? (
            <section className="card glass">
              <EmptyState title={t("nodes.emptyTitle")} text={t("nodes.emptyText")} />
            </section>
          ) : (
            <div className="grid gap-4 lg:grid-cols-2">
              {list.length > 1 && list.some((n) => n.local && !n.name) ? (
                <div className="banner warn lg:col-span-2" role="status">
                  <Pencil size={18} className="shrink-0" aria-hidden />
                  <span>{t("nodes.nameLocalHint")}</span>
                </div>
              ) : null}
              {list.map((n, idx) => (
                <NodeCard
                  key={n.id}
                  n={n}
                  idx={idx}
                  onEdit={() => setEditing(n)}
                  onWarp={() => setWarpOf(n)}
                  onCascade={() => setCascadeOf(n)}
                  onRelay={() => setRelayOf(n)}
                  onCert={() => setCertOf(n)}
                  onRekey={() => setRekeying(n)}
                  onRemove={() => setRemoving(n)}
                />
              ))}
            </div>
          )
        }
      </QueryBoundary>
      <AddNodeDrawer
        open={adding}
        onOpenChange={setAdding}
        onJoined={(j) => {
          setAdding(false);
          setJoined(j);
        }}
      />
      <EditNodeDrawer node={editing} onClose={() => setEditing(null)} />
      <KeyDrawer joined={joined} onClose={() => setJoined(null)} />
      <WarpDrawer node={warpOf ? { id: warpOf.id, name: nodeLabel(warpOf) } : null} onClose={() => setWarpOf(null)} />
      <RelayDrawer node={relayOf ? { id: relayOf.id, name: nodeLabel(relayOf) } : null} onClose={() => setRelayOf(null)} />
      <CascadeDrawer node={cascadeOf ? { id: cascadeOf.id, name: nodeLabel(cascadeOf) } : null} onClose={() => setCascadeOf(null)} />
      <CertDrawer
        open={!!certOf}
        onClose={() => setCertOf(null)}
        title={t("cert.nodeTitle")}
        meta={certOf ? nodeLabel(certOf) : undefined}
        lead={t("cert.nodeLead")}
        current={certOf?.certificate ?? null}
        save={(cert, key) => unwrap(api.PUT("/api/v1/nodes/{id}/certificate", { params: { path: { id: certOf!.id } }, body: { cert, key } })).then(() => qc.invalidateQueries({ queryKey: qk.nodes }))}
        clear={() => unwrap(api.DELETE("/api/v1/nodes/{id}/certificate", { params: { path: { id: certOf!.id } } })).then(() => qc.invalidateQueries({ queryKey: qk.nodes }))}
        clearLabel={t("cert.nodeClear")}
        clearText={t("cert.nodeClearText")}
      />
      <Confirm
        open={!!rekeying}
        onOpenChange={(v) => !v && setRekeying(null)}
        title={t("nodes.rekeyTitle", { name: rekeying ? nodeLabel(rekeying) : "" })}
        text={t("nodes.rekeyText")}
        confirm={t("nodes.rekeyConfirm")}
        loading={rekey.isPending}
        onConfirm={() => rekeying && rekey.mutate(rekeying.id)}
      />
      <Confirm
        open={!!removing}
        onOpenChange={(v) => !v && setRemoving(null)}
        title={t("nodes.deleteTitle", { name: removing ? nodeLabel(removing) : "" })}
        text={t("nodes.deleteText")}
        confirm={t("common.delete")}
        danger
        loading={remove.isPending}
        onConfirm={() => removing && remove.mutate(removing.id)}
      />
    </>
  );
}

function NodeCard({
  n,
  idx,
  onEdit,
  onWarp,
  onCascade,
  onRelay,
  onCert,
  onRekey,
  onRemove,
}: {
  n: Node;
  idx: number;
  onEdit: () => void;
  onWarp: () => void;
  onCascade: () => void;
  onRelay: () => void;
  onCert: () => void;
  onRekey: () => void;
  onRemove: () => void;
}) {
  const mem = n.mem_total ? Math.round((n.mem_used / n.mem_total) * 100) : 0;
  return (
    <section className="card glass reveal" style={{ "--i": idx } as React.CSSProperties}>
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <h2 className="font-display truncate text-lg font-medium tracking-tight">{nodeLabel(n)}</h2>
          <div className="mt-1 flex flex-wrap items-center gap-2 text-xs text-[var(--ink-500)]">
            <span className="mono">{n.host || "—"}</span>
            <span>·</span>
            <span>{n.local ? t("nodes.kindLocal") : t("nodes.kindRemote")}</span>
          </div>
        </div>
        <NodeStatus n={n} />
      </div>
      {n.status === "error" && n.enabled ? (
        <p className="mt-3 text-[13px] text-[var(--berry-600)]" role="alert">
          {n.local ? t("nodes.localOffline") : t("nodes.remoteOffline")}
        </p>
      ) : null}
      <dl className="mt-4 grid grid-cols-2 gap-x-4 gap-y-3 text-[13px]">
        <div>
          <dt className="text-xs text-[var(--ink-500)]">{t("nodes.protocols")}</dt>
          <dd className="num">{n.status === "ok" ? t("nodes.listeners", { ok: n.listeners_ok, total: n.listeners }) : num(n.inbounds)}</dd>
        </div>
        <div>
          <dt className="text-xs text-[var(--ink-500)]">{t("nodes.conns")}</dt>
          <dd className="num">{n.status === "ok" ? num(n.conns) : "—"}</dd>
        </div>
        <div>
          <dt className="text-xs text-[var(--ink-500)]">{t("nodes.cpu")}</dt>
          <dd>
            {n.status === "ok" ? (
              <div className="flex items-center gap-2">
                <Bar pct={n.cpu_percent} className="flex-1" label={t("nodes.cpu")} />
                <span className="num w-10 text-right">{Math.round(n.cpu_percent)}%</span>
              </div>
            ) : (
              "—"
            )}
          </dd>
        </div>
        <div>
          <dt className="text-xs text-[var(--ink-500)]">{t("nodes.memory")}</dt>
          <dd>
            {n.status === "ok" && n.mem_total ? (
              <div className="flex items-center gap-2" title={`${bytes(n.mem_used)} / ${bytes(n.mem_total)}`}>
                <Bar pct={mem} className="flex-1" label={t("nodes.memory")} />
                <span className="num w-10 text-right">{mem}%</span>
              </div>
            ) : (
              "—"
            )}
          </dd>
        </div>
        {!n.local ? (
          <div className="col-span-2">
            <dt className="text-xs text-[var(--ink-500)]">{t("nodes.api")}</dt>
            <dd className="mono truncate">{n.address}</dd>
          </div>
        ) : null}
        {n.version ? (
          <div className="col-span-2">
            <dt className="text-xs text-[var(--ink-500)]">{t("nodes.version")}</dt>
            <dd className="num">{n.version}</dd>
          </div>
        ) : null}
        {n.certificate ? (
          <div className="col-span-2">
            <dt className="text-xs text-[var(--ink-500)]">{t("nodes.cert")}</dt>
            <dd className={n.certificate.error ? "text-[var(--berry-600)]" : undefined}>
              {n.certificate.error ? (tMaybe(`errors.acme.${n.certificate.error}`) ?? n.certificate.error) : t("nodes.certOwn", { until: certUntil(n.certificate.not_after) })}
            </dd>
          </div>
        ) : null}
      </dl>
      <div className="mt-4 flex flex-wrap gap-2 border-t border-[var(--hairline)] pt-4">
        <Button size="sm" onClick={onEdit}>
          <Pencil size={16} aria-hidden /> {t("nodes.configure")}
        </Button>
        <Button size="sm" onClick={onRelay}>
          <ArrowRightLeft size={16} aria-hidden /> Мост / Релей
        </Button>
        <Button size="sm" onClick={onWarp}>
          <Cloud size={16} aria-hidden /> WARP
        </Button>
        <Button size="sm" onClick={onCascade}>
          <Waypoints size={16} aria-hidden /> {t("cascade.title")}
        </Button>
        <Button size="sm" onClick={onCert}>
          <ShieldCheck size={16} aria-hidden /> {t("nodes.certButton")}
        </Button>
        {!n.local ? (
          <>
            <Button size="sm" onClick={onRekey}>
              <KeyRound size={16} aria-hidden /> {t("nodes.rekey")}
            </Button>
            <Button size="sm" variant="danger" onClick={onRemove}>
              <Trash2 size={16} aria-hidden /> {t("common.delete")}
            </Button>
          </>
        ) : null}
      </div>
    </section>
  );
}

function NodeStatus({ n }: { n: Node }) {
  if (!n.enabled) return <Pill tone="off">{t("nodes.disabled")}</Pill>;
  if (n.status === "ok") return <Pill tone="ok">{t("nodes.online")}</Pill>;
  if (n.status === "error") return <Pill tone="bad">{t("nodes.offline")}</Pill>;
  return <Pill tone="off">{t("nodes.checking")}</Pill>;
}

function AddNodeDrawer({ open, onOpenChange, onJoined }: { open: boolean; onOpenChange: (v: boolean) => void; onJoined: (j: Joined) => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const init = { name: "", host: "", domain: "", api_port: "" };
  const [form, setForm] = useState(init);
  const [errors, setErrors] = useState<Record<string, string>>({});
  useEffect(() => {
    if (open) {
      setForm(init);
      setErrors({});
    }
    // Reset only when the drawer opens.
  }, [open]);
  const create = useMutation({
    mutationFn: (body: Schemas["CreateNodeInputBody"]) => unwrap(api.POST("/api/v1/nodes", { body })),
    onSuccess: (r) => {
      void qc.invalidateQueries({ queryKey: qk.nodes });
      void qc.invalidateQueries({ queryKey: qk.inbounds });
      onJoined({ name: nodeLabel(r.node), key: r.key, command: r.command });
    },
    onError: (e) => {
      if (e instanceof ApiError && Object.keys(e.fields).length) setErrors(e.fields);
      else toast.error(errorText(e));
    },
  });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    const port = form.api_port.trim();
    if (port && !/^\d{1,5}$/.test(port)) {
      setErrors({ api_port: t("errors.api.bad_port") });
      return;
    }
    create.mutate({ name: form.name.trim(), host: form.host.trim(), domain: form.domain.trim() || undefined, api_port: port ? Number(port) : undefined });
  };
  const set = (k: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement>) => {
    setForm((f) => ({ ...f, [k]: e.target.value }));
    setErrors(({ [k]: _, ...rest }) => rest);
  };
  return (
    <Drawer
      open={open}
      onOpenChange={onOpenChange}
      title={t("nodes.newTitle")}
      meta={t("nodes.newMeta")}
      footer={
        <>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            {t("common.cancel")}
          </Button>
          <Button variant="primary" type="submit" form="add-node" loading={create.isPending}>
            {t("nodes.addSubmit")}
          </Button>
        </>
      }
    >
      <form id="add-node" onSubmit={submit} className="pt-5" noValidate>
        <Field label={t("nodes.name")} htmlFor="n-name" hint={t("nodes.nameHint")} error={errors.name}>
          <input id="n-name" className="input" value={form.name} onChange={set("name")} placeholder={t("nodes.namePlaceholderNew")} maxLength={48} autoComplete="off" aria-invalid={!!errors.name} />
        </Field>
        <Field label={t("nodes.host")} htmlFor="n-host" hint={t("nodes.hostHint")} error={errors.host}>
          <input id="n-host" className="input mono" value={form.host} onChange={set("host")} placeholder="203.0.113.10" autoComplete="off" aria-invalid={!!errors.host} />
        </Field>
        <Field label={t("nodes.domain")} htmlFor="n-domain" hint={t("nodes.domainHint")} error={errors.domain}>
          <input id="n-domain" className="input mono" value={form.domain} onChange={set("domain")} placeholder="us.example.com" autoComplete="off" aria-invalid={!!errors.domain} />
        </Field>
        <Field label={t("nodes.apiPort")} htmlFor="n-port" hint={t("nodes.apiPortHint")} error={errors.api_port}>
          <input id="n-port" className="input max-w-[160px]" inputMode="numeric" value={form.api_port} onChange={set("api_port")} placeholder={t("nodes.apiPortAuto")} aria-invalid={!!errors.api_port} />
        </Field>
      </form>
    </Drawer>
  );
}

/** The join key is shown once: the panel keeps only its fingerprint. */
function KeyDrawer({ joined, onClose }: { joined: Joined | null; onClose: () => void }) {
  const copyText = useCopy();
  const copy = () => joined && copyText(joined.command, t("nodes.commandCopied"));
  return (
    <Drawer
      open={!!joined}
      onOpenChange={(v) => !v && onClose()}
      title={t("nodes.keyTitle")}
      meta={joined?.name}
      footer={
        <Button variant="primary" onClick={onClose}>
          {t("nodes.keyDone")}
        </Button>
      }
    >
      <div className="pt-5">
        <div className="banner warn mb-4">
          <KeyRound size={18} className="shrink-0" aria-hidden />
          <span>{t("nodes.keyOnce")}</span>
        </div>
        <ol className="mb-4 list-decimal space-y-2 pl-5 text-[13px]">
          <li>{t("nodes.keyStep1")}</li>
          <li>{t("nodes.keyStep2")}</li>
        </ol>
        <div className="link-field">
          <span className="mono break-all text-xs">{joined?.command}</span>
          <button type="button" className="icon-btn" onClick={() => void copy()} aria-label={t("nodes.copyCommand")}>
            <Copy size={18} />
          </button>
        </div>
      </div>
    </Drawer>
  );
}

function EditNodeDrawer({ node, onClose }: { node: Node | null; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [form, setForm] = useState({ name: "", host: "", domain: "", enabled: true });
  const [errors, setErrors] = useState<Record<string, string>>({});
  useEffect(() => {
    if (node) {
      setForm({ name: node.name, host: node.host, domain: node.domain, enabled: node.enabled });
      setErrors({});
    }
  }, [node]);
  const save = useMutation({
    mutationFn: ({ id, body }: { id: number; body: Schemas["PatchNodeInputBody"] }) => unwrap(api.PATCH("/api/v1/nodes/{id}", { params: { path: { id } }, body })),
    onSuccess: () => {
      toast.ok(t("nodes.saved"));
      onClose();
    },
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: qk.nodes });
      void qc.invalidateQueries({ queryKey: qk.inbounds });
    },
    onError: (e) => {
      if (e instanceof ApiError && Object.keys(e.fields).length) setErrors(e.fields);
      else toast.error(errorText(e));
    },
  });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (!node) return;
    const body: Schemas["PatchNodeInputBody"] = { name: form.name.trim(), enabled: form.enabled };
    if (!node.local) {
      body.host = form.host.trim();
      body.domain = form.domain.trim();
    }
    save.mutate({ id: node.id, body });
  };
  const set = (k: "name" | "host" | "domain") => (e: React.ChangeEvent<HTMLInputElement>) => {
    setForm((f) => ({ ...f, [k]: e.target.value }));
    setErrors(({ [k]: _, ...rest }) => rest);
  };
  return (
    <Drawer
      open={!!node}
      onOpenChange={(v) => !v && onClose()}
      title={node ? nodeLabel(node) : ""}
      meta={node?.local ? t("nodes.kindLocal") : t("nodes.kindRemote")}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button variant="primary" type="submit" form="edit-node" loading={save.isPending}>
            {t("common.save")}
          </Button>
        </>
      }
    >
      <form id="edit-node" onSubmit={submit} className="pt-5" noValidate>
        <Field label={t("nodes.name")} htmlFor="e-name" hint={t("nodes.nameHint")} error={errors.name}>
          <input id="e-name" className="input" value={form.name} onChange={set("name")} placeholder={t("nodes.namePlaceholderEdit")} maxLength={48} autoComplete="off" aria-invalid={!!errors.name} />
        </Field>
        {node && !node.local ? (
          <>
            <Field label={t("nodes.host")} htmlFor="e-host" hint={t("nodes.hostHint")} error={errors.host}>
              <input id="e-host" className="input mono" value={form.host} onChange={set("host")} autoComplete="off" aria-invalid={!!errors.host} />
            </Field>
            <Field label={t("nodes.domain")} htmlFor="e-domain" hint={t("nodes.domainHint")} error={errors.domain}>
              <input id="e-domain" className="input mono" value={form.domain} onChange={set("domain")} autoComplete="off" aria-invalid={!!errors.domain} />
            </Field>
          </>
        ) : (
          <p className="mb-4 text-[13px] text-[var(--ink-500)]">{t("nodes.localAddress")}</p>
        )}
        <Field label={t("nodes.serving")} hint={t("nodes.servingHint")}>
          <Switch checked={form.enabled} onChange={(v) => setForm((f) => ({ ...f, enabled: v }))} label={t("nodes.serving")} />
        </Field>
      </form>
    </Drawer>
  );
}
