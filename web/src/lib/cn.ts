import { clsx, type ClassValue } from 'clsx'
import { twMerge } from 'tailwind-merge'

/**
 * 合并 className。
 *
 * clsx 处理条件类名，twMerge 解决 Tailwind 冲突 ——
 * 没有 twMerge 的话 `px-2 px-4` 只能靠 CSS 加载顺序决定谁生效，
 * 组件内部传默认 padding、外部传覆盖 padding 时会失效。
 */
export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs))
}
