import "../styles/app.css";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "@tanstack/react-router";
import * as Tooltip from "@radix-ui/react-tooltip";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { ApiError } from "../api/client";
import { Atmosphere } from "../components/atmosphere";
import { ToastProvider } from "../components/toast";
import { useLocale } from "../i18n";
import { initTheme } from "../lib/theme";
import { createAppRouter } from "./router";

initTheme();

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 5_000,
      retry: (count, e) => !(e instanceof ApiError && [401, 403, 404].includes(e.status)) && count < 2,
      refetchOnWindowFocus: true,
    },
  },
});

const router = createAppRouter(queryClient);

window.addEventListener("mikan:unauthorized", () => {
  if (router.state.location.pathname === "/login") return;
  queryClient.clear();
  void router.navigate({ to: "/login", search: { next: router.state.location.href } });
});

// Texts are read at render time; a language switch remounts the tree (the query cache and
// the router state survive, they live outside it).
function Root() {
  const locale = useLocale();
  return (
    <ToastProvider key={locale}>
      <Atmosphere />
      <RouterProvider router={router} />
    </ToastProvider>
  );
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <Tooltip.Provider delayDuration={300}>
        <Root />
      </Tooltip.Provider>
    </QueryClientProvider>
  </StrictMode>,
);
