/**
 * Opt-in e2e: create a task via chat → wait for dry-run → assert the verify
 * harness ran. Does NOT assert generated UI quality.
 *
 * Guard: set E2E_GEN=1 in the environment to run this spec.
 * Typical run time: 1–3 minutes (LLM generation + dry-run).
 */
import { test, expect } from "@playwright/test";

const API_BASE = "http://localhost:8787";

test.describe("generate → verify harness", () => {
  test("create task, generate, assert dry-run ran", async ({ request }) => {
    test.skip(!process.env.E2E_GEN, "set E2E_GEN=1 to run the live-stack generation test");
    test.setTimeout(180_000);

    // -----------------------------------------------------------------------
    // Step 1: create the task
    // -----------------------------------------------------------------------
    const createRes = await request.post(`${API_BASE}/v1/tasks`, {
      data: {
        name: "e2e-gen-verify-test",
        description: "E2E test task for generation+verify harness",
        triggers: [],
        capabilities: [],
      },
    });
    expect([200, 201], `POST /v1/tasks failed: ${createRes.status()}`).toContain(createRes.status());
    const task = await createRes.json();
    const taskId: string = task.id;
    console.log(`[e2e] created task id=${taskId}`);

    // -----------------------------------------------------------------------
    // Step 2: send the generation prompt
    // The chat endpoint may stream SSE or return plain JSON.
    // We fire the request and consume the full response body either way.
    // -----------------------------------------------------------------------
    const chatRes = await request.fetch(`${API_BASE}/v1/chat/${taskId}/messages`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      data: JSON.stringify({
        content:
          "Show a simple dashboard UI that displays the current server time, updating every second.",
      }),
    });

    // Accept any 2xx — SSE returns 200, JSON also 200
    expect(
      chatRes.status(),
      `POST /v1/chat/${taskId}/messages failed: ${chatRes.status()}`,
    ).toBeGreaterThanOrEqual(200);
    expect(chatRes.status()).toBeLessThan(300);

    const contentType = chatRes.headers()["content-type"] ?? "";
    let chatBody: string;
    if (contentType.includes("text/event-stream")) {
      // SSE: read the full stream to completion
      chatBody = await chatRes.text();
      console.log(`[e2e] SSE stream received (${chatBody.length} bytes)`);
    } else {
      chatBody = await chatRes.text();
      console.log(`[e2e] JSON chat response (${chatBody.length} bytes)`);
    }
    // We don't assert chat body content — the generator may reply with various
    // messages; we only care that the harness subsequently runs.
    expect(chatBody.length).toBeGreaterThan(0);

    // -----------------------------------------------------------------------
    // Step 3: poll GET /v1/tasks/{id} + GET /v1/tasks/{id}/dryrun until
    // dry-run result exists or ~120 s timeout
    // -----------------------------------------------------------------------
    const POLL_INTERVAL_MS = 3_000;
    const POLL_TIMEOUT_MS = 120_000;
    const pollStart = Date.now();
    const deadline = pollStart + POLL_TIMEOUT_MS;

    let dryrunResult: unknown = null;
    let taskKind: string | undefined;

    while (Date.now() < deadline) {
      // Fetch task info
      const taskRes = await request.get(`${API_BASE}/v1/tasks/${taskId}`);
      if (taskRes.ok()) {
        const taskData = await taskRes.json();
        taskKind = taskData.kind;
      }

      // Fetch dry-run result
      const dryrunRes = await request.get(`${API_BASE}/v1/tasks/${taskId}/dryrun`);
      if (dryrunRes.ok()) {
        const body = await dryrunRes.text();
        if (body && body.trim().length > 0 && body.trim() !== "null") {
          try {
            dryrunResult = JSON.parse(body);
          } catch {
            dryrunResult = body; // treat as string
          }
          if (dryrunResult !== null && dryrunResult !== undefined) {
            console.log(`[e2e] dry-run result ready, task kind=${taskKind}`);
            break;
          }
        }
      }

      console.log(
        `[e2e] waiting for dry-run… task kind=${taskKind ?? "unknown"}, elapsed=${Math.round((Date.now() - pollStart) / 1000)}s`,
      );
      await new Promise<void>((r) => setTimeout(r, POLL_INTERVAL_MS));
    }

    // -----------------------------------------------------------------------
    // Step 4 (core assertion): dry-run result must exist
    // -----------------------------------------------------------------------
    expect(
      dryrunResult,
      "dry-run result was still null/empty after 120 s — harness did not run",
    ).not.toBeNull();
    expect(
      dryrunResult,
      "dry-run result was still undefined after 120 s — harness did not run",
    ).not.toBeUndefined();

    // Log what the harness reported
    const dryrunStr = JSON.stringify(dryrunResult);
    const dryrunOk = dryrunStr.toLowerCase().includes('"ok"') ||
      dryrunStr.toLowerCase().includes('"success"') ||
      dryrunStr.toLowerCase().includes('"passed"');
    const dryrunError = dryrunStr.toLowerCase().includes('"error"') ||
      dryrunStr.toLowerCase().includes('"failed"') ||
      dryrunStr.toLowerCase().includes('"fail"');
    console.log(
      `[e2e] dry-run result: ${dryrunOk ? "OK" : dryrunError ? "ERROR/FAIL" : "present (status unclear)"} — ${dryrunStr.slice(0, 300)}`,
    );

    // -----------------------------------------------------------------------
    // Step 4b: for service tasks, poll verification result too
    // -----------------------------------------------------------------------
    if (taskKind === "service") {
      const verifyDeadline = Date.now() + 60_000; // extra 60 s
      let verificationResult: unknown = null;

      while (Date.now() < verifyDeadline) {
        const verifyRes = await request.get(`${API_BASE}/v1/tasks/${taskId}/verification`);
        if (verifyRes.ok()) {
          const body = await verifyRes.text();
          if (body && body.trim().length > 0 && body.trim() !== "null") {
            try {
              verificationResult = JSON.parse(body);
            } catch {
              verificationResult = body;
            }
            if (verificationResult !== null && verificationResult !== undefined) {
              console.log("[e2e] verification result ready");
              break;
            }
          }
        }
        console.log(`[e2e] waiting for verification result…`);
        await new Promise<void>((r) => setTimeout(r, POLL_INTERVAL_MS));
      }

      expect(
        verificationResult,
        "service task: verification result was null/empty after 60 s",
      ).not.toBeNull();
      expect(
        verificationResult,
        "service task: verification result was undefined after 60 s",
      ).not.toBeUndefined();

      const verifyStr = JSON.stringify(verificationResult);
      console.log(`[e2e] verification result: ${verifyStr.slice(0, 300)}`);
    } else {
      console.log(`[e2e] task kind=${taskKind ?? "unknown"} — skipping browser-verify poll`);
    }
  });
});
