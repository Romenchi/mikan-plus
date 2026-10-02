import { appName } from "./format";

/** Operating systems that get a laptop icon; the rest get a phone. */
export const desktopOS = /windows|mac|linux|darwin/i;

type Described = { os: string; os_version: string; model: string; app: string };

/** What to call a device: its model, else its system, else the app, else `fallback`. */
export function deviceLabel(d: Described, fallback: string): string {
  return d.model || [d.os, d.os_version].filter(Boolean).join(" ") || appName(d.app) || fallback;
}

/** The line under a device's name: its system (when the name is the model) and the app. */
export function deviceDetails(d: Described, named: boolean): string {
  const system = named && d.model ? [d.os, d.os_version].filter(Boolean).join(" ") : "";
  return [system, appName(d.app)].filter(Boolean).join(" · ");
}
