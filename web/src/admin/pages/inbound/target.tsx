import { useMutation } from "@tanstack/react-query";
import { useEffect } from "react";
import { api, errorText, unwrap, type Schemas } from "../../../api/client";
import { Button, Pill, Skeleton } from "../../../components/ui";
import { t, tMaybe } from "../../../i18n";
import { destIsIP } from "../../../lib/format";

type Target = Schemas["Result"];

function TargetBadges({ r }: { r: Target }) {
  if (r.error) {
    return <span className="text-xs text-[var(--berry-600)]">{tMaybe(`inbounds.targetErr.${r.error}`) ?? r.error}</span>;
  }
  const items: [string, boolean][] = [
    ["TLS 1.3", r.tls13],
    ["HTTP/2", r.h2],
    ["X25519", r.x25519],
    [t("inbounds.targetCert"), r.cert_valid],
  ];
  return (
    <span className="flex flex-wrap items-center gap-1.5">
      {items.map(([label, ok]) => (
        <Pill key={label} tone={ok ? "ok" : "bad"}>
          {label}
        </Pill>
      ))}
      <span className="num text-xs text-[var(--ink-500)]">{t("inbounds.targetRtt", { ms: r.rtt_ms })}</span>
    </span>
  );
}

/** Check the camouflage site or pick one next to the server (and the self-steal option). */
export function TargetPicker({ dest, sni, nodeId, onPick }: { dest: string; sni: string; nodeId: number; onPick: (dest: string, sni: string) => void }) {
  // An IP dest is checked with the site name clients send: a TLS handshake needs one.
  const name = destIsIP(dest) ? sni.trim() : "";
  const check = useMutation({
    mutationFn: () => unwrap(api.POST("/api/v1/inbounds/check-target", { body: { dest: dest.trim(), ...(name ? { sni: name } : {}) } })),
  });
  const resetCheck = check.reset;
  // A result is about what was checked; an edit makes it stale.
  useEffect(() => resetCheck(), [dest, sni, resetCheck]);
  const scan = useMutation({ mutationFn: () => unwrap(api.POST("/api/v1/inbounds/scan-targets", { params: { query: { node_id: nodeId } } })) });
  const candidates = scan.data ? [...(scan.data.self_steal?.ok ? [scan.data.self_steal] : []), ...scan.data.results] : [];
  return (
    <div className="-mt-2 mb-4">
      <div className="flex flex-wrap gap-2">
        <Button size="sm" loading={check.isPending} disabled={!dest.trim() || (destIsIP(dest) && !name)} onClick={() => check.mutate()}>
          {t("inbounds.targetCheck")}
        </Button>
        <Button size="sm" loading={scan.isPending} onClick={() => scan.mutate()}>
          {t("inbounds.targetScan")}
        </Button>
      </div>
      {check.data ? (
        <div className="panel-soft mt-2 flex flex-col gap-1.5 p-3" role="status">
          <span className="text-[13px] font-medium">{check.data.ok ? t("inbounds.targetOk") : t("inbounds.targetBad")}</span>
          <TargetBadges r={check.data} />
        </div>
      ) : check.isError ? (
        <p className="mt-2 text-[13px] text-[var(--berry-600)]" role="alert">
          {errorText(check.error)}
        </p>
      ) : null}
      {scan.isPending ? (
        <div className="mt-2 flex flex-col gap-2" aria-busy>
          <span className="text-xs text-[var(--ink-500)]">{t("inbounds.targetScanning")}</span>
          <Skeleton style={{ height: 52, borderRadius: 12 }} />
          <Skeleton style={{ height: 52, borderRadius: 12 }} />
        </div>
      ) : scan.isError ? (
        <p className="mt-2 text-[13px] text-[var(--berry-600)]" role="alert">
          {errorText(scan.error)}
        </p>
      ) : scan.data ? (
        <div className="mt-2 flex flex-col gap-2">
          <span className="text-xs text-[var(--ink-500)]">
            {candidates.length ? t("inbounds.targetFound", { n: candidates.length, scanned: scan.data.scanned }) : t("inbounds.targetNone", { scanned: scan.data.scanned })}
          </span>
          {candidates.map((c) => {
            const self = c === scan.data?.self_steal;
            return (
              <div key={c.dest + c.sni} className="panel-soft grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3 p-3">
                <div className="min-w-0">
                  <div className="truncate text-[13px] font-medium">{self ? t("inbounds.targetSelfSteal", { sni: c.sni }) : c.sni}</div>
                  <div className="mono truncate text-xs text-[var(--ink-500)]">{self ? t("inbounds.targetSelfStealHint") : c.dest}</div>
                  <div className="mt-1.5">
                    <TargetBadges r={c} />
                  </div>
                </div>
                <Button size="sm" variant="primary" onClick={() => onPick(c.dest, c.sni)}>
                  {t("inbounds.targetPick")}
                </Button>
              </div>
            );
          })}
        </div>
      ) : null}
    </div>
  );
}
