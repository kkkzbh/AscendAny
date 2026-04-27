import { create } from "zustand";
import { persist } from "zustand/middleware";
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
  setTheme: (theme: ThemeMode) => void;
  toggleTheme: () => void;
  setOpaqueWindowBackground: (enabled: boolean) => void;
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

export const useSettingsStore = create<SettingsState>()(
  persist(
    (set) => ({
      theme: "light",
      useOpaqueWindowBackground: true,
      zoomPercent: DEFAULT_ZOOM_PERCENT,
      activeRole: DEFAULT_ROLE_ID,
      isOpen: false,

      openSettings: () => set({ isOpen: true }),
      closeSettings: () => set({ isOpen: false }),
      resetForAccount: () =>
        set({
          theme: "light",
          useOpaqueWindowBackground: true,
          zoomPercent: DEFAULT_ZOOM_PERCENT,
          activeRole: DEFAULT_ROLE_ID,
          isOpen: false,
        }),
      setTheme: (theme) => set({ theme }),
      toggleTheme: () =>
        set((state) => ({
          theme: state.theme === "light" ? "dark" : "light",
        })),
      setOpaqueWindowBackground: (enabled) =>
        set({
          useOpaqueWindowBackground: enabled,
        }),
      setZoomPercent: (zoomPercent) =>
        set({
          zoomPercent: normalizeZoomPercent(zoomPercent),
        }),

      setActiveRole: (roleId) =>
        set(() => {
          const normalized = roleId.trim();
          return { activeRole: normalized || DEFAULT_ROLE_ID };
        }),

    }),
    {
      name: "ascendany_settings_guest",
      partialize: (state) => ({
        theme: state.theme,
        useOpaqueWindowBackground: state.useOpaqueWindowBackground,
        zoomPercent: state.zoomPercent,
        activeRole: state.activeRole,
      }),
      merge: (persistedState, currentState) => {
        const persisted = (persistedState ?? {}) as Partial<SettingsState>;

        return {
          ...currentState,
          theme: isThemeMode(persisted.theme) ? persisted.theme : currentState.theme,
          useOpaqueWindowBackground:
            typeof persisted.useOpaqueWindowBackground === "boolean"
              ? persisted.useOpaqueWindowBackground
              : currentState.useOpaqueWindowBackground,
          zoomPercent: normalizeZoomPercent(persisted.zoomPercent),
          activeRole:
            typeof persisted.activeRole === "string" && persisted.activeRole.trim()
              ? persisted.activeRole.trim()
              : currentState.activeRole,
        };
      },
    },
  ),
);
