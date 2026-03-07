import { useEffect, useState } from "react";
import { ApiError } from "@/lib/api";
import { useAuthStore } from "@/stores/authStore";

function readTokenFromHash(hash: string): string | null {
  const normalized = hash.startsWith("#") ? hash.slice(1) : hash;
  if (!normalized.startsWith("/sso")) {
    return null;
  }
  const queryIndex = normalized.indexOf("?");
  if (queryIndex < 0) {
    return null;
  }
  const params = new URLSearchParams(normalized.slice(queryIndex + 1));
  const token = params.get("token")?.trim();
  return token || null;
}

function resolveErrorMessage(error: unknown): string {
  if (error instanceof ApiError) {
    return error.code ? `${error.message}（${error.code}）` : error.message;
  }
  if (error instanceof Error && error.message.trim()) {
    return error.message;
  }
  return "SSO 登录失败，请返回外部系统后重试。";
}

export function SsoExchangeScreen() {
  const exchangeSsoToken = useAuthStore((s) => s.exchangeSsoToken);
  const [status, setStatus] = useState<"loading" | "error">("loading");
  const [errorText, setErrorText] = useState<string | null>(null);

  useEffect(() => {
    const token = readTokenFromHash(window.location.hash);
    if (!token) {
      setStatus("error");
      setErrorText("缺少 SSO token。");
      return;
    }

    let active = true;
    void exchangeSsoToken(token)
      .then(() => {
        if (!active) {
          return;
        }
        window.history.replaceState(null, "", "/");
      })
      .catch((error) => {
        if (!active) {
          return;
        }
        setStatus("error");
        setErrorText(resolveErrorMessage(error));
      });

    return () => {
      active = false;
    };
  }, [exchangeSsoToken]);

  return (
    <div className="flex h-screen w-screen items-center justify-center bg-[var(--surface-base)] px-6">
      <div className="w-full max-w-md rounded-3xl border border-[var(--border-subtle)] bg-[var(--surface-raised)] px-8 py-9 shadow-[0_18px_60px_rgba(15,23,42,0.16)]">
        <p className="text-[11px] font-semibold tracking-[0.18em] text-[var(--text-soft)] uppercase">
          AscendAny SSO
        </p>
        <h1 className="mt-3 text-2xl font-semibold text-[var(--text-strong)]">
          {status === "loading" ? "正在完成登录..." : "SSO 登录失败"}
        </h1>
        <p className="mt-3 text-sm leading-6 text-[var(--text-soft)]">
          {status === "loading"
            ? "正在验证外部票据并建立 AscendAny 登录态。"
            : errorText ?? "请返回外部系统后重试。"}
        </p>
        {status === "error" && (
          <div className="mt-6 flex flex-wrap gap-3">
            <button
              type="button"
              onClick={() => window.history.back()}
              className="rounded-full bg-[var(--accent-600)] px-5 py-2.5 text-sm font-medium text-white shadow-[0_10px_18px_rgba(3,105,161,0.22)] transition-opacity hover:opacity-90"
            >
              返回外部系统
            </button>
            <button
              type="button"
              onClick={() => window.location.replace("/")}
              className="rounded-full border border-[var(--border-subtle)] bg-[var(--surface-soft)] px-5 py-2.5 text-sm font-medium text-[var(--text-strong)] transition-colors hover:bg-[var(--surface-hover)]"
            >
              回到登录页
            </button>
          </div>
        )}
      </div>
    </div>
  );
}
