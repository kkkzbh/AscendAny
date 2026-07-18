import { findRole, resolveAnyRoleWorkingCard } from "@/types/role";
import { useSettingsStore } from "@/stores/settingsStore";
import { useCustomRoleStore } from "@/stores/customRoleStore";

export function AssistantWorkingCard() {
  const activeRole = useSettingsStore((s) => s.activeRole);
  const customRoles = useCustomRoleStore((s) => s.customRoles);
  const role = findRole(activeRole, customRoles);
  const workingCard = resolveAnyRoleWorkingCard(activeRole, customRoles);
  const title = role.id === "sakiko"
    ? "小祥整理中"
    : role.id === "xiaoD"
      ? "小D分析中"
      : `${role.name}思考中`;

  return (
    <div className="message-row assistant-working-row flex w-full items-start gap-2.5 py-1.5">
      <img
        src={role.avatarUrl}
        alt={role.name}
        className="assistant-avatar mt-1 h-8 w-8 shrink-0 rounded-full object-cover"
      />

      <div
        className={`assistant-working-card assistant-working-card--${workingCard.variant} max-w-[72%]`}
      >
        <p className="assistant-working-title">
          {title}
        </p>
        <div className="assistant-working-dots" aria-hidden="true">
          <span />
          <span />
          <span />
        </div>
      </div>
    </div>
  );
}
