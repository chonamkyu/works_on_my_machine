/**
 * Built-in tools the workflow author can call without configuring a connector.
 *
 * Phase 0 ships type stubs only — Phase 1 wires these up to the supervisor's
 * tool-execution channel.
 */

const stub = (name: string) => () => {
  throw new Error(`@platform/sdk/tools: ${name} requires the supervisor runtime (Phase 1)`);
};

export interface BrowserPage {
  fill(selector: string, value: string): Promise<void>;
  click(selector: string): Promise<void>;
  extract<T>(args: { schema: Record<string, "string" | "number" | "boolean"> }): Promise<T>;
  screenshot(): Promise<Uint8Array>;
  close(): Promise<void>;
}

export const browser = {
  open: stub("browser.open") as (_: string) => Promise<BrowserPage>,
};

export const shell = {
  exec: stub("shell.exec") as (_: string[]) => Promise<{ stdout: string; stderr: string }>,
};

export const files = {
  read: stub("files.read") as (_: string) => Promise<string>,
  write: stub("files.write") as (_: string, __: string) => Promise<void>,
  list: stub("files.list") as (_: string) => Promise<string[]>,
};

export const http = {
  request: stub("http.request") as (_: { method: string; url: string; body?: unknown }) => Promise<unknown>,
};

// Re-export `claude` here too. The LLM-generated workflows often hallucinate
// `import { claude } from '@platform/sdk/tools'`; rather than block them at
// runtime, accept that path.
export { claude } from "../index.js";
