/**
 * Generic HTTP connector. No tokens injected — workflow author manages
 * authorization explicitly via headers. Subject to the capability allowlist.
 */

export interface HttpRequestArgs {
  method?: "GET" | "POST" | "PUT" | "PATCH" | "DELETE";
  url: string;
  headers?: Record<string, string>;
  body?: unknown;
  query?: Record<string, string | number | boolean>;
}

export interface HttpResponse<T = unknown> {
  status: number;
  ok: boolean;
  headers: Record<string, string>;
  body: T;
}

export const http = {
  async request<T = unknown>(args: HttpRequestArgs): Promise<HttpResponse<T>> {
    const url = new URL(args.url);
    if (args.query) {
      for (const [k, v] of Object.entries(args.query)) {
        url.searchParams.set(k, String(v));
      }
    }
    const init: RequestInit = {
      method: args.method ?? "GET",
      headers: { "User-Agent": "platform-workflow", ...(args.headers ?? {}) },
    };
    if (args.body !== undefined) {
      if (!init.headers || !("Content-Type" in (init.headers as object))) {
        init.headers = { ...(init.headers as object), "Content-Type": "application/json" };
      }
      init.body =
        typeof args.body === "string" ? args.body : JSON.stringify(args.body);
    }
    const res = await fetch(url.toString(), init);
    const headersObj: Record<string, string> = {};
    res.headers.forEach((v, k) => (headersObj[k] = v));
    const text = await res.text();
    let body: unknown = text;
    try {
      body = JSON.parse(text);
    } catch {
      /* keep as text */
    }
    return { status: res.status, ok: res.ok, headers: headersObj, body: body as T };
  },
  async get<T = unknown>(url: string, headers?: Record<string, string>) {
    return http.request<T>({ method: "GET", url, headers });
  },
  async post<T = unknown>(url: string, body: unknown, headers?: Record<string, string>) {
    return http.request<T>({ method: "POST", url, body, headers });
  },
};
