import type { UseQueryResult } from "@tanstack/react-query";
import { TriangleAlert } from "lucide-react";
import type { ReactNode } from "react";
import { errorText } from "../api/client";
import { t } from "../i18n";
import { Button, ErrorState } from "./ui";

/**
 * Pending, failed and loaded states of a query in one place.
 *
 * After its retries run out TanStack Query marks a query as failed but keeps the data it
 * had, so a poll that hit a network blip must not replace the screen with an error: the
 * data stays and a small notice says the refresh failed. The error screen is only for a
 * query that never had data. `wrap` puts that screen into a card where the page wants one.
 */
export function QueryBoundary<T>({
  query,
  pending,
  title,
  wrap,
  children,
}: {
  query: UseQueryResult<T>;
  pending: ReactNode;
  title?: string;
  wrap?: (state: ReactNode) => ReactNode;
  children: (data: T) => ReactNode;
}) {
  if (query.data === undefined) {
    if (!query.isError) return <>{pending}</>;
    const state = <ErrorState title={title} text={errorText(query.error)} onRetry={() => void query.refetch()} />;
    return <>{wrap ? wrap(state) : state}</>;
  }
  return (
    <>
      {query.isError ? <StaleNotice onRetry={() => void query.refetch()} retrying={query.isFetching} /> : null}
      {children(query.data)}
    </>
  );
}

/** A refresh failed and the screen shows the last data it got. */
export function StaleNotice({ onRetry, retrying }: { onRetry: () => void; retrying?: boolean }) {
  return (
    <div className="banner warn mb-4" role="status">
      <TriangleAlert size={16} className="shrink-0" aria-hidden />
      <span className="min-w-0 flex-1">{t("common.staleData")}</span>
      <Button size="sm" variant="ghost" loading={retrying} onClick={onRetry}>
        {t("common.retry")}
      </Button>
    </div>
  );
}
