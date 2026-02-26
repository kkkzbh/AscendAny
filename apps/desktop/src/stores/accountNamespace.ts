import { useChatStore } from "@/stores/chatStore";
import { useLayoutStore } from "@/stores/layoutStore";
import { useSettingsStore } from "@/stores/settingsStore";

const SETTINGS_BASE_KEY = "ascendany_settings";
const CHAT_BASE_KEY = "ascendany_chat";
const LAYOUT_BASE_KEY = "ascendany_layout";
const LEGACY_SETTINGS_KEY = SETTINGS_BASE_KEY;
const LEGACY_CHAT_KEY = CHAT_BASE_KEY;
const LEGACY_LAYOUT_KEY = LAYOUT_BASE_KEY;
const LEGACY_CLEANUP_MARK = "ascendany_auth_legacy_cleanup_done";

function resolveStoreKey(baseKey: string, accountId: string | null): string {
  const suffix = accountId && accountId.trim() ? accountId.trim() : "guest";
  return `${baseKey}_${suffix}`;
}

export function cleanupLegacyAnonymousStorage(): void {
  const done = localStorage.getItem(LEGACY_CLEANUP_MARK);
  if (done === "1") {
    return;
  }

  localStorage.removeItem(LEGACY_SETTINGS_KEY);
  localStorage.removeItem(LEGACY_CHAT_KEY);
  localStorage.removeItem(LEGACY_LAYOUT_KEY);
  localStorage.setItem(LEGACY_CLEANUP_MARK, "1");
}

export async function switchAccountNamespace(accountId: string | null): Promise<void> {
  const settingsStore = useSettingsStore;
  const chatStore = useChatStore;
  const layoutStore = useLayoutStore;

  const settingsStorageKey = resolveStoreKey(SETTINGS_BASE_KEY, accountId);
  const hasPersistedSettings = localStorage.getItem(settingsStorageKey) !== null;

  settingsStore.persist.setOptions({
    name: settingsStorageKey,
  });

  if (!hasPersistedSettings) {
    settingsStore.getState().resetForAccount();
  }

  await settingsStore.persist.rehydrate();

  const chatStorageKey = resolveStoreKey(CHAT_BASE_KEY, accountId);
  const hasPersistedChat = localStorage.getItem(chatStorageKey) !== null;

  chatStore.persist.setOptions({
    name: chatStorageKey,
  });

  if (!hasPersistedChat) {
    chatStore.getState().clearContext();
  }

  await chatStore.persist.rehydrate();

  const layoutStorageKey = resolveStoreKey(LAYOUT_BASE_KEY, accountId);
  const hasPersistedLayout = localStorage.getItem(layoutStorageKey) !== null;

  layoutStore.persist.setOptions({
    name: layoutStorageKey,
  });

  if (!hasPersistedLayout) {
    layoutStore.getState().resetForAccount();
  }

  await layoutStore.persist.rehydrate();
}
