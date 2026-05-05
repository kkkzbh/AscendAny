import { NavLink, Outlet, useLocation, useNavigate } from 'react-router-dom'
import { useAuth } from './AuthContext'
import './layout.css'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import * as Popover from '@radix-ui/react-popover'
import { AnimatePresence, LayoutGroup, motion, useReducedMotion } from 'motion/react'
import { SPRING_NAV_PILL, TRANSITION } from './motion'

import ThemeSwitch from '../components/ThemeSwitch'

import { useEffect, useMemo, useRef, useState } from 'react'
import {
  FaBars,
  FaBell,
  FaBook,
  FaCode,
  FaExternalLinkAlt,
  FaHistory,
  FaHome,
  FaIdCard,
  FaKey,
  FaProjectDiagram,
  FaSignOutAlt,
} from 'react-icons/fa'

function pageMeta(pathname: string): { title: string; subtitle: string } {
  if (pathname.startsWith('/integrations/ascendany')) return { title: 'AI分析', subtitle: 'SSO集成（iframe / 新窗口）' }
  if (pathname.startsWith('/knowledge')) return { title: '知识图谱', subtitle: '学习路径与知识点关联' }
  if (pathname.startsWith('/qa')) return { title: '知识问答', subtitle: '题库片段检索 + AI 生成' }
  if (pathname.startsWith('/oj')) return { title: '在线题库', subtitle: '练习、运行与提交' }
  if (pathname.startsWith('/records')) return { title: '做题记录', subtitle: '提交记录与学习进度' }
  if (pathname.startsWith('/profile')) return { title: '个人信息', subtitle: '账户与设置' }
  return { title: '我的主页', subtitle: '概览' }
}

export default function AppLayout() {
  const { user, logout } = useAuth()
  const loc = useLocation()
  const nav = useNavigate()
  const isKnowledge = loc.pathname.startsWith('/knowledge')
  const canvasMode = isKnowledge
  const [navOpen, setNavOpen] = useState(false)
  const [navCollapsed, setNavCollapsed] = useState(false)
  const [notifOpen, setNotifOpen] = useState(false)
  const [userOpen, setUserOpen] = useState(false)
  const prefersReducedMotion = useReducedMotion()
  const enableMotion = !prefersReducedMotion && !isKnowledge
  const enableNavMotion = !prefersReducedMotion

  const [isMobile, setIsMobile] = useState(false)
  const navIdleTimerRef = useRef<number | null>(null)
  const navHoverRef = useRef(false)
  const NAV_IDLE_MS = 3500

  useEffect(() => {
    if (!window.matchMedia) return
    const mq = window.matchMedia('(max-width: 980px)')
    const update = () => setIsMobile(mq.matches)

    update()
    if (mq.addEventListener) {
      mq.addEventListener('change', update)
      return () => mq.removeEventListener('change', update)
    }

    // Safari < 14
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    ;(mq as any).addListener(update)
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    return () => (mq as any).removeListener(update)
  }, [])

  function clearNavTimer() {
    if (navIdleTimerRef.current) {
      window.clearTimeout(navIdleTimerRef.current)
      navIdleTimerRef.current = null
    }
  }

  function scheduleNavCollapse() {
    clearNavTimer()
    if (isMobile || navOpen) return
    if (navHoverRef.current) return
    if (canvasMode) {
      setNavCollapsed(true)
      return
    }
    navIdleTimerRef.current = window.setTimeout(() => {
      setNavCollapsed(true)
    }, NAV_IDLE_MS)
  }

  function expandNav() {
    if (isMobile || navOpen) return
    if (!navCollapsed) {
      clearNavTimer()
      return
    }
    setNavCollapsed(false)
    clearNavTimer()
  }

  useEffect(() => {
    if (isMobile) {
      setNavCollapsed(false)
      clearNavTimer()
      return
    }

    if (canvasMode) {
      clearNavTimer()
      setNavCollapsed(true)
      return
    }

    scheduleNavCollapse()
    return () => {
      clearNavTimer()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isMobile, navOpen, canvasMode])

  useEffect(() => {
    setNavOpen(false)
    setNotifOpen(false)
    setUserOpen(false)
  }, [loc.pathname])

  const meta = pageMeta(loc.pathname)

  const shellClass = useMemo(() => {
    return `app-shell${navCollapsed ? ' nav-collapsed' : ''}`
  }, [navCollapsed])

  return (
    <div className={shellClass}>
      <div className="app-frame">
        {navOpen ? <div className="nav-backdrop" onClick={() => setNavOpen(false)} /> : null}

        <aside
          className={`app-nav ${navOpen ? 'open' : ''}`}
          onMouseEnter={() => {
            navHoverRef.current = true
            expandNav()
          }}
          onMouseLeave={() => {
            navHoverRef.current = false
            if (canvasMode) {
              clearNavTimer()
              setNavCollapsed(true)
              return
            }
            scheduleNavCollapse()
          }}
          onFocusCapture={() => {
            navHoverRef.current = true
            expandNav()
          }}
          onBlurCapture={() => {
            navHoverRef.current = false
            if (canvasMode) {
              clearNavTimer()
              setNavCollapsed(true)
              return
            }
            scheduleNavCollapse()
          }}
        >
          <div className="brand">
            <div className="title">个性化编程系统</div>
            <div className="sub">算法练习系统</div>
          </div>

          <LayoutGroup id="nav">
            <motion.nav
              className="nav-items"
              layout={enableNavMotion}
              transition={{ layout: TRANSITION.nav }}
            >
              <motion.div layout={enableNavMotion} transition={{ layout: TRANSITION.nav }}>
                <NavLink
                  to="/dashboard"
                  title="我的主页"
                  className={({ isActive }) => `nav-link ${isActive ? 'active' : ''}`}
                >
                  {({ isActive }) => (
                    <>
                      {isActive ? (
                        <motion.span
                          className="nav-pill"
                          layoutId="nav-pill"
                          transition={SPRING_NAV_PILL}
                        />
                      ) : null}
                      <span className="nav-icon" aria-hidden="true">
                        <FaHome />
                      </span>
                      <AnimatePresence initial={false}>
                        {!navCollapsed ? (
                          <motion.span
                            className="nav-text"
                            initial={{ opacity: 0, x: -8 }}
                            animate={{ opacity: 1, x: 0 }}
                            exit={{ opacity: 0, x: -8 }}
                            transition={TRANSITION.fade}
                          >
                            我的主页
                          </motion.span>
                        ) : null}
                      </AnimatePresence>
                    </>
                  )}
                </NavLink>
              </motion.div>

              <motion.div layout={enableNavMotion} transition={{ layout: TRANSITION.nav }}>
                <NavLink
                  to="/records"
                  title="做题记录"
                  className={({ isActive }) => `nav-link ${isActive ? 'active' : ''}`}
                >
                  {({ isActive }) => (
                    <>
                      {isActive ? (
                        <motion.span
                          className="nav-pill"
                          layoutId="nav-pill"
                          transition={SPRING_NAV_PILL}
                        />
                      ) : null}
                      <span className="nav-icon" aria-hidden="true">
                        <FaHistory />
                      </span>
                      <AnimatePresence initial={false}>
                        {!navCollapsed ? (
                          <motion.span
                            className="nav-text"
                            initial={{ opacity: 0, x: -8 }}
                            animate={{ opacity: 1, x: 0 }}
                            exit={{ opacity: 0, x: -8 }}
                            transition={TRANSITION.fade}
                          >
                            做题记录
                          </motion.span>
                        ) : null}
                      </AnimatePresence>
                    </>
                  )}
                </NavLink>
              </motion.div>

              <motion.div layout={enableNavMotion} transition={{ layout: TRANSITION.nav }}>
                <NavLink
                  to="/oj"
                  title="在线题库"
                  className={({ isActive }) => `nav-link ${isActive ? 'active' : ''}`}
                >
                  {({ isActive }) => (
                    <>
                      {isActive ? (
                        <motion.span
                          className="nav-pill"
                          layoutId="nav-pill"
                          transition={SPRING_NAV_PILL}
                        />
                      ) : null}
                      <span className="nav-icon" aria-hidden="true">
                        <FaCode />
                      </span>
                      <AnimatePresence initial={false}>
                        {!navCollapsed ? (
                          <motion.span
                            className="nav-text"
                            initial={{ opacity: 0, x: -8 }}
                            animate={{ opacity: 1, x: 0 }}
                            exit={{ opacity: 0, x: -8 }}
                            transition={TRANSITION.fade}
                          >
                            在线题库
                          </motion.span>
                        ) : null}
                      </AnimatePresence>
                    </>
                  )}
                </NavLink>
              </motion.div>

              <motion.div layout={enableNavMotion} transition={{ layout: TRANSITION.nav }}>
                <NavLink
                  to="/knowledge"
                  title="知识图谱"
                  className={({ isActive }) => `nav-link ${isActive ? 'active' : ''}`}
                >
                  {({ isActive }) => (
                    <>
                      {isActive ? (
                        <motion.span
                          className="nav-pill"
                          layoutId="nav-pill"
                          transition={SPRING_NAV_PILL}
                        />
                      ) : null}
                      <span className="nav-icon" aria-hidden="true">
                        <FaProjectDiagram />
                      </span>
                      <AnimatePresence initial={false}>
                        {!navCollapsed ? (
                          <motion.span
                            className="nav-text"
                            initial={{ opacity: 0, x: -8 }}
                            animate={{ opacity: 1, x: 0 }}
                            exit={{ opacity: 0, x: -8 }}
                            transition={TRANSITION.fade}
                          >
                            知识图谱
                          </motion.span>
                        ) : null}
                      </AnimatePresence>
                    </>
                  )}
                </NavLink>
              </motion.div>

              <motion.div layout={enableNavMotion} transition={{ layout: TRANSITION.nav }}>
                <NavLink
                  to="/integrations/ascendany"
                  title="AI分析"
                  className={({ isActive }) => `nav-link ${isActive ? 'active' : ''}`}
                >
                  {({ isActive }) => (
                    <>
                      {isActive ? (
                        <motion.span className="nav-pill" layoutId="nav-pill" transition={SPRING_NAV_PILL} />
                      ) : null}
                      <span className="nav-icon" aria-hidden="true">
                        <FaExternalLinkAlt />
                      </span>
                      <AnimatePresence initial={false}>
                        {!navCollapsed ? (
                          <motion.span
                            className="nav-text"
                            initial={{ opacity: 0, x: -8 }}
                            animate={{ opacity: 1, x: 0 }}
                            exit={{ opacity: 0, x: -8 }}
                            transition={TRANSITION.fade}
                          >
                            AI分析
                          </motion.span>
                        ) : null}
                      </AnimatePresence>
                    </>
                  )}
                </NavLink>
              </motion.div>
            </motion.nav>
          </LayoutGroup>
        </aside>

        <main className={`app-main ${isKnowledge ? 'no-topbar' : ''}`}>
          {!isKnowledge ? (
            <header className="topbar">
              <div className="crumb">
                <button className="btn mobile-toggle" type="button" onClick={() => setNavOpen((v) => !v)} aria-label="菜单">
                  <FaBars />
                </button>
                <div className="h">{meta.title}</div>
                <div className="p">{meta.subtitle}</div>
              </div>
              <div className="actions">
                <ThemeSwitch />

                <Popover.Root open={notifOpen} onOpenChange={setNotifOpen}>
                  <Popover.Trigger asChild>
                    <button className="btn" type="button" aria-label="通知">
                      <FaBell />
                    </button>
                  </Popover.Trigger>
                  <Popover.Portal>
                    <Popover.Content
                      className="rx-popover-content rx-notif-panel"
                      role="dialog"
                      aria-label="通知"
                      side="bottom"
                      align="end"
                      sideOffset={8}
                      collisionPadding={12}
                    >
                      <div className="notif-title">
                        <FaBook /> 通知
                      </div>
                      <div className="notif-empty">暂无新通知</div>
                      <Popover.Arrow className="rx-popover-arrow" />
                    </Popover.Content>
                  </Popover.Portal>
                </Popover.Root>

                <div className="user-menu">
                  <DropdownMenu.Root open={userOpen} onOpenChange={setUserOpen}>
                    <DropdownMenu.Trigger asChild>
                      <button className="user-menu-btn" type="button" aria-label="用户菜单">
                        <div className="avatar">{(user?.full_name || user?.username || '?').slice(0, 1).toUpperCase()}</div>
                        <div className="user-menu-meta">
                          <div className="user-menu-meta-name">{user?.full_name || user?.username || ''}</div>
                          <div className="user-menu-meta-email">{user?.email || ''}</div>
                        </div>
                      </button>
                    </DropdownMenu.Trigger>

                    <DropdownMenu.Portal>
                      <DropdownMenu.Content
                        className="rx-popover-content rx-user-menu-panel"
                        side="bottom"
                        align="end"
                        sideOffset={10}
                        collisionPadding={12}
                      >
                        <div className="user-menu-title">
                          <div className="name">{user?.full_name || user?.username || ''}</div>
                          <div className="email">{user?.email || ''}</div>
                        </div>
                        <DropdownMenu.Separator className="user-menu-divider" />

                        <DropdownMenu.Item className="rx-menu-item" onSelect={() => nav('/profile')} asChild>
                          <button className="user-menu-item" type="button">
                            <FaIdCard />
                            个人信息
                          </button>
                        </DropdownMenu.Item>
                        <DropdownMenu.Item className="rx-menu-item" onSelect={() => nav('/profile/security')} asChild>
                          <button className="user-menu-item" type="button">
                            <FaKey />
                            修改密码
                          </button>
                        </DropdownMenu.Item>
                        <DropdownMenu.Separator className="user-menu-divider" />
                        <DropdownMenu.Item
                          className="rx-menu-item"
                          onSelect={() => {
                            void logout().then(() => {
                              nav('/login', { replace: true })
                            })
                          }}
                          asChild
                        >
                          <button className="user-menu-item danger" type="button">
                            <FaSignOutAlt />
                            退出登录
                          </button>
                        </DropdownMenu.Item>
                      </DropdownMenu.Content>
                    </DropdownMenu.Portal>
                  </DropdownMenu.Root>
                </div>
              </div>
            </header>
          ) : null}
          <div className={`page ${isKnowledge ? 'page-bleed' : ''}`}>
            {enableMotion ? (
              <AnimatePresence mode="wait" initial={false}>
                <motion.div
                  key={loc.pathname}
                  className="page-motion"
                  // Avoid transform-based page transitions here;
                  // sticky layout (e.g. OJ IDE dock) can break inside transformed ancestors.
                  initial={{ opacity: 0 }}
                  animate={{ opacity: 1 }}
                  exit={{ opacity: 0 }}
                  transition={{ duration: 0.32, ease: TRANSITION.fade.ease }}
                >
                  <Outlet />
                </motion.div>
              </AnimatePresence>
            ) : (
              <Outlet />
            )}
          </div>
        </main>
      </div>
    </div>
  )
}
