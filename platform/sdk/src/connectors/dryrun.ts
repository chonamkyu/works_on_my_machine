/**
 * Dry-run mode for connectors. When PLATFORM_DRY_RUN=1 the platform spawns
 * the workflow with mock connectors so we can flush out hallucinated SDK
 * methods, undefined variables, and import errors WITHOUT touching real
 * GitHub / Slack / Jira / etc.
 *
 * Each connector module imports `wrapDryRun` and uses it to wrap its public
 * surface. The wrapper:
 *   - returns canned safe data instead of calling the real API
 *   - throws if a property that doesn't exist on the connector is accessed,
 *     so `slack.foobar()` fails fast with a clear error
 *   - logs "[dry-run] called slack.post({ ... })" to stderr so the user can
 *     see what the workflow would do
 */

export function isDryRun(): boolean {
  return process.env.PLATFORM_DRY_RUN === "1";
}

type AnyObj = Record<string, unknown>;

interface MockEntry {
  /** Mock return value (or a thunk that produces one). */
  value: unknown | ((args: unknown) => unknown);
}

export interface DryRunOptions {
  /** Connector display name used in [dry-run] log lines. */
  name: string;
  /** Per-method mock returns. Anything not in the map returns `undefined`.
   *  Use a function value to inspect args before returning. */
  mocks?: Record<string, MockEntry["value"]>;
}

/**
 * Wrap a connector object so that — when PLATFORM_DRY_RUN=1 — method calls
 * log + return mock data instead of hitting the network. Nested objects are
 * wrapped recursively (e.g. github.issues.list).
 *
 * If dry-run mode is OFF, the original target is returned untouched.
 */
export function wrapDryRun<T extends AnyObj>(target: T, opts: DryRunOptions, path: string[] = []): T {
  if (!isDryRun()) return target;
  return new Proxy(target, {
    get(obj, prop, recv) {
      const key = String(prop);
      const val = Reflect.get(obj, prop, recv);
      // Pass through symbols, non-existent keys (so error reporting works).
      if (typeof prop === "symbol") return val;
      if (val === undefined) {
        // Property doesn't exist — let the caller hit a clean TypeError so
        // hallucinated methods surface immediately during dry-run.
        return undefined;
      }
      if (typeof val === "function") {
        const full = [...path, key].join(".");
        const mockKey = full;
        return (...args: unknown[]) => {
          const arg0 = args[0];
          process.stderr.write(
            `[dry-run] ${opts.name}.${full}(${safeJson(arg0)})\n`,
          );
          const m = opts.mocks?.[mockKey];
          if (typeof m === "function") return (m as (a: unknown) => unknown)(arg0);
          return m;
        };
      }
      if (val && typeof val === "object") {
        return wrapDryRun(val as AnyObj, opts, [...path, key]);
      }
      return val;
    },
  }) as T;
}

function safeJson(x: unknown): string {
  try {
    const s = JSON.stringify(x);
    if (!s) return "";
    return s.length > 200 ? s.slice(0, 200) + "…" : s;
  } catch {
    return "[unserializable]";
  }
}
