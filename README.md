# Go Agent Studio

Go Agent Studio 是一个 Go 写的本地 Agent 工程参考实现：Echo HTTP 服务、内嵌 Web 前端、SQLite 会话/Trace 持久化、LLM tool calling、显式 Agent 编排和 MCP 客户端扩展。

当前代码以三端分离为主线，默认首页只展示 Customer 产品文档问答；Operator 和 Admin 是隐藏入口：

- Customer：公开 Dify 产品文档问答
- Operator：需要 API Key 的内容运营助手
- Admin：需要 API Key 的管理控制台

## 快速启动

```powershell
cd go-agent-studio
copy .env.example .env
# 编辑 .env，填入 DEEPSEEK_API_KEY
go mod tidy
go run . serve
```

默认服务地址是 [http://127.0.0.1:9090](http://127.0.0.1:9090)。也可以用命令行覆盖监听地址：

```powershell
go run . serve --host 127.0.0.1 --port 9090
```

`.env` 会在 `serve` 启动时自动加载；如需禁用自动加载，请在启动进程环境里设置 `APP_LOAD_DOTENV=false`。

## 三端入口

### Customer 端

访问：[http://127.0.0.1:9090/](http://127.0.0.1:9090/) 或 `/index.html`

- 公开访问，不需要 API Key
- 使用 `SimpleChat`：LLM 直接 tool calling，执行工具后再综合回答
- 内置工具：`search_product_docs`
- 可选工具：连接成功的 MCP 工具会以 `{server}_{tool}` 名称暴露
- 会话上限：`CUSTOMER_MAX_TURNS`
- 每日访客上限：`CUSTOMER_MAX_DAILY_VISITORS`
- SSE 事件：`session`、`message`、`tool_call`、`usage`、`heartbeat`、`error`、`done`

`search_product_docs` 调用外部 RAG HTTP 服务，地址由 `CUSTOMER_RAG_API_ENDPOINT` 配置。当前仓库不包含本地 RAG 构建或检索服务。

## 设计取舍：有意绑定 Dify 演示场景

当前 Customer 端不是一个完全通用的知识库问答壳子，而是有意做成 **Dify 产品文档助手**。代码、Prompt、前端文案和工具描述里会出现 Dify、产品文档、工作流、知识库、发布等领域词，这是一个明确的演示取舍：

- 面试或 HR 演示时，单一主题比“什么资料都能问”的通用壳子更稳定，回答也更像真实产品助手。
- Dify 中文文档公开、内容中性、许可证清晰，适合展示 Agent 调工具、RAG 召回和最终综合回答。
- Agent 侧会对“本地 / 线上 / 安装 / 版本 / 主要功能”等追问补充 Dify 语境，目的是提升短追问的召回质量。

这不是因为不知道存在耦合，而是为了当前演示效果主动选择了领域绑定。若要扩展为通用知识库，建议把这些内容抽成配置：

- Go 代码里也存在已知 hard code，例如 `services/tools/customer/registry.go` 中的工具名 `search_product_docs`、工具描述、示例 query，以及 `enrichProductDocsQuery` 对“本地 / 线上 / 安装 / 版本 / 主要功能”等问题补充 Dify 关键词。
- 工具名从 `search_product_docs` 泛化为 `search_knowledge_base`。
- Prompt、欢迎语、示例问题、工具描述改为按知识库 profile 加载。
- 查询扩展词、同义词和领域关键词放到 RAG 的 `profile.json` 或 Agent 侧的 domain 配置里，不要长期写死在 Go 代码里。
- 不同资料使用不同 domain，例如 `dify_docs`、`company_handbook`、`product_manual`。

### Operator 端

访问：[http://127.0.0.1:9090/operator.html](http://127.0.0.1:9090/operator.html)

- 只有设置 `AGENT_OPERATOR_API_KEY` 时才注册 API 路由
- API Key 支持 `X-Agent-API-Key` 或 `Authorization: Bearer ...`
- 使用完整 Agent 编排：`LLMPlanner -> ToolRouter -> Executor -> WorkingMemory -> Synthesis`
- 内置工具：
  - `outline_creator`
  - `content_writer`
  - `style_refiner`
  - `craft_image_prompt`
  - `craft_video_prompt`
- 可选工具：连接成功的 MCP 工具会以 `{server}_{tool}` 名称暴露
- SSE 事件：`session`、`message`、`plan`、`step`、`route`、`execution`、`usage`、`heartbeat`、`error`、`done`

注意：当前 `serve` 中 `MaxToolStepsPerRun` 是硬编码 `4`，不读取环境变量覆盖。

### Admin 端

访问：[http://127.0.0.1:9090/admin.html](http://127.0.0.1:9090/admin.html)

- 只有设置 `AGENT_ADMIN_API_KEY` 时才注册 API 路由
- API Key 支持 `X-Agent-API-Key` 或 `Authorization: Bearer ...`
- 不提供 Admin Chat
- 提供系统状态、会话列表、会话详情、Trace 查询、会话清理和 MCP 状态
- Admin 工具注册表包含 `agent_status`，供能力状态和工具体系复用

## API

### Health

- `GET /health/live`
- `HEAD /health/live`
- `GET /health/ready`
- `HEAD /health/ready`

### Customer API

- `POST /api/v1/customer/chat`
- `POST /api/v1/customer/reset`
- `GET /api/v1/customer/tools`
- `GET /api/v1/customer/sessions`
- `GET /api/v1/customer/sessions/:id`
- `GET /api/v1/customer/sessions/:id/trace`

### Operator API

需要 `AGENT_OPERATOR_API_KEY`。

- `POST /api/v1/operator/agent/chat`
- `POST /api/v1/operator/agent/reset`
- `GET /api/v1/operator/agent/tools`
- `GET /api/v1/operator/agent/sessions`
- `GET /api/v1/operator/agent/sessions/:id`
- `GET /api/v1/operator/agent/sessions/:id/trace`

### Admin API

需要 `AGENT_ADMIN_API_KEY`。

- `GET /api/v1/admin/status`
- `GET /api/v1/admin/sessions`
- `GET /api/v1/admin/sessions/stats`
- `POST /api/v1/admin/sessions/cleanup`
- `GET /api/v1/admin/sessions/:id`
- `GET /api/v1/admin/sessions/:id/trace`
- `GET /api/v1/admin/mcp/status`

## 配置

最小可用配置：

```env
DEEPSEEK_API_KEY=your-deepseek-api-key
CUSTOMER_RAG_API_ENDPOINT=http://localhost:9093/api/search

# 可选：开启内部端
AGENT_OPERATOR_API_KEY=operator-key
AGENT_ADMIN_API_KEY=admin-key
```

`AGENT_OPERATOR_API_KEY` 和 `AGENT_ADMIN_API_KEY` 可以为空；为空时对应路由不会注册。非空时至少 6 个字符，且两者不能相同。

当前有效环境变量以 `config/config.go` 为准：

- 应用：`APP_NAME`、`APP_HOST`、`APP_PORT`、`APP_DB_PATH`、`APP_DEBUG`、`APP_ACCESS_LOG_ENABLED`
- `.env` 加载：启动进程环境里的 `APP_LOAD_DOTENV=false` 可禁用自动加载
- HTTP：`APP_READ_TIMEOUT`、`APP_WRITE_TIMEOUT`、`APP_IDLE_TIMEOUT`、`APP_SHUTDOWN_TIMEOUT`、`APP_BODY_LIMIT`、`APP_CORS_ALLOWED_ORIGINS`、`APP_SECURE_HEADERS_ENABLED`、`APP_HSTS_MAX_AGE`
- 全局限流：`APP_RATE_LIMIT_ENABLED`、`APP_RATE_LIMIT_RPS`、`APP_RATE_LIMIT_BURST`
- 聊天模型：`DEEPSEEK_API_KEY`、`DEEPSEEK_BASE_URL`、`DEEPSEEK_MODEL`、`AI_TEMPERATURE`、`AI_MAX_TOKENS`、`AI_REQUEST_TIMEOUT`
- MCP：`MCP_ENABLED`、`MCP_CONFIG_PATH`
- Agent：`AGENT_MAX_MESSAGE_CHARS`、`AGENT_CHAT_TIMEOUT`、`AGENT_TOOL_TIMEOUT`、`AGENT_PLANNER_TIMEOUT`、`AGENT_AUTO_CONFIRM_MCP`
- Customer：`CUSTOMER_MAX_TURNS`、`CUSTOMER_MAX_DAILY_VISITORS`、`CUSTOMER_RAG_API_ENDPOINT`
- Operator：`AGENT_OPERATOR_API_KEY`、`OPERATOR_MAX_TURNS`
- Admin：`AGENT_ADMIN_API_KEY`、`AGENT_AUDIT_LOG_ENABLED`、`ADMIN_SESSION_RETENTION_DAYS`

## 架构

Customer 流程：

```text
User -> SimpleChat
     -> LLM tool_calls
     -> Tool execution
     -> LLM synthesis
     -> SSE response + SQLite session/trace
```

Operator 流程：

```text
User -> Agent
     -> LLMPlanner(tool_calls -> Plan)
     -> ToolRouter
     -> Executor
     -> WorkingMemory
     -> LLM synthesis
     -> SSE response + SQLite session/trace
```

Admin 流程：

```text
User -> HTTP API
     -> SQLite / capability status / MCP manager
     -> JSON response
```

## 项目结构

```text
cmd/commands/          serve 命令、.env 加载
config/                环境变量加载与校验
modules/
  customer/            Customer API Handler
  operator/            Operator API Handler
  mcp/                 MCP 状态 API
services/
  simplechat/          Customer 使用的轻量对话循环
  agentcore/           Operator 使用的 Agent 核心
  orchestrator/        Plan / Route / Execute / Memory 编排
  tools/
    customer/          Customer 工具
    operator/          Operator 工具
    admin/             Admin 工具
  llm/                 DeepSeek 聊天适配器
  memory/              会话存储
  trace/               Trace 事件记录
  mcp/                 MCP 客户端
  persistence/         SQLite 初始化与迁移
server/
  router/              HTTP 路由
  web/assets/          内嵌前端资源
data/prompts/          外部化 prompt
```

## 建议阅读顺序

1. `main.go`
2. `cmd/commands/serve.go`
3. `server/router/router.go`
4. `services/simplechat/chat.go`
5. `services/agentcore/agent.go`
6. `services/orchestrator/loop.go`
7. `services/tools/{customer,operator,admin}/`

## 验证

```powershell
gofmt -l .
go vet ./...
go build ./...
```

## 扩展方向

1. 新工具：在 `services/tools/{customer|operator|admin}/` 注册。
2. DeepSeek 调优：修改 `DEEPSEEK_MODEL`、`AI_TEMPERATURE`、`AI_MAX_TOKENS` 等配置。
3. 外部 MCP：在 `mcp.json` 添加 `url` 或 `command`，设置 `disabled: false`。
4. Prompt 调优：直接修改 `data/prompts/agent_customer.txt`、`agent_operator.txt`、`agent_admin.txt`，无需重新编译。

## License

MIT
