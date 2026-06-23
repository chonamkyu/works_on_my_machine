/**
 * Per-user pod supervisor.
 *
 * Phase 0 stub: stands up a TCP control listener that the control plane will
 * eventually call over gRPC. For now it accepts plain JSON line frames:
 *
 *   { "type": "run", "runId": "...", "workflowDir": "/path/to/wf" }
 *   { "type": "cancel", "runId": "..." }
 *
 * Each run is spawned as a child Node process; stdout/stderr are streamed
 * back over the same socket as `{ "type": "log", "runId", "stream", "text" }`
 * and state transitions as `{ "type": "state", "runId", "state" }`.
 */

import { createServer } from "node:net";
import { spawn, type ChildProcess } from "node:child_process";
import { resolve } from "node:path";

interface ControlFrame {
  type: "run" | "cancel";
  runId: string;
  workflowDir?: string;
}

const port = Number(process.env.SUPERVISOR_PORT ?? 50051);
const runs = new Map<string, ChildProcess>();

const server = createServer((socket) => {
  let buf = "";
  socket.on("data", (chunk) => {
    buf += chunk.toString("utf8");
    let nl: number;
    while ((nl = buf.indexOf("\n")) >= 0) {
      const line = buf.slice(0, nl).trim();
      buf = buf.slice(nl + 1);
      if (!line) continue;
      try {
        handle(JSON.parse(line) as ControlFrame, socket);
      } catch (err) {
        socket.write(JSON.stringify({ type: "error", message: (err as Error).message }) + "\n");
      }
    }
  });
});

server.listen(port, () => {
  console.log(`supervisor listening on tcp/${port}`);
});

function handle(frame: ControlFrame, socket: NodeJS.WritableStream): void {
  if (frame.type === "run") {
    const dir = resolve(frame.workflowDir ?? process.cwd());
    const child = spawn(process.execPath, ["--experimental-strip-types", "main.ts"], {
      cwd: dir,
      env: { ...process.env, PLATFORM_RUN_ID: frame.runId },
      stdio: ["ignore", "pipe", "pipe"],
    });
    runs.set(frame.runId, child);
    socket.write(JSON.stringify({ type: "state", runId: frame.runId, state: "running" }) + "\n");

    pipeStream(child.stdout!, frame.runId, "stdout", socket);
    pipeStream(child.stderr!, frame.runId, "stderr", socket);

    child.on("exit", (code) => {
      runs.delete(frame.runId);
      const state = code === 0 ? "success" : "failed";
      socket.write(JSON.stringify({ type: "state", runId: frame.runId, state, exitCode: code }) + "\n");
    });
    return;
  }

  if (frame.type === "cancel") {
    const child = runs.get(frame.runId);
    if (!child) {
      socket.write(JSON.stringify({ type: "error", runId: frame.runId, message: "no such run" }) + "\n");
      return;
    }
    child.kill("SIGTERM");
    setTimeout(() => {
      if (runs.has(frame.runId)) child.kill("SIGKILL");
    }, 10_000);
  }
}

function pipeStream(
  src: NodeJS.ReadableStream,
  runId: string,
  stream: "stdout" | "stderr",
  out: NodeJS.WritableStream,
) {
  let leftover = "";
  src.on("data", (data: Buffer) => {
    leftover += data.toString("utf8");
    let nl: number;
    while ((nl = leftover.indexOf("\n")) >= 0) {
      const line = leftover.slice(0, nl);
      leftover = leftover.slice(nl + 1);
      out.write(JSON.stringify({ type: "log", runId, stream, text: line }) + "\n");
    }
  });
  src.on("end", () => {
    if (leftover) {
      out.write(JSON.stringify({ type: "log", runId, stream, text: leftover }) + "\n");
    }
  });
}
