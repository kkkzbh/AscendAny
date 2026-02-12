import { TitleBar } from "./TitleBar";
import { SplitPanel } from "./SplitPanel";
import { ChatPanel } from "@/components/chat/ChatPanel";
import { MetricsPanel } from "@/components/metrics/MetricsPanel";

export function AppLayout() {
  return (
    <div className="flex h-screen w-screen flex-col overflow-hidden">
      <TitleBar />
      <main className="flex-1 overflow-hidden p-3 pt-2">
        <SplitPanel
          left={<ChatPanel />}
          right={<MetricsPanel />}
          defaultRatio={0.55}
          minRatio={0.3}
        />
      </main>
    </div>
  );
}
