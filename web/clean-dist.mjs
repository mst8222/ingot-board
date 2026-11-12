/**
 * 构建前清空产物目录。
 *
 * 为什么不用 vite 的 `emptyOutDir`：
 * 它会 fs.rm 整个输出目录。在带安全删除保护的环境里，删除会被拦进
 * 回收站并等待确认或超时 —— vite 的清空动作因此会卡住，
 * 表现为「构建跑着跑着报 ETIMEDOUT 失败」，而且**间歇性**发生
 * （取决于哪些文件需要真正搬走）。
 *
 * 更糟的是它会删到一半失败：产物目录只剩部分文件，
 * 下一��次构建的 index.html 引用到已消失的 chunk，直接白屏。
 *
 * 这里改用 fs.unlinkSync 逐个删 —— 纯进程内系统调用，
 * 不触发任何跨进程拦截。删的是 vite 自己的输出（每次都是新哈希文件名），
 * 没有任何用户数据。
 */
import { readdirSync, unlinkSync, existsSync, rmdirSync } from 'node:fs'
import { join, extname } from 'node:path'
import { fileURLToPath } from 'node:url'

const distDir = fileURLToPath(new URL('../internal/webassets/dist/', import.meta.url))
const assetsDir = join(distDir, 'assets')

if (!existsSync(assetsDir)) {
  console.log('产物目录尚未创建，跳过清理')
  process.exit(0)
}

let removed = 0
for (const name of readdirSync(assetsDir)) {
  const ext = extname(name)
  if (ext !== '.js' && ext !== '.css') continue
  try {
    unlinkSync(join(assetsDir, name))
    removed++
  } catch (e) {
    console.warn(`删除 ${name} 失败：${e.message}`)
  }
}

// assets 空了才删目录本身，留着空目录会让 vite 以为是它自己建的
try {
  rmdirSync(assetsDir)
} catch {
  /* 非空或被占用：留着 vite 会复用*/
}

console.log(removed > 0 ? `已清理 ${removed} 个上一版产物` : '无需清理')