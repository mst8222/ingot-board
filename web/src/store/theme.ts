/**
 * 主题（亮/暗/跟随系统）。
 *
 * index.html 里的内联脚本会在首屏前先定好主题，避免暗色模式白屏闪一下。
 * 这个 store 只负责后续切换 —— 首次渲染读的是内联脚本的结果，
 * 不重新计算默认值，否则会把内联脚本的判定覆盖掉。
 */

import { create } from 'zustand'

export type ThemeChoice = 'light' | 'dark' | 'system'

const STORAGE_KEY = 'ingot.theme'

interface ThemeState {
  choice: ThemeChoice
  /** 实际生效的主题（system 会解析成 light/dark） */
  resolved: 'light' | 'dark'
  setChoice: (c: ThemeChoice) => void
  toggle: () => void
}

function readStored(): ThemeChoice {
  try {
    const v = localStorage.getItem(STORAGE_KEY)
    if (v === 'light' || v === 'dark' || v === 'system') return v
  } catch {
    /* 隐私模式下读不到 */
  }
  return 'system'
}

function systemPrefersDark(): boolean {
  return window.matchMedia?.('(prefers-color-scheme: dark)').matches ?? false
}

function resolve(choice: ThemeChoice): 'light' | 'dark' {
  if (choice === 'system') return systemPrefersDark() ? 'dark' : 'light'
  return choice
}

function apply(resolved: 'light' | 'dark'): void {
  document.documentElement.classList.toggle('dark', resolved === 'dark')
  document.documentElement.dataset.theme = resolved
}

const initial = readStored()
apply(resolve(initial))

export const useTheme = create<ThemeState>((set, get) => ({
  choice: initial,
  resolved: resolve(initial),

  setChoice: (choice) => {
    try {
      localStorage.setItem(STORAGE_KEY, choice)
    } catch {
      /* 写不进去也不影响本次会话 */
    }
    const resolved = resolve(choice)
    apply(resolved)
    set({ choice, resolved })
  },

  toggle: () => {
    // 只在 light/dark 之间切换 —— 若当前是 system，
    // 第一次点击应落到"与当前显示相反"的那个具体值。
    const current = resolve(get().choice)
    get().setChoice(current === 'dark' ? 'light' : 'dark')
  },
}))

// 跟随系统时，系统主题变化要实时反映（用户可能在白天/夜晚切换系统主题）
window.matchMedia?.('(prefers-color-scheme: dark)').addEventListener('change', () => {
  const { choice, setChoice } = useTheme.getState()
  if (choice === 'system') setChoice('system')
})
