import { useEffect } from "react";
import { AppLayout } from "@/components/layout/AppLayout";
import { SettingsDialog } from "@/components/settings/SettingsDialog";
import { fetchModelProviders } from "@/lib/api";
import { useSettingsStore } from "@/stores/settingsStore";

export default function App() {
  const theme = useSettingsStore((s) => s.theme);
  const syncProviderOptions = useSettingsStore((s) => s.syncProviderOptions);

  useEffect(() => {
    const root = document.documentElement;
    root.setAttribute("data-theme", theme);
    root.style.colorScheme = theme;
  }, [theme]);

  useEffect(() => {
    let cancelled = false;

    async function loadProviderOptions() {
      try {
        const payload = await fetchModelProviders();
        if (cancelled) {
          return;
        }
        syncProviderOptions(payload);
      } catch (error) {
        console.warn("[AscendAny] Failed to load model provider options:", error);
      }
    }

    void loadProviderOptions();
    return () => {
      cancelled = true;
    };
  }, [syncProviderOptions]);

  return (
    <>
      <AppLayout />
      <SettingsDialog />
    </>
  );
}
