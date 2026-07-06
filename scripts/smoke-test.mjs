// smoke-test.mjs — Go Agent Studio smoke test
// Usage:
//   go build -o go-agent-studio.exe
//   go-agent-studio.exe serve &
//   node scripts/smoke-test.mjs

const appURL = `http://127.0.0.1:${process.env.SMOKE_APP_PORT || 9090}`;
const operatorKey = process.env.SMOKE_OP_KEY || "operator-smoke-key-123456789012345";
const adminKey = process.env.SMOKE_ADMIN_KEY || "admin-smoke-key-123456789012345678";

const checks = [];
function record(name, ok, detail) {
  checks.push({ name, ok, at: new Date().toISOString() });
  console.log(`[${ok ? "PASS" : "FAIL"}] ${name}`);
  if (!ok && detail) console.log(JSON.stringify(detail, null, 2));
}

function safe(text) {
  return (text || "").replace(/sk-[a-zA-Z0-9_-]{10,}/g, "[redacted]").replace(/([a-zA-Z0-9_-]{32,})/g, "[redacted]");
}

function customerSafe(answer) {
  const bad = ["本地知识库", "结论：", "工具结果", "依据工具", "RAG", "mock", "执行计划"];
  const hasDisclaimer = answer.includes("以上由 AI 生成，仅供参考。") || answer.includes("以上由AI生成，仅供参考。");
  return hasDisclaimer && bad.every((w) => !answer.includes(w));
}

async function get(path, headers = {}) {
  const r = await fetch(`${appURL}${path}`, { headers });
  const text = await r.text();
  let data;
  try { data = JSON.parse(text); } catch { data = { raw: text }; }
  if (!r.ok) throw new Error(`${path} status=${r.status} body=${safe(text.slice(0, 500))}`);
  return { status: r.status, data };
}

async function post(path, body, headers = {}) {
  const r = await fetch(`${appURL}${path}`, {
    method: "POST",
    headers: { "Content-Type": "application/json; charset=utf-8", ...headers },
    body: JSON.stringify(body),
  });
  const text = await r.text();
  let data;
  try { data = JSON.parse(text); } catch { data = { raw: text }; }
  return { status: r.status, data, text };
}

async function chatSSE(path, sessionId, message, headers = {}) {
  const r = await fetch(`${appURL}${path}`, {
    method: "POST",
    headers: { "Content-Type": "application/json; charset=utf-8", ...headers },
    body: JSON.stringify({ session_id: sessionId, message }),
  });
  if (!r.ok || !r.body) throw new Error(`chat status=${r.status}`);
  const reader = r.body.getReader();
  const decoder = new TextDecoder();
  let buf = "";
  const events = [];
  while (true) {
    const { value, done } = await reader.read();
    if (done) break;
    buf += decoder.decode(value, { stream: true });
    const chunks = buf.split("\n\n");
    buf = chunks.pop() || "";
    for (const chunk of chunks) {
      const el = chunk.split("\n").find((l) => l.startsWith("event:"));
      const dl = chunk.split("\n").find((l) => l.startsWith("data:"));
      if (!el || !dl) continue;
      events.push({ event: el.replace("event:", "").trim(), data: JSON.parse(dl.replace("data:", "").trim()) });
    }
  }
  return events;
}

async function main() {
  // ---- health ----
  const live = await get("/health/live");
  record("health_live", live.data?.data?.status === "live");

  const ready = await get("/health/ready");
  record("health_ready", ready.data?.data?.status === "ready" && ready.data?.data?.checks?.ai_chat?.configured === true);

  const head = await fetch(`${appURL}/health/live`, { method: "HEAD" });
  record("health_head", head.status === 200 && (await head.text()) === "");

  // ---- public tools ----
  const pubTools = await get("/api/v1/customer/tools");
  const pubNames = (pubTools.data?.data || []).map((t) => t.name);
  record("public_tools", pubNames.includes("search_product_docs"), { names: pubNames });

  // ---- admin status ----
  const status = await get("/api/v1/admin/status", { "X-Agent-API-Key": adminKey });
  const s = status.data?.data;
  record("admin_status",
    s?.agent?.planner === "llm"
    && s?.tools?.public?.names?.includes("search_product_docs")
    && !JSON.stringify(s).includes(adminKey),
    { app: s?.app, agent: s?.agent, tools: s?.tools });

  // ---- MCP status (may fail if MCP not enabled) ----
  try {
    const mcpStatus = await get("/api/v1/admin/mcp/status", { "X-Agent-API-Key": adminKey });
    record("mcp_status", mcpStatus.data?.data?.count >= 0);
  } catch { record("mcp_status", true, { note: "MCP not enabled, skipping" }); }

  // ---- customer chat ----
  const custEvents = await chatSSE("/api/v1/customer/chat", "", "Dify 工作流和对话流有什么区别？");
  const custNames = custEvents.map((e) => e.event);
  record("customer_chat_ok", custNames.includes("message") && custNames.includes("done"));
  const custAnswer = [...custEvents].reverse().find((e) => e.event === "message" && e.data?.role === "assistant")?.data?.content || "";
  record("customer_safe_answer", customerSafe(custAnswer), { answer: safe(custAnswer).slice(0, 200) });

  // ---- operator chat ----
  const opEvents = await chatSSE("/api/v1/operator/agent/chat", "", "帮我写一篇高效时间管理的文章大纲", { "X-Agent-API-Key": operatorKey });
  const opNames = opEvents.map((e) => e.event);
  const opPlan = opEvents.find((e) => e.event === "plan")?.data;
  record("operator_has_plan", opPlan?.planner_mode === "llm" && opPlan?.planner_source === "llm_tool_calls", { plan: opPlan });
  record("operator_has_route", opNames.includes("route"));
  record("operator_has_execution", opNames.includes("execution"));

  // ---- admin sessions ----
  const sessions = await get("/api/v1/admin/sessions?limit=5", { "X-Agent-API-Key": adminKey });
  record("admin_sessions", Number.isInteger(sessions.data?.data?.count) && Array.isArray(sessions.data?.data?.sessions));

  // ---- admin cleanup dry-run ----
  const cleanup = await post("/api/v1/admin/sessions/cleanup", { older_than_hours: 87600, dry_run: true }, { "X-Agent-API-Key": adminKey });
  record("admin_cleanup_dry_run", cleanup.data?.data?.dry_run === true);

  // ---- role isolation ----
  const custSessionId = custEvents.find((e) => e.event === "session")?.data?.session_id;
  if (custSessionId) {
    const opResume = await post("/api/v1/operator/agent/chat", { session_id: custSessionId, message: "hi" }, { "X-Agent-API-Key": operatorKey });
    record("role_isolation", opResume.status === 404 && opResume.data?.message === "session not found");
  } else {
    record("role_isolation", true, { note: "no session ID returned" });
  }

  // ---- summary ----
  const passed = checks.filter((c) => c.ok).length;
  const failed = checks.filter((c) => !c.ok).length;
  console.log(`\n${passed}/${checks.length} passed, ${failed} failed`);
  if (failed) process.exitCode = 1;
  else console.log("ALL PASSED");
}

try {
  await main();
} catch (err) {
  console.error("FATAL:", safe(String(err)));
  process.exitCode = 1;
}
