import xiaoDAvatarUrl from "@/assets/role-xiaoD.svg";
import sakikoAvatarUrl from "@/assets/role-sakiko.png";

export type WorkingCardVariant = "ocean" | "amber" | "violet";

export interface RoleWorkingCardConfig {
  /** 工作卡片主题 */
  variant: WorkingCardVariant;
  /** 工作阶段文案（按顺序轮播） */
  stages: string[];
}

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
  /** 角色专属工作卡片配置 */
  workingCard?: RoleWorkingCardConfig;
}

export const DEFAULT_ROLE_ID = "xiaoD";

export const BUILT_IN_ROLES: RoleConfig[] = [
  {
    id: "xiaoD",
    name: "小D",
    description: "默认助手，客观专业地分析你的学习数据",
    avatarUrl: xiaoDAvatarUrl,
    systemPromptExtra: "",
    workingCard: {
      variant: "ocean",
      stages: ["正在读取上下文", "正在分析学习数据", "正在组织回复"],
    },
  },
  {
    id: "sakiko",
    name: "丰川祥子（Sakiko）",
    description: "Ave Mujica键盘手兼队长，抽时间也打打算法竞赛",
    avatarUrl: sakikoAvatarUrl,
    systemPromptExtra: "",
    workingCard: {
      variant: "amber",
      stages: ["正在整理思路", "正在对照考试数据", "正在给出行动建议"],
    },
  },
];

export function getAllRoles(customRoles: RoleConfig[] = []): RoleConfig[] {
  return [...BUILT_IN_ROLES, ...customRoles];
}

export function findRole(
  roleId: string,
  customRoles: RoleConfig[] = [],
): RoleConfig {
  return getAllRoles(customRoles).find((r) => r.id === roleId) ?? BUILT_IN_ROLES[0]!;
}

const FALLBACK_WORKING_CARD: RoleWorkingCardConfig = {
  variant: "ocean",
  stages: ["正在读取上下文", "正在分析学习数据", "正在组织回复"],
};

export function resolveRoleWorkingCard(roleId: string): RoleWorkingCardConfig {
  const defaultRole = BUILT_IN_ROLES.find((r) => r.id === DEFAULT_ROLE_ID);
  const targetRole = BUILT_IN_ROLES.find((r) => r.id === roleId);
  const candidate = targetRole?.workingCard ?? defaultRole?.workingCard;

  if (!candidate) {
    return FALLBACK_WORKING_CARD;
  }

  const stages = candidate.stages
    .map((stage) => stage.trim())
    .filter((stage) => stage.length > 0);

  return {
    variant: candidate.variant,
    stages: stages.length > 0 ? stages : FALLBACK_WORKING_CARD.stages,
  };
}

export function resolveAnyRoleWorkingCard(
  roleId: string,
  customRoles: RoleConfig[] = [],
): RoleWorkingCardConfig {
  const targetRole = getAllRoles(customRoles).find((r) => r.id === roleId);
  const candidate = targetRole?.workingCard ?? resolveRoleWorkingCard(DEFAULT_ROLE_ID);

  const stages = candidate.stages
    .map((stage) => stage.trim())
    .filter((stage) => stage.length > 0);

  return {
    variant: candidate.variant,
    stages: stages.length > 0 ? stages : FALLBACK_WORKING_CARD.stages,
  };
}
