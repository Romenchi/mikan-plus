import { TriangleAlert } from "lucide-react";
import { Component, type ReactNode } from "react";
import { t } from "../i18n";
import { Button } from "./ui";

/**
 * What the page shows when it crashed: a way back, instead of a white screen. `text`
 * replaces the generic explanation (a failed request says what failed); `onRetry` redraws
 * in place, a reload starts the page over.
 */
export function CrashScreen({ text, onRetry }: { text?: string; onRetry?: () => void }) {
  return (
    <div className="grid min-h-screen place-items-center px-4 py-12">
      <section className="card glass w-full max-w-[520px]">
        <div className="state-box" role="alert">
          <div className="state-mark err">
            <TriangleAlert size={22} aria-hidden />
          </div>
          <h2>{t("common.crashTitle")}</h2>
          <p>{text ?? t("common.crashText")}</p>
          <div className="mt-2 flex flex-wrap justify-center gap-2">
            {onRetry ? (
              <Button variant="primary" onClick={onRetry}>
                {t("common.retry")}
              </Button>
            ) : null}
            <Button variant={onRetry ? "glass" : "primary"} onClick={() => location.reload()}>
              {t("common.reload")}
            </Button>
          </div>
        </div>
      </section>
    </div>
  );
}

/** Catches a render error anywhere below it: one broken card must not blank the whole app. */
export class ErrorBoundary extends Component<{ children: ReactNode }, { failed: boolean }> {
  state = { failed: false };

  static getDerivedStateFromError() {
    return { failed: true };
  }

  render() {
    return this.state.failed ? <CrashScreen /> : this.props.children;
  }
}
