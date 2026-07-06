const sessionStorageKey = "product-docs-agent-session";
const historyStorageKey = "product-docs-agent-history";
const sessionIDPattern = /^[A-Za-z0-9_-]{1,96}$/;

let sessionId = normalizeSessionId(readStoredSessionId());

// DOM Elements
const messagesEl = document.getElementById("messages");
const form = document.getElementById("chatForm");
const input = document.getElementById("messageInput");
const resetBtn = document.getElementById("resetBtn");
const sendBtn = form.querySelector("button[type='submit']");
const suggestionButtons = Array.from(document.querySelectorAll("[data-question]"));
const suggestionsEl = document.getElementById("suggestions");
const activeChatTitle = document.getElementById("activeChatTitle");

// Sidebar & History Elements
const historyList = document.getElementById("historyList");
const sidebarTraceBtn = document.getElementById("sidebarTraceBtn");
const clearAllHistoryBtn = document.getElementById("clearAllHistoryBtn");

// Trace Drawer Elements
const traceDrawer = document.getElementById("traceDrawer");
const traceModalTimeline = document.getElementById("traceModalTimeline");
const traceSessionValue = document.getElementById("traceSessionValue");
const modalCopySessionBtn = document.getElementById("modalCopySessionBtn");
const closeTraceDrawerBtn = document.getElementById("closeTraceDrawerBtn");

let isSending = false;
let sseMessageBuffer = ""; // buffer for the active streaming message

// Custom Renderer for marked.js with highlight.js syntax highlighting
const renderer = new marked.Renderer();
renderer.code = function(code, info) {
  const lang = (info || '').match(/\S*/)[0];
  const validLang = lang && hljs.getLanguage(lang) ? lang : 'plaintext';
  const highlighted = hljs.highlight(code, { language: validLang }).value;
  return `<pre><code class="hljs ${validLang}">${highlighted}</code></pre>`;
};

// Configure marked options
marked.setOptions({
  gfm: true,
  breaks: true,
  mangle: false,
  headerIds: false
});

function parseMarkdown(text) {
  if (!text) return "";
  return marked.parse(text, { renderer });
}

function hasRenderableContent(text) {
  return typeof text === "string" && text.trim().length > 0;
}

// Session Helper Functions
function normalizeSessionId(value) {
  value = (value || "").trim();
  if (!value) return "";
  if (!sessionIDPattern.test(value)) {
    clearStoredSessionId();
    return "";
  }
  return value;
}

function readStoredSessionId() {
  try {
    return localStorage.getItem(sessionStorageKey) || "";
  } catch (error) {
    return "";
  }
}

function saveStoredSessionId(value) {
  try {
    localStorage.setItem(sessionStorageKey, value);
  } catch (error) {
    // Local persistence fails silently
  }
}

function clearStoredSessionId() {
  try {
    localStorage.removeItem(sessionStorageKey);
  } catch (error) {
    // Local persistence fails silently
  }
}

// Local History functions
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
    // Local storage fails silently
  }
}

function formatHistoryTitle(title, fallbackTitle) {
  const value = (title || fallbackTitle || "新会话").trim();
  return value.length > 18 ? value.substring(0, 18) + "..." : value;
}

function addToLocalHistory(id, titleText, fallbackTitle = "新会话") {
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
    // update preview/timestamp and move to top
    history.splice(existingIdx, 1);
  }
  history.unshift(entry);
  saveLocalHistory(history);
  renderSidebarHistory();
}

function deleteFromLocalHistory(id) {
  // Call server reset to delete the session in SQLite database
  fetch("/api/v1/customer/reset", {
    method: "POST",
    headers: {"Content-Type": "application/json"},
    body: JSON.stringify({session_id: id})
  }).catch(() => {});

  let history = getLocalHistory();
  history = history.filter(item => item.id !== id);
  saveLocalHistory(history);
  renderSidebarHistory();
  
  if (sessionId === id) {
    startNewSession(false); // starts a new session locally
  }
}

function clearAllHistory() {
  const history = getLocalHistory();
  history.forEach(item => {
    // fetch background reset silently
    fetch("/api/v1/customer/reset", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
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
      const sidebar = document.querySelector(".app-sidebar");
      if (window.innerWidth <= 800 && sidebar) {
        sidebar.classList.remove("open");
      }
    });

    const deleteBtn = document.createElement("button");
    deleteBtn.className = "btn-delete-history";
    deleteBtn.innerHTML = `<svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><line x1="18" y1="6" x2="6" y2="18"></line><line x1="6" y1="6" x2="18" y2="18"></line></svg>`;
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

// UI Rendering Functions
function updateSessionUI() {
  if (sessionId) {
    if (sidebarTraceBtn) sidebarTraceBtn.classList.remove("hidden");
    const history = getLocalHistory();
    const activeEntry = history.find(item => item.id === sessionId);
    if (activeChatTitle) {
      activeChatTitle.textContent = activeEntry ? activeEntry.title : "当前会话";
    }
  } else {
    if (sidebarTraceBtn) sidebarTraceBtn.classList.add("hidden");
    if (activeChatTitle) activeChatTitle.textContent = "新会话";
  }
  renderSidebarHistory();
}

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
  const role = message.role || "assistant";
  const content = message.content || "";

  if (role === "assistant" && !hasRenderableContent(content)) {
    return null;
  }

  const item = document.createElement("article");
  item.className = `message ${role}`;
  
  // Create avatar badge
  const avatar = document.createElement("div");
  avatar.className = `message-avatar ${role}`;
  if (role === "user") {
    avatar.textContent = "问";
  } else {
    avatar.innerHTML = `<svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"></path></svg>`;
  }
  
  const body = document.createElement("div");
  body.className = "message-body";
  body.innerHTML = parseMarkdown(content);
  
  item.appendChild(avatar);
  item.appendChild(body);
  messagesEl.appendChild(item);
  messagesEl.scrollTop = messagesEl.scrollHeight;
  updateSuggestionsVisibility();
  return item;
}

function addStatus(text) {
  const item = document.createElement("div");
  item.className = "status";
  item.textContent = text;
  messagesEl.appendChild(item);
  messagesEl.scrollTop = messagesEl.scrollHeight;
  return item;
}

function setComposerBusy(busy) {
  isSending = busy;
  input.disabled = busy;
  if (resetBtn) resetBtn.disabled = busy;
  if (sendBtn) {
    sendBtn.disabled = busy;
  }
  suggestionButtons.forEach((button) => {
    button.disabled = busy;
  });
}

function parseSSEChunk(chunk) {
  const lines = chunk.split("\n");
  const eventLine = lines.find((line) => line.startsWith("event:"));
  const dataLines = lines.filter((line) => line.startsWith("data:"));
  if (!eventLine || dataLines.length === 0) return null;
  const eventName = eventLine.replace("event:", "").trim();
  const rawData = dataLines.map((line) => line.replace("data:", "").trim()).join("\n");
  try {
    return {eventName, data: JSON.parse(rawData)};
  } catch (error) {
    return {eventName: "error", data: {message: "响应解析失败，请稍后再试。"}};
  }
}

async function postChat(message, currentSessionId) {
  return fetch("/api/v1/customer/chat", {
    method: "POST",
    headers: {"Content-Type": "application/json"},
    body: JSON.stringify({session_id: currentSessionId, message})
  });
}

// Send Message Flow
async function sendMessage(message) {
  if (!message || isSending) return;
  
  const isFirstMessage = (messagesEl.querySelectorAll(".message").length === 0);
  const userMsgText = message;

  setComposerBusy(true);
  input.value = "";

  // Render user question bubble instantly
  addMessage({role: "user", content: userMsgText});

  const waiting = addStatus("正在整理回答...");
  let keepStatus = false;
  
  // Create an assistant element prepared for streaming tokens
  let assistantBubbleBody = null;
  
  try {
    sessionId = normalizeSessionId(sessionId);
    let response = await postChat(message, sessionId);
    if ((response.status === 400 || response.status === 404) && sessionId) {
      sessionId = "";
      clearStoredSessionId();
      updateSessionUI();
      response = await postChat(message, sessionId);
    }

    if (!response.ok || !response.body) {
      let errMsg = "请求失败，请稍后再试。";
      try {
        const errJson = await response.json();
        if (errJson && errJson.message) {
          errMsg = errJson.message;
        } else if (errJson && errJson.error) {
          errMsg = errJson.error;
        }
      } catch (e) {}
      waiting.textContent = errMsg;
      keepStatus = true;
      return;
    }

    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffer = "";
    let accumulatedContent = "";
    let receivedAssistantContent = false;

    while (true) {
      const {value, done} = await reader.read();
      if (done) break;
      buffer += decoder.decode(value, {stream: true});
      const chunks = buffer.split("\n\n");
      buffer = chunks.pop() || "";
      
      for (const chunk of chunks) {
        const parsed = parseSSEChunk(chunk);
        if (!parsed) continue;
        const {eventName, data} = parsed;
        
        if (eventName === "session") {
          sessionId = normalizeSessionId(data.session_id);
          if (sessionId) {
            saveStoredSessionId(sessionId);
            addToLocalHistory(sessionId, data.title, userMsgText);
            updateSessionUI();
          }
        } else if (eventName === "title") {
          const titleSessionId = normalizeSessionId(data.session_id || sessionId);
          if (titleSessionId) {
            sessionId = titleSessionId;
            saveStoredSessionId(sessionId);
            addToLocalHistory(sessionId, data.title, userMsgText);
            updateSessionUI();
          }
        } else if (eventName === "message") {
          // If we are actively streaming content chunks
          const role = data.role || "assistant";
          const newContent = data.content || "";
          
          if (role === "assistant") {
            if (!hasRenderableContent(newContent)) {
              continue;
            }
            receivedAssistantContent = true;

            // Remove waiting loader once streaming starts
            if (waiting.isConnected) {
              waiting.remove();
            }
            
            // Create bubble if not created yet
            if (!assistantBubbleBody) {
              const item = document.createElement("article");
              item.className = "message assistant";
              
              const avatar = document.createElement("div");
              avatar.className = "message-avatar assistant";
              avatar.innerHTML = `<svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"></path></svg>`;
              
              assistantBubbleBody = document.createElement("div");
              assistantBubbleBody.className = "message-body";
              
              item.appendChild(avatar);
              item.appendChild(assistantBubbleBody);
              messagesEl.appendChild(item);
            }
            
            // Append and parse
            accumulatedContent = newContent; // full content block is returned incrementally by backend
            assistantBubbleBody.innerHTML = parseMarkdown(accumulatedContent);
            messagesEl.scrollTop = messagesEl.scrollHeight;
          }
        } else if (eventName === "error") {
          waiting.textContent = data.message || data.error || "回答失败，请稍后再试。";
          keepStatus = true;
        }
      }
    }

    if (!receivedAssistantContent && waiting.isConnected && !keepStatus) {
      waiting.textContent = "刚才没有生成有效回答，请再试一次或补充问题细节。";
      keepStatus = true;
    }
  } catch (error) {
    waiting.textContent = "网络连接失败，请稍后再试。";
    keepStatus = true;
  } finally {
    if (!keepStatus && waiting.isConnected) {
      waiting.remove();
    }
    setComposerBusy(false);
    input.focus();
  }
}

// Session Loading
async function loadSession(sessId) {
  if (!sessId) return;
  try {
    messagesEl.innerHTML = `<div class="status">正在载入历史记录...</div>`;
    const response = await fetch(`/api/v1/customer/sessions/${sessId}`);
    if (response.status === 404) {
      deleteFromLocalHistory(sessId);
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
    fetch("/api/v1/customer/reset", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
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
    content: "您好！我是 **Dify 产品文档助手**。我可以帮您查询应用类型、工作流、对话流、知识库、节点配置、发布方式、监控日志和团队管理等产品问题。请问今天想了解什么？"
  });
}

// Event Listeners
form.addEventListener("submit", async (event) => {
  event.preventDefault();
  const message = input.value.trim();
  await sendMessage(message);
});

input.addEventListener("keydown", (event) => {
  if (event.key !== "Enter" || event.shiftKey || event.isComposing) return;
  event.preventDefault();
  if (isSending) return;
  form.requestSubmit();
});

suggestionButtons.forEach((button) => {
  button.addEventListener("click", async () => {
    await sendMessage(button.dataset.question || "");
  });
});

resetBtn.addEventListener("click", () => {
  startNewSession(false);
  const sidebar = document.querySelector(".app-sidebar");
  if (window.innerWidth <= 800 && sidebar) {
    sidebar.classList.remove("open");
  }
});

if (clearAllHistoryBtn) {
  clearAllHistoryBtn.addEventListener("click", () => {
    if (confirm("确定要清空本地的所有会话历史吗？这将无法恢复。")) {
      clearAllHistory();
    }
  });
}

// Trace Drawer Actions
if (sidebarTraceBtn && traceDrawer) {
  sidebarTraceBtn.addEventListener("click", () => {
    if (!sessionId) return;
    if (traceSessionValue) {
      traceSessionValue.textContent = sessionId;
    }
    traceDrawer.classList.add("open");
    loadSessionTrace(sessionId, traceModalTimeline);
  });
}

if (closeTraceDrawerBtn && traceDrawer) {
  closeTraceDrawerBtn.addEventListener("click", () => {
    traceDrawer.classList.remove("open");
  });
}

if (modalCopySessionBtn) {
  modalCopySessionBtn.addEventListener("click", () => {
    if (!sessionId) return;
    navigator.clipboard.writeText(sessionId).then(() => {
      const textEl = modalCopySessionBtn.querySelector(".copy-text");
      if (textEl) {
        const originalText = textEl.textContent;
        textEl.textContent = "已复制！";
        setTimeout(() => {
          textEl.textContent = originalText;
        }, 1500);
      }
    }).catch(err => {
      console.error("复制失败:", err);
    });
  });
}

async function loadSessionTrace(sessId, targetElement) {
  try {
    targetElement.innerHTML = `<div class="status text-center text-muted">正在查询 SQLite Trace 日志...</div>`;
    const response = await fetch(`/api/v1/customer/sessions/${sessId}/trace`);
    if (!response.ok) {
      targetElement.innerHTML = `<div class="status text-center text-error">无法载入 Trace: ${response.statusText}</div>`;
      return;
    }
    const data = await response.json();
    const events = data.data;
    if (!events || events.length === 0) {
      targetElement.innerHTML = `<div class="status text-center text-muted">此会话暂无 Trace 日志项。</div>`;
      return;
    }
    
    targetElement.innerHTML = "";
    events.forEach((evt, idx) => {
      const node = document.createElement("div");
      node.className = `trace-node ${evt.status || "info"}`;
      
      const header = document.createElement("div");
      header.className = "trace-header";
      
      const title = document.createElement("span");
      title.textContent = evt.type;
      header.appendChild(title);
      
      const timeSpan = document.createElement("span");
      timeSpan.className = "trace-time";
      timeSpan.textContent = formatTime(evt.created_at) + (evt.duration_ms ? ` (${evt.duration_ms}ms)` : "");
      header.appendChild(timeSpan);
      
      node.appendChild(header);
      
      if (evt.message) {
        const msg = document.createElement("div");
        msg.className = "trace-msg";
        msg.textContent = evt.message;
        node.appendChild(msg);
      }
      
      let payloadJSON = "";
      if (evt.payload && Object.keys(evt.payload).length > 0) {
        payloadJSON = JSON.stringify(evt.payload, null, 2);
      }
      
      if (payloadJSON) {
        const payloadId = `trace-payload-${sessId}-${idx}`;
        const actions = document.createElement("div");
        actions.className = "trace-payload-actions";
        
        const toggleBtn = document.createElement("button");
        toggleBtn.className = "trace-payload-toggle";
        toggleBtn.type = "button";
        toggleBtn.textContent = "显示原始报文 (JSON)";
        toggleBtn.addEventListener("click", () => {
          const p = document.getElementById(payloadId);
          if (p.style.display === "block") {
            p.style.display = "none";
            toggleBtn.textContent = "显示原始报文 (JSON)";
          } else {
            p.style.display = "block";
            toggleBtn.textContent = "隐藏原始报文 (JSON)";
          }
        });
        actions.appendChild(toggleBtn);
        
        const copyBtn = document.createElement("button");
        copyBtn.className = "trace-payload-copy";
        copyBtn.type = "button";
        copyBtn.innerHTML = `<svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path></svg><span class="copy-text">复制 JSON</span>`;
        copyBtn.addEventListener("click", () => {
          navigator.clipboard.writeText(payloadJSON).then(() => {
            const textEl = copyBtn.querySelector(".copy-text");
            textEl.textContent = "已复制！";
            setTimeout(() => {
              textEl.textContent = "复制 JSON";
            }, 1500);
          });
        });
        actions.appendChild(copyBtn);
        
        node.appendChild(actions);
        
        const payload = document.createElement("pre");
        payload.id = payloadId;
        payload.className = "trace-payload";
        payload.textContent = payloadJSON;
        node.appendChild(payload);
      }
      
      targetElement.appendChild(node);
    });
  } catch (err) {
    targetElement.innerHTML = `<div class="status text-center text-error">加载失败: ${err.message}</div>`;
  }
}

function formatTime(timeStr) {
  if (!timeStr) return "";
  try {
    const d = new Date(timeStr);
    return d.toLocaleTimeString();
  } catch (e) {
    return timeStr;
  }
}

// Mobile Sidebar Toggles
const mobileMenuBtn = document.getElementById("mobileMenuBtn");
const closeSidebarBtn = document.getElementById("closeSidebarBtn");
const appSidebar = document.querySelector(".app-sidebar");

if (mobileMenuBtn && appSidebar) {
  mobileMenuBtn.addEventListener("click", () => {
    appSidebar.classList.add("open");
  });
}

if (closeSidebarBtn && appSidebar) {
  closeSidebarBtn.addEventListener("click", () => {
    appSidebar.classList.remove("open");
  });
}

// Initial UI and history updates
updateSessionUI();
updateSuggestionsVisibility();

if (sessionId) {
  if (suggestionsEl) suggestionsEl.classList.add("hidden");
  loadSession(sessionId);
} else {
  startNewSession(false);
}

// Sync session history from server on load
syncHistoryWithServer();

input.focus();

async function syncHistoryWithServer() {
  try {
    const response = await fetch("/api/v1/customer/sessions");
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
        const title = formatHistoryTitle(s.title || existingEntry?.title || s.last_message_preview, "新会话");
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
