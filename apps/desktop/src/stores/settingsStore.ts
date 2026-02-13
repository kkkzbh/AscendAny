import { create } from "zustand";
import { persist } from "zustand/middleware";
import type { ModelProvidersResponsePayload } from "@/lib/api";
import type {
  AppSettings,
  ProviderType,
  ModelProvider,
  ThemeMode,
} from "@/types/settings";
import {
  DEFAULT_PROVIDERS,
  PROVIDER_ORDER,
  isProviderType,
} from "@/types/settings";

interface SettingsState extends AppSettings {
  isOpen: boolean;
  openSettings: () => void;
  closeSettings: () => void;
  setTheme: (theme: ThemeMode) => void;
  toggleTheme: () => void;
  setActiveProvider: (p: ProviderType) => void;
  updateProvider: (type: ProviderType, patch: Partial<ModelProvider>) => void;
  syncProviderOptions: (payload: ModelProvidersResponsePayload) => void;
  setStudentId: (id: string) => void;
  setPtaNickname: (nickname: string) => void;
}

function isThemeMode(value: unknown): value is ThemeMode {
  return value === "light" || value === "dark";
}

function cloneDefaultProviders(): Record<ProviderType, ModelProvider> {
  return PROVIDER_ORDER.reduce(
    (acc, providerType) => {
      acc[providerType] = { ...DEFAULT_PROVIDERS[providerType] };
      return acc;
    },
    {} as Record<ProviderType, ModelProvider>,
  );
}

function normalizeProvider(
  source: unknown,
  fallback: ModelProvider,
): ModelProvider {
  if (!source || typeof source !== "object") {
    return { ...fallback };
  }

  const candidate = source as Partial<ModelProvider>;
  return {
    ...fallback,
    label:
      typeof candidate.label === "string" && candidate.label.trim()
        ? candidate.label
        : fallback.label,
    baseUrl:
      typeof candidate.baseUrl === "string" ? candidate.baseUrl : fallback.baseUrl,
    model: typeof candidate.model === "string" ? candidate.model : fallback.model,
    apiKey: typeof candidate.apiKey === "string" ? candidate.apiKey : fallback.apiKey,
    usesServerConfig:
      typeof candidate.usesServerConfig === "boolean"
        ? candidate.usesServerConfig
        : fallback.usesServerConfig,
    enabled:
      typeof candidate.enabled === "boolean" ? candidate.enabled : fallback.enabled,
  };
}

function mergeProviders(source: unknown): Record<ProviderType, ModelProvider> {
  const merged = cloneDefaultProviders();
  if (!source || typeof source !== "object") {
    return merged;
  }

  const input = source as Partial<Record<ProviderType, ModelProvider>>;
  for (const providerType of PROVIDER_ORDER) {
    merged[providerType] = normalizeProvider(input[providerType], merged[providerType]);
  }
  return merged;
}

export const useSettingsStore = create<SettingsState>()(
  persist(
    (set) => ({
      theme: "light",
      activeProvider: "server_default",
      providers: cloneDefaultProviders(),
      serverDefaultTarget: "openai",
      serverDefaultTargetLabel: "OpenAI",
      serverDefaultModel: "",
      studentId: "",
      ptaNickname: "",
      isOpen: false,

      openSettings: () => set({ isOpen: true }),
      closeSettings: () => set({ isOpen: false }),
      setTheme: (theme) => set({ theme }),
      toggleTheme: () =>
        set((state) => ({
          theme: state.theme === "light" ? "dark" : "light",
        })),

      setActiveProvider: (providerType) =>
        set((state) => {
          if (state.providers[providerType]?.enabled) {
            return { activeProvider: providerType };
          }
          return { activeProvider: "server_default" };
        }),

      updateProvider: (providerType, patch) =>
        set((state) => {
          const current = state.providers[providerType];
          if (!current || current.usesServerConfig) {
            return {};
          }
          return {
            providers: {
              ...state.providers,
              [providerType]: { ...current, ...patch },
            },
          };
        }),

      syncProviderOptions: (payload) =>
        set((state) => {
          const nextProviders = mergeProviders(state.providers);
          for (const option of payload.providers) {
            const providerType = option.type.trim();
            if (!isProviderType(providerType)) {
              continue;
            }
            const current = nextProviders[providerType];
            nextProviders[providerType] = {
              ...current,
              label:
                typeof option.label === "string" && option.label.trim()
                  ? option.label.trim()
                  : current.label,
              usesServerConfig: option.usesServerConfig,
              enabled: option.enabled,
            };
          }

          nextProviders.server_default = {
            ...nextProviders.server_default,
            usesServerConfig: true,
            enabled: true,
          };

          let nextActiveProvider = state.activeProvider;
          if (!nextProviders[nextActiveProvider]?.enabled) {
            const requested = payload.defaultProvider.trim();
            if (isProviderType(requested) && nextProviders[requested]?.enabled) {
              nextActiveProvider = requested;
            } else {
              nextActiveProvider = "server_default";
            }
          }

          const serverDefaultTarget = payload.serverDefaultTarget.trim();
          const nextTargetLabel = payload.serverDefaultTargetLabel?.trim() ?? "";
          const nextTargetModel = payload.serverDefaultModel?.trim() ?? "";

          return {
            providers: nextProviders,
            activeProvider: nextActiveProvider,
            serverDefaultTarget: serverDefaultTarget || state.serverDefaultTarget,
            serverDefaultTargetLabel: nextTargetLabel || state.serverDefaultTargetLabel,
            serverDefaultModel: nextTargetModel,
          };
        }),

      setStudentId: (id) => set({ studentId: id }),
      setPtaNickname: (nickname) => set({ ptaNickname: nickname }),
    }),
    {
      name: "ascendany_settings",
      partialize: (state) => ({
        theme: state.theme,
        activeProvider: state.activeProvider,
        providers: state.providers,
        serverDefaultTarget: state.serverDefaultTarget,
        serverDefaultTargetLabel: state.serverDefaultTargetLabel,
        serverDefaultModel: state.serverDefaultModel,
        studentId: state.studentId,
        ptaNickname: state.ptaNickname,
      }),
      merge: (persistedState, currentState) => {
        const persisted = (persistedState ?? {}) as Partial<SettingsState>;
        const providers = mergeProviders(persisted.providers);

        const requestedProvider =
          typeof persisted.activeProvider === "string" &&
          isProviderType(persisted.activeProvider)
            ? persisted.activeProvider
            : currentState.activeProvider;
        const activeProvider = providers[requestedProvider]?.enabled
          ? requestedProvider
          : "server_default";

        return {
          ...currentState,
          theme: isThemeMode(persisted.theme) ? persisted.theme : currentState.theme,
          activeProvider,
          providers,
          serverDefaultTarget:
            typeof persisted.serverDefaultTarget === "string" &&
            persisted.serverDefaultTarget.trim()
              ? persisted.serverDefaultTarget
              : currentState.serverDefaultTarget,
          serverDefaultTargetLabel:
            typeof persisted.serverDefaultTargetLabel === "string" &&
            persisted.serverDefaultTargetLabel.trim()
              ? persisted.serverDefaultTargetLabel
              : currentState.serverDefaultTargetLabel,
          serverDefaultModel:
            typeof persisted.serverDefaultModel === "string"
              ? persisted.serverDefaultModel
              : currentState.serverDefaultModel,
          studentId:
            typeof persisted.studentId === "string"
              ? persisted.studentId
              : currentState.studentId,
          ptaNickname:
            typeof persisted.ptaNickname === "string"
              ? persisted.ptaNickname
              : currentState.ptaNickname,
        };
      },
    },
  ),
);
