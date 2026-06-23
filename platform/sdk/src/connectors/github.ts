/**
 * GitHub connector. Uses the platform-injected PLATFORM_TOKEN_GITHUB
 * (a personal access token or OAuth-issued token).
 */

export interface Issue {
  number: number;
  title: string;
  body: string;
  url: string;
  html_url: string;
  state: string;
  labels: Array<{ name: string }>;
}

export interface PullRequest {
  number: number;
  title: string;
  url: string;
  html_url: string;
  user: { login: string };
  state: string;
  draft: boolean;
}

export interface ListIssuesArgs {
  repo: string; // "owner/name"
  state?: "open" | "closed" | "all";
  labels?: string[];
  since?: string;
  perPage?: number;
}

export interface CreateIssueArgs {
  repo: string;
  title: string;
  body?: string;
  labels?: string[];
}

export interface ListPullsArgs {
  repo: string;
  state?: "open" | "closed" | "all";
  perPage?: number;
}

export interface CommentArgs {
  repo: string;
  issueNumber: number;
  body: string;
}

const apiBase = "https://api.github.com";

function token(): string {
  const t = process.env.PLATFORM_TOKEN_GITHUB;
  if (!t) {
    throw new Error(
      "github connector: no token. Add a 'github' credential in Settings → Connectors and bind it on this task.",
    );
  }
  return t;
}

async function req<T>(
  method: string,
  path: string,
  body?: unknown,
): Promise<T> {
  const headers: Record<string, string> = {
    Accept: "application/vnd.github+json",
    "X-GitHub-Api-Version": "2022-11-28",
    Authorization: `Bearer ${token()}`,
    "User-Agent": "platform-workflow",
  };
  if (body !== undefined) headers["Content-Type"] = "application/json";
  const res = await fetch(apiBase + path, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(`github ${method} ${path} → ${res.status}: ${text.slice(0, 500)}`);
  }
  if (res.status === 204) return undefined as T;
  return res.json() as Promise<T>;
}

export const github = {
  issues: {
    async list(args: ListIssuesArgs): Promise<Issue[]> {
      const q = new URLSearchParams();
      if (args.state) q.set("state", args.state);
      if (args.labels && args.labels.length) q.set("labels", args.labels.join(","));
      if (args.since) q.set("since", args.since);
      q.set("per_page", String(args.perPage ?? 30));
      return req<Issue[]>("GET", `/repos/${args.repo}/issues?${q}`);
    },
    async create(args: CreateIssueArgs): Promise<Issue> {
      return req<Issue>("POST", `/repos/${args.repo}/issues`, {
        title: args.title,
        body: args.body,
        labels: args.labels,
      });
    },
    async comment(args: CommentArgs): Promise<void> {
      await req<void>(
        "POST",
        `/repos/${args.repo}/issues/${args.issueNumber}/comments`,
        { body: args.body },
      );
    },
  },
  pulls: {
    async list(args: ListPullsArgs): Promise<PullRequest[]> {
      const q = new URLSearchParams();
      if (args.state) q.set("state", args.state);
      q.set("per_page", String(args.perPage ?? 30));
      return req<PullRequest[]>("GET", `/repos/${args.repo}/pulls?${q}`);
    },
  },
};
