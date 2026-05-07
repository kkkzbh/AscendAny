# Pintia 浏览器导出插件设计

## 目标

用户停留在任意 Pintia 题目集页面内，点击浏览器插件按钮，一键导出一个 AscendAny 可导入的数据包。

本设计假设：

- 人在浏览器中完成 Pintia 登录、进入考试/题目集、点击导出。
- AscendAny 本地只使用插件导出的离线数据包，不依赖 Pintia 登录态，也不直接请求 Pintia。
- 官方 Pintia 导出文件只作为可选快照，不作为主导入源。

## 数据边界

插件导出的最小单元就是一场考试。导出包使用单文件 JSON，后续如数据量明显膨胀，再升级为 zip/目录包。

文件名建议：

```text
ascendany-pintia-<problemSetId>-<yyyyMMdd-HHmmss>.json
```

顶层结构：

```json
{
  "schema": "ascendany.pintia.unit.v1",
  "exporter": {
    "name": "ascendany-pintia-exporter",
    "version": "0.1.0",
    "exportedAt": "2026-05-07T00:00:00.000Z",
    "sourceUrl": "https://pintia.cn/problem-sets/..."
  },
  "exam": {},
  "problems": [],
  "participants": [],
  "rankings": [],
  "submissions": [],
  "integrity": {}
}
```

## 必须导出的信息

### exam

- `platform`: 固定为 `pintia`
- `problemSetId`
- `title`
- `startAt` / `endAt` / `durationSeconds`
- `totalScore`
- `sourceUrl`
- `raw`: Pintia 原始摘要字段

### problems

每题保存结构化题面，而不是解析官方 HTML：

- `problemSetProblemId`
- `problemId`
- `displayIndex`: 如 `7-1`
- `title`
- `type`
- `score`
- `contentHtml` / `descriptionHtml`
- `samples`
- `limits`: 时间、内存、代码长度
- `testCaseGroups`: 公开可见的测试点名称、分值；不期待拿隐藏输入输出
- `knowledgePointPaths`
- `raw`

### participants / rankings

排行榜 API 是成绩单的主来源：

- `userId`
- `studentNo` / `account` / `emailOrPhone`
- `name`
- `groupName`
- `rank`
- `totalScore`
- `timeUsedSeconds`
- `problemScores`
- `validSubmitCount`
- `acceptTime`
- `raw`

### submissions

提交详情 API 是代码的主来源。列表只做索引，详情负责补代码。

- `submissionId`
- `problemSetProblemId`
- `problemId`
- `userId`
- `studentNo`
- `name`
- `submittedAt`
- `language`
- `status`
- `score`
- `timeMs`
- `memoryKb`
- `compiler`
- `code`
- `codeSha256`
- `caseResults`
- `compileLog`
- `raw`

## 完整性约束

导出结束时写入 `integrity`：

```json
{
  "problemCount": 15,
  "participantCount": 35,
  "submissionCount": 624,
  "submissionDetailCount": 624,
  "codeCount": 624,
  "warnings": []
}
```

本地导入器应拒绝明显不完整的数据包：

- `submissionCount > submissionDetailCount`
- `codeCount < submissionDetailCount` 且不是题型不含代码
- 缺少 `schema`
- `problemSetId` 为空

## 插件架构

```text
popup
  -> background service worker
     -> current tab route warm-up
        -> content-script
           -> page-bridge
              -> Pintia 当前路由已加载的前端 API 解码器
     -> restore original URL
     -> validate and download JSON
```

原因：

- Pintia 多个接口返回 protobuf，不适合在插件里直接维护解码协议。
- 页面自己的前端 bundle 已经包含 API 调用和 protobuf 解码器。
- 插件只借用浏览器内的 Pintia 登录态和页面解码器，最终导出的是普通 JSON。
- 第一版采用当前页跳转预热路由，不使用隐藏后台标签页。

## 一键导出流程

1. popup 连接 background service worker 并发起导出任务。
2. background 读取当前 tab URL，提取 `problemSetId`，记录原始 URL。
3. background 依次跳转当前 tab 到：
   - `/problem-sets/<id>/problems/type/7`
   - `/problem-sets/<id>/rankings`
   - `/problem-sets/<id>/submissions`
4. 每个路由加载完成后，content-script 注入 page-bridge；page-bridge 捕获当前 Webpack runtime。
5. 依次拉取：
   - 题目集摘要
   - 题目结构
   - 排行榜/成绩
   - 提交列表分页
   - 每条提交详情和代码
6. background 恢复原始 URL。
7. background 生成并校验 `ascendany.pintia.unit.v1` JSON。
8. background 调用 `chrome.downloads.download` 保存文件。

## 不采用官方导出的原因

官方导出的主要问题：

- `成绩单.xlsx` 只有面向人的最终成绩，缺少稳定 ID、提交时间线、代码、判题细节。
- `得分代码.zip` 不是完整提交历史，只包含部分得分代码。
- `答卷.zip` / `答题卡.zip` 是渲染 HTML，适合复核，不适合结构化导入。
- ZIP 的中文文件名存在兼容性问题，自动化解析不稳定。

官方导出可以作为 `rawOfficial` 可选附件，但不进入主链路。

## 与 AscendAny 本地导入的关系

后续本地导入器只需要支持：

```text
practice/<exam_type>/<exam_name>/ascendany-pintia-unit.json
```

或者直接通过导入控制台上传该 JSON。

导入时以 `(platform, problemSetId)` 或 `(exam_type, source_path)` 做考试幂等键；以 `submissionId` 或 `row_hash` 做提交幂等键；代码内容进入 OJ 代码记录或新的 Pintia 提交明细表。
