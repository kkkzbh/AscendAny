import { useEffect, useState } from "react";
import { findRole, resolveAnyRoleWorkingCard } from "@/types/role";
import { useSettingsStore } from "@/stores/settingsStore";
import { useCustomRoleStore } from "@/stores/customRoleStore";

const STAGE_SWITCH_INTERVAL_MS = 5000;

export function AssistantWorkingCard() {
  const activeRole = useSettingsStore((s) => s.activeRole);
  const customRoles = useCustomRoleStore((s) => s.customRoles);
  const role = findRole(activeRole, customRoles);
  const workingCard = resolveAnyRoleWorkingCard(activeRole, customRoles);
  const title = role.id === "sakiko" ? "小祥输入中" : `${role.name} 正在工作`;

  const stages = workingCard.stages.length > 0 ? workingCard.stages : ["正在处理请求"];
  const [stageIndex, setStageIndex] = useState(0);
  const stageKey = stages.join("|");

  useEffect(() => {
    setStageIndex(0);
  }, [activeRole, stageKey]);

  useEffect(() => {
    if (stages.length <= 1) {
      return;
    }

    const timer = window.setInterval(() => {
      setStageIndex((prev) => (prev + 1) % stages.length);
    }, STAGE_SWITCH_INTERVAL_MS);

    return () => {
      window.clearInterval(timer);
    };
  }, [stageKey, stages.length]);

  return (
    <div className="message-row assistant-working-row flex w-full items-start gap-2.5 py-1.5">
      <img
        src={role.avatarUrl}
        alt={role.name}
        className="mt-1 h-7 w-7 shrink-0 rounded-full object-cover shadow-[0_8px_20px_rgba(3,105,161,0.24)]"
      />

      <div
        className={`assistant-working-card assistant-working-card--${workingCard.variant} max-w-[72%] rounded-[18px]`}
      >
        <p className="assistant-working-title">
          {title}
        </p>
        <p className="assistant-working-stage">{stages[stageIndex]}</p>
        <div className="assistant-working-dots" aria-hidden="true">
          <span />
          <span />
          <span />
        </div>
      </div>
    </div>
  );
}
