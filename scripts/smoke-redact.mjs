const sensitiveKeyPattern = /(^authorization$|api[_-]?key$|token$|secret$|password$)/i;

export function redactSensitive(value) {
  if (value instanceof Error) return redactString(value.stack || value.message || String(value));
  if (typeof value === "string") return redactString(value);
  if (Array.isArray(value)) return value.map((item) => redactSensitive(item));
  if (!value || typeof value !== "object") return value;

  return Object.fromEntries(Object.entries(value).map(([key, child]) => {
    if (sensitiveKeyPattern.test(key)) return [key, "[redacted]"];
    return [key, redactSensitive(child)];
  }));
}

function redactString(text) {
  return text
    .replace(/(Authorization\s*[:=]\s*["']?\s*Bearer\s+)[A-Za-z0-9._~+/=-]+/gi, "$1[redacted]")
    .replace(/(Bearer\s+)[A-Za-z0-9._~+/=-]{8,}/gi, "$1[redacted]")
    .replace(/((?:X-Agent-API-Key|x-agent-api-key)["'\s:=]+)[A-Za-z0-9._~+/=-]+/g, "$1[redacted]")
    .replace(/((?:api[_-]?key|access[_-]?token|refresh[_-]?token|client[_-]?secret|password|token|secret)["'\s:=]+["']?)[A-Za-z0-9._~+/=-]+/gi, "$1[redacted]")
    .replace(/\bsk-[A-Za-z0-9._-]{6,}\b/g, "sk-[redacted]")
    .replace(/\b(?:admin|operator)(?:-hybrid)?-smoke-key-[A-Za-z0-9-]+\b/g, "[redacted-smoke-key]");
}
