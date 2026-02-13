import { useEffect, useRef, type ReactNode } from "react";
import logoImage from "../../../image/LOGO.png";
import desktopScreenshot from "../../../image/主界面.png";
import "./styles.css";

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
      <path d="M12.5 2c-1.6 0-2.9 1.8-2.9 4 0 .9.2 1.7.5 2.4-1.1.5-1.9 1.4-1.9 2.5 0 .8.4 1.5 1 2-.6.4-1 1-1 1.7 0 .5.2 1 .5 1.4-.5.4-.9 1.1-.9 1.8 0 1.4 1.5 2.5 3.4 2.5.7 0 1.3-.1 1.8-.4.5.3 1.1.4 1.8.4 1.9 0 3.4-1.1 3.4-2.5 0-.7-.3-1.4-.9-1.8.3-.4.5-.9.5-1.4 0-.7-.4-1.3-1-1.7.6-.5 1-1.2 1-2 0-1.1-.8-2-1.9-2.5.3-.7.5-1.5.5-2.4 0-2.2-1.3-4-2.9-4zm-2 14.5c0-.5.9-1 2-1s2 .5 2 1-.9 1-2 1-2-.5-2-1z" />
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

type DownloadStatus = "beta" | "soon";
type DownloadItem = {
  platform: string;
  icon: keyof typeof icons;
  pkg: string;
  arch: string;
  status: DownloadStatus;
  action: string;
  href?: string;
};

const downloads: DownloadItem[] = [
  { platform: "macOS", icon: "apple", pkg: "DMG", arch: "Apple Silicon / Intel", status: "beta", action: "申请内测", href: "#notify" },
  { platform: "Windows", icon: "windows", pkg: "EXE / MSI", arch: "x64", status: "soon", action: "即将上线" },
  { platform: "Linux", icon: "linux", pkg: "AppImage / DEB / RPM", arch: "x64 / ARM64", status: "soon", action: "即将上线" },
  { platform: "Android", icon: "android", pkg: "APK", arch: "Mobile", status: "soon", action: "即将上线" },
  { platform: "iOS", icon: "ios", pkg: "TestFlight / App Store", arch: "Mobile", status: "soon", action: "即将上线" },
];

const statusLabel: Record<DownloadStatus, string> = { beta: "内测开放", soon: "即将上线" };

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

          <a className="btn-cta" href="#download">
            获取版本
            <Icon name="arrowRight" />
          </a>
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
                    {d.status === "soon" ? (
                      <span className="download-btn download-btn--disabled">{d.action}</span>
                    ) : (
                      <a className="download-btn download-btn--active" href={d.href}>
                        {d.action}
                      </a>
                    )}
                  </article>
                ))}
              </div>
              <p className="download-note" id="notify">
                目前优先开放 macOS 内测，Windows、Linux、Android 与 iOS 将在后续版本发布时逐步开放下载。
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
