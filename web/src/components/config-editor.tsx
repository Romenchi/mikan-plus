// YAML editor for listener templates. Loaded lazily: CodeMirror is only needed when an
// admin opens the config tab.
import { autocompletion, type CompletionContext, type CompletionResult } from "@codemirror/autocomplete";
import { defaultKeymap, history, historyKeymap } from "@codemirror/commands";
import { yaml } from "@codemirror/lang-yaml";
import { defaultHighlightStyle, indentOnInput, syntaxHighlighting } from "@codemirror/language";
import { EditorState } from "@codemirror/state";
import { drawSelection, EditorView, highlightActiveLine, highlightActiveLineGutter, keymap, lineNumbers } from "@codemirror/view";
import { useEffect, useRef } from "react";
import { FINGERPRINTS } from "../lib/fingerprints";

// Mirrors the allow list in internal/proto (rules): the server is the authority, this only
// saves typing.
const TOP: Record<string, string[]> = {
  vless: ["ws-path", "grpc-service-name", "xhttp-config", "reality-config", "mux-option", "decryption"],
  vmess: ["ws-path", "grpc-service-name", "reality-config", "mux-option"],
  trojan: ["ws-path", "grpc-service-name", "reality-config", "mux-option"],
  hysteria2: ["obfs", "obfs-password", "obfs-min-packet-size", "obfs-max-packet-size", "alpn", "up", "down", "ignore-client-bandwidth", "masquerade", "max-idle-time", "cwnd", "udp-mtu", "bbr-profile"],
  tuic: ["congestion-controller", "alpn", "max-idle-time", "authentication-timeout", "max-udp-relay-packet-size", "cwnd", "bbr-profile"],
  anytls: ["padding-scheme"],
  trusttunnel: ["congestion-controller", "cwnd", "bbr-profile"],
  shadowquic: ["jls-upstream", "alpn", "quic-versions", "congestion-controller", "up", "down", "max-idle-time", "cwnd", "bbr-profile"],
  mieru: ["transport"],
  shadowsocks: ["cipher", "password"],
  sudoku: ["key", "aead-method", "padding-min", "padding-max", "table-type", "httpmask"],
  snell: ["psk", "version", "obfs-opts"],
};
const NESTED: Record<string, string[]> = {
  "reality-config": ["dest", "private-key", "short-id", "server-names", "max-time-difference", "limit-fallback-upload", "limit-fallback-download"],
  "xhttp-config": ["path", "mode", "host", "x-padding-bytes"],
  mikan: ["flow", "tls", "client"],
  client: ["server", "port", "sni", "fingerprint"],
  "jls-upstream": ["addr", "sni"],
  httpmask: ["disable", "mode", "path-root"],
  "obfs-opts": ["mode", "host"],
};
const VALUES: Record<string, string[]> = {
  type: Object.keys(TOP),
  mode: ["stream-one", "stream-up", "packet-up"],
  tls: ["node"],
  flow: ["xtls-rprx-vision"],
  obfs: ["salamander", "gecko"],
  "congestion-controller": ["bbr", "cubic", "new_reno"],
  cipher: ["2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm", "2022-blake3-chacha20-poly1305"],
  transport: ["TCP"],
  "aead-method": ["chacha20-poly1305", "aes-128-gcm"],
  "table-type": ["prefer_ascii", "prefer_entropy", "up_ascii_down_entropy", "up_entropy_down_ascii"],
  fingerprint: [...FINGERPRINTS],
};

const indent = (s: string) => s.length - s.trimStart().length;

/** The key whose block contains line n, or "" at the top level. */
function parentKey(doc: EditorState["doc"], lineNo: number, own: number): string {
  for (let n = lineNo - 1; n >= 1; n--) {
    const text = doc.line(n).text;
    if (!text.trim() || text.trimStart().startsWith("#")) continue;
    if (indent(text) < own) {
      const m = /^\s*([\w-]+):\s*$/.exec(text);
      return m ? m[1]! : "";
    }
  }
  return "";
}

function complete(ctx: CompletionContext): CompletionResult | null {
  const line = ctx.state.doc.lineAt(ctx.pos);
  const before = line.text.slice(0, ctx.pos - line.from);
  const value = /^\s*(-\s+)?([\w-]+):\s*([\w-]*)$/.exec(before);
  if (value) {
    const options = VALUES[value[2]!];
    if (!options) return null;
    return { from: ctx.pos - value[3]!.length, options: options.map((v) => ({ label: v, type: "constant" })) };
  }
  const key = /^(\s*)([\w-]*)$/.exec(before);
  if (!key || (!ctx.explicit && key[2] === "")) return null;
  const parent = parentKey(ctx.state.doc, line.number, key[1]!.length);
  let keys: string[];
  if (parent === "") {
    const type = /^type:\s*([\w-]+)/m.exec(ctx.state.doc.toString())?.[1] ?? "";
    keys = ["type", "mikan", ...(TOP[type] ?? Object.values(TOP).flat())];
  } else {
    keys = NESTED[parent] ?? [];
  }
  return { from: ctx.pos - key[2]!.length, options: [...new Set(keys)].map((k) => ({ label: k, type: "property", apply: `${k}: ` })) };
}

const theme = EditorView.theme({
  "&": { fontSize: "13px", backgroundColor: "transparent" },
  ".cm-content": { fontFamily: "var(--font-mono-stack)", padding: "8px 0" },
  ".cm-gutters": { backgroundColor: "transparent", border: "none", color: "var(--ink-400)" },
  ".cm-activeLine, .cm-activeLineGutter": { backgroundColor: "rgba(22, 26, 36, 0.035)" },
  "&.cm-focused": { outline: "none" },
  ".cm-tooltip-autocomplete": { borderRadius: "12px", overflow: "hidden" },
});

export default function ConfigEditor({ value, onChange, label, invalid }: { value: string; onChange: (v: string) => void; label: string; invalid?: boolean }) {
  const host = useRef<HTMLDivElement>(null);
  const view = useRef<EditorView | null>(null);
  const change = useRef(onChange);
  change.current = onChange;

  useEffect(() => {
    const v = new EditorView({
      parent: host.current!,
      state: EditorState.create({
        doc: value,
        extensions: [
          // What a short YAML template needs. The "codemirror" package's basicSetup also brings
          // search, lint, folding, bracket matching and more, and binds keys (Ctrl+F,
          // Ctrl+Shift+M) the form has no use for.
          lineNumbers(),
          highlightActiveLineGutter(),
          highlightActiveLine(),
          history(),
          drawSelection(),
          indentOnInput(),
          syntaxHighlighting(defaultHighlightStyle, { fallback: true }),
          keymap.of([...defaultKeymap, ...historyKeymap]),
          yaml(),
          autocompletion({ override: [complete] }),
          theme,
          EditorView.lineWrapping,
          EditorView.contentAttributes.of({ "aria-label": label, spellcheck: "false" }),
          EditorView.updateListener.of((u) => {
            if (u.docChanged) change.current(u.state.doc.toString());
          }),
        ],
      }),
    });
    view.current = v;
    return () => v.destroy();
    // The editor owns the text after mount; outside resets go through the effect below.
  }, []);

  useEffect(() => {
    const v = view.current;
    if (v && v.state.doc.toString() !== value) v.dispatch({ changes: { from: 0, to: v.state.doc.length, insert: value } });
  }, [value]);

  return <div ref={host} className="config-editor" aria-invalid={invalid || undefined} />;
}
