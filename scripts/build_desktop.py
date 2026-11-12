#!/usr/bin/env python3
r"""构建桌面客户端（Wails v3）。

为什么单独一个脚本而不是一行 go build：
  1. 桌面端默认必须用 `-H windowsgui` 链接，否则每次启动都会跟一个黑乎乎的
     控制台窗口（调试时才需要它看日志）；
  2. 桌面端内嵌的是 internal/webassets/dist 的前端产物，忘记先构建前端
     会得到一个"能起但白屏"的 exe —— 脚本默认会先跑前端构建；
  3. exe 的文件图标与窗口图标来自 Windows 资源（图标 ID 3 + manifest），
     脚本用 cmd/ingot-icon 从矢量图标生成 .ico，再用 wails3 打成 .syso
     临时放进 cmd/ingot-desktop 参与链接，构建完即删，仓库不留二进制。

用法：
    python scripts/build_desktop.py                 # 构建前端 + 无控制台窗口的桌面端
    python scripts/build_desktop.py --skip-web      # 已有前端产物，只构建桌面端
    python scripts/build_desktop.py --console       # 调试版，带控制台输出
    python scripts/build_desktop.py --out D:\dist\ingot-desktop.exe
退出码非 0 表示构建失败。
"""

import argparse
import platform
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent


def run(cmd: list[str], cwd: Path | None = None) -> int:
    printable = " ".join(cmd)
    print(f"$ {printable}")
    return subprocess.run(cmd, cwd=str(cwd or ROOT), check=False).returncode


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--out", default=str(ROOT / "dist" / "ingot-desktop.exe"), help="输出路径")
    ap.add_argument("--skip-web", action="store_true", help="跳过前端构建，直接用已有产物")
    ap.add_argument("--console", action="store_true", help="保留控制台窗口（调试用，能看到日志）")
    args = ap.parse_args()

    out = Path(args.out)
    out.parent.mkdir(parents=True, exist_ok=True)

    if not args.skip_web:
        index = ROOT / "internal" / "webassets" / "dist" / "index.html"
        print("==> 构建前端产物")
        if run(["npm", "install"], cwd=ROOT / "web") != 0:
            print("npm install 失败")
            return 1
        if run(["npm", "run", "build"], cwd=ROOT / "web") != 0:
            print("前端构建失败")
            return 1
        if not index.exists():
            print(f"前端构建后仍找不到 {index}")
            return 1
    else:
        print("==> 跳过前端构建（沿用 internal/webassets/dist）")

    windows = platform.system() == "Windows"
    syso = ROOT / "cmd" / "ingot-desktop" / "rsrc_windows_amd64.syso"
    ico = ROOT / "dist" / "ingot-desktop.ico"
    if windows:
        print("==> 生成图标资源")
        ico.parent.mkdir(parents=True, exist_ok=True)
        if run(["go", "run", "./cmd/ingot-icon", "-ico", str(ico)]) != 0:
            print("图标生成失败")
            return 1
        manifest = ROOT / "build" / "windows" / "ingot-desktop.exe.manifest"
        if run(["wails3", "generate", "syso", "-arch", "amd64",
                "-icon", str(ico), "-manifest", str(manifest), "-out", str(syso)]) != 0:
            print("资源文件生成失败（需要 wails3 在 PATH 中）")
            return 1

    cmd = ["go", "build"]
    if windows and not args.console:
        # windowsgui 子系统：不弹控制台，双击即窗口。
        cmd += ["-ldflags", "-H windowsgui"]
    cmd += ["-o", str(out), "./cmd/ingot-desktop"]

    print("==> 构建桌面端")
    try:
        code = run(cmd)
    finally:
        syso.unlink(missing_ok=True)
        ico.unlink(missing_ok=True)
    if code != 0:
        print("桌面端构建失败")
        return 1

    size_mb = out.stat().st_size / 1024 / 1024
    print(f"\n✓ 桌面端已构建: {out} ({size_mb:.1f} MB)")
    print("  启动: " + str(out) + " [--data-dir DIR] [--no-notify]")
    return 0


if __name__ == "__main__":
    sys.exit(main())
