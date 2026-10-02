// What each page keeps in its URL. Kept apart from the pages: the router checks the search
// of a route before its (lazy) page is loaded, so these must not pull the pages into the
// first chunk.
import type { User } from "../api/client";

export const USER_STATES = ["all", "active", "expiring", "limited", "expired", "disabled"] as const;
export type UsersSearch = { state: "all" | User["state"]; q: string; user?: number; create?: true };

export const SETTINGS_TABS = ["general", "subscription", "rules", "security"] as const;
export type SettingsSearch = { tab: (typeof SETTINGS_TABS)[number] };

export const TARIFF_TABS = ["tariffs", "pools", "packages"] as const;
export type TariffsSearch = { tab: (typeof TARIFF_TABS)[number] };

export const TELEGRAM_TABS = ["connect", "menu", "notify", "broadcast"] as const;
export type TelegramSearch = { tab: (typeof TELEGRAM_TABS)[number] };
