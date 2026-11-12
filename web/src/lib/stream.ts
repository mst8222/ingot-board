/**
 * SSE 单例。
 *
 * 为什么要单例：`EventSource` 每建一个就是一条 TCP 长连接 + 服务端一个
 * 订阅者。React 18+ 在 StrictMode 下会双调用 effect，组件重挂载又会重建 ——
 * 原 Vue 前端每次组件挂载都 `new EventSource`，切个页签回来就多一条连接，
 * 服务端的订阅者列表随之膨胀。改成模块级单例 + 引用计数。
 *
 * 断线续传：EventSource 会自动带 `Last-Event-ID`，服务端据此从环形缓冲补发。
 * 收到 `resync` 说明缺口已丢，必须**整体重拉**，不能尝试局部合并 ——
 * 这是本文件最重要的一个分支。
 */

import type { Change, ResyncReason } from '@/api/types'

export type ConnectionState = 'connecting' | 'open' | 'resync' | 'closed'

export interface StreamHandlers {
  onChange: (c: Change) => void
  onStateChange: (s: ConnectionState, detail?: ResyncReason) => void
}

/** 距离上次收到任何消息多久算"连接僵死"。服务端 25s 发一次心跳。 */
const STALE_AFTER_MS = 70_000

class StreamClient {
  private es: EventSource | null = null
  private handlers: StreamHandlers | null = null
  private refCount = 0
  private state: ConnectionState = 'closed'
  private lastSeq = 0
  private staleTimer: ReturnType<typeof setInterval> | null = null
  private lastMessageAt = 0

  /** 订阅。返回退订函数；计数归零时才真正关闭连接。 */
  subscribe(handlers: StreamHandlers): () => void {
    // 后订阅的覆盖先订阅的：本项目全项目只有useStreamBridge 一个消费者，
    // 它的 effect 每次 filter 变化都会重跑并交出新的 handler 闭包。
    // 保留最新的那个才是对的 —— 旧闭包里的 qc/filter 快照已经过期。
    this.handlers = handlers
    this.refCount += 1
    this.connect()

    let released = false
    return () => {
      if (released) return
      released = true
      this.refCount -= 1
      if (this.refCount <= 0) {
        this.refCount = 0
        this.handlers = null
        this.disconnect()
      }
    }
  }

  private connect(): void {
    if (this.es) return
    this.setState('connecting')

    const es = new EventSource('/api/v1/stream')
    this.es = es
    this.lastMessageAt = Date.now()

    es.addEventListener('ready', (e) => {
      this.setState('open')
      try {
        const data = JSON.parse((e as MessageEvent).data) as { seq: number }
        this.lastSeq = data.seq
      } catch {
        // ready 帧解析失败不该影响后续消费，忽略即可
      }
    })

    es.addEventListener('change', (e) => {
      this.setState('open')
      this.lastMessageAt = Date.now()
      try {
        const change = JSON.parse((e as MessageEvent).data) as Change
        // 乱序保护：理论上补发是按 seq 递增的，但代理层可能重排
        if (change.seq <= this.lastSeq) return
        this.lastSeq = change.seq
        this.handlers?.onChange(change)
      } catch {
        // 单条消息解析失败不该中断整条流
      }
    })

    es.addEventListener('resync', (e) => {
      let reason: ResyncReason = 'buffer_gap'
      try {
        const data = JSON.parse((e as MessageEvent).data) as { reason: ResyncReason }
        reason = data.reason
      } catch {
        /* 用默认原因 */
      }
      // 缺口里的事件已经丢了，任何局部合并都会留下不一致的状态。
      // 通知上层整体重拉。
      this.setState('resync', reason)
    })

    es.onerror = () => {
      // EventSource 自带重连（ready 帧里的 retry: 3000），这里不手动重建，
      // 否则会和内建重连打架，导致两条连接并存。
      if (this.state !== 'closed') this.setState('connecting')
    }

    // 僵死检测：TCP 连接可能"看起来还在"但实际已断，收不到心跳。
    // 不做这个检测的话，界面会一直显示"已连接"却不再更新。
    this.staleTimer = setInterval(() => {
      if (this.state === 'closed' || this.es === null) return
      if (Date.now() - this.lastMessageAt > STALE_AFTER_MS) {
        // 主动关掉让 EventSource 重建（它会带新的 Last-Event-ID）
        this.es?.close()
        this.es = null
        this.setState('connecting')
        this.connect()
      }
    }, 15_000)
  }

  private disconnect(): void {
    if (this.staleTimer) {
      clearInterval(this.staleTimer)
      this.staleTimer = null
    }
    this.es?.close()
    this.es = null
    this.setState('closed')
  }

  private setState(s: ConnectionState, detail?: ResyncReason): void {
    this.state = s
    this.handlers?.onStateChange(s, detail)
  }

  /** 仅供测试：当前是否持有连接 */
  get connected(): boolean {
    return this.es !== null
  }
}

export const stream = new StreamClient()
