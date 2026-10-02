/**
 * A link that came from the API (a marketplace add-on's homepage, a support address, a
 * subscription URL) as an `href`, or undefined when it is not one to follow. Only http(s)
 * links pass, and `tg:` ones where the caller says the server allows them (the support
 * link may be a Telegram deep link). React already refuses `javascript:`; this keeps
 * `data:`, `file:` and the rest out as well, and does not lean on that.
 */
export function safeHref(url: string | null | undefined, opts: { tg?: boolean } = {}): string | undefined {
  if (!url) return undefined;
  try {
    const u = new URL(url);
    if (u.protocol === "https:" || u.protocol === "http:" || (opts.tg && u.protocol === "tg:")) return u.href;
  } catch {
    // not a URL
  }
  return undefined;
}

/**
 * Where to go after signing in: an absolute path of this panel (`/users?q=a`) and nothing
 * else. `//host` and `/\host` are read by browsers as another site, so the result is
 * checked against the page's own origin rather than by its first character.
 */
export function localPath(next: string | undefined, fallback = "/"): string {
  if (!next || !next.startsWith("/") || next.startsWith("//") || next.includes("\\")) return fallback;
  try {
    const u = new URL(next, location.origin);
    if (u.origin !== location.origin) return fallback;
  } catch {
    return fallback;
  }
  return next.startsWith("/login") ? fallback : next;
}
