import { create } from "zustand";

interface AvatarState {
  avatarUrl: string | null;
  loading: boolean;
  /** Load avatar from local filesystem for the given account. */
  loadAvatar: (accountId: string) => Promise<void>;
  /** Save cropped avatar and update in-memory URL. */
  saveAvatar: (accountId: string, base64Data: string) => Promise<boolean>;
  /** Delete avatar from filesystem and clear in-memory URL. */
  deleteAvatar: (accountId: string) => Promise<boolean>;
  /** Clear in-memory avatar (e.g. on logout). */
  clear: () => void;
}

export const useAvatarStore = create<AvatarState>()((set) => ({
  avatarUrl: null,
  loading: false,

  loadAvatar: async (accountId: string) => {
    const api = window.electronAPI;
    if (!api?.avatarRead) {
      set({ avatarUrl: null, loading: false });
      return;
    }

    set({ loading: true });
    try {
      const dataUrl = await api.avatarRead(accountId);
      set({ avatarUrl: dataUrl, loading: false });
    } catch {
      set({ avatarUrl: null, loading: false });
    }
  },

  saveAvatar: async (accountId: string, base64Data: string) => {
    const api = window.electronAPI;
    if (!api?.avatarSave) return false;

    try {
      const ok = await api.avatarSave(accountId, base64Data);
      if (ok) {
        set({
          avatarUrl: base64Data.startsWith("data:")
            ? base64Data
            : `data:image/png;base64,${base64Data}`,
        });
      }
      return ok;
    } catch {
      return false;
    }
  },

  deleteAvatar: async (accountId: string) => {
    const api = window.electronAPI;
    if (!api?.avatarDelete) return false;

    try {
      const ok = await api.avatarDelete(accountId);
      if (ok) set({ avatarUrl: null });
      return ok;
    } catch {
      return false;
    }
  },

  clear: () => set({ avatarUrl: null, loading: false }),
}));
