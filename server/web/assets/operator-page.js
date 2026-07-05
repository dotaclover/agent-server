// Operator Page Script
const historyStorageKey = "operator_session_history";
const activeSessionStorageKey = "operator_active_session_id";

// DOM References
const messagesEl = document.getElementById("messages");
const suggestionsEl = document.getElementById("suggestions");
const form = document.getElementById("chatForm");
const input = document.getElementById("messageInput");
const resetBtn = document.getElementById("resetBtn");
const historyList = document.getElementById("historyList");
const clearAllHistoryBtn = document.getElementById("clearAllHistoryBtn");
const activeChatTitle = document.getElementById("activeChatTitle");

// Sidebar Drawer
const sidebarTraceBtn = document.getElementById("sidebarTraceBtn");
const traceDrawer = document.getElementById("traceDrawer");
const closeTraceDrawerBtn = document.getElementById("closeTraceDrawerBtn");
const traceSessionValue = document.getElementById("traceSessionValue");
const modalCopySessionBtn = document.getElementById("modalCopySessionBtn");
const traceModalTimeline = document.getElementById("traceModalTimeline");

// Mobile controls
const mobileMenuBtn = document.getElementById("mobileMenuBtn");
const closeSidebarBtn = document.getElementById("closeSidebarBtn");
const sidebar = document.querySelector(".app-sidebar");

// State
let apiKey = sessionStorage.getItem("operator_api_key") || "";
let sessionId = sessionStorage.getItem(activeSessionStorageKey) || "";
let isProcessing = false;
let tracePollTimer = null;

// Initialize on DOM load
document.addEventListener("DOMContentLoaded", () => {
  // Check API Key
  const apiKeyPrompt = document.getElementById("api-key-prompt");
  if (!apiKey) {
    apiKeyPrompt.classList.remove("hidden");
  } else {
    initApp();
  }

  // API Key event listeners
  document.getElementById("api-key-save-btn").addEventListener("click", saveAPIKey);
  document.getElementById("api-key-input").addEventListener("keypress", (e) => {
    if (e.key === "Enter") saveAPIKey();
  });
});

function saveAPIKey() {
  const keyInput = document.getElementById("api-key-input");
  const value = keyInput.value.trim();
  if (value && value.length >= 6) {
    apiKey = value;
    sessionStorage.setItem("operator_api_key", apiKey);
    document.getElementById("api-key-prompt").classList.add("hidden");
    initApp();
  } else {
    alert("请输入6位以上的有效API Key");
  }
}

function initApp() {
  // Setup Event Listeners
  resetBtn.addEventListener("click", () => startNewSession(false));
  clearAllHistoryBtn.addEventListener("click", clearAllHistory);
  
  if (sidebarTraceBtn) {
    sidebarTraceBtn.addEventListener("click", openTraceDrawer);
  }
  if (closeTraceDrawerBtn) {
    closeTraceDrawerBtn.addEventListener("click", closeTraceDrawer);
  }
  if (modalCopySessionBtn) {
    modalCopySessionBtn.addEventListener("click", copyActiveSessionId);
  }

  // Mobile Menu toggles
  if (mobileMenuBtn && sidebar) {
    mobileMenuBtn.addEventListener("click", () => sidebar.classList.add("open"));
  }
  if (closeSidebarBtn && sidebar) {
    closeSidebarBtn.addEventListener("click", () => sidebar.classList.remove("open"));
  }

  // Suggestions chips
  if (suggestionsEl) {
    suggestionsEl.addEventListener("click", (e) => {
      const btn = e.target.closest("button");
      if (!btn) return;
      const question = btn.dataset.question;
      if (question) {
        input.value = question;
        input.focus();
      }
    });
  }

  // Textarea enter submission
  input.addEventListener("keydown", (e) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      form.dispatchEvent(new Event("submit"));
    }
  });

  // Initial UI and history updates
  updateSessionUI();
  updateSuggestionsVisibility();

  if (sessionId) {
    loadSession(sessionId);
  } else {
    startNewSession(false);
  }

  // Sync session history from server on load
  syncHistoryWithServer();

  input.focus();
}

// Session Local Cache Helpers
function getLocalHistory() {
  try {
    const raw = localStorage.getItem(historyStorageKey);
    return raw ? JSON.parse(raw) : [];
  } catch (e) {
    return [];
  }
}

function saveLocalHistory(history) {
  try {
    localStorage.setItem(historyStorageKey, JSON.stringify(history));
  } catch (e) {
    // Fails silently
  }
}

function saveStoredSessionId(id) {
  if (id) {
    sessionStorage.setItem(activeSessionStorageKey, id);
  } else {
    sessionStorage.removeItem(activeSessionStorageKey);
  }
}

function clearStoredSessionId() {
  sessionStorage.removeItem(activeSessionStorageKey);
}

function formatHistoryTitle(title, fallbackTitle) {
  const value = (title || fallbackTitle || "新创作会话").trim();
  return value.length > 18 ? value.substring(0, 18) + "..." : value;
}

function addToLocalHistory(id, titleText, fallbackTitle = "新创作会话") {
  if (!id) return;
  const history = getLocalHistory();
  const existingIdx = history.findIndex(item => item.id === id);
  
  const existingTitle = existingIdx > -1 ? history[existingIdx].title : "";
  const title = formatHistoryTitle(titleText || existingTitle, fallbackTitle);
  const entry = {
    id: id,
    title: title,
    timestamp: Date.now()
  };

  if (existingIdx > -1) {
    history.splice(existingIdx, 1);
  }
  history.unshift(entry);
  saveLocalHistory(history);
  renderSidebarHistory();
}

function deleteFromLocalHistory(id) {
  // Call server reset to delete the session in SQLite database
  fetch(`/api/v1/operator/agent/reset`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "Authorization": "Bearer " + apiKey
    },
    body: JSON.stringify({session_id: id})
  }).catch(() => {});

  let history = getLocalHistory();
  history = history.filter(item => item.id !== id);
  saveLocalHistory(history);
  renderSidebarHistory();
  
  if (sessionId === id) {
    startNewSession(false);
  }
}

function clearAllHistory() {
  if (!confirm("确定要清空您的所有创作历史记录吗？这将在服务器上物理删除所有相关数据。")) return;
  
  const history = getLocalHistory();
  history.forEach(item => {
    fetch(`/api/v1/operator/agent/reset`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "Authorization": "Bearer " + apiKey
      },
      body: JSON.stringify({session_id: item.id})
    }).catch(() => {});
  });

  localStorage.removeItem(historyStorageKey);
  startNewSession(false);
}

function renderSidebarHistory() {
  if (!historyList) return;
  const history = getLocalHistory();
  historyList.innerHTML = "";

  if (history.length === 0) {
    historyList.innerHTML = `<li class="history-empty">暂无历史记录</li>`;
    return;
  }

  history.forEach(item => {
    const li = document.createElement("li");
    li.className = `history-item ${item.id === sessionId ? 'active' : ''}`;
    li.dataset.id = item.id;
    
    const label = document.createElement("span");
    label.className = "history-item-label";
    label.textContent = item.title;
    label.addEventListener("click", () => {
      loadSession(item.id);
      if (window.innerWidth <= 800 && sidebar) {
        sidebar.classList.remove("open");
      }
    });
    
    const deleteBtn = document.createElement("button");
    deleteBtn.className = "btn-delete-history";
    deleteBtn.innerHTML = `<svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"></polyline><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"></path></svg>`;
    deleteBtn.title = "删除会话记录";
    deleteBtn.addEventListener("click", (e) => {
      e.stopPropagation();
      deleteFromLocalHistory(item.id);
    });

    li.appendChild(label);
    li.appendChild(deleteBtn);
    historyList.appendChild(li);
  });
}

function updateSessionUI() {
  renderSidebarHistory();
  if (sessionId) {
    if (sidebarTraceBtn) sidebarTraceBtn.classList.remove("hidden");
    const history = getLocalHistory();
    const activeItem = history.find(item => item.id === sessionId);
    activeChatTitle.textContent = activeItem ? activeItem.title : "旧创作会话";
  } else {
    if (sidebarTraceBtn) sidebarTraceBtn.classList.add("hidden");
    activeChatTitle.textContent = "新创作会话";
  }
}

// Session Loading
async function loadSession(sessId) {
  if (!sessId) return;
  try {
    messagesEl.innerHTML = `<div class="status">正在载入创作记录...</div>`;
    const response = await fetch(`/api/v1/operator/agent/sessions/${sessId}`, {
      headers: { "Authorization": "Bearer " + apiKey }
    });
    if (response.status === 404) {
      deleteFromLocalHistory(sessId);
      return;
    }
    if (response.status === 401) {
      handleUnauthorized();
      return;
    }
    if (!response.ok) {
      messagesEl.innerHTML = `<div class="status text-center text-error">加载失败，请重试</div>`;
      return;
    }
    const result = await response.json();
    const session = result.data?.session;
    
    sessionId = sessId;
    saveStoredSessionId(sessionId);
    updateSessionUI();
    
    if (session && session.messages) {
      messagesEl.innerHTML = "";
      if (session.messages.length === 0) {
        showWelcomeMessage();
      } else {
        session.messages.forEach(msg => {
          addMessage(msg);
        });
      }
      updateSuggestionsVisibility();
    }
  } catch (error) {
    console.error("Error loading session history:", error);
    messagesEl.innerHTML = `<div class="status text-center text-error">网络错误，请刷新页面</div>`;
  }
}

function startNewSession(callServerReset = false) {
  if (callServerReset && sessionId) {
    fetch(`/api/v1/operator/agent/reset`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "Authorization": "Bearer " + apiKey
      },
      body: JSON.stringify({session_id: sessionId})
    }).catch(() => {});
  }
  
  sessionId = "";
  clearStoredSessionId();
  updateSessionUI();
  messagesEl.innerHTML = "";
  showWelcomeMessage();
  updateSuggestionsVisibility();
  input.focus();
}

function showWelcomeMessage() {
  addMessage({
    role: "assistant",
    content: `✍️ 您好！我是您的 **AI 写作助手**。在此创作端中，我可以使用完整的大语言模型编排（Orchestration）为您创作高质量内容：

1. 📋 **大纲策划**：为您的文章、故事或视频脚本策划结构化的章节大纲（可配置目标受众与通俗/专业深度）。
2. 📝 **内容撰写**：按大纲分段撰写，支持对话（Dialogue）、一问一答（Q&A）或叙事正文，预设多种语气。
3. ✨ **风格润色**：根据需求进行学术化、简洁化、生动故事化或小红书文案风的多预设精细抛光。
4. 🎨 **提示词生成**：生成适合外部图片/视频生成工具的绘图提示词与短视频分镜提示词。

请在下方描述您的创作需求，例如："帮我策划一篇职场干货文章大纲"或"生成一段小红书封面图的 AI 绘图提示词"，开启创作！`
  });
}

function handleUnauthorized() {
  sessionStorage.removeItem("operator_api_key");
  alert("您的 API Key 无效或已过期，请重新验证");
  location.reload();
}

// Event Listeners
form.addEventListener("submit", async (event) => {
  event.preventDefault();
  const text = input.value.trim();
  if (!text || isProcessing) return;

  // Add user bubble
  addMessage({ role: "user", content: text });
  input.value = "";
  isProcessing = true;
  
  // Disable send button
  const sendBtn = form.querySelector(".btn-send-client");
  if (sendBtn) sendBtn.disabled = true;

  // Add loading status indicator
  const statusIndicator = addStatus("AI 写作助手正在思考创作步骤...");
  
  try {
    let isNew = !sessionId;
    let body = { message: text };
    if (sessionId) body.session_id = sessionId;

    let response = await fetch(`/api/v1/operator/agent/chat`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json; charset=utf-8",
        "Authorization": "Bearer " + apiKey
      },
      body: JSON.stringify(body)
    });

    if ((response.status === 400 || response.status === 404) && sessionId) {
      sessionId = "";
      clearStoredSessionId();
      updateSessionUI();
      isNew = true;
      body = { message: text };
      response = await fetch(`/api/v1/operator/agent/chat`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json; charset=utf-8",
          "Authorization": "Bearer " + apiKey
        },
        body: JSON.stringify(body)
      });
    }

    if (response.status === 401) {
      handleUnauthorized();
      return;
    }
    if (!response.ok) {
      throw new Error(`Server returned HTTP ${response.status}`);
    }

    // SSE Stream parsing
    const reader = response.body.getReader();
    const decoder = new TextDecoder("utf-8");
    let buffer = "";
    
    // Bubble element reference for stream updating
    let assistantBubble = null;
    // Don't remove status yet — update it as steps arrive
    let statusRemoved = false;
    function removeStatus() {
      if (!statusRemoved) {
        statusRemoved = true;
        statusIndicator.remove();
      }
    }

    while (true) {
      const { value, done } = await reader.read();
      if (done) break;

      buffer += decoder.decode(value, { stream: true });
      const parts = buffer.split("\n\n");
      buffer = parts.pop();

      for (const part of parts) {
        const line = part.trim();
        if (!line || !line.startsWith("event:")) continue;
        
        const eventMatch = line.match(/^event:\s*(\w+)/);
        const dataMatch = line.match(/^data:\s*(.+)/m);
        
        if (eventMatch && dataMatch) {
          const event = eventMatch[1];
          const data = JSON.parse(dataMatch[1]);
          
          if (event === "session") {
            sessionId = data.session_id;
            saveStoredSessionId(sessionId);
            if (isNew) {
              addToLocalHistory(sessionId, data.title, text);
              updateSessionUI();
            }
          } else if (event === "title") {
            sessionId = data.session_id || sessionId;
            saveStoredSessionId(sessionId);
            if (isNew) {
              addToLocalHistory(sessionId, data.title, text);
              updateSessionUI();
            }
          } else if (event === "plan") {
            const steps = (data.steps || []).filter(s => s.need_tool);
            if (steps.length > 0) {
              const names = steps.map(s => s.tool_hint || s.title || "工具").join("、");
              statusIndicator.textContent = `📋 已生成计划，准备调用：${names}`;
            } else {
              statusIndicator.textContent = "📋 正在整理回答...";
            }
          } else if (event === "step") {
            const title = data.title || data.tool_hint || "步骤";
            const status = data.status || "";
            if (status === "running") {
              statusIndicator.textContent = `⚙️ 正在执行：${title}`;
            } else if (status === "succeeded") {
              statusIndicator.textContent = `✅ 完成：${title}，正在整理答案...`;
            } else if (status === "failed") {
              statusIndicator.textContent = `⚠️ ${title} 执行遇到问题，继续整理...`;
            }
          } else if (event === "route") {
            const tool = data.tool_name || "";
            if (tool) statusIndicator.textContent = `🔀 正在调用工具：${tool}`;
          } else if (event === "execution") {
            statusIndicator.textContent = "💡 工具执行完成，正在整理答案...";
          } else if (event === "message") {
            const role = data.role || "assistant";
            if (role === "assistant") {
              removeStatus();
              if (!assistantBubble) {
                assistantBubble = createAssistantBubbleElement();
              }
              assistantBubble.updateContent(data.content || "");
            }
          } else if (event === "error") {
            removeStatus();
            const errorMsg = data.message || "写作助手运行过程中出现错误";
            addStatus(`❌ 错误: ${errorMsg}`);
          }
        }
      }
    }
    removeStatus();

    
    // Redraw timeline if drawer is open
    if (traceDrawer.classList.contains("open")) {
      loadTraceTimeline();
    }
  } catch (error) {
    console.error("Chat streaming error:", error);
    statusIndicator.textContent = "❌ 创作请求失败，请稍后重试";
  } finally {
    isProcessing = false;
    if (sendBtn) sendBtn.disabled = false;
    updateSuggestionsVisibility();
    input.focus();
  }
});

function updateSuggestionsVisibility() {
  if (!suggestionsEl) return;
  const hasMessages = messagesEl.querySelectorAll(".message.user").length > 0;
  if (hasMessages) {
    suggestionsEl.classList.add("hidden");
  } else {
    suggestionsEl.classList.remove("hidden");
  }
}

function addMessage(message) {
  const item = document.createElement("article");
  const role = message.role || "assistant";
  item.className = `message ${role}`;
  
  // Create avatar badge
  const avatar = document.createElement("div");
  avatar.className = `message-avatar ${role}`;
  if (role === "user") {
    avatar.textContent = "写";
  } else {
    avatar.innerHTML = `<svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M12 20h9M16.5 3.5a2.121 2.121 0 0 1 3 3L7 19l-4 1 1-4L16.5 3.5z"></path></svg>`;
  }
  
  const body = document.createElement("div");
  body.className = "message-body";
  body.innerHTML = parseMarkdown(message.content || "");
  
  item.appendChild(avatar);
  item.appendChild(body);
  messagesEl.appendChild(item);
  messagesEl.scrollTop = messagesEl.scrollHeight;
  updateSuggestionsVisibility();
}

function createAssistantBubbleElement() {
  const item = document.createElement("article");
  item.className = "message assistant";
  
  const avatar = document.createElement("div");
  avatar.className = "message-avatar assistant";
  avatar.innerHTML = `<svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M12 20h9M16.5 3.5a2.121 2.121 0 0 1 3 3L7 19l-4 1 1-4L16.5 3.5z"></path></svg>`;
  
  const body = document.createElement("div");
  body.className = "message-body";
  body.innerHTML = "...";
  
  item.appendChild(avatar);
  item.appendChild(body);
  messagesEl.appendChild(item);
  messagesEl.scrollTop = messagesEl.scrollHeight;
  
  return {
    updateContent: (content) => {
      body.innerHTML = parseMarkdown(content);
      messagesEl.scrollTop = messagesEl.scrollHeight;
    }
  };
}

function addStatus(text) {
  const item = document.createElement("div");
  item.className = "status";
  item.textContent = text;
  messagesEl.appendChild(item);
  messagesEl.scrollTop = messagesEl.scrollHeight;
  return item;
}

// Sync session history from server on load
async function syncHistoryWithServer() {
  try {
    const response = await fetch("/api/v1/operator/agent/sessions", {
      headers: { "Authorization": "Bearer " + apiKey }
    });
    if (response.status === 401) {
      handleUnauthorized();
      return;
    }
    if (!response.ok) return;
    const result = await response.json();
    const serverSessions = result.data?.sessions || [];
    
    // Filter sessions to only keep the ones owned by this browser (staged in localHistory or active)
    const localHistory = getLocalHistory();
    const localIds = new Set(localHistory.map(item => item.id));
    
    const history = serverSessions
      .filter(s => localIds.has(s.id) || s.id === sessionId)
      .map(s => {
        const existingEntry = localHistory.find(item => item.id === s.id);
        const title = formatHistoryTitle(s.title || existingEntry?.title || s.last_message_preview, "新创作会话");
        return {
          id: s.id,
          title: title,
          timestamp: new Date(s.updated_at).getTime()
        };
      });

    saveLocalHistory(history);
    updateSessionUI();
  } catch (error) {
    console.error("Failed to sync history with server:", error);
  }
}

// Slide-out Drawer: Observable Trace Browser
function openTraceDrawer() {
  if (!sessionId) return;
  traceSessionValue.textContent = sessionId;
  traceDrawer.classList.add("open");
  loadTraceTimeline();

  // Start polling
  if (tracePollTimer) clearInterval(tracePollTimer);
  tracePollTimer = setInterval(loadTraceTimeline, 5000);
}

function closeTraceDrawer() {
  traceDrawer.classList.remove("open");
  if (tracePollTimer) {
    clearInterval(tracePollTimer);
    tracePollTimer = null;
  }
}

function copyActiveSessionId() {
  if (!sessionId) return;
  navigator.clipboard.writeText(sessionId).then(() => {
    const copyText = modalCopySessionBtn.querySelector(".copy-text");
    if (copyText) {
      copyText.textContent = "已复制";
      setTimeout(() => {
        copyText.textContent = "复制";
      }, 1500);
    }
  }).catch(err => {
    console.error("Failed to copy session ID:", err);
  });
}

async function loadTraceTimeline() {
  if (!sessionId) return;
  try {
    const response = await fetch(`/api/v1/operator/agent/sessions/${sessionId}/trace`, {
      headers: { "Authorization": "Bearer " + apiKey }
    });
    if (response.status === 401) {
      closeTraceDrawer();
      handleUnauthorized();
      return;
    }
    if (!response.ok) {
      traceModalTimeline.innerHTML = `<div class="status text-center text-error">获取轨迹失败</div>`;
      return;
    }
    const result = await response.json();
    const events = result.data || [];
    
    if (events.length === 0) {
      traceModalTimeline.innerHTML = `<div class="status text-center text-muted">暂无 Agent 编排执行日志</div>`;
      return;
    }

    traceModalTimeline.innerHTML = "";
    events.forEach(evt => {
      const node = document.createElement("div");
      const status = evt.status || "succeeded";
      node.className = `trace-node ${status}`;
      
      const header = document.createElement("div");
      header.className = "trace-header";
      
      const titleSpan = document.createElement("span");
      titleSpan.textContent = translateTraceType(evt.type) || evt.type;
      
      const timeSpan = document.createElement("span");
      timeSpan.className = "trace-time";
      timeSpan.textContent = formatTraceTime(evt.timestamp);
      
      header.appendChild(titleSpan);
      header.appendChild(timeSpan);
      
      const msg = document.createElement("div");
      msg.className = "trace-msg";
      msg.textContent = evt.message || "";
      
      node.appendChild(header);
      node.appendChild(msg);

      // Render payload toggle buttons if payload exists
      if (evt.payload && evt.payload !== "{}") {
        const actions = document.createElement("div");
        actions.className = "trace-payload-actions";
        
        const toggleBtn = document.createElement("button");
        toggleBtn.className = "trace-payload-toggle";
        toggleBtn.textContent = "查看详情";
        
        const copyBtn = document.createElement("button");
        copyBtn.className = "trace-payload-copy";
        copyBtn.textContent = "复制 Payload";
        
        const payloadPre = document.createElement("pre");
        payloadPre.className = "trace-payload";
        
        payloadPre.textContent = formatTracePayload(evt.payload);

        toggleBtn.addEventListener("click", () => {
          const isVisible = payloadPre.style.display === "block";
          payloadPre.style.display = isVisible ? "none" : "block";
          toggleBtn.textContent = isVisible ? "查看详情" : "收起详情";
        });

        copyBtn.addEventListener("click", () => {
          navigator.clipboard.writeText(payloadPre.textContent).then(() => {
            copyBtn.textContent = "已复制";
            setTimeout(() => {
              copyBtn.textContent = "复制 Payload";
            }, 1200);
          });
        });

        actions.appendChild(toggleBtn);
        actions.appendChild(copyBtn);
        node.appendChild(actions);
        node.appendChild(payloadPre);
      }

      traceModalTimeline.appendChild(node);
    });
  } catch (err) {
    console.error("Failed to load trace timeline:", err);
    traceModalTimeline.innerHTML = `<div class="status text-center text-error">网络错误，无法加载 Trace 轨迹</div>`;
  }
}

function formatTracePayload(payload) {
  if (!payload) return "";
  if (typeof payload === "string") {
    try {
      return JSON.stringify(JSON.parse(payload), null, 2);
    } catch {
      return payload;
    }
  }
  try {
    return JSON.stringify(payload, null, 2);
  } catch {
    return String(payload);
  }
}

function translateTraceType(type) {
  const dict = {
    "session_created": "🆕 开启会话",
    "agent_start": "🚀 Agent 开始执行",
    "agent_route": "🧭 决策分析",
    "tool_call": "🛠️ 调用工具",
    "tool_response": "📥 工具返回结果",
    "agent_stop": "🏁 Agent 执行结束",
    "session_load_error": "❌ 载入失败",
    "session_save_error": "❌ 存盘失败",
    "agent_run_failed": "❌ 运算出错",
    "mcp_call": "🌐 MCP 工具调用",
    "mcp_response": "📥 MCP 工具返回"
  };
  return dict[type] || type;
}

function formatTraceTime(tsString) {
  if (!tsString) return "";
  try {
    const d = new Date(tsString);
    const pad = (n) => String(n).padStart(2, '0');
    return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}.${String(d.getMilliseconds()).padStart(3, '0')}`;
  } catch {
    return tsString;
  }
}

// Simplified Markdown Parsing Utility
function parseMarkdown(text) {
  if (typeof marked !== "undefined" && typeof marked.parse === "function") {
    try {
      let html = marked.parse(text);
      setTimeout(() => {
        if (typeof hljs !== "undefined" && typeof hljs.highlightAll === "function") {
          hljs.highlightAll();
        }
      }, 50);
      return html;
    } catch (e) {
      console.warn("Failed to parse markdown using marked library:", e);
    }
  }
  
  // Minimal fallback markdown parsing
  let escaped = text
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;");
  
  // bold
  escaped = escaped.replace(/\*\*(.*?)\*\*/g, "<strong>$1</strong>");
  // bullet lists
  escaped = escaped.replace(/^\s*-\s+(.*?)$/gm, "<li>$1</li>");
  escaped = escaped.replace(/(<li>.*<\/li>)/s, "<ul>$1</ul>");
  // paragraphs
  escaped = escaped.replace(/\n\n/g, "</p><p>");
  return `<p>${escaped}</p>`;
}
