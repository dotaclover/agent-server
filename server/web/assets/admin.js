// Storage and validation settings
const STORAGE_ADMIN_KEY = "go-agent-studio-admin-key";

// DOM Elements - Login Mode
const loginContainer = document.getElementById("loginContainer");
const loginForm = document.getElementById("loginForm");
const adminKeySecret = document.getElementById("adminKeySecret");
const loginSubmitBtn = document.getElementById("loginSubmitBtn");
const loginErrorMsg = document.getElementById("loginErrorMsg");

// DOM Elements - Workspace Mode
const workspaceContainer = document.getElementById("workspaceContainer");
const profileBtn = document.getElementById("profileBtn");
const profileDropdown = document.getElementById("profileDropdown");
const changeKeyInput = document.getElementById("changeKeyInput");
const changeKeySaveBtn = document.getElementById("changeKeySaveBtn");
const changeKeyCancelBtn = document.getElementById("changeKeyCancelBtn");

// DOM Elements - Admin Panels
const capabilityMatrix = document.getElementById("capabilityMatrix");
const mcpStatusBody = document.getElementById("mcpStatusBody");
const sessionListBody = document.getElementById("sessionListBody");
const refreshSessionsBtn = document.getElementById("refreshSessionsBtn");
const sessionRoleFilter = document.getElementById("sessionRoleFilter");
const sessionTimeFilter = document.getElementById("sessionTimeFilter");

// Initialize page state
initPage();

function initPage() {
  const cachedKey = localStorage.getItem(STORAGE_ADMIN_KEY);
  loginErrorMsg.classList.add("hidden");
  loginErrorMsg.style.display = "none";
  if (cachedKey) {
    loginContainer.classList.add("hidden");
    workspaceContainer.classList.remove("hidden");
    loadAdminData();
  } else {
    loginContainer.classList.remove("hidden");
    workspaceContainer.classList.add("hidden");
  }
}

function getAdminKey() {
  return localStorage.getItem(STORAGE_ADMIN_KEY) || "";
}

// Authentication check helper
async function verifyKey(key) {
  const response = await fetch("/api/v1/admin/status", {
    method: "GET",
    headers: { "X-Agent-API-Key": key }
  });
  return response.ok;
}

// Login form submit handler
loginForm.addEventListener("submit", async (e) => {
  e.preventDefault();
  const key = adminKeySecret.value.trim();
  if (!key) return;

  loginSubmitBtn.disabled = true;
  loginSubmitBtn.textContent = "正在校验密钥...";
  loginErrorMsg.style.display = "none";

  try {
    const isValid = await verifyKey(key);
    if (isValid) {
      localStorage.setItem(STORAGE_ADMIN_KEY, key);
      adminKeySecret.value = "";
      initPage();
    } else {
      loginErrorMsg.classList.remove("hidden");
      loginErrorMsg.style.display = "block";
      loginErrorMsg.textContent = "鉴权失败: 密钥无效，请核对您的 Admin API Key。";
    }
  } catch (err) {
    loginErrorMsg.classList.remove("hidden");
    loginErrorMsg.style.display = "block";
    loginErrorMsg.textContent = "网络校验故障: 无法连接服务。";
  } finally {
    loginSubmitBtn.disabled = false;
    loginSubmitBtn.textContent = "解锁控制台";
  }
});

// Profile / dropdown key modification triggers
profileBtn.addEventListener("click", (e) => {
  e.stopPropagation();
  profileDropdown.classList.toggle("show");
  changeKeyInput.value = "";
  if (profileDropdown.classList.contains("show")) {
    changeKeyInput.focus();
  }
});

changeKeyCancelBtn.addEventListener("click", (e) => {
  e.stopPropagation();
  profileDropdown.classList.remove("show");
});

changeKeySaveBtn.addEventListener("click", async (e) => {
  e.stopPropagation();
  const newKey = changeKeyInput.value.trim();
  if (!newKey) {
    alert("请输入新的 Admin API Key！");
    return;
  }

  changeKeySaveBtn.disabled = true;
  changeKeySaveBtn.textContent = "校验中";

  try {
    const isValid = await verifyKey(newKey);
    if (isValid) {
      localStorage.setItem(STORAGE_ADMIN_KEY, newKey);
      profileDropdown.classList.remove("show");
      alert("Admin 密钥修改成功！");
      loadAdminData();
    } else {
      alert("修改失败: 输入的密钥校验未通过。");
    }
  } catch (_) {
    alert("校验发生网络异常。");
  } finally {
    changeKeySaveBtn.disabled = false;
    changeKeySaveBtn.textContent = "保存";
  }
});

// Hide key dropdown if clicking backdrop
window.addEventListener("click", () => {
  profileDropdown.classList.remove("show");
});
profileDropdown.addEventListener("click", (e) => {
  e.stopPropagation();
});

// Fetch wrapper
async function adminFetch(url, options = {}) {
  const key = getAdminKey();
  if (!key) {
    localStorage.removeItem(STORAGE_ADMIN_KEY);
    initPage();
    throw new Error("Missing Key");
  }
  options.headers = {
    ...options.headers,
    "X-Agent-API-Key": key
  };
  return fetch(url, options);
}

/* =========================================================================
   ADMIN PANEL APIS
   ========================================================================= */
async function loadAdminData() {
  loadSystemStatus();
  loadMCPStatus();
  refreshSessionsBtn.click();
}

async function loadSystemStatus() {
  try {
    capabilityMatrix.innerHTML = `<div class="status text-center text-muted grid-col-all">正在拉取配置矩阵...</div>`;
    const response = await adminFetch("/api/v1/admin/status");
    if (!response.ok) {
      capabilityMatrix.innerHTML = `<div class="status text-center text-error grid-col-all">连接校验失败: ${response.statusText}</div>`;
      return;
    }
    const resData = await response.json();
    const status = resData.data || {};
    
    capabilityMatrix.innerHTML = `
      <div class="capability-card">
        <span class="capability-title">核心会话数据库 (SQLite)</span>
        <div class="capability-info">
          <span class="status-indicator ok"></span>运行正常
        </div>
        <div class="capability-detail">
          路径: ${status.persistence?.database_configured ? "data/agent_studio.db" : "未配置"}<br>
          会话持久化: ${status.persistence?.memory_persistent ? "已开启" : "已关闭"}<br>
          Trace日志: ${status.persistence?.trace_persistent ? "已开启" : "已关闭"}
        </div>
      </div>
      
      <div class="capability-card">
        <span class="capability-title">Chat 聊天模型供应商</span>
        <div class="capability-info">
          Provider: <strong class="text-primary">${status.chat?.provider || "mock"}</strong>
        </div>
        <div class="capability-detail">
          使用模型: ${status.chat?.model || "local-mock"}<br>
          端点: ${status.chat?.base_url || "local"}<br>
          Planner: LLM 驱动
        </div>
      </div>
      
      <div class="capability-card">
        <span class="capability-title">外部劳动法 RAG</span>
        <div class="capability-info">
          Customer 工具: <strong class="text-success">${status.tools?.customer?.names?.includes("search_labor_law") ? "已注册" : "未注册"}</strong>
        </div>
        <div class="capability-detail">
          检索能力由外部 HTTP 服务提供。<br>
          主程序只负责调用 search_labor_law 工具。
        </div>
      </div>
      
      <div class="capability-card">
        <span class="capability-title">Operator 内容工具</span>
        <div class="capability-info">
          已注册: <strong class="text-primary">${status.tools?.operator?.count || 0}</strong> 个
        </div>
        <div class="capability-detail">
          ${(status.tools?.operator?.names || []).join(" / ") || "未注册"}
        </div>
      </div>
    `;

  } catch (error) {
    capabilityMatrix.innerHTML = `<div class="status text-center text-error grid-col-all">请求大模型矩阵状态网络异常</div>`;
  }
}

async function loadMCPStatus() {
  try {
    mcpStatusBody.innerHTML = `<tr><td colspan="4" class="status text-center text-muted">正在加载 MCP 工具清单...</td></tr>`;
    const response = await adminFetch("/api/v1/admin/mcp/status");
    if (!response.ok) {
      mcpStatusBody.innerHTML = `<tr><td colspan="4" class="status text-center text-error">MCP 载入异常: ${response.statusText}</td></tr>`;
      return;
    }
    const resData = await response.json();
    const mcpData = resData.data || {};
    const serversRaw = mcpData.servers_detail || mcpData.servers || [];
    const servers = serversRaw.map(srv => {
      if (typeof srv === "string") {
        return { name: srv, transport: "stdio", tool_count: 0, status: "connected" };
      }
      return srv;
    });

    if (servers.length === 0) {
      mcpStatusBody.innerHTML = `<tr><td colspan="4" class="status text-center text-muted">未发现已注册的外部 MCP Server。请在 mcp.json 中开启配置。</td></tr>`;
      return;
    }

    mcpStatusBody.innerHTML = "";
    servers.forEach(srv => {
      const row = document.createElement("tr");
      row.innerHTML = `
        <td><strong>${srv.name}</strong></td>
        <td>${srv.transport || "stdio"}</td>
        <td>${srv.tool_count || 0} 个</td>
        <td>
          <span class="status-pill">
            <span class="status-indicator ${srv.status === "connected" ? "ok" : "err"}"></span>
            ${srv.status === "connected" ? "已连接" : "已断开"}
          </span>
        </td>
      `;
      mcpStatusBody.appendChild(row);
    });

  } catch (error) {
    mcpStatusBody.innerHTML = `<tr><td colspan="4" class="status text-center text-error">获取 MCP 状态网络异常</td></tr>`;
  }
}

// Session database table management
refreshSessionsBtn.addEventListener("click", async () => {
  try {
    sessionListBody.innerHTML = `<tr><td colspan="5" class="status text-center text-muted p-30-0">正在拉取 SQLite 会话列表...</td></tr>`;
    
    const role = sessionRoleFilter.value;
    const hours = sessionTimeFilter.value;
    
    let url = `/api/v1/admin/sessions?limit=50&updated_within_hours=${hours}`;
    if (role) {
      url += `&role=${role}`;
    }
    
    const response = await adminFetch(url);
    if (!response.ok) {
      sessionListBody.innerHTML = `<tr><td colspan="5" class="status text-center text-error p-30-0">获取会话列表失败: ${response.statusText}</td></tr>`;
      return;
    }
    
    const resData = await response.json();
    const sessions = resData.data?.sessions || resData.sessions || [];

    if (sessions.length === 0) {
      sessionListBody.innerHTML = `<tr><td colspan="5" class="status text-center text-muted p-30-0">未发现符合查询条件的会话记录。</td></tr>`;
      return;
    }

    sessionListBody.innerHTML = "";
    sessions.forEach(sess => {
      const row = document.createElement("tr");
      const dateStr = sess.updated_at ? new Date(sess.updated_at).toLocaleString() : "-";
      const roleStr = sess.role || "legacy";
      
      row.innerHTML = `
        <td><code class="text-xxs">${sess.id}</code></td>
        <td><span class="role-badge ${roleStr}">${roleStr}</span></td>
        <td>${sess.message_count || 0} 轮对话</td>
        <td>${dateStr}</td>
        <td>
          <button class="btn btn-sm btn-dialogue-detail" data-id="${sess.id}">查看对话</button>
          <button class="btn btn-sm btn-primary btn-trace-detail" data-id="${sess.id}">查看 Trace</button>
        </td>
      `;
      sessionListBody.appendChild(row);
    });

  } catch (error) {
    sessionListBody.innerHTML = `<tr><td colspan="5" class="status text-center text-error p-30-0">加载列表时网络连接异常</td></tr>`;
  }
});

// Event delegation for view buttons to bypass CSP
sessionListBody.addEventListener("click", (e) => {
  const dialogueBtn = e.target.closest(".btn-dialogue-detail");
  if (dialogueBtn) {
    const sessId = dialogueBtn.getAttribute("data-id");
    showDialogueDetail(sessId);
    return;
  }
  const traceBtn = e.target.closest(".btn-trace-detail");
  if (traceBtn) {
    const sessId = traceBtn.getAttribute("data-id");
    showTraceDetail(sessId);
    return;
  }
});

// View dialogue modal handler
const dialogueModal = document.getElementById("dialogueModal");
const dialogueModalMessages = document.getElementById("dialogueModalMessages");
const dialogueModalTitle = document.getElementById("dialogueModalTitle");
const closeDialogueModalBtn = document.getElementById("closeDialogueModalBtn");

window.showDialogueDetail = async function(sessionId) {
  try {
    dialogueModalTitle.textContent = `会话对话历史: ${sessionId}`;
    dialogueModalMessages.innerHTML = `<div class="status text-center text-muted">正在加载对话内容...</div>`;
    dialogueModal.classList.remove("hidden");
    
    const response = await adminFetch(`/api/v1/admin/sessions/${sessionId}`);
    if (!response.ok) {
      dialogueModalMessages.innerHTML = `<div class="status text-center text-error">获取会话失败: ${response.statusText}</div>`;
      return;
    }
    
    const resData = await response.json();
    const sessionObj = resData.data?.session || resData.session || {};
    const messages = sessionObj.messages || [];

    // Render Working Memory
    const memoryEl = document.getElementById("dialogueModalMemory");
    const summaryEl = document.getElementById("dialogueModalSummary");
    const factsEl = document.getElementById("dialogueModalFacts");
    
    if (sessionObj.summary || (sessionObj.facts && sessionObj.facts.length)) {
      memoryEl.classList.remove("hidden");
      summaryEl.textContent = sessionObj.summary || "无会话摘要";
      factsEl.innerHTML = "";
      if (sessionObj.facts && sessionObj.facts.length) {
        sessionObj.facts.forEach(fact => {
          const li = document.createElement("li");
          li.textContent = fact;
          factsEl.appendChild(li);
        });
      } else {
        factsEl.innerHTML = `<li class="text-muted">暂无提取事实清单</li>`;
      }
    } else {
      memoryEl.classList.add("hidden");
    }

    if (messages.length === 0) {
      dialogueModalMessages.innerHTML = `<div class="status text-center text-muted">会话空空如也，无消息记录。</div>`;
      return;
    }

    dialogueModalMessages.innerHTML = "";
    messages.forEach(msg => {
      const bubble = document.createElement("div");
      bubble.className = `chat-bubble ${msg.role}`;
      bubble.textContent = msg.content;
      dialogueModalMessages.appendChild(bubble);
    });

  } catch (error) {
    dialogueModalMessages.innerHTML = `<div class="status text-center text-error">网络异常，加载对话细节失败。</div>`;
  }
};

closeDialogueModalBtn.addEventListener("click", () => {
  dialogueModal.classList.add("hidden");
});

// View trace modal handler
const traceModal = document.getElementById("traceModal");
const traceModalTimeline = document.getElementById("traceModalTimeline");
const traceModalTitle = document.getElementById("traceModalTitle");
const closeTraceModalBtn = document.getElementById("closeTraceModalBtn");

window.showTraceDetail = function(sessionId) {
  traceModalTitle.textContent = `会话可观测 Trace 轨迹: ${sessionId}`;
  traceModal.classList.remove("hidden");
  loadSessionTrace(sessionId, traceModalTimeline);
};

closeTraceModalBtn.addEventListener("click", () => {
  traceModal.classList.add("hidden");
});

// Close modal when clicking on layout backdrop
window.addEventListener("click", (e) => {
  if (e.target === dialogueModal) dialogueModal.classList.add("hidden");
  if (e.target === traceModal) traceModal.classList.add("hidden");
});

// Load session trace
async function loadSessionTrace(sessionId, targetElement) {
  try {
    targetElement.innerHTML = `<div class="status text-center text-muted">正在查询 SQLite Trace 日志...</div>`;
    const response = await adminFetch(`/api/v1/admin/sessions/${sessionId}/trace`);
    if (!response.ok) {
      targetElement.innerHTML = `<div class="status text-center text-error">无法载入 Trace: ${response.statusText}</div>`;
      return;
    }
    const resData = await response.json();
    const events = resData.data || resData || [];
    
    if (events.length === 0) {
      targetElement.innerHTML = `<div class="status text-center text-muted">此会话暂无 Trace 日志项。</div>`;
      return;
    }
    
    targetElement.innerHTML = "";
    events.forEach((evt, idx) => {
      const node = document.createElement("div");
      node.className = `trace-node ${evt.status || "info"}`;
      
      const dateStr = evt.created_at ? new Date(evt.created_at).toLocaleTimeString() : "";
      const durationStr = evt.duration_ms ? ` (${evt.duration_ms}ms)` : "";
      const payloadId = `trace-payload-${sessionId}-${idx}`;
      
      let payloadJSON = "";
      if (evt.payload) {
        try {
          payloadJSON = JSON.stringify(evt.payload, null, 2);
        } catch (_) {}
      }

      const header = document.createElement("div");
      header.className = "trace-header";

      const title = document.createElement("span");
      title.textContent = `[${evt.type}] ${evt.tool_name ? `Tool: ${evt.tool_name}` : ""}`;
      header.appendChild(title);

      const time = document.createElement("span");
      time.className = "trace-time";
      time.textContent = `${dateStr}${durationStr}`;
      header.appendChild(time);
      node.appendChild(header);

      const message = document.createElement("div");
      message.className = "trace-msg";
      message.textContent = evt.message || "";
      node.appendChild(message);

      if (payloadJSON) {
        const toggle = document.createElement("button");
        toggle.className = "trace-payload-toggle";
        toggle.dataset.target = payloadId;
        toggle.textContent = "显示原始报文 (JSON)";
        node.appendChild(toggle);

        const payload = document.createElement("pre");
        payload.className = "trace-payload";
        payload.id = payloadId;
        payload.textContent = payloadJSON;
        node.appendChild(payload);
      }
      targetElement.appendChild(node);
    });

  } catch (error) {
    targetElement.innerHTML = `<div class="status text-center text-error">加载 Trace 时网络异常</div>`;
  }
}

window.toggleTracePayload = function(id) {
  const el = document.getElementById(id);
  if (el) {
    el.classList.toggle("show-block");
  }
};

// Event delegation for trace timeline payload toggles
traceModalTimeline.addEventListener("click", (e) => {
  if (e.target && e.target.classList.contains("trace-payload-toggle")) {
    const targetId = e.target.getAttribute("data-target");
    toggleTracePayload(targetId);
  }
});

/* =========================================================================
   DATABASE CLEANUP
   ========================================================================= */
const cleanupHours = document.getElementById("cleanupHours");
const cleanupRole = document.getElementById("cleanupRole");
const cleanupDryRunBtn = document.getElementById("cleanupDryRunBtn");
const cleanupPurgeBtn = document.getElementById("cleanupPurgeBtn");
const cleanupResultContainer = document.getElementById("cleanupResultContainer");
const cleanupResultHeader = document.getElementById("cleanupResultHeader");
const cleanupResultDetails = document.getElementById("cleanupResultDetails");

async function performCleanup(dryRun, confirmDelete) {
  try {
    cleanupResultContainer.classList.remove("hidden");
    cleanupResultHeader.textContent = dryRun ? "正在生成清理试运行预览..." : "正在执行物理删除...";
    cleanupResultDetails.textContent = "";

    const role = cleanupRole.value;
    const hours = parseInt(cleanupHours.value, 10);
    
    if (isNaN(hours) || hours <= 0) {
      alert("请输入正确的失效小时数！");
      return;
    }

    const payload = {
      older_than_hours: hours,
      dry_run: dryRun,
      confirm_delete: confirmDelete
    };
    if (role) {
      payload.role = role;
    }

    const response = await adminFetch("/api/v1/admin/sessions/cleanup", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify(payload)
    });

    if (!response.ok) {
      const errData = await response.json().catch(() => ({}));
      cleanupResultHeader.textContent = "操作失败";
      cleanupResultDetails.textContent = `错误: ${errData.message || response.statusText}`;
      return;
    }

    const resData = await response.json();
    const summary = resData.data || resData || {};

    if (dryRun) {
      cleanupResultHeader.textContent = "试运行预览 (Dry Run Preview) 完成";
      cleanupResultDetails.textContent = `命中失效会话数: ${summary.matched_sessions || 0} 个\n` +
        `按角色过滤: ${summary.role || "未分配角色 (全部)"}\n` +
        `拟清理会话 ID 清单:\n${(summary.session_ids || []).join("\n") || "无匹配项"}`;
    } else {
      cleanupResultHeader.textContent = "数据库物理删除成功";
      cleanupResultDetails.textContent = `已清除会话数: ${summary.deleted_sessions || 0} 个\n` +
        `按角色过滤: ${summary.role || "未分配角色 (全部)"}\n` +
        `被清理会话 ID 清单:\n${(summary.session_ids || []).join("\n") || "无"}`;
      
      // Auto-refresh the session manager list if visible
      refreshSessionsBtn.click();
    }

  } catch (error) {
    cleanupResultHeader.textContent = "网络中断";
    cleanupResultDetails.textContent = "清理失败，网络发生故障，请重新登录鉴权测试。";
  }
}

cleanupDryRunBtn.addEventListener("click", () => {
  performCleanup(true, false);
});

cleanupPurgeBtn.addEventListener("click", () => {
  const hours = cleanupHours.value;
  const roleName = cleanupRole.value || "所有";
  const confirmMsg = `警告！您正准备物理删除数据库中：\n` +
    `- 创建或更新时间早于 ${hours} 小时以前的\n` +
    `- 角色过滤条件为：[${roleName}] 的所有会话和 Trace 日志项。\n\n` +
    `此操作不可恢复，确定要彻底删除吗？`;
    
  if (confirm(confirmMsg)) {
    performCleanup(false, true);
  }
});
