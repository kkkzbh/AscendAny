import { useChatStore } from "@/stores/chatStore";
import { useSettingsStore } from "@/stores/settingsStore";

const SETTINGS_BASE_KEY = "ascendany_settings";
const CHAT_BASE_KEY = "ascendany_chat";
const LEGACY_SETTINGS_KEY = SETTINGS_BASE_KEY;
const LEGACY_CHAT_KEY = CHAT_BASE_KEY;
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
  localStorage.setItem(LEGACY_CLEANUP_MARK, "1");
}

export async function switchAccountNamespace(accountId: string | null): Promise<void> {
  const settingsStore = useSettingsStore;
  const chatStore = useChatStore;

  const settingsStorageKey = resolveStoreKey(SETTINGS_BASE_KEY, accountId);
  const hasPersistedSettings = localStorage.getItem(settingsStorageKey) !== null;

  settingsStore.persist.setOptions({
    name: settingsStorageKey,
  });

  if (!hasPersistedSettings) {
    settingsStore.getState().resetForAccount();
  }

  await settingsStore.persist.rehydrate();

  chatStore.persist.setOptions({
    name: resolveStoreKey(CHAT_BASE_KEY, accountId),
  });
  chatStore.getState().clearContext();
  await chatStore.persist.rehydrate();
}
