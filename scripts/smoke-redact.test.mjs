import assert from "node:assert/strict";
import { redactSensitive } from "./smoke-redact.mjs";

const redacted = redactSensitive({
  headers: {
    Authorization: "Bearer admin-smoke-key-123456789012345678",
    "X-Agent-API-Key": "operator-smoke-key-123456789012345",
  },
  text: "api_key=sk-live-secret Authorization: Bearer live-token-123456 secret=plain-secret",
  nested: [
    { access_token: "access-secret-token" },
    "Bearer another-secret-token",
  ],
});

const body = JSON.stringify(redacted);

for (const forbidden of [
  "admin-smoke-key",
  "operator-smoke-key",
  "sk-live-secret",
  "live-token-123456",
  "plain-secret",
  "access-secret-token",
  "another-secret-token",
]) {
  assert.equal(body.includes(forbidden), false, `leaked ${forbidden}: ${body}`);
}

assert.equal(redacted.headers.Authorization, "[redacted]");
assert.equal(redacted.headers["X-Agent-API-Key"], "[redacted]");
assert.equal(redacted.nested[0].access_token, "[redacted]");
assert.equal(body.includes("[redacted]"), true);

const redactedError = redactSensitive(new Error("failed with Authorization: Bearer error-token-secret and api_key=sk-error-secret"));
assert.equal(redactedError.includes("error-token-secret"), false);
assert.equal(redactedError.includes("sk-error-secret"), false);
