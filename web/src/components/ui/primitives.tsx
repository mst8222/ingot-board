/**
 * 基础 UI 组件。
 *
 * 用 Radix 原语 + Tailwind 而不是组件库（naive-ui 等）：
 * 组件库会把设计 token 关在自己的内部主题里，暗色模式只能整体换皮，
 * 很难做"同一套语义色在两套主题下对比度都达标"。
 *
 * 全部组件遵循两条约定：
 * 1. **语义化标签**：能用 button 就不用 div onClick，键盘与读屏才正常。
 * 2. **focus-visible**：只在键盘导航时显示焦点环（见 styles.css 全局规则）。
 */

import { Slot } from '@radix-ui/react-slot'
import { cva, type VariantProps } from 'class-variance-authority'
import { forwardRef } from 'react'
import { cn } from '@/lib/cn'

// ---- Button ----

const buttonVariants = cva(
  'inline-flex items-center justify-center gap-1.5 rounded-md font-medium ' +
    'transition-colors disabled:pointer-events-none disabled:opacity-50 ' +
    'whitespace-nowrap select-none',
  {
    variants: {
      variant: {
        primary: 'bg-[var(--accent)] text-[var(--accent-ink)] hover:bg-[var(--accent-hover)]',
        secondary:
          'bg-[var(--surface)] text-[var(--ink)] border border-[var(--border)] hover:bg-[var(--surface-hover)]',
        ghost: 'text-[var(--ink-muted)] hover:bg-[var(--surface-hover)] hover:text-[var(--ink)]',
        danger: 'bg-[var(--danger)] text-white hover:opacity-90',
      },
      size: {
        sm: 'h-7 px-2 text-xs',
        md: 'h-8 px-3 text-sm',
        lg: 'h-10 px-4 text-sm',
        icon: 'h-8 w-8',
      },
    },
    defaultVariants: { variant: 'secondary', size: 'md' },
  },
)

export interface ButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement>,
    VariantProps<typeof buttonVariants> {
  asChild?: boolean
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant, size, asChild = false, type, ...props }, ref) => {
    const Comp = asChild ? Slot : 'button'
    return (
      <Comp
        ref={ref}
        // 默认 type="button"：不写的话放在 <form> 里会意外提交表单
        type={asChild ? undefined : (type ?? 'button')}
        className={cn(buttonVariants({ variant, size }), className)}
        {...props}
      />
    )
  },
)
Button.displayName = 'Button'

// ---- Input ----

export const Input = forwardRef<HTMLInputElement, React.InputHTMLAttributes<HTMLInputElement>>(
  ({ className, ...props }, ref) => (
    <input
      ref={ref}
      className={cn(
        'h-8 w-full rounded-md border border-[var(--border)] bg-[var(--surface)] px-2.5 text-sm',
        'text-[var(--ink)] placeholder:text-[var(--ink-subtle)]',
        'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent)]',
        'disabled:opacity-50',
        className,
      )}
      {...props}
    />
  ),
)
Input.displayName = 'Input'

// ---- Textarea ----

export const Textarea = forwardRef<
  HTMLTextAreaElement,
  React.TextareaHTMLAttributes<HTMLTextAreaElement>
>(({ className, ...props }, ref) => (
  <textarea
    ref={ref}
    className={cn(
      'w-full rounded-md border border-[var(--border)] bg-[var(--surface)] px-2.5 py-1.5 text-sm',
      'text-[var(--ink)] placeholder:text-[var(--ink-subtle)]',
      'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent)]',
      className,
    )}
    {...props}
  />
))
Textarea.displayName = 'Textarea'

// ---- Badge ----

export function Badge({
  className,
  ...props
}: React.HTMLAttributes<HTMLSpanElement>) {
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-xs font-medium',
        'bg-[var(--surface-hover)] text-[var(--ink-muted)]',
        className,
      )}
      {...props}
    />
  )
}

// ---- Spinner ----

export function Spinner({ className }: { className?: string }) {
  return (
    <span
      role="status"
      aria-label="加载中"
      className={cn(
        'inline-block h-3.5 w-3.5 animate-spin rounded-full border-2',
        'border-current border-t-transparent opacity-60',
        className,
      )}
    />
  )
}

// ---- EmptyState ----

export function EmptyState({
  icon,
  title,
  hint,
  action,
}: {
  icon?: React.ReactNode
  title: string
  hint?: string
  action?: React.ReactNode
}) {
  return (
    <div className="flex flex-col items-center justify-center gap-2 px-4 py-10 text-center">
      {icon ? <div className="text-[var(--ink-subtle)]">{icon}</div> : null}
      <p className="text-sm font-medium text-[var(--ink)]">{title}</p>
      {hint ? <p className="max-w-xs text-xs text-[var(--ink-muted)]">{hint}</p> : null}
      {action ? <div className="mt-1">{action}</div> : null}
    </div>
  )
}
