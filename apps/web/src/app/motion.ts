export const EASE_SMOOTH = [0.16, 1, 0.3, 1] as const

export const DUR = {
  fast: 0.22,
  fade: 0.28,
  nav: 0.42,
  cardIn: 0.52,
} as const

export const TRANSITION = {
  fast: { duration: DUR.fast, ease: EASE_SMOOTH },
  fade: { duration: DUR.fade, ease: EASE_SMOOTH },
  nav: { duration: DUR.nav, ease: EASE_SMOOTH },
  cardIn: { duration: DUR.cardIn, ease: EASE_SMOOTH },
} as const

export const SPRING_NAV_PILL = {
  type: 'spring',
  stiffness: 520,
  damping: 44,
  mass: 0.9,
} as const
