/** 工作目录输入：可手填，也可通过本机原生对话框选择。 */

import { useState } from 'react'
import { FolderOpen } from 'lucide-react'
import { api } from '@/api/client'
import { Button, Input } from '@/components/ui/primitives'
import { errorMessage } from '@/hooks/useApi'

export function WorkdirField({
  value,
  onChange,
  disabled,
  placeholder = 'D:\\projects\\foo',
}: {
  value: string
  onChange: (next: string) => void
  disabled?: boolean
  placeholder?: string
}) {
  const [picking, setPicking] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function browse() {
    if (disabled || picking) return
    setPicking(true)
    setError(null)
    try {
      const res = await api.pickDirectory(value.trim() || undefined)
      if (!res.cancelled && res.path) {
        onChange(res.path)
      }
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setPicking(false)
    }
  }

  return (
    <div className="flex flex-col gap-1">
      <div className="flex gap-2">
        <Input
          value={value}
          onChange={(e) => onChange(e.target.value)}
          disabled={disabled}
          placeholder={placeholder}
          className="min-w-0 flex-1"
          aria-label="工作目录路径"
        />
        <Button
          type="button"
          variant="secondary"
          disabled={disabled || picking}
          onClick={() => void browse()}
          aria-label="浏览选择工作目录"
        >
          <FolderOpen className="h-3.5 w-3.5" aria-hidden />
          {picking ? '选择中…' : '浏览'}
        </Button>
      </div>
      {error ? (
        <p role="alert" className="text-xs text-[var(--danger)]">
          {error}
        </p>
      ) : null}
    </div>
  )
}
