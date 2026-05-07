import { useAuthStore } from "@/stores/authStore";
import { useLayoutStore } from "@/stores/layoutStore";
import { useRecommendationsStore } from "@/stores/recommendationsStore";

interface NodeRefBlockProps {
  point: string;
  label?: string;
}

export function NodeRefBlock({ point, label }: NodeRefBlockProps) {
  const setActiveTab = useLayoutStore((s) => s.setActiveRightPanelTab);
  const openNodeDetail = useRecommendationsStore((s) => s.openNodeDetail);
  const accessToken = useAuthStore((s) => s.accessToken);

  const handleClick = () => {
    setActiveTab("path");
    void openNodeDetail(point, { authToken: accessToken ?? undefined });
  };

  return (
    <button
      type="button"
      className="chat-node-ref-block"
      onClick={handleClick}
      title={`在地图中查看「${point}」`}
    >
      <span className="chat-node-ref-block__icon" aria-hidden>
        ◆
      </span>
      <span className="chat-node-ref-block__label">{label ?? point}</span>
    </button>
  );
}
