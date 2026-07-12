# AscendAny Desktop

Electron 桌面端使用 `ascendany-app://bundle` 安全应用 Origin，renderer 运行在 sandbox 中。所有在线业务调用来自 `@ascendany/sdk` 的 v2 OpenAPI client：

- 用户名与密码登录
- enrollment claim 激活
- 学生 analytics 与 leaderboard
- 持久化 Chat/Agent 对话、SSE 运行事件续传与推理详情
- profile 与 account session 管理

`BrowserSession` 只在 renderer 运行内存中保存短期 access credential，refresh credential 位于 API 的 HttpOnly cookie，持久化存储只包含轮换 CSRF token。

打包时必须设置规范化的 `VITE_API_BASE_URL`，并设置 Chat prompt/model configuration key。Electron 本地开发可以让 API origin 留空并使用 Vite 同源代理；Chat 配置键缺失或不符合 contract 时会直接拒绝发送请求。

## 验证

```bash
pnpm --filter @ascendany/desktop typecheck
pnpm --filter @ascendany/desktop test:unit
pnpm --filter @ascendany/desktop test:electron
pnpm --filter @ascendany/desktop build
```

SQLite local-state ABI、应用 protocol path、sandbox 和 permission controls 由独立测试保护。

## 正式发布

Windows NSIS 和 Linux RPM 发布都要求显式 SemVer、canonical HTTPS API origin、Chat 配置键和正式签名材料。发布目录必须预先不存在；脚本会验证签名并为最终安装包生成 SHA-512 文件。

两个发布脚本还要求提供最多 128 ASCII bytes 的 canonical SemVer、经过评审的 lowercase 40-hex Git commit，以及位于安全父目录下的绝对输出路径。正式发布必须使用独立 non-root release identity，并直接执行 builder 的 `/usr/bin/bash -p` shebang；package.json 不暴露 release script。缺少 privileged mode、`bash -c`、interactive/stdin execution、source stack 或 forged `$0` 会立即终止进程。builder 先捕获 raw commit object 并重新计算 commit hash，再逐个捕获 blob、用 `--no-filters` 重新计算 blob hash、通过 NUL-safe temporary index 重建 root tree。物化后的 detached source 会再走一次 `hash-object --no-filters` 与 `write-tree`，只有 materialized root tree 与 reviewed commit root tree 完全相等后，live builder 才能与其中固定 mode `100755` 的 builder 比较。Git attributes、filters、working tree、replace object、global config 与捕获后的 object-store 变更都无法改变构建输入。release identity 是受信任的 host capability boundary；发布期间禁止运行其他同 UID host process 或并发修改 live builder。

依赖安装、TypeScript build 与 `electron-builder` 全部在 bubblewrap 中运行。sandbox 使用 empty root，只读挂载 `/usr` 与 `ASCENDANY_DESKTOP_BUILD_TOOL_ROOT`，并显式挂载 detached source、output、private cache/store 和 Windows broker IPC；宿主 `/run`、`/var`、`/tmp`、完整 release HOME、live repository、P12 与 keyring均不可见。sandbox 还使用独立 PID/network/IPC/UTS namespace、new session、private `/proc`，并在 namespace init 退出时终止残留 descendant。launcher 继承的 descriptor 会在 bubblewrap exec 前按 closed allowlist 关闭。`ASCENDANY_DESKTOP_PNPM_STORE_PATH` 与 `ASCENDANY_DESKTOP_BUILD_CACHE_PATH` 分别指向 release host 上受保护的离线 pnpm store seed 和 Electron/electron-builder download cache seed；脚本把二者复制到一次性 private path，sandbox 只使用副本。build tool root、seed、输出目录与 Windows P12 必须位于 release HOME 和 live repository 之外。sandbox environment 由 allowlist 重建，不包含 P12、password、`GNUPGHOME`、caller npm/pnpm/Corepack/Node/Electron setting。输出父目录的 device/inode 会在 workspace 创建、isolated build 返回、签名、publication 前后重复校验。产物和目录在 atomic publication 前后执行 `fsync`。最终输出采用 exact two-file closed set：一个带版本号的安装包和一个只记录安装包 basename 的 `.sha512` 文件。

Windows 构建要求 `CSC_LINK` 指向当前发布用户独占读取、link count 为 1 的 canonical absolute PKCS#12 文件，并要求固定的 OpenSSL 与 `osslsigncode`。password environment contract 已移除；`CSC_KEY_PASSWORD`、`WIN_CSC_KEY_PASSWORD` 或 `CERTIFICATE_PASSWORD` 出现在 builder 初始 environment 会直接失败。launcher 通过 `ASCENDANY_DESKTOP_CSC_PASSWORD_FD` 传入 canonical inherited descriptor（范围 `3..1023`），payload 为 `1..4096` bytes 且不能包含 NUL、CR 或 LF。

`electron-builder` 的 certificate auto-discovery 与内建 signer 被强制关闭。reviewed custom sign hook 只允许 `AscendAny.exe`、`resources/elevate.exe`、NSIS uninstaller 和最终 NSIS installer 四个确定路径，只允许一次 SHA-256 Authenticode signing。hook 通过 private FIFO 请求 sandbox 外的独立 broker。builder 在验证 PKCS#12 后立即捕获并核对 file descriptor identity；broker 从 password FD 读取 PKCS#12，通过 OpenSSL `-passin fd:` 把 leaf certificate 与 private key 写入已 unlink 的 anonymous file descriptor；password、certificate 和 key 都不会进入 argv、environment、sandbox filesystem 或 Electron process。每次签名前 broker 通过 validated parent directory FD 锚定 allowlisted basename，将 unsigned PE 原子移入 private directory，固定 `osslsigncode` 完成签名与 expected leaf fingerprint 验证，并在 no-clobber rename 后核对 inode、size 与 digest。broker 拒绝 symlink、hardlink、重复 path、未知 path、错误 hash 与嵌套签名，并核对 exact four-entry request log。NSIS uninstaller 在被嵌入前完成同一 broker 签名。最终 installer 还会独立提取 Authenticode signer certificate，与 PKCS#12 leaf SHA-256 fingerprint 精确比对。

```bash
release_commit=0123456789abcdef0123456789abcdef01234567
release_parent=/srv/ascendany-release/output
build_tool_root=/opt/ascendany-release-tools
release_state=/var/lib/ascendany-release
install -d -m 0700 "$release_parent"

(
  exec {csc_password_fd}</var/lib/ascendany-release/credentials/windows-password
  ASCENDANY_DESKTOP_VERSION=2.0.0 \
  ASCENDANY_DESKTOP_RELEASE_COMMIT="$release_commit" \
  ASCENDANY_DESKTOP_OUTPUT_DIRECTORY="$release_parent/windows-2.0.0" \
  ASCENDANY_DESKTOP_NODE_PATH=/usr/bin/node \
  ASCENDANY_DESKTOP_PNPM_CLI_PATH="$build_tool_root/pnpm/bin/pnpm.cjs" \
  ASCENDANY_DESKTOP_BWRAP_PATH=/usr/bin/bwrap \
  ASCENDANY_DESKTOP_BUILD_TOOL_ROOT="$build_tool_root" \
  ASCENDANY_DESKTOP_PNPM_STORE_PATH="$release_state/pnpm-store-v10" \
  ASCENDANY_DESKTOP_BUILD_CACHE_PATH="$release_state/build-cache" \
  ASCENDANY_DESKTOP_OPENSSL_PATH=/usr/bin/openssl \
  ASCENDANY_DESKTOP_OSSLSIGNCODE_PATH=/usr/bin/osslsigncode \
  ASCENDANY_DESKTOP_CSC_PASSWORD_FD="$csc_password_fd" \
  CSC_LINK=/var/lib/ascendany-release/credentials/windows.p12 \
  VITE_API_BASE_URL=https://ascendany.kkkzbh.cn \
  VITE_CHAT_PROMPT_CONFIGURATION_KEY=agent.prompt.default \
  VITE_CHAT_MODEL_CONFIGURATION_KEY=agent.model.default \
    apps/desktop/scripts/build-windows-release.sh
)
```

Linux RPM 构建固定使用 canonical `$HOME/.gnupg`，该目录必须由发布用户独占且通过完整 protected-ancestry 校验。`GNUPGHOME` 省略或显式设为这个相同路径；其他 keyring 路径直接失败。keyring 中必须存在受信任的 secret signing key。`rpmsign` 使用一次性 empty HOME 与固定 root-owned RPM rc/macro path，release HOME 中的 RPM user macro不会参与签名。`ASCENDANY_RPM_SIGNING_FINGERPRINT` 必须提供实际签名 primary key 或 signing subkey 的完整 40-hex fingerprint；脚本从最终 RPM OpenPGP packet 提取 v4 issuer fingerprint，精确比对后再用隔离 RPM key database 验证产物签名：

```bash
ASCENDANY_DESKTOP_VERSION=2.0.0 \
ASCENDANY_DESKTOP_RELEASE_COMMIT="$release_commit" \
ASCENDANY_DESKTOP_OUTPUT_DIRECTORY="$release_parent/linux-rpm-2.0.0" \
ASCENDANY_RPM_SIGNING_FINGERPRINT=0123456789ABCDEF0123456789ABCDEF01234567 \
ASCENDANY_DESKTOP_NODE_PATH=/usr/bin/node \
ASCENDANY_DESKTOP_PNPM_CLI_PATH="$build_tool_root/pnpm/bin/pnpm.cjs" \
ASCENDANY_DESKTOP_BWRAP_PATH=/usr/bin/bwrap \
ASCENDANY_DESKTOP_BUILD_TOOL_ROOT="$build_tool_root" \
ASCENDANY_DESKTOP_PNPM_STORE_PATH="$release_state/pnpm-store-v10" \
ASCENDANY_DESKTOP_BUILD_CACHE_PATH="$release_state/build-cache" \
ASCENDANY_DESKTOP_GPG_PATH=/usr/bin/gpg \
ASCENDANY_DESKTOP_RPM_PATH=/usr/bin/rpm \
ASCENDANY_DESKTOP_RPMKEYS_PATH=/usr/bin/rpmkeys \
ASCENDANY_DESKTOP_RPMSIGN_PATH=/usr/bin/rpmsign \
GNUPGHOME="$HOME/.gnupg" \
VITE_API_BASE_URL=https://ascendany.kkkzbh.cn \
VITE_CHAT_PROMPT_CONFIGURATION_KEY=agent.prompt.default \
VITE_CHAT_MODEL_CONFIGURATION_KEY=agent.model.default \
  apps/desktop/scripts/build-linux-rpm-release.sh
```

签名材料无需进入 CI。离线 fixture 使用隔离 Git repository、真实 bubblewrap visibility probe 和 fake signing tools 覆盖 privileged/direct entry、source/`$0` spoof、commit/blob/tree re-hash、corrupt object、dirty live builder、live symlink、hostile Git attributes/config/path、empty-root sandbox、host HOME/AF_UNIX socket absence、ambient FD closure、P12 descriptor、Windows password `/proc`/argv/environment absence、dirfd-anchored closed sign path、artifact symlink victim、sign request log、hostile RPM user macro、unsafe HOME/tool/signing/output ancestry、output-parent device/inode replacement、Windows/RPM signer identity mismatch、`fsync`、cleanup、closed output 与 checksum portability：

```bash
pnpm --filter @ascendany/desktop test:release-scripts
```
