import { Check, TriangleAlert } from "lucide-react";
import { AnimatePresence, motion } from "motion/react";
import { createContext, useCallback, useContext, useMemo, useRef, useState, type ReactNode } from "react";

type Toast = { id: number; text: string; error?: boolean; action?: { label: string; run: () => void } };
type Api = {
  ok: (text: string, action?: Toast["action"]) => void;
  error: (text: string) => void;
};

const Ctx = createContext<Api | null>(null);

/** How long a toast stays (an error or one with an action longer), and what is left of it once the pointer or focus leaves. */
const LIFETIME = { ok: 4_200, error: 6_000, action: 8_000, resume: 2_500 };

export function ToastProvider({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<Toast[]>([]);
  const seq = useRef(0);
  const timers = useRef(new Map<number, number>());

  const dismiss = useCallback((id: number) => {
    window.clearTimeout(timers.current.get(id));
    timers.current.delete(id);
    setItems((xs) => xs.filter((x) => x.id !== id));
  }, []);
  const arm = useCallback(
    (id: number, ms: number) => {
      window.clearTimeout(timers.current.get(id));
      timers.current.set(id, window.setTimeout(() => dismiss(id), ms));
    },
    [dismiss],
  );
  // A toast under the pointer or with focus inside stays until the reader is done with it
  // (WCAG 2.2.1: a message with an action must not vanish while it is being reached for).
  const hold = useCallback((id: number) => {
    window.clearTimeout(timers.current.get(id));
    timers.current.delete(id);
  }, []);
  const push = useCallback(
    (t: Omit<Toast, "id">) => {
      const id = ++seq.current;
      // Three at most; an older one pushed out has a timer left that finds nothing to remove.
      setItems((xs) => [...xs.slice(-2), { ...t, id }]);
      arm(id, t.error ? LIFETIME.error : t.action ? LIFETIME.action : LIFETIME.ok);
    },
    [arm],
  );
  const api = useMemo<Api>(() => ({ ok: (text, action) => push({ text, action }), error: (text) => push({ text, error: true }) }), [push]);

  return (
    <Ctx.Provider value={api}>
      {children}
      {/* Each toast announces itself through its own role (status / alert). */}
      <div className="toast-region">
        <AnimatePresence>
          {items.map((t) => (
            <motion.div
              key={t.id}
              className={t.error ? "toast err" : "toast"}
              role={t.error ? "alert" : "status"}
              initial={{ opacity: 0, y: -8, scale: 0.98 }}
              animate={{ opacity: 1, y: 0, scale: 1 }}
              exit={{ opacity: 0, y: -8 }}
              transition={{ type: "spring", stiffness: 500, damping: 34 }}
              onMouseEnter={() => hold(t.id)}
              onMouseLeave={() => arm(t.id, LIFETIME.resume)}
              onFocus={() => hold(t.id)}
              onBlur={() => arm(t.id, LIFETIME.resume)}
            >
              {t.error ? <TriangleAlert size={16} aria-hidden /> : <Check size={16} aria-hidden className="text-[var(--mikan-400)]" />}
              <span>{t.text}</span>
              {t.action ? (
                <button
                  type="button"
                  className="toast-act"
                  onClick={() => {
                    t.action!.run();
                    dismiss(t.id);
                  }}
                >
                  {t.action.label}
                </button>
              ) : null}
            </motion.div>
          ))}
        </AnimatePresence>
      </div>
    </Ctx.Provider>
  );
}

export function useToast(): Api {
  const v = useContext(Ctx);
  if (!v) throw new Error("useToast outside ToastProvider");
  return v;
}
