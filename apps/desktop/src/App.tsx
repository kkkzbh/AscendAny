import { useEffect, useRef, useState } from "react";
import { AuthScreen } from "@/components/auth/AuthScreen";
import { SsoExchangeScreen } from "@/components/auth/SsoExchangeScreen";
import { AppLayout } from "@/components/layout/AppLayout";
import { UpdateFlowDialog } from "@/components/updater/UpdateFlowDialog";
import { useAuthStore } from "@/stores/authStore";
import { hydrateLocalStateFromDesktop } from "@/stores/localStateHydration";
import { useSettingsStore } from "@/stores/settingsStore";

type ThemeMode = "light" | "dark";
const THEME_TRANSITION_MS = 280;

export default function App() {
  const themeTransitionTimerRef = useRef<number | null>(null);
  const isSsoMode = window.location.hash.startsWith("#/sso");
  const [localStateReady, setLocalStateReady] = useState(false);
  const theme = useSettingsStore((s) => s.theme);
  const useOpaqueSidebarBackground = useSettingsStore((s) => s.useOpaqueSidebarBackground);
  const zoomPercent = useSettingsStore((s) => s.zoomPercent);
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
    root.setAttribute("data-opaque-sidebar", useOpaqueSidebarBackground ? "true" : "false");
  }, [useOpaqueSidebarBackground]);

  useEffect(() => {
    const factor = zoomPercent / 100;
    const api = window.electronAPI;
    if (api?.setZoomFactor) {
      void api.setZoomFactor(factor);
      return;
    }
    // Fallback for browser/test environments without Electron bridge.
    document.documentElement.style.zoom = `${zoomPercent}%`;
  }, [zoomPercent]);

  useEffect(() => {
    if (isSsoMode) {
      setLocalStateReady(true);
      return;
    }
    let cancelled = false;
    void hydrateLocalStateFromDesktop().finally(() => {
      if (!cancelled) {
        setLocalStateReady(true);
      }
    });
    return () => {
      cancelled = true;
    };
  }, [isSsoMode]);

  useEffect(() => {
    if (isSsoMode || !localStateReady) {
      return;
    }
    void bootstrap();
  }, [bootstrap, isSsoMode, localStateReady]);

  if (isSsoMode) {
    return <SsoExchangeScreen />;
  }

  if (authStatus === "booting") {
    return (
      <div className="flex h-screen w-screen items-center justify-center bg-[var(--surface-base)] text-sm text-[var(--text-soft)]">
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
      <UpdateFlowDialog />
    </>
  );
}
