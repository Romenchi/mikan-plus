import { Sun, Moon } from "lucide-react";
import { useThemeState, type Accent } from "../lib/theme";

export function ThemeToggle({ compact = false }: { compact?: boolean }) {
  const { theme, toggleDark, accent, setAccent } = useThemeState();

  const isDark =
    theme === "dark" ||
    (theme === "system" &&
      typeof window !== "undefined" &&
      window.matchMedia("(prefers-color-scheme: dark)").matches);

  const accents: { id: Accent; label: string; color: string }[] = [
    { id: "orange", label: "Orange", color: "#f07a2e" },
    { id: "ocean", label: "Ocean", color: "#0ea5e9" },
    { id: "sakura", label: "Sakura", color: "#ec4899" },
  ];

  return (
    <div className={`theme-controls flex items-center ${compact ? "justify-between" : "gap-2"} p-1.5 rounded-xl bg-[var(--hairline)]`}>
      {/* Light / Dark Toggle */}
      <button
        type="button"
        onClick={toggleDark}
        className="icon-btn !w-8 !h-8 !rounded-lg text-[var(--ink-700)] hover:text-[var(--ink-900)] transition-transform active:scale-95"
        title={isDark ? "Светлая тема" : "Тёмная тема"}
        aria-label="Переключить тему"
      >
        {isDark ? <Sun size={16} /> : <Moon size={16} />}
      </button>

      {/* Accent Color Palette */}
      <div className="flex items-center gap-1.5 ml-auto">
        {accents.map((a) => {
          const active = accent === a.id;
          return (
            <button
              key={a.id}
              type="button"
              onClick={() => setAccent(a.id)}
              className={`w-6 h-6 rounded-full transition-all duration-200 flex items-center justify-center ${
                active ? "scale-110 ring-2 ring-offset-2 ring-[var(--ink-600)] ring-offset-[var(--bg)]" : "opacity-75 hover:opacity-100 hover:scale-105"
              }`}
              style={{ backgroundColor: a.color }}
              title={a.label}
              aria-label={`Акцент: ${a.label}`}
            >
              {active && <span className="w-1.5 h-1.5 bg-white rounded-full shadow-sm" />}
            </button>
          );
        })}
      </div>
    </div>
  );
}
