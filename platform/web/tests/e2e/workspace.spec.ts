import { test, expect } from "@playwright/test";

test.describe("workspace switcher", () => {
  test("shows Dev Personal as initial active workspace", async ({ page }) => {
    await page.goto("/", { waitUntil: "domcontentloaded" });
    const btn = page.locator('[title="Active workspace"]');
    await expect(btn).toBeVisible({ timeout: 8_000 });
    await expect(btn).toContainText("Dev Personal");
  });

  test("opens popover with search input on click", async ({ page }) => {
    await page.goto("/", { waitUntil: "domcontentloaded" });
    const btn = page.locator('[title="Active workspace"]');
    await btn.waitFor({ state: "visible", timeout: 8_000 });
    await btn.click();

    // Search input should be autofocused inside the popover
    const search = page.getByPlaceholder("Search workspaces…");
    await expect(search).toBeVisible({ timeout: 3_000 });

    // "Dev Personal" appears in the popover list (not in the trigger button)
    const listItem = page.locator("ul li").filter({ hasText: "Dev Personal" });
    await expect(listItem).toBeVisible({ timeout: 3_000 });
  });

  test("search input filters workspace list", async ({ page }) => {
    await page.goto("/", { waitUntil: "domcontentloaded" });
    const btn = page.locator('[title="Active workspace"]');
    await btn.waitFor({ state: "visible", timeout: 8_000 });
    await btn.click();

    const search = page.getByPlaceholder("Search workspaces…");
    await search.fill("XXXXXXXXXXXXXXXXXX-no-match");
    await expect(page.getByText("No matches")).toBeVisible({ timeout: 3_000 });

    // Clear → workspaces return
    await search.clear();
    const listItem = page.locator("ul li").filter({ hasText: "Dev Personal" });
    await expect(listItem).toBeVisible({ timeout: 3_000 });
  });

  test("create workspace → appears and becomes active; then delete it → gone; personal has no delete", async ({
    page,
  }) => {
    await page.goto("/", { waitUntil: "domcontentloaded" });
    const btn = page.locator('[title="Active workspace"]');
    await btn.waitFor({ state: "visible", timeout: 8_000 });
    await btn.click();

    // Click "New workspace"
    await page.getByText("New workspace").click();

    const uniqueName = `e2e-ws-${Date.now()}`;
    const nameInput = page.getByPlaceholder("Workspace name…");
    await nameInput.fill(uniqueName);
    await page.getByRole("button", { name: "Create" }).click();

    // After creation the popover closes; the trigger button shows the new name
    await expect(page.locator('[title="Active workspace"]')).toContainText(uniqueName, {
      timeout: 8_000,
    });

    // Open popover again to verify list includes the new workspace
    await page.locator('[title="Active workspace"]').click();
    const newWsEntry = page.locator("ul li").filter({ hasText: uniqueName });
    await expect(newWsEntry).toBeVisible({ timeout: 5_000 });

    // Personal workspace has no delete button
    const personalRow = page.locator("ul li").filter({ hasText: "Dev Personal" });
    await expect(personalRow.locator('[title="Delete workspace"]')).toHaveCount(0);

    // Delete the newly created workspace
    const deleteBtn = newWsEntry.locator('[title="Delete workspace"]');
    // Hover to reveal (opacity-0 group-hover:opacity-100) — force click
    page.on("dialog", (dialog) => dialog.accept());
    await deleteBtn.click({ force: true });

    // After deletion the workspace should no longer be in the list
    // Re-open the popover (it may have closed after deletion)
    await page.locator('[title="Active workspace"]').click();
    // The popover re-opens; give a moment for the refetch
    await expect(page.locator("ul li").filter({ hasText: uniqueName })).toHaveCount(0, {
      timeout: 8_000,
    });
  });
});
