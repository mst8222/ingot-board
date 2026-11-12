import { useState } from 'react'
import { X } from 'lucide-react'
import { Badge, Button, Input } from '@/components/ui/primitives'
import {
  errorMessage,
  useCreatePairCode,
  usePhoneDevices,
  usePhoneStatus,
  useProjects,
  useReviewWebhook,
  useRevokePhoneDevice,
  useUpdatePhoneSettings,
  useUpdateReviewWebhook,
} from '@/hooks/useApi'
import type { DeviceScopes } from '@/api/types'
import { DEFAULT_PHONE_OPS, describeScopes } from './pairScope'
import { PairScopeEditor } from './PairScopeEditor'

export function PhoneAccessPanel({ onClose }: { onClose: () => void }) {
  const status = usePhoneStatus(true)
  const devices = usePhoneDevices(true)
  const projects = useProjects()
  const save = useUpdatePhoneSettings()
  const pair = useCreatePairCode()
  const revoke = useRevokePhoneDevice()
  const webhook = useReviewWebhook(true)
  const saveWebhook = useUpdateReviewWebhook()
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [webhookDraft, setWebhookDraft] = useState<string | null>(null)
  // 026：范围编辑。custom=true 后签发一律发显式 body（可预期性优先）
  const [custom, setCustom] = useState(false)
  const [scopes, setScopes] = useState<DeviceScopes>({ ops: [...DEFAULT_PHONE_OPS], project_ids: [] })
  const current = status.data
  const webhookURL = webhookDraft ?? webhook.data?.url ?? ''
  const scopeEmpty = custom && scopes.ops.length === 0

  const toggle = (patch: { lan_enabled?: boolean; remote_enabled?: boolean }) => {
    if (!current) return
    setError(null)
    save.mutate(
      {
        lan_enabled: patch.lan_enabled ?? current.lan_enabled,
        remote_enabled: patch.remote_enabled ?? current.remote_enabled,
      },
      { onError: (e) => setError(errorMessage(e)) },
    )
  }

  return (
    <div className="fixed inset-0 z-50 flex justify-end" role="dialog" aria-modal="true" aria-label="手机连接">
      <div className="absolute inset-0 bg-black/30" onClick={onClose} aria-hidden />
      <div className="relative flex h-full w-full max-w-md flex-col gap-3 overflow-y-auto border-l border-[var(--border)] bg-[var(--surface)] p-4">
        <div className="flex items-center">
          <h2 className="text-sm font-semibold">手机</h2>
          <Button variant="ghost" size="icon" className="ml-auto" onClick={onClose} aria-label="关闭">
            <X className="h-4 w-4" aria-hidden />
          </Button>
        </div>
        <p className="text-xs text-[var(--ink-muted)]">
          手机只下达任务和看结果。本机 Agent 仍由这台电脑上的 Runner 和编辑器执行。
        </p>
        {error ? <p className="text-sm text-[var(--danger)]">{error}</p> : null}
        {notice ? <p className="text-sm text-[var(--ink-muted)]">{notice}</p> : null}
        {current?.remote_error ? <p className="text-sm text-[var(--danger)]">{current.remote_error}</p> : null}
        <Button
          className="h-11 min-h-11"
          onClick={() => toggle({ lan_enabled: !current?.lan_enabled })}
          disabled={!current || save.isPending}
        >
          {current?.lan_enabled ? '关闭局域网' : '开启局域网'}
        </Button>
        {current?.lan_url ? <p className="break-all text-xs">{current.lan_url}</p> : null}
        <Button
          className="h-11 min-h-11"
          onClick={() => toggle({ remote_enabled: !current?.remote_enabled })}
          disabled={!current || save.isPending}
        >
          {current?.remote_enabled ? '关闭远程' : '允许手机远程连接'}
        </Button>
        {current && !current.tailscale_installed ? (
          <p className="text-xs text-[var(--ink-muted)]">远程连接需要本机已安装并登录 Tailscale。</p>
        ) : null}
        {current?.remote_url ? <p className="break-all text-xs">{current.remote_url}</p> : null}
        <PairScopeEditor
          value={scopes}
          onChange={(s) => {
            setCustom(true)
            setScopes(s)
          }}
          projects={projects.data ?? []}
          disabled={pair.isPending}
        />
        {scopeEmpty ? <p className="text-sm text-[var(--danger)]">至少保留一个操作，否则手机无法使用</p> : null}
        <Button
          variant="primary"
          className="h-11 min-h-11"
          disabled={pair.isPending || scopeEmpty || (!current?.lan_url && !current?.remote_url)}
          onClick={() => {
            setError(null)
            pair.mutate(custom ? scopes : undefined, { onError: (e) => setError(errorMessage(e)) })
          }}
        >
          生成配对码
        </Button>
        {(pair.data?.links ?? []).map((link) => (
          <div key={link.kind} className="flex flex-col items-start gap-2">
            <p className="break-all text-xs">{link.url}</p>
            {link.qr_png ? <img src={link.qr_png} alt="配对二维码" width={220} height={220} /> : null}
          </div>
        ))}
        <h3 className="text-xs font-semibold">待评审通知地址</h3>
        <p className="text-xs text-[var(--ink-muted)]">
          任务第一次进入待评审时向这个地址 POST 一次。群机器人填 HTTPS。本机适配器可填 http://127.0.0.1。
        </p>
        <Input
          id="review-webhook"
          aria-label="待评审通知地址"
          value={webhookURL}
          placeholder="https://example.com/hook"
          onChange={(e) => {
            setNotice(null)
            setError(null)
            setWebhookDraft(e.target.value)
          }}
        />
        <Button
          className="h-11 min-h-11"
          disabled={saveWebhook.isPending}
          onClick={() => {
            setError(null)
            setNotice(null)
            saveWebhook.mutate(
              { url: webhookURL.trim() },
              {
                onSuccess: (data) => {
                  setWebhookDraft(data.url)
                  setNotice(data.url ? '已保存通知地址' : '已关闭待评审通知')
                },
                onError: (e) => setError(errorMessage(e)),
              },
            )
          }}
        >
          保存通知地址
        </Button>
        <h3 className="text-xs font-semibold">已配对的手机</h3>
        <ul className="flex flex-col gap-2">
          {(devices.data?.items ?? []).map((device) => {
            const summary = describeScopes(device.scopes, projects.data ?? [])
            return (
              <li key={device.id} className="flex flex-col gap-1 text-sm">
                <div className="flex items-center gap-2">
                  <span>{device.name}</span>
                  <Badge>{summary.badge}</Badge>
                  <Button variant="ghost" className="ml-auto min-h-11" onClick={() => revoke.mutate(device.id)}>
                    吊销
                  </Button>
                </div>
                {summary.detail ? <p className="text-xs text-[var(--ink-muted)]">{summary.detail}</p> : null}
              </li>
            )
          })}
        </ul>
      </div>
    </div>
  )
}
