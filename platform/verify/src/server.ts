// Verification service for generated workflows.
//
// Two endpoints:
//   POST /probe   - plain HTTP fetch + content sanity (cheap, ~3s)
//   POST /browse  - chromium via playwright + screenshot + console capture
//                   (heavy, ~6-10s; falls back to /probe if playwright fails
//                   to start so the API doesn't hard-block on it)
//
// The API calls this asynchronously after `kind: service` tasks are created
// or updated, then publishes the result on `chat:progress:<sessionId>` so
// the UI shows verification status and the next generator turn sees the
// findings in `recent_log_tail`.

import Fastify from "fastify";
import { chromium, type Browser } from "playwright";
import { writeFileSync, mkdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { homedir } from "node:os";

const app = Fastify({ logger: false });
app.get("/healthz", async () => ({ status: "ok" }));

interface ProbeBody {
  url: string;
  /** Max seconds to wait for the upstream to start listening. */
  waitSeconds?: number;
}

interface ProbeResult {
  ok: boolean;
  attempts: number;
  status?: number;
  bodyBytes?: number;
  bodyText?: string;
  errors: string[];
  durationMs: number;
}

async function probeOnce(url: string): Promise<{ status: number; body: string } | { error: string }> {
  try {
    const r = await fetch(url, { redirect: "follow" });
    const body = await r.text();
    return { status: r.status, body };
  } catch (err) {
    return { error: (err as Error).message };
  }
}

function classifyBody(text: string, status: number, errors: string[]): void {
  if (status >= 500) errors.push(`upstream ${status} server error`);
  else if (status >= 400) errors.push(`upstream ${status} client error`);
  if (text.length < 30) errors.push(`response body suspiciously short: ${text.length} bytes`);
  // Common indicators that the workflow code threw at request time.
  const patterns = [
    /TypeError:/,
    /ReferenceError:/,
    /SyntaxError:/,
    /Cannot find module/,
    /Cannot read prop/,
    /workflow handler error:/,
    /at file:\/\//,
  ];
  for (const pat of patterns) {
    const m = pat.exec(text);
    if (m) {
      errors.push(`error trace in response: ${m[0]}`);
      break;
    }
  }
}

app.post<{ Body: ProbeBody }>("/probe", async (req) => {
  const { url, waitSeconds = 15 } = req.body;
  const start = Date.now();
  const errors: string[] = [];
  const deadline = start + waitSeconds * 1000;
  let attempts = 0;
  let last: Awaited<ReturnType<typeof probeOnce>> | null = null;
  while (Date.now() < deadline) {
    attempts++;
    last = await probeOnce(url);
    if ("status" in last) break;
    // network not ready (ECONNREFUSED, fetch failed, etc.) — wait and retry
    await new Promise((r) => setTimeout(r, 800));
  }
  if (!last || "error" in last) {
    errors.push(last && "error" in last ? last.error : "no upstream response in time");
    const result: ProbeResult = {
      ok: false,
      attempts,
      errors,
      durationMs: Date.now() - start,
    };
    return result;
  }
  classifyBody(last.body, last.status, errors);
  return {
    ok: errors.length === 0,
    attempts,
    status: last.status,
    bodyBytes: last.body.length,
    bodyText: last.body.slice(0, 4000),
    errors,
    durationMs: Date.now() - start,
  } satisfies ProbeResult;
});

// --- Browser-level verification (playwright) ---

interface BrowseBody {
  url: string;
  taskId?: string;
  waitSeconds?: number;
}

let browserSingleton: Browser | null = null;
async function getBrowser(): Promise<Browser | null> {
  if (browserSingleton) return browserSingleton;
  try {
    browserSingleton = await chromium.launch({ headless: true });
    return browserSingleton;
  } catch (err) {
    console.warn("playwright launch failed:", (err as Error).message);
    return null;
  }
}

interface BrowseResult {
  ok: boolean;
  url: string;
  status?: number;
  title?: string;
  textChars?: number;
  consoleErrors: string[];
  pageErrors: string[];
  screenshotPath?: string;
  errors: string[];
  durationMs: number;
}

app.post<{ Body: BrowseBody }>("/browse", async (req) => {
  const { url, taskId, waitSeconds = 12 } = req.body;
  const start = Date.now();
  const errors: string[] = [];
  const consoleErrors: string[] = [];
  const pageErrors: string[] = [];

  // First wait for the upstream to be reachable at all — playwright's
  // navigation timeout is unhelpful when the workflow hasn't bound yet.
  const deadline = start + waitSeconds * 1000;
  while (Date.now() < deadline) {
    const p = await probeOnce(url);
    if ("status" in p) break;
    await new Promise((r) => setTimeout(r, 600));
  }

  const browser = await getBrowser();
  if (!browser) {
    errors.push("playwright unavailable; install chromium with `npx playwright install chromium`");
    return {
      ok: false,
      url,
      consoleErrors,
      pageErrors,
      errors,
      durationMs: Date.now() - start,
    } satisfies BrowseResult;
  }

  const ctx = await browser.newContext({ viewport: { width: 1280, height: 800 } });
  const page = await ctx.newPage();
  page.on("console", (msg) => {
    if (msg.type() === "error") consoleErrors.push(msg.text().slice(0, 500));
  });
  page.on("pageerror", (err) => {
    pageErrors.push(err.message.slice(0, 500));
  });

  let status: number | undefined;
  try {
    const resp = await page.goto(url, { waitUntil: "domcontentloaded", timeout: waitSeconds * 1000 });
    status = resp?.status();
  } catch (err) {
    errors.push(`page.goto failed: ${(err as Error).message.slice(0, 300)}`);
  }

  let title: string | undefined;
  let textChars: number | undefined;
  let screenshotPath: string | undefined;
  if (!errors.length) {
    try {
      title = await page.title();
      textChars = (await page.evaluate(() => document.body?.innerText?.length ?? 0)) || 0;
      const dir = join(homedir(), ".platform", "verifications", taskId ?? "untagged");
      mkdirSync(dir, { recursive: true });
      const path = join(dir, `${Date.now()}.png`);
      mkdirSync(dirname(path), { recursive: true });
      await page.screenshot({ path, fullPage: false });
      screenshotPath = path;
      if (textChars && textChars < 30) errors.push(`page body very short: ${textChars} chars (probably empty or broken)`);
      if (status && status >= 400) errors.push(`upstream HTTP ${status}`);
    } catch (err) {
      errors.push(`inspection failed: ${(err as Error).message.slice(0, 300)}`);
    }
  }
  if (consoleErrors.length > 0) errors.push(`${consoleErrors.length} console error(s)`);
  if (pageErrors.length > 0) errors.push(`${pageErrors.length} page error(s)`);

  await ctx.close();

  return {
    ok: errors.length === 0,
    url,
    status,
    title,
    textChars,
    consoleErrors,
    pageErrors,
    screenshotPath,
    errors,
    durationMs: Date.now() - start,
  } satisfies BrowseResult;
});

// Convenience: write screenshot file path back as JSON content.
app.get<{ Querystring: { path: string } }>("/screenshot", async (req, reply) => {
  const p = req.query.path;
  if (!p || !p.startsWith(join(homedir(), ".platform", "verifications"))) {
    return reply.code(400).send({ error: "bad path" });
  }
  reply.type("image/png");
  return readFile(p);
});

import { readFile } from "node:fs/promises";

const port = Number(process.env.PORT ?? 8083);
app.listen({ host: "0.0.0.0", port }).then(() => {
  console.log(`verify listening on :${port}`);
});
