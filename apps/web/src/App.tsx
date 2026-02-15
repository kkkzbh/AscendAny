import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import logoImage from "../../../image/LOGO.png";
import desktopScreenshot from "../../../image/主界面.png";
import "./styles.css";

const RELEASE_OWNER = (import.meta.env.VITE_RELEASE_OWNER ?? "kkkzbh").trim() || "kkkzbh";
const RELEASE_REPO = (import.meta.env.VITE_RELEASE_REPO ?? "AscendAny").trim() || "AscendAny";
const RELEASE_API_URL = `https://api.github.com/repos/${encodeURIComponent(RELEASE_OWNER)}/${encodeURIComponent(RELEASE_REPO)}/releases/latest`;
const RELEASE_MANIFEST_URL = `${import.meta.env.BASE_URL}release-assets.json`;

/* ─── Theme Hook (localStorage) ─── */
const STORAGE_KEY = "ascendany-theme";
type Theme = "light" | "dark";

function getInitialTheme(): Theme {
  try {
    const stored = localStorage.getItem(STORAGE_KEY);
    if (stored === "light" || stored === "dark") return stored;
  } catch { /* SSR or blocked storage */ }
  if (typeof window !== "undefined" && window.matchMedia("(prefers-color-scheme: dark)").matches) {
    return "dark";
  }
  return "light";
}

function useTheme() {
  const [theme, setTheme] = useState<Theme>(getInitialTheme);

  useEffect(() => {
    document.documentElement.setAttribute("data-theme", theme);
    try { localStorage.setItem(STORAGE_KEY, theme); } catch { /* noop */ }
  }, [theme]);

  const toggle = useCallback(() => {
    setTheme((prev) => (prev === "light" ? "dark" : "light"));
  }, []);

  return { theme, toggle } as const;
}

/* ─── Scroll Reveal Hook ─── */
function useReveal() {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting) {
            entry.target.classList.add("is-visible");
          }
        }
      },
      { threshold: 0.12, rootMargin: "0px 0px -40px 0px" },
    );
    const targets = el.querySelectorAll(".reveal");
    targets.forEach((t) => observer.observe(t));
    return () => observer.disconnect();
  }, []);
  return ref;
}

/* ─── SVG Icons ─── */
const icons = {
  import: (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round">
      <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
      <polyline points="7 10 12 15 17 10" />
      <line x1="12" y1="15" x2="12" y2="3" />
    </svg>
  ),
  ability: (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round">
      <polygon points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26 12 2" />
    </svg>
  ),
  rating: (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round">
      <polyline points="22 12 18 12 15 21 9 3 6 12 2 12" />
    </svg>
  ),
  ai: (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round">
      <path d="M12 2a4 4 0 0 1 4 4v2a4 4 0 0 1-8 0V6a4 4 0 0 1 4-4z" />
      <path d="M16 14H8a4 4 0 0 0-4 4v2h16v-2a4 4 0 0 0-4-4z" />
      <circle cx="9" cy="7" r="0.5" fill="currentColor" />
      <circle cx="15" cy="7" r="0.5" fill="currentColor" />
    </svg>
  ),
  arrowRight: (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round">
      <line x1="5" y1="12" x2="19" y2="12" />
      <polyline points="12 5 19 12 12 19" />
    </svg>
  ),
  check: (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round">
      <polyline points="20 6 9 17 4 12" />
    </svg>
  ),
  sparkle: (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round">
      <path d="M12 3v1m0 16v1m8.66-13.66l-.71.71M4.05 19.95l-.71.71M21 12h-1M4 12H3m16.95 7.95l-.71-.71M4.05 4.05l-.71-.71" />
      <circle cx="12" cy="12" r="4" />
    </svg>
  ),
  knowledge: (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round">
      <path d="M2 3h6a4 4 0 0 1 4 4v14a3 3 0 0 0-3-3H2z" />
      <path d="M22 3h-6a4 4 0 0 0-4 4v14a3 3 0 0 1 3-3h7z" />
    </svg>
  ),
  accuracy: (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round">
      <circle cx="12" cy="12" r="10" />
      <circle cx="12" cy="12" r="6" />
      <circle cx="12" cy="12" r="2" />
    </svg>
  ),
  quality: (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round">
      <path d="M12 2L2 7l10 5 10-5-10-5z" />
      <path d="M2 17l10 5 10-5" />
      <path d="M2 12l10 5 10-5" />
    </svg>
  ),
  flexibility: (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round">
      <polyline points="16 3 21 3 21 8" />
      <line x1="4" y1="20" x2="21" y2="3" />
      <polyline points="21 16 21 21 16 21" />
      <line x1="15" y1="15" x2="21" y2="21" />
      <line x1="4" y1="4" x2="9" y2="9" />
    </svg>
  ),
  speed: (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round">
      <circle cx="12" cy="12" r="10" />
      <polyline points="12 6 12 12 16 14" />
    </svg>
  ),
  apple: (
    <svg viewBox="0 0 24 24" fill="currentColor">
      <path d="M18.71 19.5c-.83 1.24-1.71 2.45-3.05 2.47-1.34.03-1.77-.79-3.29-.79-1.53 0-2 .77-3.27.82-1.31.05-2.3-1.32-3.14-2.53C4.25 17 2.94 12.45 4.7 9.39c.87-1.52 2.43-2.48 4.12-2.51 1.28-.02 2.5.87 3.29.87.78 0 2.26-1.07 3.8-.91.65.03 2.47.26 3.64 1.98-.09.06-2.17 1.28-2.15 3.81.03 3.02 2.65 4.03 2.68 4.04-.03.07-.42 1.44-1.38 2.83M13 3.5c.73-.83 1.94-1.46 2.94-1.5.13 1.17-.34 2.35-1.04 3.19-.69.85-1.83 1.51-2.95 1.42-.15-1.15.41-2.35 1.05-3.11z" />
    </svg>
  ),
  windows: (
    <svg viewBox="0 0 24 24" fill="currentColor">
      <path d="M3 12V6.75l8-1.25V12H3zm0 .5h8v6.5l-8-1.25V12.5zM11.5 5.35l9.5-1.6V12h-9.5V5.35zM11.5 12.5H21v7.75l-9.5-1.6V12.5z" />
    </svg>
  ),
  linux: (
    <svg viewBox="0 0 24 24" fill="currentColor">
      <path d="M12.504 0c-.155 0-.315.008-.48.021-4.226.333-3.105 4.807-3.17 6.298-.076 1.092-.3 1.953-1.05 3.02-.885 1.051-2.127 2.75-2.716 4.521-.278.832-.41 1.684-.287 2.489a.424.424 0 00-.11.135c-.26.268-.45.6-.663.839-.199.199-.485.267-.797.4-.313.136-.658.269-.864.68-.09.189-.136.394-.132.602 0 .199.027.4.055.536.058.399.116.728.04.97-.249.68-.28 1.145-.106 1.484.174.334.535.47.94.601.81.2 1.91.135 2.774.6.926.466 1.866.67 2.616.47.526-.116.97-.464 1.208-.946.587-.003 1.23-.269 2.26-.334.699-.058 1.574.267 2.577.2.025.134.063.198.114.333l.003.003c.391.778 1.113 1.132 1.884 1.071.771-.06 1.592-.536 2.257-1.306.631-.765 1.683-1.084 2.378-1.503.348-.199.629-.469.649-.853.023-.4-.2-.811-.714-1.376v-.097l-.003-.003c-.17-.2-.25-.535-.338-.926-.085-.401-.182-.786-.492-1.046h-.003c-.059-.054-.123-.067-.188-.135a.357.357 0 00-.19-.064c.431-1.278.264-2.55-.173-3.694-.533-1.41-1.465-2.638-2.175-3.483-.796-1.005-1.576-1.957-1.56-3.368.026-2.152.236-6.133-3.544-6.139zm.529 3.405h.013c.213 0 .396.062.584.198.19.135.33.332.438.533.105.259.158.459.166.724 0-.02.006-.04.006-.06v.105a.086.086 0 01-.004-.021l-.004-.024a1.807 1.807 0 01-.15.706.953.953 0 01-.213.335.71.71 0 00-.088-.042c-.104-.045-.198-.064-.284-.133a1.312 1.312 0 00-.22-.066c.05-.06.146-.133.183-.198.053-.128.082-.264.088-.402v-.02a1.21 1.21 0 00-.061-.4c-.045-.134-.101-.2-.183-.333-.084-.066-.167-.132-.267-.132h-.016c-.093 0-.176.03-.262.132a.8.8 0 00-.205.334 1.18 1.18 0 00-.09.4v.019c.002.089.008.179.02.267-.193-.067-.438-.135-.607-.202a1.635 1.635 0 01-.018-.2v-.02a1.772 1.772 0 01.15-.768c.082-.22.232-.406.43-.533a.985.985 0 01.594-.2zm-2.962.059h.036c.142 0 .27.048.399.135.146.129.264.288.344.465.09.199.14.4.153.667v.004c.007.134.006.2-.002.266v.08c-.03.007-.056.018-.083.024-.152.055-.274.135-.393.2.012-.09.013-.18.003-.267v-.015c-.012-.133-.04-.2-.082-.333a.613.613 0 00-.166-.267.248.248 0 00-.183-.064h-.021c-.071.006-.13.04-.186.132a.552.552 0 00-.12.27.944.944 0 00-.023.33v.015c.012.135.037.2.08.334.046.134.098.2.166.268.01.009.02.018.034.024-.07.057-.117.07-.176.136a.304.304 0 01-.131.068 2.62 2.62 0 01-.275-.402 1.772 1.772 0 01-.155-.667 1.759 1.759 0 01.08-.668 1.43 1.43 0 01.283-.535c.128-.133.26-.2.418-.2zm1.37 1.706c.332 0 .733.065 1.216.399.293.2.523.269 1.052.468h.003c.255.136.405.266.478.399v-.131a.571.571 0 01.016.47c-.123.31-.516.643-1.063.842v.002c-.268.135-.501.333-.775.465-.276.135-.588.292-1.012.267a1.139 1.139 0 01-.448-.067 3.566 3.566 0 01-.322-.198c-.195-.135-.363-.332-.612-.465v-.005h-.005c-.4-.246-.616-.512-.686-.71-.07-.268-.005-.47.193-.6.224-.135.38-.271.483-.336.104-.074.143-.102.176-.131h.002v-.003c.169-.202.436-.47.839-.601.139-.036.294-.065.466-.065zm2.8 2.142c.358 1.417 1.196 3.475 1.735 4.473.286.534.855 1.659 1.102 3.024.156-.005.33.018.513.064.646-1.671-.546-3.467-1.089-3.966-.22-.2-.232-.335-.123-.335.59.534 1.365 1.572 1.646 2.757.13.535.16 1.104.021 1.67.067.028.135.06.205.067 1.032.534 1.413.938 1.23 1.537v-.043c-.06-.003-.12 0-.18 0h-.016c.151-.467-.182-.825-1.065-1.224-.915-.4-1.646-.336-1.77.465-.008.043-.013.066-.018.135-.068.023-.139.053-.209.064-.43.268-.662.669-.793 1.187-.13.533-.17 1.156-.205 1.869v.003c-.02.334-.17.838-.319 1.35-1.5 1.072-3.58 1.538-5.348.334a2.645 2.645 0 00-.402-.533 1.45 1.45 0 00-.275-.333c.182 0 .338-.03.465-.067a.615.615 0 00.314-.334c.108-.267 0-.697-.345-1.163-.345-.467-.931-.995-1.788-1.521-.63-.4-.986-.87-1.15-1.396-.165-.534-.143-1.085-.015-1.645.245-1.07.873-2.11 1.274-2.763.107-.065.037.135-.408.974-.396.751-1.14 2.497-.122 3.854a8.123 8.123 0 01.647-2.876c.564-1.278 1.743-3.504 1.836-5.268.048.036.217.135.289.202.218.133.38.333.59.465.21.201.477.335.876.335.039.003.075.006.11.006.412 0 .73-.134.997-.268.29-.134.52-.334.74-.4h.005c.467-.135.835-.402 1.044-.7zm2.185 8.958c.037.6.343 1.245.882 1.377.588.134 1.434-.333 1.791-.765l.211-.01c.315-.007.577.01.847.268l.003.003c.208.199.305.53.391.876.085.4.154.78.409 1.066.486.527.645.906.636 1.14l.003-.007v.018l-.003-.012c-.015.262-.185.396-.498.595-.63.401-1.746.712-2.457 1.57-.618.737-1.37 1.14-2.036 1.191-.664.053-1.237-.2-1.574-.898l-.005-.003c-.21-.4-.12-1.025.056-1.69.176-.668.428-1.344.463-1.897.037-.714.076-1.335.195-1.814.12-.465.308-.797.641-.984l.045-.022zm-10.814.049h.01c.053 0 .105.005.157.014.376.055.706.333 1.023.752l.91 1.664.003.003c.243.533.754 1.064 1.189 1.637.434.598.77 1.131.729 1.57v.006c-.057.744-.48 1.148-1.125 1.294-.645.135-1.52.002-2.395-.464-.968-.536-2.118-.469-2.857-.602-.369-.066-.61-.2-.723-.4-.11-.2-.113-.602.123-1.23v-.004l.002-.003c.117-.334.03-.752-.027-1.118-.055-.401-.083-.71.043-.94.16-.334.396-.4.69-.533.294-.135.64-.202.915-.47h.002v-.002c.256-.268.445-.601.668-.838.19-.201.38-.336.663-.336zm7.159-9.074c-.435.201-.945.535-1.488.535-.542 0-.97-.267-1.28-.466-.154-.134-.28-.268-.373-.335-.164-.134-.144-.333-.074-.333.109.016.129.134.199.2.096.066.215.2.36.333.292.2.68.467 1.167.467.485 0 1.053-.267 1.398-.466.195-.135.445-.334.648-.467.156-.136.149-.267.279-.267.128.016.034.134-.147.332a8.097 8.097 0 01-.69.468zm-1.082-1.583V5.64c-.006-.02.013-.042.029-.05.074-.043.18-.027.26.004.063 0 .16.067.15.135-.006.049-.085.066-.135.066-.055 0-.092-.043-.141-.068-.052-.018-.146-.008-.163-.065zm-.551 0c-.02.058-.113.049-.166.066-.047.025-.086.068-.14.068-.05 0-.13-.02-.136-.068-.01-.066.088-.133.15-.133.08-.031.184-.047.259-.005.019.009.036.03.03.05v.02h.003z" />
    </svg>
  ),
  android: (
    <svg viewBox="0 0 24 24" fill="currentColor">
      <path d="M6 18c0 .55.45 1 1 1h1v3.5c0 .83.67 1.5 1.5 1.5s1.5-.67 1.5-1.5V19h2v3.5c0 .83.67 1.5 1.5 1.5s1.5-.67 1.5-1.5V19h1c.55 0 1-.45 1-1V8H6v10zM3.5 8C2.67 8 2 8.67 2 9.5v7c0 .83.67 1.5 1.5 1.5S5 17.33 5 16.5v-7C5 8.67 4.33 8 3.5 8zm17 0c-.83 0-1.5.67-1.5 1.5v7c0 .83.67 1.5 1.5 1.5s1.5-.67 1.5-1.5v-7c0-.83-.67-1.5-1.5-1.5zm-4.97-5.84l1.3-1.3c.2-.2.2-.51 0-.71-.2-.2-.51-.2-.71 0l-1.48 1.48C13.85 1.23 12.95 1 12 1c-.96 0-1.86.23-2.66.63L7.85.15c-.2-.2-.51-.2-.71 0-.2.2-.2.51 0 .71l1.31 1.31C6.97 3.26 6 5.01 6 7h12c0-1.99-.97-3.75-2.47-4.84zM10 5H9V4h1v1zm5 0h-1V4h1v1z" />
    </svg>
  ),
  ios: (
    <svg viewBox="0 0 24 24" fill="currentColor">
      <path d="M15.5 1h-8C6.12 1 5 2.12 5 3.5v17C5 21.88 6.12 23 7.5 23h8c1.38 0 2.5-1.12 2.5-2.5v-17C18 2.12 16.88 1 15.5 1zm-4 21c-.83 0-1.5-.67-1.5-1.5s.67-1.5 1.5-1.5 1.5.67 1.5 1.5-.67 1.5-1.5 1.5zm4.5-4H7V4h9v14z" />
    </svg>
  ),
  download: (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round">
      <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
      <polyline points="7 10 12 15 17 10" />
      <line x1="12" y1="15" x2="12" y2="3" />
    </svg>
  ),
  sun: (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round">
      <circle cx="12" cy="12" r="5" />
      <line x1="12" y1="1" x2="12" y2="3" />
      <line x1="12" y1="21" x2="12" y2="23" />
      <line x1="4.22" y1="4.22" x2="5.64" y2="5.64" />
      <line x1="18.36" y1="18.36" x2="19.78" y2="19.78" />
      <line x1="1" y1="12" x2="3" y2="12" />
      <line x1="21" y1="12" x2="23" y2="12" />
      <line x1="4.22" y1="19.78" x2="5.64" y2="18.36" />
      <line x1="18.36" y1="5.64" x2="19.78" y2="4.22" />
    </svg>
  ),
  moon: (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round">
      <path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z" />
    </svg>
  ),
} as const;

/* ─── Data ─── */
type FeatureItem = {
  icon: keyof typeof icons;
  iconClass: string;
  label: string;
  metric: string;
  desc: string;
};

const features: FeatureItem[] = [
  { icon: "import", iconClass: "feature-icon--import", label: "增量导入", metric: "100% 幂等", desc: "新增考试自动识别，重复执行不会重复入库。" },
  { icon: "ability", iconClass: "feature-icon--ability", label: "能力画像", metric: "5 大维度", desc: "知识、准确、质量、灵活、熟练形成多角度评估。" },
  { icon: "rating", iconClass: "feature-icon--rating", label: "评分追踪", metric: "逐场更新", desc: "每场考试都记录 rating 变化，成长趋势清晰可见。" },
  { icon: "ai", iconClass: "feature-icon--ai", label: "智能解读", metric: "自动总结", desc: "AI 给出近期表现洞察和改进方向。" },
];

type AbilityItem = {
  icon: keyof typeof icons;
  iconClass: string;
  name: string;
  subtitle: string;
  desc: string;
};

const abilities: AbilityItem[] = [
  { icon: "knowledge", iconClass: "ability-card-icon--know", name: "知识", subtitle: "掌握程度", desc: "根据通过率和得分率评估知识点掌握，并强调近期表现。" },
  { icon: "accuracy", iconClass: "ability-card-icon--acc", name: "准确", subtitle: "提交效率", desc: "关注 AC 前提交次数，奖励高效解题与稳定正确率。" },
  { icon: "quality", iconClass: "ability-card-icon--qual", name: "质量", subtitle: "解法质量", desc: "结合运行时数据衡量解法质量，支持后续扩展代码风格维度。" },
  { icon: "flexibility", iconClass: "ability-card-icon--flex", name: "灵活", subtitle: "应变能力", desc: "通过切题节奏与卡题时长反映临场策略调整能力。" },
  { icon: "speed", iconClass: "ability-card-icon--speed", name: "熟练", subtitle: "解题速度", desc: "比较同场解题耗时，直观看出学生的速度与熟练度。" },
];

type WorkflowItem = { title: string; desc: string };
const workflow: WorkflowItem[] = [
  { title: "扫描新增考试", desc: "自动识别新目录与快照，兼容多种数据编码。" },
  { title: "标准化入库", desc: "以唯一键与行指纹保证幂等导入。" },
  { title: "计算能力与 rating", desc: "产出五维指标和每场考试的评分变化。" },
  { title: "生成 AI 洞察", desc: "面向老师和学生提供可执行的学习建议。" },
];

type DownloadStatus = "available" | "soon" | "later";
type DownloadTarget = "macos" | "windows" | "linux" | "android" | "ios";
type DownloadItem = {
  target: DownloadTarget;
  platform: string;
  icon: keyof typeof icons;
  pkg: string;
  arch: string;
  status: DownloadStatus;
  action: string;
  href?: string;
};

type GithubReleaseAsset = {
  name?: string;
  browser_download_url?: string;
};

type GithubLatestRelease = {
  assets?: GithubReleaseAsset[];
};

type ReleaseFetchResult = {
  status: number;
  assets: GithubReleaseAsset[];
};

const defaultDownloads: DownloadItem[] = [
  { target: "linux", platform: "Linux", icon: "linux", pkg: "RPM", arch: "x64", status: "soon", action: "暂无资源" },
  { target: "windows", platform: "Windows", icon: "windows", pkg: "EXE", arch: "x64", status: "soon", action: "暂无资源" },
  { target: "android", platform: "Android", icon: "android", pkg: "APK", arch: "Mobile", status: "soon", action: "即将支持" },
  { target: "ios", platform: "iOS", icon: "ios", pkg: "TestFlight / App Store", arch: "Mobile", status: "later", action: "敬请期待" },
  { target: "macos", platform: "macOS", icon: "apple", pkg: "DMG", arch: "Apple Silicon / Intel", status: "later", action: "敬请期待" },
];

const statusLabel: Record<DownloadStatus, string> = {
  available: "已发布",
  soon: "即将支持",
  later: "敬请期待",
};

function getAssetLink(
  assets: GithubReleaseAsset[],
  predicate: (name: string) => boolean,
): string | undefined {
  const hit = assets.find((asset) => {
    const name = asset.name?.toLowerCase();
    return typeof name === "string" && predicate(name);
  });
  return hit?.browser_download_url;
}

function resolveDownloads(assets: GithubReleaseAsset[]): DownloadItem[] {
  const hasX64Alias = (name: string) =>
    name.includes("x64") || name.includes("amd64") || name.includes("x86_64");

  const windowsHref = getAssetLink(
    assets,
    (name) =>
      name.endsWith(".exe")
      && (name.includes("win") || name.includes("windows"))
      && hasX64Alias(name)
      && !name.includes("elevate"),
  );
  const linuxHref = getAssetLink(
    assets,
    (name) => name.endsWith(".rpm") && hasX64Alias(name),
  );

  return defaultDownloads.map((item) => {
    if (item.target === "windows" && windowsHref) {
      return { ...item, status: "available", action: "立即下载", href: windowsHref };
    }
    if (item.target === "linux" && linuxHref) {
      return { ...item, status: "available", action: "立即下载", href: linuxHref };
    }
    return item;
  });
}

async function fetchReleaseAssets(
  url: string,
  signal: AbortSignal,
  init?: Omit<RequestInit, "signal">,
): Promise<ReleaseFetchResult> {
  const response = await fetch(url, {
    ...init,
    signal,
  });
  if (!response.ok) {
    return { status: response.status, assets: [] };
  }

  const payload = (await response.json()) as GithubLatestRelease;
  if (!Array.isArray(payload.assets)) {
    return { status: response.status, assets: [] };
  }
  return { status: response.status, assets: payload.assets };
}

/* ─── Small Components ─── */
function Icon({ name, className }: { name: keyof typeof icons; className?: string }) {
  return <span className={className}>{icons[name]}</span>;
}

function SectionHeader({ label, title, desc }: { label: string; title: string; desc?: string }) {
  return (
    <div>
      <p className="section-label">{label}</p>
      <h2 className="section-title">{title}</h2>
      {desc && <p className="section-desc">{desc}</p>}
    </div>
  );
}

function RevealGroup({ children }: { children: ReactNode }) {
  const ref = useReveal();
  return <div ref={ref}>{children}</div>;
}

/* ─── App ─── */
export default function App() {
  const { theme, toggle } = useTheme();
  const [downloads, setDownloads] = useState<DownloadItem[]>(defaultDownloads);

  useEffect(() => {
    const controller = new AbortController();

    async function loadReleaseAssets() {
      try {
        const manifest = await fetchReleaseAssets(RELEASE_MANIFEST_URL, controller.signal, {
          cache: "no-store",
        });
        if (manifest.assets.length > 0) {
          setDownloads(resolveDownloads(manifest.assets));
          return;
        }

        const api = await fetchReleaseAssets(RELEASE_API_URL, controller.signal, {
          headers: {
            Accept: "application/vnd.github+json",
          },
        });
        if (api.assets.length > 0) {
          setDownloads(resolveDownloads(api.assets));
          return;
        }

        if (api.status === 403) {
          console.warn("[AscendAny] GitHub API rate limited; release assets are temporarily unavailable.");
        }
      } catch (error) {
        if ((error as Error).name !== "AbortError") {
          console.warn("[AscendAny] Failed to load release assets.", error);
        }
      }
    }

    void loadReleaseAssets();
    return () => {
      controller.abort();
    };
  }, []);

  return (
    <div className="site-shell" id="top">
      {/* Background layer */}
      <div className="site-bg" aria-hidden="true">
        <div className="site-bg-orb site-bg-orb--1" />
        <div className="site-bg-orb site-bg-orb--2" />
        <div className="site-bg-orb site-bg-orb--3" />
        <div className="site-bg-mesh" />
        <div className="site-bg-grid" />
      </div>

      <div className="site-content">
        {/* ── Header ── */}
        <header className="site-header">
          <a className="site-brand" href="#top">
            <img src={logoImage} alt="AscendAny" />
            <span>AscendAny</span>
          </a>

          <nav className="site-nav" aria-label="导航">
            <a href="#features">能力亮点</a>
            <a href="#workflow">工作流</a>
            <a href="#download">下载</a>
          </nav>

          <div className="site-header-right">
            <button
              className="theme-toggle"
              onClick={toggle}
              aria-label={theme === "light" ? "切换到暗色模式" : "切换到亮色模式"}
              title={theme === "light" ? "暗色模式" : "亮色模式"}
            >
              {theme === "light" ? icons.moon : icons.sun}
            </button>
            <a className="btn-cta" href="#download">
              获取版本
              <Icon name="arrowRight" />
            </a>
          </div>
        </header>

        <main className="site-main">
          {/* ── Hero ── */}
          <RevealGroup>
            <section className="hero reveal">
              <div className="hero-copy">
                <div className="hero-badge">
                  <span className="hero-badge-dot" />
                  学生能力分析平台
                </div>

                <h1>把考试数据转化为可解释的成长洞察</h1>

                <p className="hero-subtitle">
                  AscendAny 支持增量导入、五维能力评分和 rating 追踪，帮助教师快速定位问题、帮助学生明确提升方向。
                </p>

                <div className="hero-actions">
                  <a className="hero-btn-primary" href="#download">
                    查看下载方式
                    <Icon name="arrowRight" />
                  </a>
                  <a className="hero-btn-secondary" href="#features">
                    了解产品能力
                  </a>
                </div>

                <div className="hero-tags">
                  <span className="hero-tag">
                    <Icon name="check" />
                    增量幂等导入
                  </span>
                  <span className="hero-tag">
                    <Icon name="check" />
                    五维能力模型
                  </span>
                  <span className="hero-tag">
                    <Icon name="check" />
                    AI 智能分析
                  </span>
                </div>
              </div>

              <div className="hero-visual">
                <div className="hero-visual-frame">
                  <img src={desktopScreenshot} alt="AscendAny 应用主界面" />
                </div>

                <div className="hero-floating-card">
                  <div className="hero-floating-icon">
                    <Icon name="sparkle" />
                  </div>
                  <strong>本周洞察</strong>
                  <p>班级近三场考试平均准确度提升 9.4%。</p>
                </div>

                <div className="hero-stats">
                  <div className="hero-stat">
                    <strong>5</strong>
                    <span>能力维度</span>
                  </div>
                  <div className="hero-stat">
                    <strong>100%</strong>
                    <span>幂等导入</span>
                  </div>
                </div>
              </div>
            </section>
          </RevealGroup>

          {/* ── Features ── */}
          <RevealGroup>
            <section id="features">
              <div className="feature-grid">
                {features.map((f, i) => (
                  <article className={`feature-card reveal reveal-delay-${i + 1}`} key={f.label}>
                    <div className={`feature-icon ${f.iconClass}`}>
                      {icons[f.icon]}
                    </div>
                    <span className="feature-label">{f.label}</span>
                    <strong className="feature-metric">{f.metric}</strong>
                    <span className="feature-desc">{f.desc}</span>
                  </article>
                ))}
              </div>
            </section>
          </RevealGroup>

          {/* ── Ability Model ── */}
          <RevealGroup>
            <section className="panel reveal">
              <SectionHeader
                label="能力模型"
                title="五个维度，完整呈现近期学习表现"
                desc="基于考试数据自动计算五个核心维度的能力评分，为每位学生形成全面的能力画像。"
              />
              <div className="ability-grid">
                {abilities.map((a) => (
                  <article className="ability-card" key={a.name}>
                    <div className={`ability-card-icon ${a.iconClass}`}>
                      {icons[a.icon]}
                    </div>
                    <div>
                      <p className="ability-name">{a.name}</p>
                      <span className="ability-sub">{a.subtitle}</span>
                    </div>
                    <span className="ability-desc">{a.desc}</span>
                  </article>
                ))}
              </div>
            </section>
          </RevealGroup>

          {/* ── Workflow ── */}
          <RevealGroup>
            <section className="panel reveal" id="workflow">
              <SectionHeader
                label="执行流程"
                title="从新增考试到 AI 建议，全链路自动完成"
                desc="四步流水线，无需手动干预，只需放入新的考试数据即可获得完整分析结果。"
              />
              <div className="workflow-grid">
                {workflow.map((w, i) => (
                  <article className="workflow-card" key={w.title}>
                    <span className="workflow-step">{i + 1}</span>
                    <h3 className="workflow-title">{w.title}</h3>
                    <p className="workflow-desc">{w.desc}</p>
                  </article>
                ))}
              </div>
            </section>
          </RevealGroup>

          {/* ── Download ── */}
          <RevealGroup>
            <section className="panel reveal" id="download">
              <SectionHeader
                label="下载中心"
                title="多平台覆盖，统一体验"
              />
              <div className="download-grid">
                {downloads.map((d) => (
                  <article className="download-card" key={d.platform}>
                    <div className="download-card-head">
                      <div className="download-platform-icon">
                        {icons[d.icon]}
                      </div>
                      <span className={`download-status status-${d.status}`}>
                        {statusLabel[d.status]}
                      </span>
                    </div>
                    <strong className="download-platform">{d.platform}</strong>
                    <span className="download-arch">{d.arch}</span>
                    <span className="download-pkg">{d.pkg}</span>
                    {d.status === "available" && d.href ? (
                      <a
                        className="download-btn download-btn--active"
                        href={d.href}
                        target="_blank"
                        rel="noreferrer"
                      >
                        {d.action}
                      </a>
                    ) : (
                      <span className="download-btn download-btn--disabled">{d.action}</span>
                    )}
                  </article>
                ))}
              </div>
              <p className="download-note" id="notify">
                Windows EXE 与 Linux RPM (x64) 会在 GitHub Releases 发布后自动开放下载；Android 即将支持，macOS 与 iOS 敬请期待。
              </p>
            </section>
          </RevealGroup>
        </main>

        {/* ── Footer ── */}
        <footer className="site-footer">
          <div className="footer-brand">
            <span>AscendAny</span>
            <span className="footer-sep" />
            <span>学生能力分析平台</span>
          </div>
          <div className="footer-links">
            <a href="#features">产品能力</a>
            <a href="#workflow">工作流</a>
            <a href="#top">回到顶部</a>
          </div>
        </footer>
      </div>
    </div>
  );
}
