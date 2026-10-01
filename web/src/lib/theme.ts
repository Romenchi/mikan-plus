import { useState, useEffect } from "react";

export type Theme = "light" | "dark" | "system";
export type Accent = "orange" | "ocean" | "sakura";

const THEME_KEY = "hc-theme";
const ACCENT_KEY = "hc-accent";

export function getStoredTheme(): Theme {
  try {
    return (localStorage.getItem(THEME_KEY) as Theme) || "system";
  } catch {
    return "system";
  }
}

export function getStoredAccent(): Accent {
  try {
    return (localStorage.getItem(ACCENT_KEY) as Accent) || "orange";
  } catch {
    return "orange";
  }
}

export function applyTheme(theme: Theme) {
  try {
    localStorage.setItem(THEME_KEY, theme);
  } catch {}

  const isDark =
    theme === "dark" ||
    (theme === "system" &&
      typeof window !== "undefined" &&
      window.matchMedia("(prefers-color-scheme: dark)").matches);

  if (typeof document !== "undefined") {
    if (isDark) {
      document.documentElement.setAttribute("data-theme", "dark");
      document.documentElement.classList.add("dark");
    } else {
      document.documentElement.removeAttribute("data-theme");
      document.documentElement.classList.remove("dark");
    }
  }
}

export function applyAccent(accent: Accent) {
  try {
    localStorage.setItem(ACCENT_KEY, accent);
  } catch {}

  if (typeof document !== "undefined") {
    document.documentElement.setAttribute("data-accent", accent);
  }
}

export function initTheme() {
  if (typeof window === "undefined") return;

  applyTheme(getStoredTheme());
  applyAccent(getStoredAccent());

  // Listen to OS theme changes if on system
  const mq = window.matchMedia("(prefers-color-scheme: dark)");
  mq.addEventListener("change", () => {
    if (getStoredTheme() === "system") {
      applyTheme("system");
    }
  });
}

// React hook for UI toggles
export function useThemeState() {
  const [theme, setTheme] = useState<Theme>(getStoredTheme);
  const [accent, setAccent] = useState<Accent>(getStoredAccent);

  useEffect(() => {
    applyTheme(theme);
  }, [theme]);

  useEffect(() => {
    applyAccent(accent);
  }, [accent]);

  const toggleDark = () => {
    const isDark =
      theme === "dark" ||
      (theme === "system" && window.matchMedia("(prefers-color-scheme: dark)").matches);
    setTheme(isDark ? "light" : "dark");
  };

  return { theme, setTheme, accent, setAccent, toggleDark };
}
