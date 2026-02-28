import { useEffect, useRef } from "react";
import { AuthScreen } from "@/components/auth/AuthScreen";
import { AppLayout } from "@/components/layout/AppLayout";
import { SettingsDialog } from "@/components/settings/SettingsDialog";
import { FeedbackWindow } from "@/components/feedback/FeedbackWindow";
import { UpdateFlowDialog } from "@/components/updater/UpdateFlowDialog";
import { fetchModelProviders } from "@/lib/api";
import { useAuthStore } from "@/stores/authStore";
import { useSettingsStore } from "@/stores/settingsStore";

type ThemeMode = "light" | "dark";
const THEME_TRANSITION_MS = 280;

export default function App() {
  const themeTransitionTimerRef = useRef<number | null>(null);
  const isFeedbackMode = window.location.hash.startsWith("#/feedback");
  const theme = useSettingsStore((s) => s.theme);
  const useOpaqueWindowBackground = useSettingsStore((s) => s.useOpaqueWindowBackground);
  const setOpaqueWindowBackground = useSettingsStore((s) => s.setOpaqueWindowBackground);
  const zoomPercent = useSettingsStore((s) => s.zoomPercent);
  const syncProviderOptions = useSettingsStore((s) => s.syncProviderOptions);
  const authStatus = useAuthStore((s) => s.status);
  const bootstrap = useAuthStore((s) => s.bootstrap);

  useEffect(() => {
    const root = document.documentElement;
    const clearTransitionState = () => {
      if (themeTransitionTimerRef.current !== null) {
        window.clearTimeout(themeTransitionTimerRef.current);
        themeTransitionTimerRef.current = null;
      }
      root.classList.remove("theme-switching");
    };
    const applyTheme = (mode: ThemeMode) => {
      root.setAttribute("data-theme", mode);
      root.style.colorScheme = mode;
    };

    const currentThemeAttr = root.getAttribute("data-theme");
    const currentTheme: ThemeMode = currentThemeAttr === "dark" ? "dark" : "light";

    if (!currentThemeAttr) {
      clearTransitionState();
      applyTheme(theme);
      return;
    }

    if (currentTheme === theme) {
      clearTransitionState();
      applyTheme(theme);
      return;
    }

    clearTransitionState();
    root.classList.add("theme-switching");
    applyTheme(theme);
    const prefersReducedMotion = window.matchMedia?.("(prefers-reduced-motion: reduce)").matches ?? false;

    if (prefersReducedMotion) {
      root.classList.remove("theme-switching");
      return;
    }

    themeTransitionTimerRef.current = window.setTimeout(() => {
      root.classList.remove("theme-switching");
      themeTransitionTimerRef.current = null;
    }, THEME_TRANSITION_MS);

    return () => {
      clearTransitionState();
    };
  }, [theme]);

  useEffect(() => {
    const root = document.documentElement;
    root.setAttribute("data-opaque-window", useOpaqueWindowBackground ? "true" : "false");
  }, [useOpaqueWindowBackground]);

  useEffect(() => {
    if (isFeedbackMode) {
      return;
    }
    const factor = zoomPercent / 100;
    const api = window.electronAPI;
    if (api?.setZoomFactor) {
      void api.setZoomFactor(factor);
      return;
    }
    // Fallback for browser/test environments without Electron bridge.
    document.documentElement.style.zoom = `${zoomPercent}%`;
  }, [isFeedbackMode, zoomPercent]);

  useEffect(() => {
    const api = window.electronAPI;
    if (!api?.getOpaqueWindowBackground) {
      return;
    }
    void api.getOpaqueWindowBackground().then((value) => {
      setOpaqueWindowBackground(value);
    }).catch(() => {
      // Keep local persisted value when IPC is unavailable.
    });
  }, [setOpaqueWindowBackground]);

  useEffect(() => {
    if (isFeedbackMode) {
      return;
    }
    void bootstrap();
  }, [bootstrap, isFeedbackMode]);

  useEffect(() => {
    if (isFeedbackMode) {
      return;
    }
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
  }, [authStatus, isFeedbackMode, syncProviderOptions]);

  if (isFeedbackMode) {
    return <FeedbackWindow />;
  }

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
      <UpdateFlowDialog />
    </>
  );
}
