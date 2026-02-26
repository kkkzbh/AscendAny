import { TitleBar } from "./TitleBar";
import { SplitPanel } from "./SplitPanel";
import { ChatPanel } from "@/components/chat/ChatPanel";
import { MetricsPanel } from "@/components/metrics/MetricsPanel";
import { useAvatarSync } from "@/hooks/useAvatar";
import { useLayoutStore } from "@/stores/layoutStore";

export function AppLayout() {
  useAvatarSync();
  const isMetricsPanelVisible = useLayoutStore((s) => s.isMetricsPanelVisible);
  return (
    <div className="app-shell flex h-screen w-screen flex-col overflow-hidden">
      <TitleBar />
      <main className="flex-1 overflow-hidden px-[var(--app-gutter-x)] pb-[var(--app-gutter-y)] pt-3 max-[960px]:pt-2">
        <div className="h-full">
          <SplitPanel
            left={<ChatPanel />}
            right={<MetricsPanel />}
            defaultRatio={0.55}
            minRatio={0.3}
            showRightPanel={isMetricsPanelVisible}
          />
        </div>
      </main>
    </div>
  );
}
