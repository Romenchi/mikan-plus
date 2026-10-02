import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Trash2, Zap } from "lucide-react";
import { useEffect, useState } from "react";
import { errorText } from "../../api/client";
import { Drawer } from "../../components/overlay";
import { useToast } from "../../components/toast";
import { Button, ErrorState, Field, Skeleton, Switch } from "../../components/ui";

export interface RelayData {
  configured: boolean;
  enabled: boolean;
  protocol: string;
  server: string;
  port: number;
  uuid?: string;
  flow?: string;
  tls: boolean;
  sni?: string;
  public_key?: string;
  short_id?: string;
  spider_x?: string;
  fingerprint?: string;
  inbounds: string[];
}

export function useRelay(nodeId: number | null, enabled = true) {
  return useQuery({
    queryKey: ["node-relay", nodeId ?? 0],
    queryFn: async () => {
      const res = await fetch(`/api/v1/nodes/${nodeId}/relay`, {
        headers: { "Content-Type": "application/json" },
      });
      if (!res.ok) throw new Error("Failed to fetch relay settings");
      return (await res.json()) as RelayData;
    },
    enabled: enabled && nodeId != null,
  });
}

export function RelayDrawer({ node, onClose }: { node: { id: number; name: string } | null; onClose: () => void }) {
  const relay = useRelay(node?.id ?? null);
  const qc = useQueryClient();
  const toast = useToast();

  const [linkInput, setLinkInput] = useState("");
  const [enabled, setEnabled] = useState(true);
  const [server, setServer] = useState("");
  const [port, setPort] = useState(443);
  const [uuid, setUuid] = useState("");
  const [sni, setSni] = useState("");
  const [pbk, setPbk] = useState("");
  const [sid, setSid] = useState("");
  const [spx, setSpx] = useState("");
  const [fp, setFp] = useState("firefox");
  const [flow, setFlow] = useState("xtls-rprx-vision");

  useEffect(() => {
    if (relay.data && relay.data.configured) {
      setEnabled(relay.data.enabled);
      setServer(relay.data.server || "");
      setPort(relay.data.port || 443);
      setUuid(relay.data.uuid || "");
      setSni(relay.data.sni || "");
      setPbk(relay.data.public_key || "");
      setSid(relay.data.short_id || "");
      setSpx(relay.data.spider_x || "");
      setFp(relay.data.fingerprint || "firefox");
      setFlow(relay.data.flow || "xtls-rprx-vision");
    }
  }, [relay.data]);

  const parseLink = () => {
    const raw = linkInput.trim();
    if (!raw.startsWith("vless://")) {
      toast.error("Ссылка должна начинаться с vless://");
      return;
    }
    try {
      const u = new URL(raw);
      setUuid(decodeURIComponent(u.username));
      setServer(u.hostname);
      setPort(u.port ? parseInt(u.port, 10) : 443);
      const q = u.searchParams;
      if (q.get("sni")) setSni(q.get("sni")!);
      if (q.get("pbk")) setPbk(q.get("pbk")!);
      if (q.get("sid")) setSid(q.get("sid")!);
      if (q.get("spx")) setSpx(q.get("spx")!);
      if (q.get("fp")) setFp(q.get("fp")!);
      if (q.get("flow")) setFlow(q.get("flow")!);
      setEnabled(true);
      toast.ok("Ссылка распознана!");
    } catch (e) {
      toast.error("Ошибка парсинга ссылки: " + String(e));
    }
  };

  const saveMutation = useMutation({
    mutationFn: async () => {
      const body = {
        enabled,
        protocol: "vless",
        server: server.trim(),
        port: Number(port),
        uuid: uuid.trim(),
        flow: flow.trim(),
        tls: true,
        sni: sni.trim(),
        public_key: pbk.trim(),
        short_id: sid.trim(),
        spider_x: spx.trim(),
        fingerprint: fp.trim() || "firefox",
        inbounds: [],
      };
      const res = await fetch(`/api/v1/nodes/${node!.id}/relay`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      if (!res.ok) throw new Error(await res.text());
      return (await res.json()) as RelayData;
    },
    onSuccess: (data) => {
      qc.setQueryData(["node-relay", node!.id], data);
      toast.ok("Настройки релея сохранены и применены!");
      onClose();
    },
    onError: (e) => toast.error(errorText(e)),
  });

  const deleteMutation = useMutation({
    mutationFn: async () => {
      const res = await fetch(`/api/v1/nodes/${node!.id}/relay`, {
        method: "DELETE",
      });
      if (!res.ok) throw new Error(await res.text());
    },
    onSuccess: () => {
      qc.setQueryData(["node-relay", node!.id], { configured: false, enabled: false, inbounds: [] });
      toast.ok("Релей отключен");
      onClose();
    },
    onError: (e) => toast.error(errorText(e)),
  });

  return (
    <Drawer open={!!node} onOpenChange={(v) => !v && onClose()} title="Мост / Релей ноды" meta={node?.name}>
      <div className="pt-4 space-y-4">
        {relay.isPending ? (
          <Skeleton style={{ height: 240, borderRadius: 16 }} />
        ) : relay.isError ? (
          <ErrorState text={errorText(relay.error)} onRetry={() => void relay.refetch()} />
        ) : (
          <>
            <p className="text-[13px] text-[var(--ink-600)]">
              Весь входящий трафик этой ноды (например, РФ) будет автоматически перенаправляться через указанный сервер (например, Германию). Клиенты выходят в интернет с зарубежным IP без блокировок.
            </p>

            <div className="card glass p-3.5">
              <Switch checked={enabled} onChange={setEnabled} label="Включить мост (Relay)" />
            </div>

            <div className="card glass p-3.5 space-y-2">
              <label className="text-xs font-semibold uppercase tracking-wider text-[var(--ink-500)]">
                Быстрый импорт из ссылки VLESS
              </label>
              <div className="flex gap-2">
                <input
                  className="input mono text-xs flex-1"
                  placeholder="vless://uuid@server:port?type=tcp&security=reality..."
                  value={linkInput}
                  onChange={(e) => setLinkInput(e.target.value)}
                />
                <Button variant="glass" onClick={parseLink}>
                  <Zap size={15} />
                  <span>Вставить</span>
                </Button>
              </div>
            </div>

            <div className="grid grid-cols-2 gap-3">
              <Field label="IP / Домен сервера" htmlFor="r-srv">
                <input id="r-srv" className="input mono" placeholder="2.26.84.45" value={server} onChange={(e) => setServer(e.target.value)} />
              </Field>
              <Field label="Порт" htmlFor="r-port">
                <input id="r-port" type="number" className="input mono" placeholder="57709" value={port} onChange={(e) => setPort(Number(e.target.value))} />
              </Field>
            </div>

            <Field label="UUID клиента" htmlFor="r-uuid">
              <input id="r-uuid" className="input mono text-xs" placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx" value={uuid} onChange={(e) => setUuid(e.target.value)} />
            </Field>

            <div className="grid grid-cols-2 gap-3">
              <Field label="SNI (Маскировка)" htmlFor="r-sni">
                <input id="r-sni" className="input mono" placeholder="ikea.com" value={sni} onChange={(e) => setSni(e.target.value)} />
              </Field>
              <Field label="Short ID" htmlFor="r-sid">
                <input id="r-sid" className="input mono" placeholder="56ec1116237b078c" value={sid} onChange={(e) => setSid(e.target.value)} />
              </Field>
            </div>

            <Field label="Public Key (Reality)" htmlFor="r-pbk">
              <input id="r-pbk" className="input mono text-xs" placeholder="z2AipA6qXdk..." value={pbk} onChange={(e) => setPbk(e.target.value)} />
            </Field>

            <div className="grid grid-cols-3 gap-2">
              <Field label="Flow" htmlFor="r-flow">
                <input id="r-flow" className="input mono text-xs" placeholder="xtls-rprx-vision" value={flow} onChange={(e) => setFlow(e.target.value)} />
              </Field>
              <Field label="SpiderX" htmlFor="r-spx">
                <input id="r-spx" className="input mono text-xs" placeholder="/YN8t1BFLnxbAuqW" value={spx} onChange={(e) => setSpx(e.target.value)} />
              </Field>
              <Field label="Fingerprint" htmlFor="r-fp">
                <input id="r-fp" className="input mono text-xs" placeholder="firefox" value={fp} onChange={(e) => setFp(e.target.value)} />
              </Field>
            </div>

            <div className="flex gap-2 pt-3">
              <Button variant="primary" loading={saveMutation.isPending} onClick={() => saveMutation.mutate()} className="flex-1">
                Сохранить и применить
              </Button>
              {relay.data?.configured && (
                <Button variant="danger" loading={deleteMutation.isPending} onClick={() => deleteMutation.mutate()}>
                  <Trash2 size={16} />
                </Button>
              )}
            </div>
          </>
        )}
      </div>
    </Drawer>
  );
}
