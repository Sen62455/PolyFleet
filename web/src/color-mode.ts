import { computed, readonly, ref } from "vue";

export type ColorMode = "light" | "dark" | "eye";
export type ColorModePreference = ColorMode | "system";

const STORAGE_KEY = "hyfleet:color-mode";
const THEME_COLORS: Record<ColorMode, string> = {
  light: "#f4f7fb",
  dark: "#0b1220",
  eye: "#f3f6f1",
};

function isColorModePreference(value: string | null): value is ColorModePreference {
  return value === "system" || value === "light" || value === "dark" || value === "eye";
}

function readStoredMode(): ColorModePreference | null {
  if (typeof window === "undefined") return null;

  try {
    const stored = window.localStorage.getItem(STORAGE_KEY);
    return isColorModePreference(stored) ? stored : null;
  } catch {
    return null;
  }
}

function preferredMode(): ColorMode {
  if (typeof window !== "undefined" && typeof window.matchMedia === "function") {
    return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
  }
  return "light";
}

function resolveMode(preference: ColorModePreference): ColorMode {
  return preference === "system" ? preferredMode() : preference;
}

function applyMode(mode: ColorMode, preference: ColorModePreference) {
  if (typeof document === "undefined") return;

  document.documentElement.dataset.theme = mode;
  document.documentElement.dataset.themePreference = preference;
  document.documentElement.style.colorScheme = mode === "dark" ? "dark" : "light";

  const colorSchemeMeta = document.querySelector<HTMLMetaElement>('meta[name="color-scheme"]');
  colorSchemeMeta?.setAttribute("content", mode === "dark" ? "dark" : "light");

  const themeColorMeta = document.querySelector<HTMLMetaElement>('meta[name="theme-color"]');
  themeColorMeta?.setAttribute("content", THEME_COLORS[mode]);
}

const activePreference = ref<ColorModePreference>(readStoredMode() ?? "system");
const activeMode = ref<ColorMode>(resolveMode(activePreference.value));
applyMode(activeMode.value, activePreference.value);

function syncSystemMode() {
  if (activePreference.value !== "system") return;
  activeMode.value = preferredMode();
  applyMode(activeMode.value, activePreference.value);
}

if (typeof window !== "undefined" && typeof window.matchMedia === "function") {
  const colorSchemeQuery = window.matchMedia("(prefers-color-scheme: dark)");
  if (typeof colorSchemeQuery.addEventListener === "function") {
    colorSchemeQuery.addEventListener("change", syncSystemMode);
  } else {
    colorSchemeQuery.addListener?.(syncSystemMode);
  }

  window.addEventListener("storage", (event) => {
    if (event.key !== STORAGE_KEY) return;
    const preference = isColorModePreference(event.newValue) ? event.newValue : "system";
    activePreference.value = preference;
    activeMode.value = resolveMode(preference);
    applyMode(activeMode.value, preference);
  });
}

export const colorMode = readonly(activeMode);
export const colorModePreference = readonly(activePreference);
export const isDarkMode = computed(() => activeMode.value === "dark");

export function setColorMode(preference: ColorModePreference) {
  activePreference.value = preference;
  activeMode.value = resolveMode(preference);
  applyMode(activeMode.value, preference);

  if (typeof window !== "undefined") {
    try {
      window.localStorage.setItem(STORAGE_KEY, preference);
    } catch {
      // The selected mode still applies when browser storage is unavailable.
    }
  }
}

export function toggleColorMode() {
  setColorMode(activeMode.value === "dark" ? "light" : "dark");
}
