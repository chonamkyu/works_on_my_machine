/**
 * API e2e suite — exercises the HTTP API on :8787 directly via Playwright's
 * `request` fixture. No browser UI involved; uses the dev user (PLATFORM_AUTH_DEV=1).
 */
import { test, expect } from "@playwright/test";

const API_BASE = "http://localhost:8787";

test.describe("API e2e", () => {
  test("GET /v1/me → 200, sub=dev, isAdmin=true", async ({ request }) => {
    const res = await request.get(`${API_BASE}/v1/me`);
    expect(res.status()).toBe(200);
    const body = await res.json();
    expect(body.sub).toBe("dev");
    expect(body.isAdmin).toBe(true);
  });

  test("workspace CRUD: create → list → delete → gone; personal delete rejected", async ({
    request,
  }) => {
    const uniqueName = `e2e-api-ws-${Date.now()}`;

    // Create
    const createRes = await request.post(`${API_BASE}/v1/workspaces`, {
      data: { name: uniqueName },
    });
    expect([200, 201]).toContain(createRes.status());
    const created = await createRes.json();
    expect(typeof created.id).toBe("string");
    expect(created.id.length).toBeGreaterThan(0);
    expect(created.name).toBe(uniqueName);
    const newId = created.id;

    // List includes it
    const listRes = await request.get(`${API_BASE}/v1/workspaces`);
    expect(listRes.status()).toBe(200);
    const listBody = await listRes.json();
    expect(Array.isArray(listBody.workspaces)).toBe(true);
    const found = listBody.workspaces.find(
      (w: { id: string; name: string }) => w.id === newId,
    );
    expect(found).toBeTruthy();
    expect(found.name).toBe(uniqueName);

    // Delete
    const delRes = await request.delete(`${API_BASE}/v1/workspaces/${newId}`);
    expect(delRes.status()).toBe(204);

    // No longer in list
    const listRes2 = await request.get(`${API_BASE}/v1/workspaces`);
    const listBody2 = await listRes2.json();
    const foundAfterDelete = listBody2.workspaces.find(
      (w: { id: string }) => w.id === newId,
    );
    expect(foundAfterDelete).toBeUndefined();

    // Deleting personal workspace → rejected (4xx)
    const delPersonalRes = await request.delete(`${API_BASE}/v1/workspaces/dev`);
    expect(delPersonalRes.status()).toBeGreaterThanOrEqual(400);
    expect(delPersonalRes.status()).toBeLessThan(500);
  });

  test("GET /v1/admin/overview → 200 with numeric counts", async ({ request }) => {
    const res = await request.get(`${API_BASE}/v1/admin/overview`);
    expect(res.status()).toBe(200);
    const body = await res.json();
    const counts = body.counts;
    expect(counts).toBeTruthy();
    // Every count field is a non-negative integer
    for (const key of [
      "users",
      "workspaces",
      "mcpServers",
      "skills",
      "runningServices",
      "activeRuns",
      "pendingProposals",
      "pendingEvolutionPRs",
    ]) {
      expect(typeof counts[key]).toBe("number");
      expect(counts[key]).toBeGreaterThanOrEqual(0);
    }
    // At least one user exists
    expect(counts.users).toBeGreaterThan(0);
  });

  test("GET /v1/admin/users → first entry has camelCase sub + email", async ({ request }) => {
    const res = await request.get(`${API_BASE}/v1/admin/users`);
    expect(res.status()).toBe(200);
    const body = await res.json();
    expect(Array.isArray(body.users)).toBe(true);
    expect(body.users.length).toBeGreaterThan(0);
    const first = body.users[0];
    // camelCase keys
    expect(typeof first.sub).toBe("string");
    expect(first.sub.length).toBeGreaterThan(0);
    expect(typeof first.email).toBe("string");
    expect(first.email.length).toBeGreaterThan(0);
    // The dev user is always first and should be admin
    expect(first.sub).toBe("dev");
    expect(first.isAdmin).toBe(true);
  });
});
