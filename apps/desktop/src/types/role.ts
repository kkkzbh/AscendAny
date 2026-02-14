import xiaoDAvatarUrl from "@/assets/role-xiaoD.svg";

export interface RoleConfig {
  /** 角色唯一标识 */
  id: string;
  /** 显示名称 */
  name: string;
  /** 角色简介 */
  description: string;
  /** 头像 URL（由 Vite 处理的 import） */
  avatarUrl: string;
  /** 额外角色风格 prompt，默认角色留空 */
  systemPromptExtra: string;
}

export const DEFAULT_ROLE_ID = "xiaoD";

export const BUILT_IN_ROLES: RoleConfig[] = [
  {
    id: "xiaoD",
    name: "小D",
    description: "默认助手，客观专业地分析你的学习数据",
    avatarUrl: xiaoDAvatarUrl,
    systemPromptExtra: "",
  },
];

export function findRole(roleId: string): RoleConfig {
  return BUILT_IN_ROLES.find((r) => r.id === roleId) ?? BUILT_IN_ROLES[0]!;
}
