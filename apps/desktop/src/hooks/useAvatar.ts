import { useEffect } from "react";
import { useAuthStore } from "@/stores/authStore";
import { useAvatarStore } from "@/stores/avatarStore";

/**
 * Call this once near the app root (e.g. in AppLayout) to sync
 * the avatar store with the current auth account.
 *
 * - When the user logs in  → loads their avatar from the filesystem.
 * - When the user logs out → clears the in-memory avatar.
 */
export function useAvatarSync() {
  const accountId = useAuthStore((s) => s.account?.accountId ?? null);
  const loadAvatar = useAvatarStore((s) => s.loadAvatar);
  const clear = useAvatarStore((s) => s.clear);

  useEffect(() => {
    if (accountId) {
      void loadAvatar(accountId);
    } else {
      clear();
    }
  }, [accountId, loadAvatar, clear]);
}
