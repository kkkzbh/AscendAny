import { create } from "zustand";
import { persist } from "zustand/middleware";
import type { RoleConfig } from "@/types/role";

const CUSTOM_ROLE_STORAGE_KEY = "ascendany_custom_roles_global_v1";
const CUSTOM_ROLE_ID_PREFIX = "custom_role_";

export interface CustomRoleDraft {
  id?: string;
  name: string;
  avatarUrl: string;
  systemPromptExtra: string;
}

interface CustomRoleState {
  customRoles: RoleConfig[];
  saveCustomRole: (draft: CustomRoleDraft) => string;
  removeCustomRole: (roleId: string) => void;
}

function generateCustomRoleId(): string {
  return `${CUSTOM_ROLE_ID_PREFIX}${Date.now()}_${Math.random().toString(36).slice(2, 8)}`;
}

function normalizeCustomRole(source: unknown): RoleConfig | null {
  if (!source || typeof source !== "object") {
    return null;
  }

  const candidate = source as Partial<RoleConfig>;
  const id = typeof candidate.id === "string" ? candidate.id.trim() : "";
  const name = typeof candidate.name === "string" ? candidate.name.trim() : "";
  const avatarUrl =
    typeof candidate.avatarUrl === "string" ? candidate.avatarUrl.trim() : "";
  const systemPromptExtra =
    typeof candidate.systemPromptExtra === "string"
      ? candidate.systemPromptExtra.trim()
      : "";

  if (
    !id ||
    !id.startsWith(CUSTOM_ROLE_ID_PREFIX) ||
    !name ||
    !avatarUrl ||
    !systemPromptExtra
  ) {
    return null;
  }

  return {
    id,
    name,
    avatarUrl,
    systemPromptExtra,
    description: "本地自定义角色",
    workingCard: {
      variant: "violet",
      stages: ["正在读取上下文", "正在结合你的提示词", "正在组织回复"],
    },
  };
}

export const useCustomRoleStore = create<CustomRoleState>()(
  persist(
    (set, get) => ({
      customRoles: [],
      saveCustomRole: (draft) => {
        const normalized: RoleConfig = {
          id: draft.id?.trim() || generateCustomRoleId(),
          name: draft.name.trim(),
          avatarUrl: draft.avatarUrl.trim(),
          systemPromptExtra: draft.systemPromptExtra.trim(),
          description: "本地自定义角色",
          workingCard: {
            variant: "violet",
            stages: ["正在读取上下文", "正在结合你的提示词", "正在组织回复"],
          },
        };
        const current = get().customRoles;
        const next = current.some((role) => role.id === normalized.id)
          ? current.map((role) => (role.id === normalized.id ? normalized : role))
          : [...current, normalized];
        set({ customRoles: next });
        return normalized.id;
      },
      removeCustomRole: (roleId) =>
        set((state) => ({
          customRoles: state.customRoles.filter((role) => role.id !== roleId),
        })),
    }),
    {
      name: CUSTOM_ROLE_STORAGE_KEY,
      partialize: (state) => ({
        customRoles: state.customRoles,
      }),
      merge: (persistedState, currentState) => {
        const persisted = (persistedState ?? {}) as Partial<CustomRoleState>;
        const input = Array.isArray(persisted.customRoles) ? persisted.customRoles : [];
        const normalized = input
          .map((item) => normalizeCustomRole(item))
          .filter((item): item is RoleConfig => item !== null);
        return {
          ...currentState,
          customRoles: normalized,
        };
      },
    },
  ),
);
