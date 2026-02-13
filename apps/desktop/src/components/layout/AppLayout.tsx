import { TitleBar } from "./TitleBar";
import { SplitPanel } from "./SplitPanel";
import { ChatPanel } from "@/components/chat/ChatPanel";
import { MetricsPanel } from "@/components/metrics/MetricsPanel";

export function AppLayout() {
  return (
    <div className="app-shell flex h-screen w-screen flex-col overflow-hidden">
      <TitleBar />
      <main className="flex-1 overflow-hidden px-[var(--app-gutter)] pb-[var(--app-gutter)] pt-3 max-[960px]:pt-2">
        <div className="h-full">
          <SplitPanel
            left={<ChatPanel />}
            right={<MetricsPanel />}
            defaultRatio={0.55}
            minRatio={0.3}
          />
        </div>
      </main>
    </div>
  );
}
