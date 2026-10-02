import type { LucideIcon } from "lucide-react";
import { useRef, type KeyboardEvent, type ReactNode } from "react";

type Tab<T extends string> = { id: T; label: string; icon?: LucideIcon };

/**
 * A page's sections as tabs (WAI-ARIA tab list: arrows, Home and End move between them).
 * The caller keeps the value, usually in the URL so a link opens the section; the panel's
 * content goes in children.
 */
export function Tabs<T extends string>({ id, tabs, value, onChange, label, children }: { id: string; tabs: Tab<T>[]; value: T; onChange: (v: T) => void; label: string; children: ReactNode }) {
  const refs = useRef<Partial<Record<T, HTMLButtonElement | null>>>({});
  const go = (next: T) => {
    onChange(next);
    refs.current[next]?.focus();
  };
  const onKey = (e: KeyboardEvent) => {
    const i = tabs.findIndex((x) => x.id === value);
    const step = e.key === "ArrowRight" ? 1 : e.key === "ArrowLeft" ? -1 : 0;
    if (e.key === "Home") go(tabs[0]!.id);
    else if (e.key === "End") go(tabs[tabs.length - 1]!.id);
    else if (step) go(tabs[(i + step + tabs.length) % tabs.length]!.id);
    else return;
    e.preventDefault();
  };
  return (
    <>
      <div className="tabs mb-4" role="tablist" aria-label={label} onKeyDown={onKey}>
        {tabs.map(({ id: tab, label: text, icon: Icon }) => (
          <button
            key={tab}
            ref={(el) => {
              refs.current[tab] = el;
            }}
            type="button"
            role="tab"
            id={`${id}-tab-${tab}`}
            aria-selected={value === tab}
            aria-controls={`${id}-panel`}
            tabIndex={value === tab ? 0 : -1}
            onClick={() => onChange(tab)}
          >
            {Icon ? <Icon size={16} aria-hidden /> : null} {text}
          </button>
        ))}
      </div>
      <div id={`${id}-panel`} role="tabpanel" aria-labelledby={`${id}-tab-${value}`}>
        {children}
      </div>
    </>
  );
}

/** Two columns of cards on wide screens, one on phones; wide "left" keeps a narrow right column (a preview) in view. */
export function Columns({ left, right, wide }: { left: ReactNode; right?: ReactNode; wide?: "left" | "even" }) {
  const cols = wide === "left" ? "xl:grid-cols-[minmax(0,1fr)_360px]" : "xl:grid-cols-2";
  return (
    <div className={`grid items-start gap-4 ${right ? cols : ""}`}>
      <div className="flex min-w-0 flex-col gap-4">{left}</div>
      {right ? <div className={`flex min-w-0 flex-col gap-4 ${wide === "left" ? "xl:sticky xl:top-4" : ""}`}>{right}</div> : null}
    </div>
  );
}
