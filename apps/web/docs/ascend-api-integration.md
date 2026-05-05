# Ascend API 集成使用文档

本文档介绍 AscendWeb 与 Ascend Django 后端 API 的集成方案，包括配置、使用方法和视觉效果说明。

---

## 目录

- [概述](#概述)
- [快速开始](#快速开始)
- [配置说明](#配置说明)
- [功能特性](#功能特性)
- [API 接口](#api-接口)
- [组件说明](#组件说明)
- [知识点映射](#知识点映射)
- [故障排除](#故障排除)

---

## 概述

本集成实现了以下核心功能：

| 功能 | 描述 |
|------|------|
| **掌握度亮度映射** | 标签球亮度与学生知识点掌握度关联 |
| **薄弱知识点雷电特效** | 低掌握度的核心标签显示动态雷电环绕效果 |
| **学习路径激光流** | API 计算的学习路径以有向激光流形式连接标签 |

### 架构图

```
┌─────────────────┐     HTTP/JSON      ┌──────────────────┐
│  Ascend Django  │◄──────────────────►│    AscendWeb     │
│     API         │                     │   React + D3     │
└─────────────────┘                     └──────────────────┘
       │                                        │
       ▼                                        ▼
 ┌───────────────┐                    ┌──────────────────┐
 │ API 端点       │                    │   新增组件        │
 │ - /mastery/*  │                    │ - StudentContext │
 │ - /path/plan  │                    │ - PathConnectors │
 └───────────────┘                    │ - LightningEffect│
                                      └──────────────────┘
```

---

## 快速开始

### 1. 启动（推荐）

在 `code/django_project/` 下执行：

```bash
python start.py
```

Windows 也可以继续用 `start.bat`（旧版，多窗口）。

默认端口：

- 后端（Django API）：`http://127.0.0.1:8000`
- 前端（Vite）：`http://127.0.0.1:5175/`

### 2. 手动启动（可选）

```bat
cd code/django_project
python manage.py runserver 127.0.0.1:8000

cd code/django_project/AscendWeb
npm install
npm run dev
```

说明：Vite dev server 会代理 `/api` 与 `/ws` 到后端（见 `AscendWeb/vite.config.ts`）。

---

## 配置说明

### 环境变量（Vite）

| 变量名 | 默认值 | 描述 |
|--------|--------|------|
| `VITE_BACKEND_ORIGIN` | `` | Django 后端 Origin（为空则使用相对路径；本地开发通常走 Vite proxy） |
| `VITE_APP_BASENAME` | `/` | SPA Router basename |

### student 标识

后端会校验请求体里的 `student`/`student_id` 必须等于当前登录用户的 `full_name`（内部令牌调用除外）。前端默认从登录态读取 `full_name`，不再使用 URL 参数传入 studentId。

---

## 功能特性

### 1. 掌握度亮度映射

标签球的亮度根据学生对该知识点的掌握程度动态调整：

```
亮度 = 0.3 + 掌握度 × 0.7
发光强度 = 掌握度 × 0.5
```

| 掌握度 | 亮度 | 视觉效果 |
|--------|------|----------|
| 0% | 30% | 暗淡，几乎不发光 |
| 50% | 65% | 中等亮度 |
| 100% | 100% | 明亮，强烈发光 |

### 2. 薄弱知识点雷电特效

当某个知识点被 API 标记为薄弱点时，该标签球周围会显示动态雷电环绕效果：

- **颜色**: 蓝白色 (`#4da6ff`)
- **动画**: 闪烁 + 路径抖动
- **滤镜**: `lightningGlow` 发光效果

### 3. 学习路径激光流

API 返回的学习路径会以激光流形式可视化：

- **颜色**: 青色到蓝色渐变 (`#00ffff` → `#0040ff`)
- **动画**: 粒子沿路径流动
- **方向**: 箭头标记指示学习顺序
- **显示条件**: 仅在主视图（未选中任何标签）时显示

---

## API 接口

### 获取学生掌握度

```typescript
POST /api/mastery/knowledge-points
```

**请求体示例：**

```json
{
  "student": "<full_name>",
  "include_recommendations": true,
  "include_hierarchy": true,
  "weak_point_threshold": 0.6
}
```

说明：`student` 通常传当前登录用户的 `full_name`（后端会校验一致性）。同时兼容接收 `student_id`（历史字段名）。

**响应格式：**

```json
{
  "knowledge_mastery": {
    "基本概念": { "mastery": 0.8, "level": "熟练" },
    "线性结构": { "mastery": 0.5, "level": "一般" },
    "树": { "mastery": 0.3, "level": "薄弱" }
  },
  "weak_points": ["树", "图"],
  "summary": {
    "overall_mastery": 0.6
  }
}
```

### 获取学习路径

```typescript
POST /api/path/plan
```

**请求体示例：**

```json
{
  "student": "<full_name>",
  "top_n_targets": 5,
  "min_evidence": 1,
  "include_mastered": false,
  "mastered_threshold": 0.8,
  "attempt_penalty_alpha": 0.15
}
```

**响应格式：**

```json
{
  "targets": ["动态规划", "图"],
  "path": ["基本概念", "线性结构", "树", "图", "动态规划"]
}
```

---

## 组件说明

### StudentContext

学生数据状态管理 Context，提供以下数据：

```typescript
interface StudentState {
  studentId: string | null;      // 学生标识（默认=登录用户 full_name）
  masteryMap: Record<string, number>;  // 标签ID → 掌握度
  weakPoints: string[];          // 薄弱知识点ID列表
  learningPath: string[];        // 学习路径（标签ID顺序）
  loading: boolean;              // 加载状态
  error: string | null;          // 错误信息
  overallMastery: number;        // 整体掌握度
}
```

**使用示例：**

```tsx
import { useStudent } from '../contexts/StudentContext';

function MyComponent() {
  const { masteryMap, weakPoints, loading } = useStudent();

  if (loading) return <div>加载中...</div>;

  return (
    <div>
      {Object.entries(masteryMap).map(([tagId, mastery]) => (
        <div key={tagId}>
          {tagId}: {(mastery * 100).toFixed(0)}%
          {weakPoints.includes(tagId) && ' ⚡ 薄弱点'}
        </div>
      ))}
    </div>
  );
}
```

### PathConnectors

学习路径连接线组件，渲染激光流效果：

```tsx
<PathConnectors
  gRef={svgGroupRef}
  learningPath={['basic', 'linear', 'tree']}
  tagPositions={positionsMap}
  visible={true}
/>
```

### LightningEffect

雷电环绕效果组件：

```tsx
<LightningEffect
  cx={100}        // 中心X坐标
  cy={100}        // 中心Y坐标
  radius={50}     // 环绕半径
  color="#4da6ff" // 雷电颜色
  intensity={1}   // 强度 (0-1)
/>
```

---

## 知识点映射

API 返回的知识点名称与前端标签ID的对应关系：

| API 知识点名称 | Web 标签 ID |
|---------------|------------|
| 基本概念 | `basic` |
| 线性结构 | `linear` |
| 树 | `tree` |
| 模拟 | `simulation` |
| 搜索 | `search` |
| 图 | `graph` |
| 数据结构 | `data-structure` |
| 算法 | `algorithm` |
| 数学 | `math` |
| 动态规划 | `dp` |
| 贪心 | `greedy` |
| 字符串 | `string` |

如需添加新的映射，请编辑 `src/services/api.ts` 中的 `knowledgePointMapping` 对象。

---

## 故障排除

### API 连接失败

**症状**: 控制台显示 `[StudentContext] API fetch failed`

**解决方案**:

1. 确认 Ascend API 服务已启动
2. 检查 `.env` 中的 `VITE_BACKEND_ORIGIN` 配置
3. 确认 CORS 配置正确

### 提交后数据未更新（五维指标/掌握度/路径）

现象：提交后立刻刷新主页/知识图谱，数据仍是旧的。

排查顺序：

1. 确认 outbox 已 sent（`AscendSubmissionOutbox.status=sent`）
2. 确认 Ascend ingest 成功（后端日志无 500）
3. 若只看到 hover 计数变化但掌握度/指标不变：
   - hover 的 `tag-detail` 会额外查询 OJ DB 补齐未 ingest 的提交，因此可能看起来更“实时”
   - `mastery/metrics/path` 仍以 Ascend submissions 数据为主，需要等 ingest 到达后再请求

补充：主页的 `/api/metrics/student` 默认开启 `use_cache=true`；当前后端已在 ingest 时对受影响学生做版本化失效，正常情况下刷新页面即可看到更新。如需强制跳过缓存，可传 `use_cache=false`。

**Django CORS 配置**：

本项目通过环境变量 `CORS_ALLOWED_ORIGINS`（逗号分隔）控制允许的前端 Origin，例如：

```bash
CORS_ALLOWED_ORIGINS=http://localhost:5175,http://127.0.0.1:5175
```

提示：`start.py`（或 `start.bat`）会自动设置该变量；如你修改了前端端口，需要同步更新。

### 降级模式

当 API 不可用时，系统自动进入降级模式：

- 所有标签显示默认亮度 (50%)
- 无雷电特效
- 无学习路径连接线
- 基本交互功能正常

### 标签位置不正确

如果学习路径连接线位置异常：

1. 确认 `CoreTags` 组件的 `onPositionsUpdate` 回调已正确传递
2. 检查 `tagPositions` Map 是否包含路径中的所有标签ID

### 特效不显示

1. 确认浏览器支持 SVG 滤镜
2. 检查 `SVGDefinitions` 组件是否正确渲染
3. 查看控制台是否有相关错误

---

## 文件结构

```
src/
├── services/
│   └── api.ts                 # API 服务层
├── contexts/
│   └── StudentContext.tsx     # 学生数据 Context
├── components/
│   └── KnowledgeGraph/
│       ├── PathConnectors.tsx # 路径连接线组件
│       ├── effects/
│       │   └── LightningEffect.tsx  # 雷电效果组件
│       ├── SVGDefinitions.tsx # SVG 滤镜定义（已扩展）
│       ├── CoreTags.tsx       # 核心标签（已扩展）
│       ├── types.ts           # 类型定义（已扩展）
│       └── ...
└── App.tsx                    # 应用入口（已集成 StudentProvider）
```

---

## 更新日志

### v1.0.0 (2025-01-25)

- 初始集成 Ascend API
- 实现掌握度亮度映射
- 实现薄弱知识点雷电特效
- 实现学习路径激光流连接线
- 添加降级策略支持
