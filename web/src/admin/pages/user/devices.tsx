import { Laptop, Layers, Smartphone, Unlink } from "lucide-react";
import { useState } from "react";
import { errorText, type Schemas, type User } from "../../../api/client";
import { useBoundDevices, useDevices, userActions, useSettings, useUserMutation } from "../../../api/hooks";
import { Confirm } from "../../../components/overlay";
import { useToast } from "../../../components/toast";
import { ErrorState, Skeleton } from "../../../components/ui";
import { t } from "../../../i18n";
import { desktopOS, deviceDetails, deviceLabel } from "../../../lib/devices";
import { ago, maskIP } from "../../../lib/format";
import { Section } from "./section";

export function DevicesSection({ u }: { u: User }) {
  const devices = useDevices(u.id);
  const toast = useToast();
  const update = useUserMutation(userActions.update);
  const list = devices.data ?? [];
  const setLimit = (n: number | null) =>
    update.mutate({ id: u.id, body: n === null ? { devices_unlimited: true } : { device_limit: n } }, { onError: (e) => toast.error(errorText(e)) });
  return (
    <Section title={t("userDrawer.devices")} aside={t("userDrawer.devicesAside", { online: u.online_ips.length, limit: u.device_limit ?? "∞" })}>
      <div className="mb-4 flex items-center gap-2 text-[13px]">
        <span className="text-[var(--ink-600)]">{t("userDrawer.deviceLimit")}</span>
        <div className="seg" role="group" aria-label={t("userDrawer.deviceLimit")}>
          {[1, 2, 3, 5, 10].map((n) => (
            <button key={n} type="button" aria-pressed={u.device_limit === n} onClick={() => setLimit(n)}>
              {n}
            </button>
          ))}
          <button type="button" aria-pressed={u.device_limit == null} onClick={() => setLimit(null)}>
            ∞
          </button>
        </div>
      </div>
      <BoundDevices u={u} />
      <h4 className="mt-5 mb-2 text-xs font-medium text-[var(--ink-500)]">{t("userDrawer.addresses")}</h4>
      {devices.isPending ? (
        <Skeleton style={{ height: 52, borderRadius: 16 }} />
      ) : devices.data === undefined ? (
        <ErrorState text={errorText(devices.error)} onRetry={() => void devices.refetch()} />
      ) : list.length === 0 ? (
        <p className="text-[13px] text-[var(--ink-500)]">{t("userDrawer.noDevices")}</p>
      ) : (
        <ul className="flex flex-col gap-2">
          {list.slice(0, 8).map((d) => (
            <li key={d.ip} className="panel-soft grid grid-cols-[36px_minmax(0,1fr)] items-center gap-3 p-2">
              <span className="grid h-9 w-9 place-items-center rounded-[10px] bg-[var(--hover)] text-[var(--ink-600)]"><Smartphone size={18} /></span>
              <div className="min-w-0">
                <div className="text-[13px] font-medium">{t("userDrawer.device")}</div>
                <div className="text-xs text-[var(--ink-500)]">
                  <span className="mono">{maskIP(d.ip)}</span> · {d.online ? <span className="text-[var(--leaf-700)]">{t("users.onlineNow")}</span> : ago(d.last_seen)}
                </div>
              </div>
            </li>
          ))}
        </ul>
      )}
      <p className="mt-2 text-xs text-[var(--ink-500)]">{t("userDrawer.devicesNote")}</p>
    </Section>
  );
}

type BoundDevice = Schemas["BoundDeviceView"];

/** What to call a bound device: its model, else its system, else the app. */
function deviceName(d: BoundDevice): string {
  return d.hwid ? deviceLabel(d, t("userDrawer.device")) : t("userDrawer.sharedPlace");
}

function BoundDevices({ u }: { u: User }) {
  const settings = useSettings();
  const bound = useBoundDevices(u.id);
  const unbind = useUserMutation(userActions.unbindDevice);
  const toast = useToast();
  const [pick, setPick] = useState<BoundDevice | null>(null);
  const list = bound.data ?? [];
  if (!settings.data?.device_binding && list.length === 0) return null;
  return (
    <>
      <h4 className="mb-2 flex justify-between gap-2 text-xs font-medium text-[var(--ink-500)]">
        {t("userDrawer.boundTitle")}
        {bound.data ? <span className="num">{u.device_limit != null ? t("userDrawer.boundCount", { n: list.length, limit: u.device_limit }) : list.length}</span> : null}
      </h4>
      {bound.data === undefined && !bound.isError ? (
        <Skeleton style={{ height: 52, borderRadius: 16 }} />
      ) : bound.data === undefined ? (
        <ErrorState text={errorText(bound.error)} onRetry={() => void bound.refetch()} />
      ) : list.length === 0 ? (
        <p className="text-[13px] text-[var(--ink-500)]">{t("userDrawer.boundEmpty")}</p>
      ) : (
        <ul className="flex flex-col gap-2">
          {list.map((d) => {
            const meta = deviceDetails(d, !!d.hwid);
            return (
              <li key={d.id} className="panel-soft grid grid-cols-[36px_minmax(0,1fr)_auto] items-center gap-3 p-2">
                <span className="grid h-9 w-9 place-items-center rounded-[10px] bg-[var(--hover)] text-[var(--ink-600)]" aria-hidden>
                  {!d.hwid ? <Layers size={18} /> : desktopOS.test(d.os) ? <Laptop size={18} /> : <Smartphone size={18} />}
                </span>
                <div className="min-w-0">
                  <div className="truncate text-[13px] font-medium">{deviceName(d)}</div>
                  <div className="truncate text-xs text-[var(--ink-500)]">
                    {meta ? `${meta} · ` : ""}
                    {d.online ? <span className="text-[var(--leaf-700)]">{t("users.onlineNow")}</span> : ago(d.last_seen)}
                  </div>
                </div>
                <button type="button" className="icon-btn" aria-label={t("userDrawer.unbindLabel", { name: deviceName(d) })} title={t("userDrawer.unbind")} onClick={() => setPick(d)}>
                  <Unlink size={16} />
                </button>
              </li>
            );
          })}
        </ul>
      )}
      <p className="mt-2 text-xs text-[var(--ink-500)]">{settings.data?.device_require_hwid ? t("userDrawer.boundNoteStrict") : t("userDrawer.boundNote")}</p>
      <Confirm
        open={pick !== null}
        onOpenChange={(v) => !v && setPick(null)}
        title={t("userDrawer.unbindTitle", { name: pick ? deviceName(pick) : "" })}
        text={pick && !pick.hwid ? t("userDrawer.unbindSharedText") : t("userDrawer.unbindText")}
        confirm={t("userDrawer.unbind")}
        danger
        loading={unbind.isPending}
        onConfirm={() =>
          pick &&
          unbind.mutate(
            { id: u.id, device: pick.id },
            {
              onSuccess: () => {
                toast.ok(t("userDrawer.unbound", { name: deviceName(pick) }));
                setPick(null);
              },
              onError: (e) => toast.error(errorText(e)),
            },
          )
        }
      />
    </>
  );
}
