import { useNavigate, useSearch } from "@tanstack/react-router";
import { Globe, Link2, ListFilter, ShieldCheck } from "lucide-react";
import { useSettings } from "../../../api/hooks";
import { LangSwitch } from "../../../components/lang";
import { QueryBoundary } from "../../../components/query";
import { Columns, Tabs } from "../../../components/tabs";
import { PageHeader, Skeleton } from "../../../components/ui";
import { t } from "../../../i18n";
import { SETTINGS_TABS } from "../../search";
import { AutoCard, LanguageCard, SalesCard, ServerCard, UpdatesCard } from "./general";
import { ClashRulesCard } from "./rules";
import { AccessCard, ApiCard, CertificateCard, PasswordCard, SessionsCard, TwoFactorCard } from "./security";
import { DevicesCard, SubPortCard, SubscriptionCard } from "./subscription";

const ICONS = { general: Globe, subscription: Link2, rules: ListFilter, security: ShieldCheck } as const;

/** Settings in four sections, one at a time; the section is in the URL, so a link opens it. */
export function SettingsPage() {
  const settings = useSettings();
  const { tab } = useSearch({ from: "/_app/settings" });
  const navigate = useNavigate({ from: "/settings" });
  return (
    <>
      <PageHeader title={t("nav.settings")} sub={t("settings.subtitle")} actions={<LangSwitch />} />
      <Tabs
        id="settings"
        label={t("settings.sections")}
        tabs={SETTINGS_TABS.map((id) => ({ id, label: t(`settings.tabs.${id}`), icon: ICONS[id] }))}
        value={tab}
        onChange={(next) => void navigate({ search: { tab: next }, replace: true })}
      >
        <QueryBoundary query={settings} pending={<Skeleton style={{ height: 320, borderRadius: 20 }} />} wrap={(state) => <section className="card glass">{state}</section>}>
          {(s) =>
            tab === "general" ? (
              <Columns
                left={
                  <>
                    <ServerCard s={s} />
                    <LanguageCard s={s} />
                    <AutoCard s={s} />
                  </>
                }
                right={
                  <>
                    <UpdatesCard />
                    <SalesCard />
                  </>
                }
              />
            ) : tab === "subscription" ? (
              <Columns
                left={
                  <>
                    <SubscriptionCard s={s} />
                    <DevicesCard s={s} />
                  </>
                }
                right={<SubPortCard s={s} />}
              />
            ) : tab === "rules" ? (
              <div className="max-w-4xl">
                <ClashRulesCard s={s} />
              </div>
            ) : (
              <Columns
                left={
                  <>
                    <AccessCard s={s} />
                    <CertificateCard s={s} />
                    <ApiCard />
                  </>
                }
                right={
                  <>
                    <PasswordCard />
                    <TwoFactorCard />
                    <SessionsCard />
                  </>
                }
              />
            )
          }
        </QueryBoundary>
      </Tabs>
    </>
  );
}
