import { useEffect } from "react";
import { AuthScreen } from "@/components/auth/AuthScreen";
import { AppLayout } from "@/components/layout/AppLayout";
import { SettingsDialog } from "@/components/settings/SettingsDialog";
import { fetchModelProviders } from "@/lib/api";
import { useAuthStore } from "@/stores/authStore";
import { useSettingsStore } from "@/stores/settingsStore";

export default function App() {
  const theme = useSettingsStore((s) => s.theme);
  const syncProviderOptions = useSettingsStore((s) => s.syncProviderOptions);
  const authStatus = useAuthStore((s) => s.status);
  const bootstrap = useAuthStore((s) => s.bootstrap);

  useEffect(() => {
    const root = document.documentElement;
    root.setAttribute("data-theme", theme);
    root.style.colorScheme = theme;
  }, [theme]);

  useEffect(() => {
    void bootstrap();
  }, [bootstrap]);

  useEffect(() => {
    if (authStatus !== "authenticated") {
      return;
    }

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
  }, [authStatus, syncProviderOptions]);

  if (authStatus === "booting") {
    return (
      <div className="flex h-screen w-screen items-center justify-center text-sm text-[var(--text-soft)]">
        启动中...
      </div>
    );
  }

  if (authStatus !== "authenticated") {
    return <AuthScreen />;
  }

  return (
    <>
      <AppLayout />
      <SettingsDialog />
    </>
  );
}
