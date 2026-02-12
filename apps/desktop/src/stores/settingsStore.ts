import { create } from "zustand";
import { persist } from "zustand/middleware";
import type { AppSettings, ProviderType, ModelProvider } from "@/types/settings";
import { DEFAULT_PROVIDERS } from "@/types/settings";

interface SettingsState extends AppSettings {
  isOpen: boolean;
  openSettings: () => void;
  closeSettings: () => void;
  setActiveProvider: (p: ProviderType) => void;
  updateProvider: (type: ProviderType, patch: Partial<ModelProvider>) => void;
  setStudentId: (id: string) => void;
  setApiBaseUrl: (url: string) => void;
}

export const useSettingsStore = create<SettingsState>()(
  persist(
    (set) => ({
      activeProvider: "openai",
      providers: { ...DEFAULT_PROVIDERS },
      studentId: "",
      apiBaseUrl: "http://127.0.0.1:8000",
      isOpen: false,

      openSettings: () => set({ isOpen: true }),
      closeSettings: () => set({ isOpen: false }),

      setActiveProvider: (p) => set({ activeProvider: p }),

      updateProvider: (type, patch) =>
        set((state) => ({
          providers: {
            ...state.providers,
            [type]: { ...state.providers[type], ...patch },
          },
        })),

      setStudentId: (id) => set({ studentId: id }),
      setApiBaseUrl: (url) => set({ apiBaseUrl: url }),
    }),
    {
      name: "ascendany_settings",
      partialize: (state) => ({
        activeProvider: state.activeProvider,
        providers: state.providers,
        studentId: state.studentId,
        apiBaseUrl: state.apiBaseUrl,
      }),
    },
  ),
);
