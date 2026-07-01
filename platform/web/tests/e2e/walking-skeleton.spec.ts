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
  test.setTimeout(120_000);
  await page.goto("/", { waitUntil: "domcontentloaded" });
  // Chat input is only available after selecting a task (1:1 chat-per-task model).
  // Select the seeded daily-bugs task first.
  await page.getByTestId("task-card-daily-bugs").waitFor({ state: "visible", timeout: 8_000 });
  await page.getByTestId("task-card-daily-bugs").click();
  await expect(page.getByTestId("chat-input")).toBeVisible({ timeout: 5_000 });
  await page
    .getByTestId("chat-input")
    .fill("create a task that prints hello world plus the current iso timestamp");
  await page.getByTestId("chat-input").press("Enter");
  // Generator → LLM round trip can take a few seconds; allow generous wait.
  await expect(page.getByText(/Created task/i).first()).toBeVisible({ timeout: 90_000 });
});

test("command palette opens and can navigate", async ({ page }) => {
  // Navigation moved from nav tabs to the ⌘K command palette.
  await page.goto("/", { waitUntil: "domcontentloaded" });
  await expect(page.getByTestId("open-palette")).toBeVisible({ timeout: 5_000 });
  // Open palette via button click
  await page.getByTestId("open-palette").click();
  // Command palette shows a search input with the known placeholder text
  await expect(
    page.getByPlaceholder("Search tasks, jump to memory · settings · evolution…"),
  ).toBeVisible({ timeout: 3_000 });
});
