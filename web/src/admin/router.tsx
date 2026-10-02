import type { QueryClient } from "@tanstack/react-query";
import { createRootRouteWithContext, createRoute, createRouter, lazyRouteComponent, Outlet, redirect, type AsyncRouteComponent } from "@tanstack/react-router";
import { ApiError, basePath, setCsrf } from "../api/client";
import { meQuery } from "../api/hooks";
import { useLocale } from "../i18n";
import { NotFoundPage, PageLoading, RouteError } from "./route-states";
import { SETTINGS_TABS, TARIFF_TABS, TELEGRAM_TABS, USER_STATES, type SettingsSearch, type TariffsSearch, type TelegramSearch, type UsersSearch } from "./search";
import { Shell } from "./shell";

/**
 * A page loaded on demand: the first screen (login, the shell) does not carry the code of
 * every page. The page subscribes to the language itself, so a switch redraws it with its
 * form state and open drawers intact (texts are read at render time).
 */
function page<M extends Record<string, unknown>, K extends keyof M & string>(load: () => Promise<M>, name: K) {
  const Lazy = lazyRouteComponent(load, name) as AsyncRouteComponent<object>;
  const Page = () => {
    useLocale();
    return <Lazy />;
  };
  // The router warms the chunk up when a link is hovered (defaultPreload: "intent").
  return Object.assign(Page, { preload: Lazy.preload });
}

export function createAppRouter(queryClient: QueryClient) {
  const root = createRootRouteWithContext<{ queryClient: QueryClient }>()({ component: () => <Outlet /> });

  const login = createRoute({
    getParentRoute: () => root,
    path: "/login",
    component: page(() => import("./pages/login"), "LoginPage"),
    validateSearch: (s: Record<string, unknown>): { next?: string } => ({ next: typeof s.next === "string" ? s.next : undefined }),
  });

  const app = createRoute({
    getParentRoute: () => root,
    id: "_app",
    component: Shell,
    beforeLoad: async ({ context, location }) => {
      try {
        const me = await context.queryClient.ensureQueryData(meQuery);
        setCsrf(me.csrf_token);
      } catch (e) {
        if (e instanceof ApiError && e.status === 401) {
          throw redirect({ to: "/login", search: { next: location.href } });
        }
        throw e;
      }
    },
  });

  const dashboard = createRoute({ getParentRoute: () => app, path: "/", component: page(() => import("./pages/dashboard"), "Dashboard") });
  const users = createRoute({
    getParentRoute: () => app,
    path: "/users",
    component: page(() => import("./pages/users"), "UsersPage"),
    validateSearch: (s: Record<string, unknown>): UsersSearch => ({
      state: USER_STATES.includes(s.state as (typeof USER_STATES)[number]) ? (s.state as UsersSearch["state"]) : "all",
      q: typeof s.q === "string" ? s.q : "",
      user: typeof s.user === "number" ? s.user : Number(s.user) || undefined,
      create: s.create === true || s.create === "true" ? true : undefined,
    }),
  });
  const tariffs = createRoute({
    getParentRoute: () => app,
    path: "/tariffs",
    component: page(() => import("./pages/tariffs"), "TariffsPage"),
    validateSearch: (s: Record<string, unknown>): TariffsSearch => ({
      tab: TARIFF_TABS.includes(s.tab as TariffsSearch["tab"]) ? (s.tab as TariffsSearch["tab"]) : "tariffs",
    }),
  });
  const inbounds = createRoute({ getParentRoute: () => app, path: "/inbounds", component: page(() => import("./pages/inbounds"), "InboundsPage") });
  const nodes = createRoute({ getParentRoute: () => app, path: "/nodes", component: page(() => import("./pages/nodes"), "NodesPage") });
  const settings = createRoute({
    getParentRoute: () => app,
    path: "/settings",
    component: page(() => import("./pages/settings"), "SettingsPage"),
    validateSearch: (s: Record<string, unknown>): SettingsSearch => ({
      tab: SETTINGS_TABS.includes(s.tab as SettingsSearch["tab"]) ? (s.tab as SettingsSearch["tab"]) : "general",
    }),
  });
  const telegram = createRoute({
    getParentRoute: () => app,
    path: "/telegram",
    component: page(() => import("./pages/telegram"), "TelegramPage"),
    validateSearch: (s: Record<string, unknown>): TelegramSearch => ({
      tab: TELEGRAM_TABS.includes(s.tab as TelegramSearch["tab"]) ? (s.tab as TelegramSearch["tab"]) : "connect",
    }),
  });
  const payments = createRoute({ getParentRoute: () => app, path: "/payments", component: page(() => import("./pages/payments"), "PaymentsPage") });
  const apiDocs = createRoute({ getParentRoute: () => app, path: "/settings/api", component: page(() => import("./pages/api"), "ApiPage") });
  // The API section lived in the sidebar until 0.4.2: old links land on its new place.
  const apiDocsOld = createRoute({ getParentRoute: () => app, path: "/api-docs", beforeLoad: () => { throw redirect({ to: "/settings/api" }); } });

  const routeTree = root.addChildren([login, app.addChildren([dashboard, users, tariffs, inbounds, nodes, payments, telegram, apiDocs, apiDocsOld, settings])]);
  return createRouter({
    routeTree,
    basepath: basePath || "/",
    context: { queryClient },
    defaultPreload: "intent",
    scrollRestoration: true,
    defaultErrorComponent: RouteError,
    defaultNotFoundComponent: NotFoundPage,
    // A page's code is usually cached after the first visit: the placeholder shows only
    // when loading really takes a moment, and then stays long enough not to flash.
    defaultPendingComponent: PageLoading,
    defaultPendingMs: 200,
    defaultPendingMinMs: 300,
  });
}

declare module "@tanstack/react-router" {
  interface Register {
    router: ReturnType<typeof createAppRouter>;
  }
}
