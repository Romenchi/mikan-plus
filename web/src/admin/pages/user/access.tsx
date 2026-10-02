import { Copy, ExternalLink, RefreshCw, Send } from "lucide-react";
import { useState } from "react";
import { api, errorText, unwrap, type User } from "../../../api/client";
import { useInbounds, userActions, useUserMutation } from "../../../api/hooks";
import { Confirm } from "../../../components/overlay";
import { useToast } from "../../../components/toast";
import { Button, QR } from "../../../components/ui";
import { Switch } from "../../../components/switch";
import { t } from "../../../i18n";
import { useCopy } from "../../../lib/copy";
import { useDraft } from "../../../lib/draft";
import { safeHref } from "../../../lib/url";
import { Section } from "./section";

export function SubscriptionSection({ u, onReissue }: { u: User; onReissue: () => void }) {
  const copyText = useCopy();
  const copy = () => copyText(u.sub_url, t("common.linkCopied"));
  if (!u.sub_url) {
    return (
      <Section title={t("userDrawer.subscription")}>
        <p className="text-[13px] text-[var(--ink-500)]">{t("userDrawer.noHost")}</p>
      </Section>
    );
  }
  return (
    <Section title={t("userDrawer.subscription")}>
      <div className="link-field">
        <span className="mono" title={u.sub_url}>
          {u.sub_url}
        </span>
        <button type="button" className="icon-btn" onClick={copy} aria-label={t("common.copyLink")}>
          <Copy size={18} />
        </button>
      </div>
      <div className="mt-4 grid grid-cols-1 items-start gap-4 sm:grid-cols-[136px_minmax(0,1fr)]">
        <QR value={u.sub_url} />
        <div className="flex flex-col items-start gap-2">
          <p className="mb-1 text-[13px] text-[var(--ink-500)]">{t("userDrawer.sendHint")}</p>
          <a className="btn btn-glass btn-sm" href={safeHref(u.sub_url)} target="_blank" rel="noreferrer noopener">
            {t("userDrawer.subPage")} <ExternalLink size={14} aria-hidden />
          </a>
          <Button size="sm" variant="danger" onClick={onReissue}>
            <RefreshCw size={16} aria-hidden /> {t("userDrawer.reissue")}
          </Button>
        </div>
      </div>
    </Section>
  );
}

export function TelegramSection({ u }: { u: User }) {
  const toast = useToast();
  const unlink = useUserMutation((id: number) => unwrap(api.DELETE("/api/v1/users/{id}/telegram", { params: { path: { id } } })));
  const [confirm, setConfirm] = useState(false);
  const tg = u.telegram;
  return (
    <Section title={t("userDrawer.telegram")}>
      {tg ? (
        <div className="panel-soft flex items-center gap-3 p-3">
          <span className="grid h-9 w-9 place-items-center rounded-[10px] bg-[var(--hover)] text-[var(--ink-600)]" aria-hidden>
            <Send size={18} />
          </span>
          <div className="min-w-0 flex-1">
            <div className="truncate text-[13px] font-medium">{tg.username ? `@${tg.username}` : tg.name || tg.id}</div>
            {tg.username && tg.name ? <div className="truncate text-xs text-[var(--ink-500)]">{tg.name}</div> : null}
          </div>
          <Button size="sm" variant="danger" onClick={() => setConfirm(true)}>
            {t("userDrawer.telegramUnlink")}
          </Button>
        </div>
      ) : (
        <p className="text-[13px] text-[var(--ink-500)]">{t("userDrawer.telegramNone")}</p>
      )}
      <Confirm
        open={confirm}
        onOpenChange={setConfirm}
        title={t("userDrawer.telegramUnlinkTitle")}
        text={t("userDrawer.telegramUnlinkText")}
        confirm={t("userDrawer.telegramUnlink")}
        danger
        loading={unlink.isPending}
        onConfirm={() =>
          unlink.mutate(u.id, {
            onSuccess: () => {
              setConfirm(false);
              toast.ok(t("userDrawer.telegramUnlinked"));
            },
            onError: (e) => toast.error(errorText(e)),
          })
        }
      />
    </Section>
  );
}

export function ProtocolsSection({ u }: { u: User }) {
  const inbounds = useInbounds();
  const update = useUserMutation(userActions.update);
  const toast = useToast();
  const all = (inbounds.data ?? []).filter((i) => i.enabled);
  const allowed = new Set(u.inbounds.length ? u.inbounds : all.map((i) => i.id));
  const toggle = (id: number, on: boolean) => {
    const next = new Set(allowed);
    if (on) next.add(id);
    else next.delete(id);
    if (next.size === 0) {
      toast.error(t("userDrawer.needOneProtocol"));
      return;
    }
    const ids = next.size === all.length ? [] : [...next];
    update.mutate({ id: u.id, body: { inbounds: ids } }, { onError: (e) => toast.error(errorText(e)) });
  };
  return (
    <Section title={t("userDrawer.protocols")}>
      {all.map((i) => (
        <div key={i.id} className="flex items-center justify-between gap-3 py-2">
          <div>
            <div className="text-[13px] font-medium">{i.sub_name}</div>
            <div className="text-xs text-[var(--ink-500)]">
              {i.title} · {i.port}/{i.network}
            </div>
          </div>
          <Switch checked={allowed.has(i.id)} onChange={(v) => toggle(i.id, v)} label={i.sub_name} disabled={update.isPending} />
        </div>
      ))}
    </Section>
  );
}

export function NoteSection({ u }: { u: User }) {
  // The drawer polls the user every few seconds: a note being typed is not replaced.
  const { draft: note, setDraft: setNote, dirty } = useDraft(u.note);
  const update = useUserMutation(userActions.update);
  const toast = useToast();
  return (
    <Section title={t("userDrawer.note")} aside={update.isPending ? t("userDrawer.saving") : undefined}>
      <textarea
        className="input"
        aria-label={t("userDrawer.noteLabel")}
        placeholder={t("userDrawer.notePlaceholder")}
        maxLength={2000}
        value={note}
        onChange={(e) => setNote(e.target.value)}
        onBlur={() => {
          if (dirty) update.mutate({ id: u.id, body: { note } }, { onSuccess: () => toast.ok(t("userDrawer.noteSaved")), onError: (e) => toast.error(errorText(e)) });
        }}
      />
    </Section>
  );
}
