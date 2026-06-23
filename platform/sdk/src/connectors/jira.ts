/**
 * Jira connector. Uses PLATFORM_TOKEN_JIRA in the form "email:api_token",
 * and PLATFORM_JIRA_BASE_URL for the site URL.
 */

interface SearchResult {
  issues: Array<{
    key: string;
    fields: { summary: string; status: { name: string } };
  }>;
}

function auth(): string {
  const t = process.env.PLATFORM_TOKEN_JIRA;
  if (!t || !t.includes(":")) {
    throw new Error(
      "jira connector: PLATFORM_TOKEN_JIRA must be 'email:api_token'.",
    );
  }
  return "Basic " + Buffer.from(t).toString("base64");
}

function baseUrl(): string {
  const u = process.env.PLATFORM_JIRA_BASE_URL;
  if (!u) throw new Error("jira connector: PLATFORM_JIRA_BASE_URL unset.");
  return u.replace(/\/$/, "");
}

export const jira = {
  async search(jql: string): Promise<SearchResult["issues"]> {
    const res = await fetch(`${baseUrl()}/rest/api/3/search?jql=${encodeURIComponent(jql)}`, {
      headers: { Authorization: auth(), Accept: "application/json" },
    });
    if (!res.ok) {
      const text = await res.text();
      throw new Error(`jira search failed: ${res.status} ${text.slice(0, 300)}`);
    }
    const data = (await res.json()) as SearchResult;
    return data.issues;
  },
};
