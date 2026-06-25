/**
 * Connector polyfills. Each function uses fetch + the platform token that the
 * supervisor injects via PLATFORM_TOKEN_<CONNECTOR>. The workflow code never
 * sees the raw token directly.
 *
 * When PLATFORM_DRY_RUN=1 we wrap each connector with `wrapDryRun` so calls
 * return mock data and the workflow can be smoke-tested without touching
 * real GitHub / Slack / Jira.
 */

export * from "./github.js";
export * from "./slack.js";
export * from "./jira.js";
export * from "./http.js";

import { github as gh } from "./github.js";
import { slack as sk } from "./slack.js";
import { jira as jr } from "./jira.js";
import { http as ht } from "./http.js";
import { wrapDryRun } from "./dryrun.js";

export const github = wrapDryRun(gh, {
  name: "github",
  mocks: {
    "issues.list": () => [],
    "issues.create": (a: unknown) => ({ id: 1, number: 1, html_url: "https://example/dry/1", ...((a as object) ?? {}) }),
    "issues.comment": () => ({ id: 1 }),
    "pulls.list": () => [],
    "pulls.review": () => ({}),
  },
});

export const slack = wrapDryRun(sk, {
  name: "slack",
  mocks: {
    "post": () => ({ ok: true, ts: "0.0", channel: "DRY-RUN" }),
    "chat.postMessage": () => ({ ok: true, ts: "0.0", channel: "DRY-RUN" }),
    "conversations.list": () => ({ channels: [] }),
    "conversations.findByName": () => null,
    "conversations.join": () => ({ ok: true }),
    "listChannels": () => ({ channels: [] }),
  },
});

export const jira = wrapDryRun(jr, {
  name: "jira",
  mocks: {
    "search": () => [],
  },
});

export const http = wrapDryRun(ht, {
  name: "http",
  mocks: {
    "request": () => ({ status: 200, headers: {}, body: "" }),
  },
});

export { isDryRun, wrapDryRun } from "./dryrun.js";
