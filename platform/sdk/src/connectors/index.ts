/**
 * Connector polyfills. Each function uses fetch + the platform token that the
 * supervisor injects via PLATFORM_TOKEN_<CONNECTOR>. The workflow code never
 * sees the raw token directly.
 */

export * from "./github.js";
export * from "./slack.js";
export * from "./jira.js";
export * from "./http.js";

import { github as gh } from "./github.js";
import { slack as sk } from "./slack.js";
import { jira as jr } from "./jira.js";
import { http as ht } from "./http.js";

export const github = gh;
export const slack = sk;
export const jira = jr;
export const http = ht;
