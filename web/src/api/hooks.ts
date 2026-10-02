import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, unwrap, type Schemas, type User } from "./client";

export const qk = {
  me: ["me"] as const,
  users: ["users"] as const,
  user: (id: number) => ["users", "one", id] as const,
  userTraffic: (id: number) => ["users", "traffic", id] as const,
  devices: (id: number) => ["users", "devices", id] as const,
  boundDevices: (id: number) => ["users", "bound", id] as const,
  tariffs: ["tariffs"] as const,
  inbounds: ["inbounds"] as const,
  presets: ["presets"] as const,
  overview: ["overview"] as const,
  traffic: (range: string) => ["traffic", range] as const,
  node: ["node"] as const,
  nodes: ["nodes"] as const,
  settings: ["settings"] as const,
  sessions: ["sessions"] as const,
  telegram: ["telegram"] as const,
  updates: ["updates"] as const,
  apiKeys: ["api-keys"] as const,
  payments: ["payments"] as const,
  paymentSettings: ["payment-settings"] as const,
  addons: ["addons"] as const,
  warp: (node: number) => ["warp", node] as const,
  cascade: (node: number) => ["cascade", node] as const,
  pools: ["pools"] as const,
  userPools: (id: number) => ["users", "pools", id] as const,
  packages: ["packages"] as const,
  userGrants: (id: number) => ["users", "grants", id] as const,
};

export const meQuery = {
  queryKey: qk.me,
  queryFn: ({ signal }: { signal: AbortSignal }) => unwrap(api.GET("/api/v1/auth/me", { signal })),
  staleTime: 60_000,
  retry: false,
};

type UsersFilter = { state: "all" | User["state"]; q: string };

/** The users page lists everyone it can (the API's cap); a card that shows a few asks for just those. */
const USERS_MAX = 500;

export function useUsers(f: UsersFilter, o: { limit?: number; refetchInterval?: number } = {}) {
  const limit = o.limit ?? USERS_MAX;
  return useQuery({
    queryKey: [...qk.users, "list", f, limit],
    queryFn: ({ signal }) => unwrap(api.GET("/api/v1/users", { params: { query: { state: f.state, q: f.q || undefined, limit } }, signal })),
    placeholderData: keepPreviousData,
    refetchInterval: o.refetchInterval ?? 10_000,
  });
}

export function useUser(id: number | undefined) {
  return useQuery({
    queryKey: qk.user(id ?? 0),
    queryFn: ({ signal }) => unwrap(api.GET("/api/v1/users/{id}", { params: { path: { id: id! } }, signal })),
    enabled: !!id,
    refetchInterval: 5_000,
  });
}

export function useUserTraffic(id: number) {
  return useQuery({
    queryKey: qk.userTraffic(id),
    queryFn: ({ signal }) => unwrap(api.GET("/api/v1/users/{id}/traffic", { params: { path: { id }, query: { range: "30d" } }, signal })),
  });
}

export function useDevices(id: number) {
  return useQuery({
    queryKey: qk.devices(id),
    queryFn: ({ signal }) => unwrap(api.GET("/api/v1/users/{id}/devices", { params: { path: { id } }, signal })),
    refetchInterval: 10_000,
  });
}

/** Devices bound to the subscription (each with keys of its own). */
export function useBoundDevices(id: number) {
  return useQuery({
    queryKey: qk.boundDevices(id),
    queryFn: ({ signal }) => unwrap(api.GET("/api/v1/users/{id}/bound-devices", { params: { path: { id } }, signal })),
    refetchInterval: 10_000,
  });
}

export function useTariffs() {
  return useQuery({ queryKey: qk.tariffs, queryFn: ({ signal }) => unwrap(api.GET("/api/v1/tariffs", { signal })) });
}

export function useInbounds() {
  return useQuery({ queryKey: qk.inbounds, queryFn: ({ signal }) => unwrap(api.GET("/api/v1/inbounds", { signal })), refetchInterval: 10_000 });
}

export function usePresets() {
  return useQuery({ queryKey: qk.presets, queryFn: ({ signal }) => unwrap(api.GET("/api/v1/presets", { signal })), staleTime: Infinity });
}

export function useOverview() {
  return useQuery({ queryKey: qk.overview, queryFn: ({ signal }) => unwrap(api.GET("/api/v1/stats/overview", { signal })), refetchInterval: 10_000 });
}

export function useServerTraffic(range: "24h" | "7d" | "30d") {
  return useQuery({
    queryKey: qk.traffic(range),
    queryFn: ({ signal }) => unwrap(api.GET("/api/v1/stats/traffic", { params: { query: { range } }, signal })),
    placeholderData: keepPreviousData,
    refetchInterval: 60_000,
  });
}

export function useNode() {
  return useQuery({ queryKey: qk.node, queryFn: ({ signal }) => unwrap(api.GET("/api/v1/node", { signal })), refetchInterval: 5_000 });
}

export function useNodes() {
  return useQuery({ queryKey: qk.nodes, queryFn: ({ signal }) => unwrap(api.GET("/api/v1/nodes", { signal })), refetchInterval: 10_000 });
}

/** Payment settings: also whether selling is on, which shows Payments in the menu. */
export function usePaymentSettings() {
  return useQuery({ queryKey: qk.paymentSettings, queryFn: ({ signal }) => unwrap(api.GET("/api/v1/payments/settings", { signal })) });
}

export function useSettings() {
  return useQuery({ queryKey: qk.settings, queryFn: ({ signal }) => unwrap(api.GET("/api/v1/settings", { signal })) });
}

/** Followed every few seconds while the server updates, hourly otherwise. */
export function useUpdates() {
  return useQuery({
    queryKey: qk.updates,
    queryFn: ({ signal }) => unwrap(api.GET("/api/v1/updates", { signal })),
    refetchInterval: (q) => (q.state.data?.requested_at || q.state.data?.host?.state === "running" ? 5_000 : 3_600_000),
    retry: (n) => n < 30,
    retryDelay: 3_000,
  });
}

/** Mutations that change a user refresh every user-related view. */
export function useUserMutation<TArgs, TResult>(fn: (args: TArgs) => Promise<TResult>) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: fn,
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: qk.users });
      void qc.invalidateQueries({ queryKey: qk.overview });
    },
  });
}

export const userActions = {
  create: (body: Schemas["CreateUserInputBody"]) => unwrap(api.POST("/api/v1/users", { body })),
  update: ({ id, body }: { id: number; body: Schemas["PatchUserInputBody"] }) =>
    unwrap(api.PATCH("/api/v1/users/{id}", { params: { path: { id } }, body })),
  extend: ({ id, ...body }: { id: number } & Schemas["ExtendInputBody"]) => unwrap(api.POST("/api/v1/users/{id}/extend", { params: { path: { id } }, body })),
  reset: (id: number) => unwrap(api.POST("/api/v1/users/{id}/reset-traffic", { params: { path: { id } } })),
  reissue: (id: number) => unwrap(api.POST("/api/v1/users/{id}/reissue", { params: { path: { id } } })),
  remove: (id: number) => unwrap(api.DELETE("/api/v1/users/{id}", { params: { path: { id } } })),
  bulk: (body: Schemas["BulkInputBody"]) => unwrap(api.POST("/api/v1/users/bulk", { body })),
  unbindDevice: ({ id, device }: { id: number; device: number }) =>
    unwrap(api.DELETE("/api/v1/users/{id}/bound-devices/{device}", { params: { path: { id, device } } })),
};

/** One paid period: a month up to the billing day, or 30 days without one. */
export const onePeriod = (u: Pick<User, "billing_day">): Schemas["ExtendInputBody"] => (u.billing_day != null ? { months: 1 } : { days: 30 });

export function usePools() {
  return useQuery({ queryKey: qk.pools, queryFn: ({ signal }) => unwrap(api.GET("/api/v1/pools", { signal })) });
}

export function usePackages() {
  return useQuery({ queryKey: qk.packages, queryFn: ({ signal }) => unwrap(api.GET("/api/v1/packages", { signal })) });
}

export function useUserGrants(id: number) {
  return useQuery({ queryKey: qk.userGrants(id), queryFn: ({ signal }) => unwrap(api.GET("/api/v1/users/{id}/grants", { params: { path: { id } }, signal })) });
}
