import { create } from "zustand";
import type {
  AppSettings,
  ThemeMode,
} from "@/types/settings";
import {
  DEFAULT_ZOOM_PERCENT,
  ZOOM_PERCENT_MAX,
  ZOOM_PERCENT_MIN,
  ZOOM_PERCENT_STEP,
} from "@/types/settings";
import { DEFAULT_ROLE_ID } from "@/types/role";

interface SettingsState extends AppSettings {
  isOpen: boolean;
  openSettings: () => void;
  closeSettings: () => void;
  resetForAccount: () => void;
  hydrateFromLocalState: (settings: unknown) => void;
  setTheme: (theme: ThemeMode) => void;
  toggleTheme: () => void;
  setOpaqueSidebarBackground: (enabled: boolean) => void;
  setZoomPercent: (zoomPercent: number) => void;
  setActiveRole: (roleId: string) => void;
}

function isThemeMode(value: unknown): value is ThemeMode {
  return value === "light" || value === "dark";
}

function normalizeZoomPercent(value: unknown): number {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    return DEFAULT_ZOOM_PERCENT;
  }
  const rounded = Math.round(value / ZOOM_PERCENT_STEP) * ZOOM_PERCENT_STEP;
  return Math.min(ZOOM_PERCENT_MAX, Math.max(ZOOM_PERCENT_MIN, rounded));
}

function normalizeSettingsSnapshot(value: unknown, fallback: AppSettings): AppSettings {
  const persisted = (value ?? {}) as Partial<AppSettings> & {
    useOpaqueWindowBackground?: unknown;
  };
  const persistedSidebarBackground =
    typeof persisted.useOpaqueSidebarBackground === "boolean"
      ? persisted.useOpaqueSidebarBackground
      : typeof persisted.useOpaqueWindowBackground === "boolean"
        ? persisted.useOpaqueWindowBackground
        : fallback.useOpaqueSidebarBackground;

  return {
    theme: isThemeMode(persisted.theme) ? persisted.theme : fallback.theme,
    useOpaqueSidebarBackground: persistedSidebarBackground,
    zoomPercent: normalizeZoomPercent(persisted.zoomPercent),
    activeRole:
      typeof persisted.activeRole === "string" && persisted.activeRole.trim()
        ? persisted.activeRole.trim()
        : fallback.activeRole,
  };
}

function persistSettingsSnapshot(snapshot: AppSettings): void {
  const api = typeof window === "undefined" ? undefined : window.electronAPI;
  if (!api?.localStateSaveSettings) {
    return;
  }
  void api.localStateSaveSettings(snapshot).catch(() => {
    // Local UI state remains authoritative until the next successful save.
  });
}

function pickSettingsSnapshot(state: SettingsState): AppSettings {
  return {
    theme: state.theme,
    useOpaqueSidebarBackground: state.useOpaqueSidebarBackground,
    zoomPercent: state.zoomPercent,
    activeRole: state.activeRole,
  };
}

export const useSettingsStore = create<SettingsState>()(
  (set, get) => ({
      theme: "light",
      useOpaqueSidebarBackground: true,
      zoomPercent: DEFAULT_ZOOM_PERCENT,
      activeRole: DEFAULT_ROLE_ID,
      isOpen: false,

      openSettings: () => set({ isOpen: true }),
      closeSettings: () => set({ isOpen: false }),
      resetForAccount: () =>
        set({
          theme: "light",
          useOpaqueSidebarBackground: true,
          zoomPercent: DEFAULT_ZOOM_PERCENT,
          activeRole: DEFAULT_ROLE_ID,
          isOpen: false,
        }),
      hydrateFromLocalState: (settings) =>
        set((state) => ({
          ...normalizeSettingsSnapshot(settings, pickSettingsSnapshot(state)),
        })),
      setTheme: (theme) => {
        set({ theme });
        persistSettingsSnapshot(pickSettingsSnapshot(get()));
      },
      toggleTheme: () => {
        set((state) => ({
          theme: state.theme === "light" ? "dark" : "light",
        }));
        persistSettingsSnapshot(pickSettingsSnapshot(get()));
      },
      setOpaqueSidebarBackground: (enabled) => {
        set({
          useOpaqueSidebarBackground: enabled,
        });
        persistSettingsSnapshot(pickSettingsSnapshot(get()));
      },
      setZoomPercent: (zoomPercent) => {
        set({
          zoomPercent: normalizeZoomPercent(zoomPercent),
        });
        persistSettingsSnapshot(pickSettingsSnapshot(get()));
      },

      setActiveRole: (roleId) => {
        set(() => {
          const normalized = roleId.trim();
          return { activeRole: normalized || DEFAULT_ROLE_ID };
        });
        persistSettingsSnapshot(pickSettingsSnapshot(get()));
      },

    }),
);
