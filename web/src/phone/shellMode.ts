/**
 * 手机壳的判定。
 *
 * 本机回环上的精细指针继续用五列看板。
 * 手机打开的是局域网或 Funnel 地址，主机名不是回环，直接进手机壳。
 * 配对链接带 code，同样进手机壳，避免二维码在桌面站点模式下还看到看板。
 */

export const PHONE_MEDIA = '(pointer: coarse), (hover: none) and (max-width: 640px)'

export function isRemoteHost(hostname: string): boolean {
  return hostname !== 'localhost' && hostname !== '127.0.0.1' && hostname !== '[::1]'
}

export function wantsPhoneShell(input: {
  remoteHost: boolean
  pairing: boolean
  media: boolean
}): boolean {
  return input.remoteHost || input.pairing || input.media
}
