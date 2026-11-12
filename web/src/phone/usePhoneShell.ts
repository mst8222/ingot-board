import { useEffect, useState } from 'react'
import { isRemoteHost, PHONE_MEDIA, wantsPhoneShell } from './shellMode'

function detect(media: boolean): boolean {
  const params = new URLSearchParams(window.location.search)
  return wantsPhoneShell({
    remoteHost: isRemoteHost(window.location.hostname),
    pairing: params.has('code'),
    media,
  })
}

/** 当前窗口该不该用手机壳。首屏同步判断，避免先画出五列再跳走。 */
export function usePhoneShell(): boolean {
  const [on, setOn] = useState(() => detect(window.matchMedia(PHONE_MEDIA).matches))

  useEffect(() => {
    const mq = window.matchMedia(PHONE_MEDIA)
    const apply = () => setOn(detect(mq.matches))
    apply()
    mq.addEventListener('change', apply)
    return () => mq.removeEventListener('change', apply)
  }, [])

  return on
}
