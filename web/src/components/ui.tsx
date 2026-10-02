import clsx from "clsx";
import { LoaderCircle, SearchX, TriangleAlert, UserRound } from "lucide-react";
import { cloneElement, isValidElement, lazy, Suspense, useId, type ButtonHTMLAttributes, type CSSProperties, type ReactElement, type ReactNode } from "react";
import type { UserState } from "../api/client";
import { t } from "../i18n";

type Variant = "primary" | "glass" | "ghost" | "danger" | "danger-solid";

export function Button({
  variant = "glass",
  size,
  loading,
  block,
  className,
  children,
  disabled,
  ...rest
}: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: Variant; size?: "sm"; loading?: boolean; block?: boolean }) {
  const v = variant === "danger" ? "btn-ghost danger" : variant === "danger-solid" ? "btn-danger-solid" : `btn-${variant}`;
  return (
    <button
      type="button"
      className={clsx("btn", v, size === "sm" && "btn-sm", block && "btn-block", className)}
      disabled={disabled || loading}
      aria-busy={loading || undefined}
      {...rest}
    >
      {loading ? <LoaderCircle size={16} className="spin" aria-hidden /> : null}
      {children}
    </button>
  );
}

const stateTone: Record<UserState, "ok" | "warn" | "bad" | "off"> = {
  active: "ok",
  expiring: "warn",
  limited: "bad",
  expired: "bad",
  disabled: "off",
};

export function Pill({ tone, children }: { tone: "ok" | "warn" | "bad" | "off"; children: ReactNode }) {
  return <span className={clsx("pill", tone)}>{children}</span>;
}

export function StatePill({ state }: { state: UserState }) {
  return <Pill tone={stateTone[state]}>{t(`states.${state}`)}</Pill>;
}

const AVATAR_COLORS = ["#FBE3D2", "#DCEBFA", "#DDF1E6", "#F4E6C9", "#EADFF3", "#F6DDE0", "#DDEFF1"];

export function Avatar({ name, seed, size }: { name: string; seed: number; size?: "sm" | "lg" }) {
  const initials = name
    .replace(/[«»"@()]/g, "")
    .trim()
    .split(/\s+/)
    .map((w) => w[0] ?? "")
    .slice(0, 2)
    .join("")
    .toUpperCase();
  const style = { "--av": AVATAR_COLORS[seed % AVATAR_COLORS.length] } as CSSProperties;
  return (
    <span className={clsx("avatar", size)} style={style} aria-hidden>
      {initials || "?"}
    </span>
  );
}

export function Segmented<T extends string>({ value, options, onChange, label }: { value: T; options: { value: T; label: string }[]; onChange: (v: T) => void; label: string }) {
  return (
    <div className="seg" role="group" aria-label={label}>
      {options.map((o) => (
        <button key={o.value} type="button" aria-pressed={o.value === value} onClick={() => onChange(o.value)}>
          {o.label}
        </button>
      ))}
    </div>
  );
}

/**
 * A bar. Beside its number it is decoration; give it a `label` and it is a progressbar
 * of its own, with the value for a screen reader.
 */
export function Bar({ pct, className, label }: { pct: number; className?: string; label?: string }) {
  const v = Math.max(0, Math.min(100, pct));
  return (
    <div
      className={clsx("bar", className)}
      {...(label ? { role: "progressbar", "aria-label": label, "aria-valuemin": 0, "aria-valuemax": 100, "aria-valuenow": Math.round(v) } : { "aria-hidden": true })}
    >
      <i style={{ width: `${v}%` }} />
    </div>
  );
}

export function Ring({ pct, label, sub, size = 112 }: { pct: number; label: ReactNode; sub: ReactNode; size?: number }) {
  const r = size / 2 - 8;
  const c = 2 * Math.PI * r;
  const tone = pct >= 100 ? "bad" : pct >= 85 ? "warn" : "";
  return (
    <div className={clsx("gauge", tone)} style={{ width: size, height: size }}>
      <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`} aria-hidden>
        <circle className="track" cx={size / 2} cy={size / 2} r={r} fill="none" strokeWidth={10} />
        <circle
          className="val"
          cx={size / 2}
          cy={size / 2}
          r={r}
          fill="none"
          strokeWidth={10}
          strokeLinecap="round"
          strokeDasharray={c}
          strokeDashoffset={c * (1 - Math.min(Math.max(pct, 0), 100) / 100)}
        />
      </svg>
      <div className="gauge-label">
        <div>
          <b className="num">{label}</b>
          <span>{sub}</span>
        </div>
      </div>
    </div>
  );
}

const QRCode = lazy(() => import("./qr"));

/** A QR code of `value`; the encoder is a chunk of its own, fetched when a code first shows. */
export function QR({ value, size = 136, label }: { value: string; size?: number; label?: string }) {
  return (
    <Suspense fallback={<Skeleton style={{ width: size, height: size, borderRadius: 16 }} />}>
      <QRCode value={value} size={size} label={label} />
    </Suspense>
  );
}

export function Skeleton({ className, style }: { className?: string; style?: CSSProperties }) {
  return <span className={clsx("sk", className)} style={style} aria-hidden />;
}

export function Spinner({ size = 16 }: { size?: number }) {
  return <LoaderCircle size={size} className="spin" role="img" aria-label={t("common.loading")} />;
}

export function EmptyState({ title, text, children, search }: { title: string; text: ReactNode; children?: ReactNode; search?: boolean }) {
  return (
    <div className="state-box">
      <div className="state-mark">{search ? <SearchX size={22} /> : <UserRound size={22} />}</div>
      <h2>{title}</h2>
      <p>{text}</p>
      {children ? <div className="mt-2 flex flex-wrap justify-center gap-2">{children}</div> : null}
    </div>
  );
}

export function ErrorState({ title, text, onRetry }: { title?: string; text: string; onRetry?: () => void }) {
  return (
    <div className="state-box" role="alert">
      <div className="state-mark err">
        <TriangleAlert size={22} />
      </div>
      <h2>{title ?? t("common.loadFailed")}</h2>
      <p>{text}</p>
      {onRetry ? (
        <Button variant="primary" onClick={onRetry}>
          {t("common.retry")}
        </Button>
      ) : null}
    </div>
  );
}

/**
 * A labelled field with its hint or error. With `htmlFor` the label belongs to that input
 * and the hint/error is tied to it (aria-describedby, put on the input when it is the
 * direct child); without it the label names the group of controls inside (a segmented
 * switch, a select next to a switch).
 */
export function Field({ label, htmlFor, hint, error, children }: { label: string; htmlFor?: string; hint?: ReactNode; error?: string; children: ReactNode }) {
  const uid = useId();
  const noteId = error || hint ? `${uid}-note` : undefined;
  const control = htmlFor && noteId && isValidElement<{ id?: string; "aria-describedby"?: string }>(children) && children.props.id === htmlFor ? children : null;
  return (
    <div className="field" {...(htmlFor ? {} : { role: "group", "aria-labelledby": `${uid}-label` })}>
      {htmlFor ? <label htmlFor={htmlFor}>{label}</label> : <span className="lbl" id={`${uid}-label`}>{label}</span>}
      {control ? cloneElement(control as ReactElement<{ "aria-describedby"?: string }>, { "aria-describedby": [control.props["aria-describedby"], noteId].filter(Boolean).join(" ") }) : children}
      {error ? (
        <span className="err" role="alert" id={noteId}>
          {error}
        </span>
      ) : hint ? (
        <span className="hint" id={noteId}>
          {hint}
        </span>
      ) : null}
    </div>
  );
}

export function PageHeader({ title, sub, actions }: { title: string; sub?: ReactNode; actions?: ReactNode }) {
  return (
    <header className="topbar">
      <div className="min-w-0">
        <h1 className="page-title">{title}</h1>
        {sub ? <p className="page-sub">{sub}</p> : null}
      </div>
      {actions ? <div className="top-actions">{actions}</div> : null}
    </header>
  );
}
