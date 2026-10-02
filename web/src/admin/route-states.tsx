import { Link, useRouter, type ErrorComponentProps } from "@tanstack/react-router";
import { Compass } from "lucide-react";
import { ApiError, errorText } from "../api/client";
import { CrashScreen } from "../components/error-boundary";
import { Skeleton } from "../components/ui";
import { t, useLocale } from "../i18n";

/** A route that threw (while rendering, or a request its `beforeLoad` made): the page says so. */
export function RouteError({ error, reset }: ErrorComponentProps) {
  useLocale();
  const router = useRouter();
  return (
    <CrashScreen
      // Only a request that failed has something to tell; any other error is ours.
      text={error instanceof ApiError ? errorText(error) : undefined}
      onRetry={() => {
        reset();
        void router.invalidate();
      }}
    />
  );
}

/** An address that is no page of the panel. */
export function NotFoundPage() {
  useLocale();
  return (
    <div className="grid min-h-[60vh] place-items-center px-4 py-12">
      <section className="card glass w-full max-w-[520px]">
        <div className="state-box">
          <div className="state-mark">
            <Compass size={22} aria-hidden />
          </div>
          <h2>{t("errors.pageNotFoundTitle")}</h2>
          <p>{t("errors.pageNotFoundText")}</p>
          <Link to="/" className="btn btn-primary mt-2">
            {t("errors.toOverview")}
          </Link>
        </div>
      </section>
    </div>
  );
}

/** While a page's code loads: the shape of a page, not an empty window. */
export function PageLoading() {
  return (
    <div className="flex flex-col gap-4" role="status" aria-busy aria-label={t("common.loading")}>
      <Skeleton style={{ width: 200, height: 32, marginTop: 12 }} />
      <Skeleton style={{ height: 132, borderRadius: 20 }} />
      <Skeleton style={{ height: 280, borderRadius: 20 }} />
    </div>
  );
}
