import { test, expect } from "@playwright/test";

test("walking skeleton: seed tasks render and a run streams logs", async ({ page }) => {
  await page.goto("/");

  // Sidebar shows the seeded tasks.
  await expect(page.getByTestId("task-card-daily-bugs")).toBeVisible();
  await expect(page.getByTestId("task-card-pr-review")).toBeVisible();

  // Select daily-bugs → detail panel opens.
  await page.getByTestId("task-card-daily-bugs").click();
  await expect(page.getByTestId("task-run")).toBeVisible();

  // Trigger a run and assert logs appear via SSE.
  await page.getByTestId("task-run").click();
  await page.getByTestId("tab-logs").click();
  await expect(page.getByTestId("logs-pane")).toContainText("starting workflow daily-bugs", {
    timeout: 10_000,
  });
  await expect(page.getByTestId("logs-pane")).toContainText("done", {
    timeout: 15_000,
  });
});

test("chat can create a real task via the generator", async ({ page }) => {
  await page.goto("/");
  await page
    .getByTestId("chat-input")
    .fill("create a task that prints hello world plus the current iso timestamp");
  await page.getByTestId("chat-input").press("Enter");
  // Generator → LLM round trip can take a few seconds; allow generous wait.
  await expect(page.getByText(/Created task/i).first()).toBeVisible({ timeout: 90_000 });
});

test("nav tabs switch main view", async ({ page }) => {
  await page.goto("/");
  await page.getByTestId("nav-memory").click();
  await expect(page.getByText(/No memory yet/i)).toBeVisible({ timeout: 5_000 });
  await page.getByTestId("nav-evolution").click();
  await expect(page.getByText(/No PRs yet|Select a PR/i)).toBeVisible({ timeout: 5_000 });
  await page.getByTestId("nav-chat").click();
  await expect(page.getByTestId("chat-input")).toBeVisible();
});
