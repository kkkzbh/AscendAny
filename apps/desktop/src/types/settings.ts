export type ProviderType = "server_default";
export type ThemeMode = "light" | "dark";
export const ZOOM_PERCENT_MIN = 80;
export const ZOOM_PERCENT_MAX = 130;
export const ZOOM_PERCENT_STEP = 5;
export const DEFAULT_ZOOM_PERCENT = 100;

export interface AppSettings {
  theme: ThemeMode;
  /** True uses solid window background, false enables translucency when OS supports it */
  useOpaqueWindowBackground: boolean;
  /** UI zoom percentage applied to the desktop renderer */
  zoomPercent: number;
  /** Currently selected role id */
  activeRole: string;
}
