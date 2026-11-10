// Light, dark or system theme. The choice is a per-viewer convenience in
// localStorage; without storage the system setting applies.
export type Theme = "system" | "light" | "dark";
const KEY = "plumb.theme";

export function loadTheme(): Theme {
  try {
    const v = localStorage.getItem(KEY);
    return v === "light" || v === "dark" ? v : "system";
  } catch {
    return "system";
  }
}

export function applyTheme(t: Theme): void {
  const root = document.documentElement;
  if (t === "system") delete root.dataset["theme"];
  else root.dataset["theme"] = t;
  try {
    if (t === "system") localStorage.removeItem(KEY);
    else localStorage.setItem(KEY, t);
  } catch {
    // storage blocked: the choice lasts for this page only
  }
}
