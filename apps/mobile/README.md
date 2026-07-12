# AscendAny Mobile

独立的 Capacitor/React 移动应用，只调用 AscendAny Go v2 API。

## 已实现功能

- 本地账号登录与 HttpOnly refresh session
- 消费管理员发放的一次性 enrollment claim
- 当前学生的五维能力、Rating 和考试历史
- 已发布学生排行榜
- 持久化 Chat/Agent 对话、SSE 运行事件续传与推理详情
- 更新当前账号显示名称
- 查看并撤销当前账号的会话

头像上传和桌面设置没有对应的 v2 contract，因此不进入 Mobile 运行路径。

## 安全边界

应用使用 `@ascendany/sdk` 的 `BrowserSession`：

- access token 只存在于当前 WebView 内存；
- refresh credential 由 API origin 的 HttpOnly cookie 保存；
- WebView storage 只保存与 API origin 绑定的旋转 CSRF token；
- 每次业务请求前调用 `ensureAuthenticated()`；
- 应用不读取 URL credential，也不持久化 access token、refresh credential 或 enrollment claim。

## 配置

Web 开发默认使用当前页面 origin，Vite 将 `/api` 代理到 `http://127.0.0.1:18000`：

```bash
pnpm --filter @ascendany/mobile dev
```

原生 Android 构建必须显式提供 canonical HTTPS API origin：

```bash
VITE_API_BASE_URL=https://ascendany.kkkzbh.cn \
VITE_CHAT_PROMPT_CONFIGURATION_KEY=agent.prompt.default \
VITE_CHAT_MODEL_CONFIGURATION_KEY=agent.model.default \
  pnpm --filter @ascendany/mobile sync:android
```

缺少 `VITE_API_BASE_URL` 的原生运行会直接显示配置错误。Chat 配置键缺失或不满足 configuration key contract 时，应用会拒绝发送 Agent 请求。Go runtime 必须允许 Capacitor WebView 的实际 Origin，并在目标设备上通过 refresh-cookie/CSRF E2E 验证。

正式 APK 只能直接执行 `apps/mobile/scripts/build-android-release.sh`。package script 不提供 release entrypoint。`apps/mobile/android/` 是 reviewed commit 中的固定 native tree，`sync:android` 只允许 `cap sync`，缺少 tree 会失败，也不会执行 `cap add` 生成 scaffold。wrapper 从指定的 reviewed 40-hex Git commit 逐 blob materialize 隔离 snapshot，实时工作树与 Git attributes 不参与 APK。

release host contract：

- 最多 128 ASCII bytes 的 canonical SemVer 与正整数 version code；
- Gradle worker 上限必须显式设置为 `1..256` 的 canonical integer，prefetch 与正式 build 使用同一值；
- canonical HTTPS API origin 和两项 configuration key；
- Node.js `>=22.18.0,<23` binary 与 pnpm JavaScript entry 使用显式 absolute canonical path，文件由 root/release user 所有且没有 group/other write；
- `/usr/bin/bwrap` 由 root 所有，用于所有 Node/pnpm/Gradle build namespace；
- 必须显式提供 canonical `JAVA_HOME` 与 `ANDROID_HOME`，JDK tree、Android 36 platform、Build Tools `36.0.0` 和 Platform Tools 的 descendant owner/mode/type 会被递归验证；directory symlink 与 special node 会失败；
- Android SDK `apksigner` 使用显式 absolute canonical executable path；launcher 与 sibling `lib/apksigner.jar` 必须分别匹配 wrapper 内 reviewed 的 Android Build Tools 36.0.0 SHA-256，并复制到 private signing root 后再次校验。caller `PATH` 与 caller-provided digest 都不参与 signer identity；
- keystore 使用无 symlink 的 absolute canonical path，由当前 release user 所有，权限为 `0600` 或更严格，并且 hard-link count 必须为一；
- signer fingerprint 使用完整的 64 位 lowercase SHA-256 certificate digest；
- output directory 尚不存在，其 parent 由当前 release user 所有且没有 group/other write 权限。

wrapper 只能由 absolute `#!/usr/bin/bash -p` entrypoint 启动；non-privileged Bash、sourced invocation、`BASH_ENV` 和 exported Bash function 注入都会被拒绝。启动后立即禁用 core dump，把 basename system tool resolution 固定为 `/usr/bin:/bin`。executing wrapper 必须逐 byte 等于 reviewed commit 内 mode `0755` 的 wrapper。Gradle 8.14.3 distribution、launcher、wrapper properties、wrapper JAR 和 `gradle/verification-metadata.xml` 都有固定 SHA-256；install/sync 后会再次校验。

Node/pnpm 与 Gradle 在 Bubblewrap PID namespace、新 `/proc`、minimal filesystem mounts 和 `--die-with-parent` 下运行。bwrap 自身也由 `env -i` 启动，namespace PID 1 只继承固定 `PATH`/`LC_ALL`。keystore 在 build namespace 中映射为 `/dev/null`，build code 看不到签名材料与 host process。pnpm `fetch` 只把 lockfile integrity-bound tarball 写入 private store；`install` 使用 mobile workspace filter、`--offline --ignore-scripts --frozen-lockfile`，`sync:android` 也在无网络 namespace 中执行。Gradle 先在独立 prefetch source copy 中联网执行 strict verified `assembleRelease` 填充 private cache，随后删除该 copy；正式 `SOURCE_ROOT` 使用同一 cache，以 `--offline --no-build-cache --dependency-verification=strict` 在无网络 namespace 中重新 assemble。正式产物只接受一个 regular `app-release-unsigned.apk`。

两项签名密码必须分别通过不同 FD 输入。每个 FD 恰好包含一项 non-empty NUL-terminated secret；密码禁止进入 wrapper environment 或 argv。下例中的 `secret-manager ... --nul` 表示运维 secret provider 的 pipe 输出：

```bash
JAVA_HOME=/opt/ascendany-release-tools/jdk-21 \
ANDROID_HOME=/opt/android-sdk \
apps/mobile/scripts/build-android-release.sh \
    --version 2.0.0 \
    --version-code 20000 \
    --gradle-max-workers 4 \
    --commit 0123456789abcdef0123456789abcdef01234567 \
    --output-dir /secure/releases/ascendany-android-2.0.0 \
    --api-origin https://ascendany.kkkzbh.cn \
    --prompt-key agent.prompt.default \
    --model-key agent.model.default \
    --node-bin /usr/bin/node-22 \
    --pnpm-entry /opt/ascendany-release-tools/pnpm-9.15.4.cjs \
    --apksigner-bin /opt/android-sdk/build-tools/36.0.0/apksigner \
    --keystore /secure/keys/ascendany-release.jks \
    --key-alias ascendany \
    --store-password-fd 3 \
    --key-password-fd 4 \
    --signer-sha256 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef \
    3< <(secret-manager read android-store-password --nul) \
    4< <(secret-manager read android-key-password --nul)
```

所有 build namespace 退出后，wrapper 才创建随机 mode `0700` signing root 以及全新的 signing `HOME`/`TMPDIR`。reviewed signer launcher/JAR 被复制到该 root 并重新计算 SHA-256；private `tool-bin/java` 只解析到已验证的 `JAVA_HOME/bin/java`，launcher 的其他 basename tools 继续来自 `/usr/bin:/bin`。private launcher 的 `sign` 在 closed environment 中临时接收密码，`verify` environment 不含密码。wrapper 调用 `apksigner verify -Werr --verbose --print-certs`，要求 APK 恰好只有一个 signer digest 且与 `--signer-sha256` 精确一致。发布文件固定命名为 `AscendAny-Android-<version>.apk`，同目录生成 portable SHA-512 sidecar。exact two-file closed set 在 staged copy 与 no-replace rename 后分别校验 digest、size、mode、owner、link count 和 sidecar，再 fsync artifact、directory 与 output parent。最终 sync 或复核失败时，wrapper 仅在 target directory 与预先保持的 descriptor identity 同时匹配时删除失败发布。

Signer pin 来源为 Google 官方 `repository2-3.xml` 中的 Linux `build-tools_r36_linux.zip`：size `63737259`、SHA-1 `b0b6376977657e8ad9b969bacf4093601da2c6fb`。验证该 archive 后，`android-16/apksigner` 的 SHA-256 为 `b47549e373b895ce6ca620d0c7887e674d9615ffa837a86ac601dcfd04adb0f0`，`android-16/lib/apksigner.jar` 的 SHA-256 为 `3716d9311e55d2b0918a2fd9d54ba9e406c5f6abeea700b287f11259bc163dec`。

Gradle task graph 自身也执行 wrapper gate。直接调用 `:app:assembleRelease`、`:app:bundleRelease`，以及会展开 release variant 的 aggregate `:app:assemble`、`:app:build`、`:app:bundle`，缺少 wrapper marker、release version 或 version code 时都会直接失败。Gradle 永远不接收 keystore path、alias 或 signing password，最终签名只归 release wrapper 所有。

## 验证

```bash
pnpm --filter @ascendany/mobile exec tsc -b
pnpm --filter @ascendany/mobile test
pnpm --filter @ascendany/mobile build
apps/mobile/scripts/test-android-release-contract.sh
```
