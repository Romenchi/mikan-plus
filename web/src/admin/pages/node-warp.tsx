// WARP on a node (GitHub issue #4): a Cloudflare WARP account the node runs as a
// WireGuard outbound. Inbounds pick it in their own settings; the lists here send
// domains and networks through it for every inbound of the node.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { RefreshCw, Trash2 } from "lucide-react";
import { useState } from "react";
import { api, errorText, unwrap, type Schemas } from "../../api/client";
import { qk } from "../../api/hooks";
import { Confirm, Drawer } from "../../components/overlay";
import { useToast } from "../../components/toast";
import { QueryBoundary } from "../../components/query";
import { Button, Field, Pill, Segmented, Skeleton } from "../../components/ui";
import { Switch } from "../../components/switch";
import { t } from "../../i18n";
import { useDraft } from "../../lib/draft";
import { fieldErrors } from "../../lib/fields";
import { ago } from "../../lib/format";

type Warp = Schemas["WarpView"];

export function useWarp(nodeId: number | null, enabled = true) {
  return useQuery({
    queryKey: qk.warp(nodeId ?? 0),
    queryFn: ({ signal }) => unwrap(api.GET("/api/v1/nodes/{id}/warp", { params: { path: { id: nodeId! } }, signal })),
    enabled: enabled && nodeId != null,
  });
}

export function WarpDrawer({ node, onClose }: { node: { id: number; name: string } | null; onClose: () => void }) {
  const warp = useWarp(node?.id ?? null);
  return (
    <Drawer open={!!node} onOpenChange={(v) => !v && onClose()} title={t("warp.title")} meta={node?.name}>
      <div className="pt-5">
        <QueryBoundary query={warp} pending={<Skeleton style={{ height: 240, borderRadius: 16 }} />}>
          {(w) =>
            w.configured ? (
              <Configured key={node!.id} nodeId={node!.id} w={w} refetch={() => void warp.refetch()} checking={warp.isFetching} onClose={onClose} />
            ) : (
              <Setup key={node!.id} nodeId={node!.id} />
            )
          }
        </QueryBoundary>
      </div>
    </Drawer>
  );
}

function Setup({ nodeId }: { nodeId: number }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [how, setHow] = useState<"register" | "import">("register");
  const [license, setLicense] = useState("");
  const [conf, setConf] = useState("");
  const done = (w: Warp) => {
    qc.setQueryData(qk.warp(nodeId), w);
    toast.ok(t("warp.ready"));
  };
  const register = useMutation({
    mutationFn: () => unwrap(api.POST("/api/v1/nodes/{id}/warp/register", { params: { path: { id: nodeId } }, body: license.trim() ? { license: license.trim() } : {} })),
    onSuccess: done,
  });
  const load = useMutation({
    mutationFn: () => unwrap(api.POST("/api/v1/nodes/{id}/warp/import", { params: { path: { id: nodeId } }, body: { config: conf } })),
    onSuccess: done,
  });
  const err = (how === "register" ? register.error : load.error) as unknown;
  const fields = fieldErrors(err);
  return (
    <>
      <p className="mb-4 text-[13px] text-[var(--ink-600)]">{t("warp.intro")}</p>
      <Segmented
        label={t("warp.how")}
        value={how}
        onChange={setHow}
        options={[
          { value: "register", label: t("warp.register") },
          { value: "import", label: t("warp.import") },
        ]}
      />
      <div className="mt-4">
        {err && !Object.keys(fields).length ? <div className="banner err mb-4">{errorText(err)}</div> : null}
        {how === "register" ? (
          <>
            <p className="mb-3 text-xs text-[var(--ink-500)]">{t("warp.registerHint")}</p>
            <Field label={t("warp.license")} htmlFor="w-lic" hint={t("warp.licenseHint")} error={fields.license}>
              <input id="w-lic" className="input mono" value={license} onChange={(e) => setLicense(e.target.value)} placeholder="xxxxxxxx-xxxxxxxx-xxxxxxxx" autoComplete="off" spellCheck={false} />
            </Field>
            <Button variant="primary" loading={register.isPending} onClick={() => register.mutate()}>
              {t("warp.registerDo")}
            </Button>
          </>
        ) : (
          <>
            <Field label={t("warp.conf")} htmlFor="w-conf" hint={t("warp.confHint")} error={fields.config}>
              <textarea
                id="w-conf"
                className="input mono min-h-[180px] py-2 text-xs"
                value={conf}
                onChange={(e) => setConf(e.target.value)}
                placeholder={"[Interface]\nPrivateKey = …\nAddress = 172.16.0.2/32\n[Peer]\nPublicKey = …\nEndpoint = 162.159.192.1:2408"}
                spellCheck={false}
                aria-invalid={!!fields.config}
              />
            </Field>
            <Button variant="primary" loading={load.isPending} disabled={!conf.trim()} onClick={() => load.mutate()}>
              {t("warp.importDo")}
            </Button>
          </>
        )}
      </div>
    </>
  );
}

function Configured({ nodeId, w, refetch, checking, onClose }: { nodeId: number; w: Warp; refetch: () => void; checking: boolean; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  // The status check refetches `w`: the typed routes survive it.
  const {
    draft: { enabled, routes },
    setDraft,
  } = useDraft({ enabled: w.enabled, routes: w.routes.join("\n") });
  const setEnabled = (v: boolean) => setDraft((d) => ({ ...d, enabled: v }));
  const setRoutes = (v: string) => setDraft((d) => ({ ...d, routes: v }));
  const [license, setLicense] = useState("");
  const [remove, setRemove] = useState(false);
  const save = useMutation({
    mutationFn: () =>
      unwrap(
        api.PATCH("/api/v1/nodes/{id}/warp", {
          params: { path: { id: nodeId } },
          body: { enabled, routes: routes.split(/\n/).map((s) => s.trim()).filter(Boolean), ...(license.trim() ? { license: license.trim() } : {}) },
        }),
      ),
    onSuccess: (v) => {
      qc.setQueryData(qk.warp(nodeId), v);
      setLicense("");
      toast.ok(t("warp.saved"));
    },
  });
  const del = useMutation({
    mutationFn: () => unwrap(api.DELETE("/api/v1/nodes/{id}/warp", { params: { path: { id: nodeId } } })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: qk.warp(nodeId) });
      setRemove(false);
      toast.ok(t("warp.deleted"));
      onClose();
    },
    onError: (e) => toast.error(errorText(e)),
  });
  const fields = fieldErrors(save.error);
  const s = w.status;
  return (
    <>
      <div className="panel-soft mb-4 p-3" role="status">
        <div className="flex items-center justify-between gap-2">
          <div className="flex flex-wrap items-center gap-2 text-[13px] font-semibold">
            {t("warp.statusTitle")}
            {!w.enabled ? (
              <Pill tone="off">{t("warp.off")}</Pill>
            ) : !s ? (
              <Pill tone="off">{t("warp.unknown")}</Pill>
            ) : s.ok ? (
              <Pill tone="ok">{s.warp === "plus" ? "WARP+" : t("warp.works")}</Pill>
            ) : (
              <Pill tone="bad">{t("warp.down")}</Pill>
            )}
          </div>
          <Button size="sm" variant="ghost" loading={checking} onClick={refetch} aria-label={t("warp.check")}>
            <RefreshCw size={14} aria-hidden /> {t("warp.check")}
          </Button>
        </div>
        {s && w.enabled ? (
          <div className="mt-2 text-xs text-[var(--ink-500)]">
            {s.ok ? t("warp.exit", { ip: s.ip ?? "", colo: s.colo ?? "" }) : t("warp.downText")} · {ago(s.checked_at)}
          </div>
        ) : w.enabled ? (
          <div className="mt-2 text-xs text-[var(--ink-500)]">{t("warp.nodeUnreachable")}</div>
        ) : null}
      </div>

      {save.error && !Object.keys(fields).length ? <div className="banner err mb-4">{errorText(save.error)}</div> : null}
      <div className="mb-4 flex items-start justify-between gap-3">
        <div>
          <div className="text-[13px] font-semibold">{t("warp.enabled")}</div>
          <div className="text-xs text-[var(--ink-500)]">{t("warp.enabledSub")}</div>
        </div>
        <Switch checked={enabled} onChange={setEnabled} label={t("warp.enabled")} />
      </div>

      <dl className="mb-4 grid grid-cols-2 gap-3 text-xs">
        <div>
          <dt className="text-[var(--ink-500)]">{t("warp.account")}</dt>
          <dd className="text-[13px]">{w.source === "import" ? t("warp.imported") : w.plus ? "WARP+" : "WARP"}</dd>
        </div>
        <div>
          <dt className="text-[var(--ink-500)]">{t("warp.endpoint")}</dt>
          <dd className="mono">{w.endpoint}</dd>
        </div>
      </dl>

      <div className="mb-4">
        <div className="mb-1 text-[13px] font-semibold">{t("warp.inbounds")}</div>
        <p className="text-xs text-[var(--ink-500)]">{w.inbounds.length ? w.inbounds.join(", ") : t("warp.inboundsNone")}</p>
        <p className="mt-1 text-xs text-[var(--ink-500)]">{t("warp.inboundsHint")}</p>
      </div>

      <Field label={t("warp.routes")} htmlFor="w-routes" hint={t("warp.routesHint")} error={fields.routes}>
        <textarea
          id="w-routes"
          className="input mono min-h-[120px] py-2 text-xs"
          value={routes}
          onChange={(e) => setRoutes(e.target.value)}
          placeholder={"openai.com\nnetflix.com\n104.16.0.0/13"}
          spellCheck={false}
          aria-invalid={!!fields.routes}
        />
      </Field>

      {w.source === "register" && !w.plus ? (
        <Field label={t("warp.license")} htmlFor="w-lic2" hint={t("warp.licenseHint")} error={fields.license}>
          <input id="w-lic2" className="input mono" value={license} onChange={(e) => setLicense(e.target.value)} placeholder="xxxxxxxx-xxxxxxxx-xxxxxxxx" autoComplete="off" spellCheck={false} />
        </Field>
      ) : null}

      <div className="flex flex-wrap gap-2">
        <Button variant="primary" loading={save.isPending} onClick={() => save.mutate()}>
          {t("common.save")}
        </Button>
        <Button variant="danger" onClick={() => setRemove(true)}>
          <Trash2 size={16} aria-hidden /> {t("warp.delete")}
        </Button>
      </div>
      <Confirm open={remove} onOpenChange={setRemove} title={t("warp.deleteTitle")} text={t("warp.deleteText")} confirm={t("warp.delete")} danger loading={del.isPending} onConfirm={() => del.mutate()} />
    </>
  );
}
