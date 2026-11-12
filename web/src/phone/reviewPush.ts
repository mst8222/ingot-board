import { api } from '@/api/client'

function urlBase64ToUint8Array(base64String: string): Uint8Array {
  const padding = '='.repeat((4 - (base64String.length % 4)) % 4)
  const base64 = (base64String + padding).replace(/-/g, '+').replace(/_/g, '/')
  const raw = atob(base64)
  const out = new Uint8Array(raw.length)
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i)
  return out
}

/** 在 HTTPS 的已安装页面上订阅待评审通知。局域网 HTTP 没有安全上下文，调用会失败。 */
export async function enableReviewPush(): Promise<void> {
  if (!('serviceWorker' in navigator) || !window.isSecureContext || !('PushManager' in window)) {
    throw new Error('待评审通知需要用 HTTPS 打开，例如 Tailscale Funnel 地址')
  }
  const perm = await Notification.requestPermission()
  if (perm !== 'granted') {
    throw new Error('没有通知权限')
  }
  const { public_key } = await api.vapidPublicKey()
  const reg = await navigator.serviceWorker.ready
  const sub = await reg.pushManager.subscribe({
    userVisibleOnly: true,
    applicationServerKey: urlBase64ToUint8Array(public_key) as BufferSource,
  })
  const json = sub.toJSON()
  await api.putPushSubscription({
    endpoint: sub.endpoint,
    keys: {
      p256dh: json.keys?.p256dh ?? '',
      auth: json.keys?.auth ?? '',
    },
  })
}
