import { test, expect } from "@playwright/test";

test.describe("admin dashboard", () => {
  test("clicking Admin button opens full-page dashboard with back button and tabs", async ({
    page,
  }) => {
    await page.goto("/", { waitUntil: "domcontentloaded" });

    // Admin button is visible for dev user (isAdmin=true)
    const adminBtn = page.getByTestId("open-admin");
    await adminBtn.waitFor({ state: "visible", timeout: 8_000 });
    await adminBtn.click();

    // Full-page header with "Admin Dashboard" text
    await expect(page.getByText("Admin Dashboard")).toBeVisible({ timeout: 5_000 });

    // Back button
    const backBtn = page.getByRole("button", { name: /Back/i });
    await expect(backBtn).toBeVisible({ timeout: 3_000 });

    // All six tabs must be present
    for (const tab of ["Overview", "Users", "Workspaces", "Audit", "Triggers", "Self-Evolve"]) {
      await expect(page.getByRole("button", { name: tab })).toBeVisible({ timeout: 5_000 });
    }
  });

  test("overview tab shows count cards with numeric values", async ({ page }) => {
    await page.goto("/?admin=1", { waitUntil: "domcontentloaded" });
    await expect(page.getByText("Admin Dashboard")).toBeVisible({ timeout: 8_000 });

    // Wait for the count cards to load — the overview section heading
    await expect(page.getByText("platform counts")).toBeVisible({ timeout: 8_000 });

    // The "users" card must show a number ≥ 1
    const usersCard = page.locator(".grid").locator("div").filter({ hasText: /^users$/i }).first();
    // The count is a sibling span with font-mono text-2xl — find the card container
    const usersCount = page
      .locator("div")
      .filter({ has: page.locator("span", { hasText: /^users$/i }) })
      .locator("span.text-2xl")
      .first();
    await expect(usersCount).toBeVisible({ timeout: 8_000 });
    const txt = await usersCount.textContent();
    expect(Number(txt)).toBeGreaterThan(0);
  });

  test("users tab shows at least one row with sub, email and a real date", async ({ page }) => {
    await page.goto("/?admin=1", { waitUntil: "domcontentloaded" });
    await expect(page.getByText("Admin Dashboard")).toBeVisible({ timeout: 8_000 });

    // Click the Users tab
    await page.getByRole("button", { name: "Users" }).click();

    // Wait for table to load (look for a cell with "dev" sub text)
    const subCell = page.locator("td").filter({ hasText: /^dev$/ }).first();
    await expect(subCell).toBeVisible({ timeout: 8_000 });

    // Email cell must be non-empty
    const emailCell = page.locator("td").filter({ hasText: /dev@example\.com/ }).first();
    await expect(emailCell).toBeVisible({ timeout: 3_000 });

    // Created-at column must NOT contain "Invalid Date"
    const rows = page.locator("tbody tr");
    const firstRow = rows.first();
    const createdCell = firstRow.locator("td").nth(2); // 0=email, 1=sub, 2=created
    const dateText = await createdCell.textContent();
    expect(dateText).toBeTruthy();
    expect(dateText).not.toContain("Invalid Date");
    // Must look like a date (e.g., "6/30/2026" or "2026-06-30")
    expect(dateText!.trim()).toMatch(/\d/);
  });

  test("Back button returns to the main app", async ({ page }) => {
    await page.goto("/?admin=1", { waitUntil: "domcontentloaded" });
    await expect(page.getByText("Admin Dashboard")).toBeVisible({ timeout: 8_000 });

    const backBtn = page.getByRole("button", { name: /Back/i });
    await backBtn.click();

    // Main app: wordmark + workspace switcher should be back
    await expect(page.locator('[title="Active workspace"]')).toBeVisible({ timeout: 5_000 });
    await expect(page.getByText("Admin Dashboard")).toHaveCount(0);
  });
});
