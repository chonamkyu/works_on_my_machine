import { test, expect } from "@playwright/test";

test.describe("app shell", () => {
  test("loads at / without console errors, TopBar renders, sidebar renders", async ({ page }) => {
    const consoleErrors: string[] = [];
    page.on("console", (msg) => {
      if (msg.type() === "error") consoleErrors.push(msg.text());
    });
    const pageErrors: string[] = [];
    page.on("pageerror", (err) => pageErrors.push(err.message));

    await page.goto("/", { waitUntil: "domcontentloaded" });

    // Wordmark
    await expect(page.getByText("works_on_my_machine").first()).toBeVisible({ timeout: 5_000 });

    // Workspace switcher button appears (button shows workspace name + chevron)
    await expect(page.locator('[title="Active workspace"]')).toBeVisible({ timeout: 5_000 });

    // Admin button (dev mode → isAdmin=true)
    await expect(page.getByTestId("open-admin")).toBeVisible({ timeout: 5_000 });

    // Sidebar renders — at minimum the new-task button
    await expect(page.getByTestId("new-task-btn")).toBeVisible({ timeout: 5_000 });

    // Sidebar task groups — wait for at least one group heading to appear
    // Groups are "active", "idle", or "stopped" — at least one must appear if tasks exist.
    // The API has tasks, so one group header should be present.
    await expect(
      page.locator("text=/^(active|idle|stopped)$/i").first(),
    ).toBeVisible({ timeout: 8_000 });

    // No page-level JS errors
    expect(pageErrors).toHaveLength(0);
    // Filter out non-critical console errors (SSE/network noise is acceptable in dev)
    const criticalErrors = consoleErrors.filter(
      (e) =>
        !e.includes("ERR_CONNECTION_REFUSED") &&
        !e.includes("net::") &&
        !e.includes("favicon"),
    );
    expect(criticalErrors).toHaveLength(0);
  });
});
