// The API reference: rendered from the OpenAPI spec the server exports at build time
// (`mikan openapi`), so it always matches the panel it ships with.
import { Copy, Download, Play, Search } from "lucide-react";
import { useMemo, useState, type ReactNode } from "react";
import { basePath } from "../../api/client";
import { useUpdates } from "../../api/hooks";
import rawSpec from "../../api/openapi.json";
import { Button, EmptyState } from "../../components/ui";
import { t, tMaybe } from "../../i18n";
import { useCopy } from "../../lib/copy";

type Schema = {
  $ref?: string;
  type?: string | string[];
  format?: string;
  description?: string;
  enum?: unknown[];
  default?: unknown;
  minimum?: number;
  maximum?: number;
  minLength?: number;
  maxLength?: number;
  pattern?: string;
  items?: Schema;
  properties?: Record<string, Schema>;
  required?: string[];
  additionalProperties?: Schema | boolean;
  readOnly?: boolean;
};
type Param = { name: string; in: "path" | "query" | "header" | "cookie"; required?: boolean; description?: string; schema?: Schema };
type Media = { schema?: Schema };
type Operation = {
  operationId: string;
  summary?: string;
  description?: string;
  tags?: string[];
  parameters?: Param[];
  requestBody?: { content?: Record<string, Media>; required?: boolean };
  responses?: Record<string, { description?: string; content?: Record<string, Media> }>;
  security?: Record<string, string[]>[];
  "x-session-only"?: boolean;
};
type Spec = {
  info: { title: string; version: string; description?: string };
  paths: Record<string, Record<string, Operation>>;
  components: { schemas: Record<string, Schema> };
};

const spec = rawSpec as unknown as Spec;
const METHODS = ["get", "post", "put", "patch", "delete"] as const;
// Endpoints an API key cannot reach (internal/panel/api/apikeys.go, sessionOnlyTags).
const SESSION_ONLY = ["auth", "api-keys"];
// Sections in the order of the admin panel.
const TAG_ORDER = ["users", "tariffs", "payments", "inbounds", "node", "stats", "settings", "telegram", "api-keys", "auth"];

type Op = { method: (typeof METHODS)[number]; path: string; op: Operation; tag: string };

function allOps(): Op[] {
  const out: Op[] = [];
  for (const [path, item] of Object.entries(spec.paths)) {
    for (const method of METHODS) {
      const op = item[method];
      if (op) out.push({ method, path, op, tag: op.tags?.[0] ?? "other" });
    }
  }
  const rank = (tag: string) => (TAG_ORDER.indexOf(tag) + TAG_ORDER.length + 1) % (TAG_ORDER.length + 1);
  return out.sort((a, b) => rank(a.tag) - rank(b.tag) || a.path.localeCompare(b.path) || METHODS.indexOf(a.method) - METHODS.indexOf(b.method));
}

function resolve(s: Schema | undefined): [Schema | undefined, string] {
  if (!s?.$ref) return [s, ""];
  const name = s.$ref.replace("#/components/schemas/", "");
  return [spec.components.schemas[name], name];
}

const apiBase = () => `${window.location.origin}${basePath}`;

export default function ApiReference() {
  const ops = useMemo(allOps, []);
  const [q, setQ] = useState("");
  const query = q.trim().toLowerCase();
  const shown = query
    ? ops.filter((o) => `${o.method} ${o.path} ${o.op.summary ?? ""} ${o.op.operationId}`.toLowerCase().includes(query))
    : ops;
  const groups = new Map<string, Op[]>();
  for (const o of shown) groups.set(o.tag, [...(groups.get(o.tag) ?? []), o]);
  return (
    <>
      <Intro />
      <section className="card glass reveal" style={{ "--i": 2 } as React.CSSProperties}>
        <div className="card-head">
          <div>
            <h2 className="card-title">{t("apiPage.reference")}</h2>
            <div className="card-sub">{t("apiPage.referenceSub", { n: ops.length })}</div>
          </div>
        </div>
        <label className="search-field mb-4 flex max-w-[420px] items-center gap-2">
          <Search size={16} aria-hidden className="text-[var(--ink-400)]" />
          <input className="input" type="search" value={q} onChange={(e) => setQ(e.target.value)} placeholder={t("apiPage.search")} aria-label={t("apiPage.search")} />
        </label>
        {shown.length === 0 ? (
          <EmptyState title={t("apiPage.nothingFound")} text={t("apiPage.nothingFoundText")} search />
        ) : (
          [...groups].map(([tag, list]) => (
            <div key={tag} className="mb-6 last:mb-0">
              <h3 className="mb-2 text-[15px] font-semibold">{tMaybe(`apiPage.tags.${tag}`) ?? tag}</h3>
              <div className="flex flex-col gap-2">
                {list.map((o) => (
                  <OperationView key={o.op.operationId} o={o} />
                ))}
              </div>
            </div>
          ))
        )}
      </section>
    </>
  );
}

// download saves the spec as openapi.json with this panel as its server, so Postman and
// client generators work with it as is. The address has the secret admin path in it.
function download(version: string | undefined) {
  const doc = { ...rawSpec, info: { ...rawSpec.info, version: version ?? rawSpec.info.version }, servers: [{ url: apiBase() }] };
  const url = URL.createObjectURL(new Blob([JSON.stringify(doc, null, 2)], { type: "application/json" }));
  const a = document.createElement("a");
  a.href = url;
  a.download = "mikan-openapi.json";
  a.click();
  URL.revokeObjectURL(url);
}

function Intro() {
  const base = `${apiBase()}/api/v1`;
  const updates = useUpdates();
  return (
    <section className="card glass reveal" style={{ "--i": 1 } as React.CSSProperties}>
      <div className="card-head">
        <div>
          <h2 className="card-title">{t("apiPage.start")}</h2>
          <div className="card-sub">{t("apiPage.startSub")}</div>
        </div>
        <Button size="sm" onClick={() => download(updates.data?.current)}>
          <Download size={16} aria-hidden /> {t("apiPage.download")}
        </Button>
      </div>
      <div className="api-h">{t("apiPage.baseUrl")}</div>
      <CodeBlock code={base} label={t("apiPage.copyBase")} />
      <ul className="my-4 list-disc space-y-2 pl-5 text-[13px] text-[var(--ink-700)]">
        <li>{t("apiPage.authKey")}</li>
        <li>{t("apiPage.authScopes")}</li>
        <li>{t("apiPage.authSession")}</li>
        <li>{t("apiPage.errorsFormat")}</li>
        <li>{t("apiPage.secret")}</li>
      </ul>
      <div className="api-h">{t("apiPage.example")}</div>
      <CodeBlock code={`curl -s -H "Authorization: Bearer $MIKAN_KEY" \\\n  "${base}/users?state=active&limit=10"`} label={t("apiPage.copyExample")} />
      <p className="mt-3 text-xs text-[var(--ink-500)]">{t("apiPage.downloadHint")}</p>
    </section>
  );
}

function CodeBlock({ code, label }: { code: string; label: string }) {
  const copy = useCopy();
  return (
    <div className="code-block">
      {code}
      <button type="button" className="icon-btn" onClick={() => void copy(code, t("apiPage.copied"))} aria-label={label}>
        <Copy size={16} />
      </button>
    </div>
  );
}

function access(o: Op): { text: string; tone: string } {
  if (o.op.security && o.op.security.every((s) => Object.keys(s).length === 0)) return { text: t("apiPage.public"), tone: "off" };
  if (SESSION_ONLY.includes(o.tag) || o.op["x-session-only"]) return { text: t("apiPage.sessionOnly"), tone: "warn" };
  return o.method === "get" ? { text: t("apiPage.scopeRead"), tone: "ok" } : { text: t("apiPage.scopeFull"), tone: "off" };
}

function OperationView({ o }: { o: Op }) {
  const [open, setOpen] = useState(false);
  const a = access(o);
  return (
    <details className="api-op" onToggle={(e) => setOpen((e.target as HTMLDetailsElement).open)}>
      <summary>
        <span className={`method ${o.method}`}>{o.method.toUpperCase()}</span>
        <span className="min-w-0">
          <span className="mono block truncate text-[13px] text-[var(--ink-900)]">{o.path.replace(/^\/api\/v1/, "")}</span>
          {o.op.summary ? <span className="block truncate text-xs text-[var(--ink-500)]">{o.op.summary}</span> : null}
        </span>
        <span className={`pill ${a.tone} max-sm:hidden`}>{a.text}</span>
      </summary>
      {open ? <OperationBody o={o} /> : null}
    </details>
  );
}

function OperationBody({ o }: { o: Op }) {
  const params = o.op.parameters ?? [];
  const body = o.op.requestBody?.content?.["application/json"]?.schema;
  const ok = Object.entries(o.op.responses ?? {}).filter(([code]) => code.startsWith("2"));
  return (
    <div className="api-op-body">
      {o.op.description ? <p className="text-[13px] text-[var(--ink-700)]">{o.op.description}</p> : null}
      {params.length ? (
        <div>
          <div className="api-h">{t("apiPage.params")}</div>
          <div className="schema">
            {params.map((p) => (
              <FieldRow key={p.in + p.name} name={p.name} schema={p.schema} required={p.required} note={p.in} description={p.description} depth={0} />
            ))}
          </div>
        </div>
      ) : null}
      {body ? (
        <div>
          <div className="api-h">{t("apiPage.body")}</div>
          <SchemaView schema={body} depth={0} />
        </div>
      ) : null}
      <div>
        <div className="api-h">{t("apiPage.response")}</div>
        {ok.map(([code, r]) => {
          const s = r.content?.["application/json"]?.schema;
          return (
            <div key={code} className="mb-2">
              <span className="tag mb-2">
                {code} {r.description}
              </span>
              {s ? <SchemaView schema={s} depth={0} /> : <p className="text-xs text-[var(--ink-500)]">{t("apiPage.noContent")}</p>}
            </div>
          );
        })}
        <p className="text-xs text-[var(--ink-500)]">{t("apiPage.errorsShort")}</p>
      </div>
      <div>
        <div className="api-h">curl</div>
        <CodeBlock code={curl(o)} label={t("apiPage.copyExample")} />
      </div>
      {o.method === "get" ? <TryIt o={o} /> : null}
    </div>
  );
}

function curl(o: Op): string {
  const url = `${apiBase()}${o.path.replace(/\{(\w+)\}/g, (_, n: string) => `<${n}>`)}`;
  const lines = [`curl -s${o.method === "get" ? "" : ` -X ${o.method.toUpperCase()}`} "${url}"`, `  -H "Authorization: Bearer $MIKAN_KEY"`];
  const body = o.op.requestBody?.content?.["application/json"]?.schema;
  if (body) {
    lines.push(`  -H "Content-Type: application/json"`, `  -d '${JSON.stringify(example(body, 0))}'`);
  }
  return lines.join(" \\\n");
}

/** A minimal request body: the required fields with defaults or placeholders. */
function example(s: Schema | undefined, depth: number): unknown {
  const [r] = resolve(s);
  if (!r || depth > 4) return null;
  if (r.default !== undefined) return r.default;
  if (r.enum?.length) return r.enum[0];
  const type = Array.isArray(r.type) ? r.type.find((x) => x !== "null") : r.type;
  switch (type) {
    case "object": {
      const out: Record<string, unknown> = {};
      for (const name of r.required ?? []) {
        if (name !== "$schema") out[name] = example(r.properties?.[name], depth + 1);
      }
      return out;
    }
    case "array":
      return [];
    case "integer":
    case "number":
      return r.minimum ?? 0;
    case "boolean":
      return false;
    default:
      return "";
  }
}

function typeLabel(s: Schema | undefined): string {
  const [r, name] = resolve(s);
  if (!r) return "";
  const type = Array.isArray(r.type) ? r.type.filter((x) => x !== "null").join(" | ") : (r.type ?? "");
  if (type === "array") return `${typeLabel(r.items)}[]`;
  if (type === "object" && name) return name;
  return r.format ? `${type} (${r.format})` : type;
}

function limits(s: Schema): string[] {
  const out: string[] = [];
  if (s.enum) out.push(s.enum.map(String).join(" · "));
  if (s.minimum !== undefined || s.maximum !== undefined) out.push(`${s.minimum ?? "…"}–${s.maximum ?? "…"}`);
  if (s.maxLength) out.push(t("apiPage.maxLength", { n: s.maxLength }));
  if (s.pattern) out.push(s.pattern);
  if (s.default !== undefined && s.default !== "") out.push(t("apiPage.default", { v: JSON.stringify(s.default) }));
  return out;
}

function SchemaView({ schema, depth }: { schema: Schema; depth: number }) {
  const [r] = resolve(schema);
  if (!r) return null;
  if (r.type === "array" || (Array.isArray(r.type) && r.type.includes("array"))) {
    return (
      <div className="schema">
        <div className="mb-1 text-xs text-[var(--ink-500)]">{t("apiPage.arrayOf", { type: typeLabel(r.items) })}</div>
        {r.items ? <SchemaView schema={r.items} depth={depth + 1} /> : null}
      </div>
    );
  }
  const props = Object.entries(r.properties ?? {}).filter(([n]) => n !== "$schema");
  if (!props.length) return <div className="mono text-xs">{typeLabel(r)}</div>;
  return (
    <div className="schema">
      {props.map(([name, p]) => (
        <FieldRow key={name} name={name} schema={p} required={r.required?.includes(name)} depth={depth} />
      ))}
    </div>
  );
}

function FieldRow({ name, schema, required, note, description, depth }: { name: string; schema?: Schema; required?: boolean; note?: string; description?: string; depth: number }) {
  const [r] = resolve(schema);
  const nested = r && depth < 4 && (r.properties || (r.items && resolve(r.items)[0]?.properties));
  const extra: ReactNode[] = r ? limits(r).map((l) => <span key={l} className="mono mr-2 text-[11px] text-[var(--ink-500)]">{l}</span>) : [];
  return (
    <div className="schema-row">
      <div className="min-w-0">
        <span className="mono break-all text-[var(--ink-900)]">{name}</span>
        {required ? <span className="ml-1 text-[var(--berry-600)]" title={t("apiPage.required")}>*</span> : null}
        <div className="text-[11px] text-[var(--ink-400)]">
          {typeLabel(schema)}
          {note ? ` · ${note}` : null}
        </div>
      </div>
      <div className="min-w-0 text-[var(--ink-700)]">
        {description ?? r?.description ? <div className="mb-1">{description ?? r?.description}</div> : null}
        {extra.length ? <div>{extra}</div> : null}
        {nested ? (
          <div className="schema-nest">
            <SchemaView schema={r.properties ? r : (r.items as Schema)} depth={depth + 1} />
          </div>
        ) : null}
      </div>
    </div>
  );
}

/** Sends a GET with the admin's own session: nothing changes on the panel. */
function TryIt({ o }: { o: Op }) {
  const params = (o.op.parameters ?? []).filter((p) => p.in === "path" || p.in === "query");
  const [values, setValues] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<{ status: number; body: string } | null>(null);
  const missing = params.some((p) => p.in === "path" && !values[p.name]?.trim());
  const run = async () => {
    let path = o.path;
    const qs = new URLSearchParams();
    for (const p of params) {
      const v = values[p.name]?.trim();
      if (!v) continue;
      if (p.in === "path") path = path.replace(`{${p.name}}`, encodeURIComponent(v));
      else qs.set(p.name, v);
    }
    setBusy(true);
    try {
      const resp = await fetch(`${apiBase()}${path}${qs.size ? `?${qs}` : ""}`, { credentials: "same-origin", headers: { Accept: "application/json" } });
      const text = await resp.text();
      let pretty = text;
      try {
        pretty = JSON.stringify(JSON.parse(text), null, 2);
      } catch {
        // not JSON: shown as is
      }
      setResult({ status: resp.status, body: pretty.length > 20000 ? `${pretty.slice(0, 20000)}\n…` : pretty });
    } catch (e) {
      setResult({ status: 0, body: String(e) });
    } finally {
      setBusy(false);
    }
  };
  return (
    <div>
      <div className="api-h">{t("apiPage.tryIt")}</div>
      <p className="mb-2 text-xs text-[var(--ink-500)]">{t("apiPage.tryItHint")}</p>
      {params.length ? (
        <div className="mb-2 grid gap-2 sm:grid-cols-2">
          {params.map((p) => (
            <label key={p.name} className="flex flex-col gap-1 text-xs text-[var(--ink-500)]">
              <span className="mono">
                {p.name}
                {p.in === "path" ? <span className="text-[var(--berry-600)]"> *</span> : null}
              </span>
              <input className="input mono" value={values[p.name] ?? ""} onChange={(e) => setValues((v) => ({ ...v, [p.name]: e.target.value }))} placeholder={p.schema?.default !== undefined ? String(p.schema.default) : ""} autoComplete="off" spellCheck={false} />
            </label>
          ))}
        </div>
      ) : null}
      <Button size="sm" loading={busy} disabled={missing} onClick={() => void run()}>
        <Play size={14} aria-hidden /> {t("apiPage.run")}
      </Button>
      {result ? (
        <div className="mt-2">
          <span className={`pill ${result.status >= 200 && result.status < 300 ? "ok" : "bad"} mb-2`}>{result.status || t("apiPage.networkError")}</span>
          <div className="code-block">{result.body || t("apiPage.noContent")}</div>
        </div>
      ) : null}
    </div>
  );
}
