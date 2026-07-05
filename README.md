# Go Agent Demo

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.23+-00ADD8.svg)](https://go.dev)

> ⚠️ **重要提示**：本项目仅供技术学习与演示用途。项目中涉及的劳动法相关内容由 AI 生成，**不具有法律效力，不构成法律建议**。使用者应确保遵守所在国家和地区的法律法规，因使用本软件产生的任何后果由使用者自行承担。详见 [DISCLAIMER.md](DISCLAIMER.md)。

Go 智能体工程平台。集成显式编排引擎、多领域 RAG、MCP 协议扩展和角色隔离，可作为 Agent 应用的参考实现或二次开发基础。

## 功能特性

- **显式编排引擎** — Planner → Router → Executor → Memory → 综合生成，单趟执行，每步可追踪
- **多供应商支持** — 聊天 / 图片 / 视频 / Embedding 独立配置，支持 Ark、DeepSeek、OpenAI、Anthropic
- **多领域 RAG** — 通用引擎 + 领域配置注入（同义词、权重、来源过滤），新增领域不改引擎
- **MCP 协议** — HTTP/stdio 双传输，支持动态工具扩展
- **角色隔离** — Customer / Operator / Admin 三端独立工具集和会话空间
- **Prompt 外部化** — 提示词存为 `.txt` 文件，修改不需重新编译
- **SSE 流式 + Trace** — 实时事件推送 + SQLite 持久化调用链路
- **零 Mock** — 无 API key 直接报错，不返回假数据

## 技术栈

Go 1.23+ · Echo · SQLite（纯 Go，无 CGO）· 内嵌 Web 前端

## 快速开始

```bash
git clone https://github.com/your-username/go-agent-demo.git
cd go-agent-demo

cp .env.simple .env
# 编辑 .env，填入 API key

go run . rag-build   # 构建 RAG 索引
go run . serve       # 启动服务
```

打开 http://127.0.0.1:9090

```bash
# 可选：启动 MCP 工具服务
go run . mcp-serve --port 9091
```

### Docker

```bash
docker build -t go-agent-demo .
docker run --rm -p 9090:9090 -e ARK_API_KEY=your_key go-agent-demo
```

含 Ollama 本地 Embedding 的 Compose 部署见 `docker-compose.yml`。

## 配置

最简配置只需填一个 API key（`.env.simple`）：

```env
ARK_API_KEY=your_key
ARK_CHAT_MODEL=doubao-seed-2-0-pro-260215
AI_EMBEDDING_PROVIDER=ark
ARK_EMBEDDING_MODEL=doubao-embedding-vision-250615
```

完整配置见 `.env.example`：

| 能力 | Provider |
|---|---|
| 聊天 | ark · deepseek · openai · openai-compatible · anthropic |
| Embedding | local · ark · openai · openai-compatible |
| 图片 | ark · openai |
| 视频 | ark |

## 架构

```
User → Planner(工具 schema + 用户目标 → tool_calls → Plan)
     → Router(选工具 + 补参数)
     → Executor(超时 + 重试 + 截断)
     → Memory(提取事实 + 收集结果)
     → Synthesis(LLM 综合生成答案)
```

Planner 一次性把用户目标映射为多个工具步骤，Executor 单趟遍历执行后综合作答。内置安全栏限制单次运行的工具步骤数。

## 项目结构

```text
cmd/commands/          CLI: serve, rag-build, mcp-serve
config/                环境变量加载与校验
modules/               HTTP Handler (agent, rag, mcp)
services/
  agentcore/           Agent 运行调度
  orchestrator/        Planner / Router / Executor / Memory
  llm/                 LLM Provider 实现
  rag/                 RAG 引擎 + Embedding
  tools/               内置工具注册
  mcp/                 MCP 客户端
  memory/              会话存储
  trace/               调用链路追踪
  persistence/         SQLite 连接管理
server/                路由注册 + 内嵌前端
data/prompts/          系统提示词（外部化）
data/domains/          RAG 语料 / 索引 / 配置
```

## 工具

| 工具 | 角色 | 说明 |
|---|---|---|
| `search_labor_law` | 全部 | RAG 知识库检索 |
| `craft_image_prompt` | 全部 | LLM 润色图片生成 Prompt |
| `craft_video_prompt` | 全部 | LLM 编写短视频分镜 Prompt |
| `example_now` | 全部 | MCP 示例：当前时间 |
| `example_text_stats` | 全部 | MCP 示例：字数统计 |
| `agent_status` | Admin | 系统状态与配置 |
| `generate_image` | Admin | 调用图片生成 API |
| `generate_video` | Admin | 调用视频生成 API |

MCP 工具通过 `mcp.json` 配置，支持 HTTP 和 stdio 两种接入方式。

## API

| 角色 | 端点 | 可见事件 |
|---|---|---|
| Customer | `/api/v1/agent/chat` | session · message · usage · done |
| Operator | `/api/v1/operator/agent/chat` | + plan · step · route · execution |
| Admin | `/api/v1/admin/agent/chat` | 全部 |

```
GET  /health/live                    健康检查
GET  /api/v1/agent/tools             工具列表
POST /api/v1/agent/chat              聊天（SSE 流式响应）
GET  /api/v1/admin/status            系统状态
GET  /api/v1/admin/sessions          会话列表
POST /api/v1/admin/sessions/cleanup  会话清理
GET  /api/v1/admin/mcp/status        MCP 状态
```

## RAG

通用搜索引擎，通过配置文件注入领域行为。默认使用关键词 + 向量两路混合评分。支持同义词扩展、来源过滤、可插拔 Embedding。

新增领域：

```bash
# 准备 JSONL 语料 + profile.json
go run . rag-build --domain <domain_name>
```

## 验证

```bash
gofmt -l .            # 应无输出
go vet ./...
staticcheck ./...
go test ./...
go build ./...
```

## 扩展

1. **新领域** — `data/domains/` 添加语料和配置
2. **新工具** — `services/tools/registry.go` 注册
3. **新 Provider** — 实现 `aitypes.LLMProvider` 接口
4. **MCP 扩展** — `mcp.json` 添加服务端配置
5. **真实 RAG** — 切换 Embedding Provider + 接入向量数据库
6. **多轮编排** — 在编排引擎外层包有界循环，支持跨步骤依赖

## 免责声明

本软件仅供技术学习与演示用途，不构成法律意见或专业咨询。详见 [DISCLAIMER.md](DISCLAIMER.md)。

## License

[MIT](LICENSE)
