import { useEffect, useRef } from "react";
import { storage } from "@/lib/storage";

/**
 * Hook that checks if this is the first app launch today and if new exams
 * have been imported since the last auto-analysis.
 *
 * Currently a stub -- will connect to the FastAPI backend when available.
 */
export function useAutoAnalysis(onTrigger: (prompt: string) => void) {
  const triggered = useRef(false);

  useEffect(() => {
    if (triggered.current) return;

    const today = new Date().toISOString().slice(0, 10);
    const lastAnalysis = storage.get<string>("last_auto_analysis_date", "");

    if (lastAnalysis === today) return;

    // TODO: Call GET /meta/latest_exam_imported_at and compare with
    // storage.get("last_auto_analysis_at") to see if there are new exams.
    // For now, auto-analysis is skipped until the API is available.
    const hasNewExams = false;

    if (hasNewExams) {
      triggered.current = true;
      storage.set("last_auto_analysis_date", today);
      storage.set("last_auto_analysis_at", new Date().toISOString());
      onTrigger(
        "请分析我最近几场考试的表现，总结能力变化趋势和需要重点提升的方向。",
      );
    }
  }, [onTrigger]);
}
