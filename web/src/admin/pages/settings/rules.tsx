import { useRef, type FormEvent } from "react";
import { ApiError, type Schemas } from "../../../api/client";
import { Button, Pill } from "../../../components/ui";
import { t } from "../../../i18n";
import { useDraft } from "../../../lib/draft";
import { useSaveSettings } from "./shared";

// Ready-made rules the admin adds with one click; PROXY is the main group's alias that
// survives renaming it.
const RULE_EXAMPLES = [
  { key: "siteDirect", rule: "DOMAIN-SUFFIX,example.com,DIRECT" },
  { key: "siteVpn", rule: "DOMAIN-SUFFIX,example.com,PROXY" },
  { key: "ads", rule: "GEOSITE,category-ads-all,REJECT" },
  { key: "app", rule: "PROCESS-NAME,Telegram.exe,PROXY" },
  { key: "lan", rule: "IP-CIDR,192.168.0.0/16,DIRECT,no-resolve" },
] as const;

// The admin's own rules for Clash apps (subs.ParseRules checks them line by line).
export function ClashRulesCard({ s }: { s: Schemas["SettingsView"] }) {
  const save = useSaveSettings();
  const { draft: text, setDraft: setText, dirty: changed, reset } = useDraft(s.sub_rules);
  const gutter = useRef<HTMLDivElement>(null);
  const area = useRef<HTMLTextAreaElement>(null);
  const lines = text.split("\n");
  const count = lines.filter((l) => l.trim() && !l.trim().startsWith("#")).length;
  const apiErr = save.error instanceof ApiError ? save.error : null;
  const error = apiErr?.fields.sub_rules;
  const badLine = error && typeof apiErr?.values.sub_rules === "number" ? apiErr.values.sub_rules : 0;
  const add = (rule: string) => {
    setText((v) => (v.trim() ? v.replace(/\s*$/, "\n") : "") + rule);
    requestAnimationFrame(() => {
      const el = area.current;
      if (!el) return;
      el.focus();
      el.selectionStart = el.selectionEnd = el.value.length;
      el.scrollTop = el.scrollHeight;
    });
  };
  const submit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate({ sub_rules: text });
  };
  return (
    <section className="card glass reveal" style={{ "--i": 2 } as React.CSSProperties}>
      <form onSubmit={submit} noValidate>
        <div className="card-head">
          <div>
            <h2 className="card-title">{t("settings.rules")}</h2>
            <div className="card-sub">{t("settings.rulesSub")}</div>
          </div>
          {count ? <Pill tone="off">{t("settings.rulesCount", { n: count })}</Pill> : null}
        </div>
        <div className="rules-editor" aria-invalid={!!error}>
          <div className="rules-gutter" ref={gutter} aria-hidden>
            {lines.map((_, i) => (
              <div key={i} className={i + 1 === badLine ? "bad" : undefined}>
                {i + 1}
              </div>
            ))}
          </div>
          <textarea
            ref={area}
            id="s-rules"
            value={text}
            onChange={(e) => setText(e.target.value)}
            onScroll={(e) => {
              if (gutter.current) gutter.current.scrollTop = e.currentTarget.scrollTop;
            }}
            spellCheck={false}
            autoCapitalize="off"
            autoComplete="off"
            wrap="off"
            maxLength={65536}
            placeholder={t("settings.rulesPlaceholder")}
            aria-label={t("settings.rules")}
            aria-invalid={!!error}
            aria-describedby="s-rules-hint"
          />
        </div>
        {error ? (
          <p className="mt-2 text-xs text-[var(--berry-600)]" role="alert">
            {error}
          </p>
        ) : null}
        <div className="mt-3 flex flex-wrap gap-2" role="group" aria-label={t("settings.rulesExamples")}>
          {RULE_EXAMPLES.map((x) => (
            <button key={x.key} type="button" className="chip-btn" onClick={() => add(x.rule)} title={x.rule}>
              + {t(`settings.rulesEx.${x.key}`)}
            </button>
          ))}
        </div>
        <div id="s-rules-hint" className="mt-3 text-xs text-[var(--ink-500)]">
          <p>{t("settings.rulesHint")}</p>
          <p className="mt-1">
            {t("settings.rulesTargets")}{" "}
            {s.rule_targets.map((x, i) => (
              <span key={x}>
                {i ? " · " : ""}
                <code className="mono">{x}</code>
              </span>
            ))}
          </p>
          <details className="mt-2">
            <summary className="cursor-pointer font-medium text-[var(--ink-700)]">{t("settings.rulesTypes")}</summary>
            <p className="mt-2">{t("settings.rulesTypesText")}</p>
          </details>
        </div>
        <div className="mt-4 flex flex-wrap gap-2">
          <Button type="submit" variant="primary" loading={save.isPending && save.variables?.sub_rules !== undefined} disabled={!changed}>
            {t("common.save")}
          </Button>
          {changed ? (
            <Button variant="ghost" onClick={reset}>
              {t("telegram.discard")}
            </Button>
          ) : null}
        </div>
      </form>
    </section>
  );
}
