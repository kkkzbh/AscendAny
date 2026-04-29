import { useChatStore } from "@/stores/chatStore";
import { useLayoutStore } from "@/stores/layoutStore";
import { useSettingsStore } from "@/stores/settingsStore";

export async function hydrateLocalStateFromDesktop(): Promise<void> {
  const api = typeof window === "undefined" ? undefined : window.electronAPI;
  if (!api?.localStateHydrate) {
    return;
  }

  const snapshot = await api.localStateHydrate();
  if (!snapshot) {
    return;
  }

  useSettingsStore.getState().hydrateFromLocalState(snapshot.settings);
  useLayoutStore.getState().hydrateFromLocalState(snapshot.layout);
  useChatStore.getState().hydrateFromLocalState(snapshot.chat);
}

export async function bindCurrentLocalProfile(params: {
  accountId: string;
  username?: string | null;
  displayName?: string | null;
}): Promise<void> {
  const api = typeof window === "undefined" ? undefined : window.electronAPI;
  if (!api?.localStateBindProfile) {
    return;
  }
  await api.localStateBindProfile(params);
}
