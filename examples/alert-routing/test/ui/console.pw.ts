// The console's smoke test: an alert sent from the form is previewed in the
// browser, decided by the server the same way, and shows up in the live
// feed, the inbox and its detail page. The router's own behavior is the
// integration and e2e suites' job; this checks the pages hold together.
import { expect, type Page, test } from "@playwright/test";

/** A name no other run used, so the feed row is this run's. */
function uniqueName(prefix: string): string {
  return `${prefix}${Date.now().toString(36)}${Math.floor(Math.random() * 1e4)}`;
}

async function fillAlert(page: Page, name: string) {
  await page.goto("/send");
  await page.getByLabel("Alert name").fill(name);
  // checkout, critical, production, 2m: platform.paging pages the on-call.
  await expect(page.getByTestId("preview").getByTestId("outcome-reason")).toHaveText("critical_alert");
  await expect(page.getByTestId("preview").getByTestId("outcome-destination")).toHaveText("checkout-primary");
}

test("an alert sent to the route endpoint matches its preview and reaches the feed", async ({ page }) => {
  const name = uniqueName("SmokeRoute");
  await fillAlert(page, name);
  await expect(page.getByTestId("preview").getByTestId("trace-candidate").first()).toHaveAttribute(
    "data-winner",
    "true",
  );

  await page.getByRole("button", { name: "Send to the router" }).click();
  const server = page.getByTestId("server-answer");
  await expect(server.getByText("HTTP 200")).toBeVisible();
  await expect(server.getByTestId("outcome-reason")).toHaveText("critical_alert");
  await expect(server.getByTestId("agreement")).toHaveAttribute("data-agrees", "true");

  await page.getByRole("link", { name: "Overview" }).click();
  const row = page.locator(`[data-testid="feed-row"][data-alertname="${name}"]`);
  await expect(row).toBeVisible();
  await expect(row).toContainText("checkout-primary");

  await row.getByRole("link", { name }).click();
  await expect(page.getByRole("heading", { level: 1, name })).toBeVisible();
  await expect(page.getByTestId("trace-candidate").first()).toHaveAttribute("data-winner", "true");
});

test("an alert sent as a webhook matches its preview and lands in the on-call inbox", async ({ page }) => {
  const name = uniqueName("SmokeWebhook");
  await fillAlert(page, name);
  await page.getByLabel("Send as Alertmanager webhook").check();
  await expect(page.getByText("POST /api/v1/alerts")).toBeVisible();

  await page.getByRole("button", { name: "Send to the router" }).click();
  const server = page.getByTestId("server-answer");
  await expect(server.getByText("HTTP 200")).toBeVisible();
  await expect(server.getByTestId("agreement")).toHaveAttribute("data-agrees", "true");

  await page.getByRole("link", { name: "Notifications" }).click();
  await page.getByRole("tab", { name: /On-call/ }).click();
  await expect(
    page.locator('[data-testid="inbox"][data-destination="checkout-primary"]').getByText(name),
  ).toBeVisible();
});

// Playwright can't route a dedicated worker's requests (a routed sigil.wasm
// fetch hangs instead of reaching the handler), so the engine goes missing
// one step earlier: the worker helper the page imports.
test("a missing policy engine leaves the preview unavailable and the form working", async ({ page }) => {
  await page.route("**/sigil/worker.js", (route) => route.fulfill({ status: 404, body: "not found" }));
  const name = uniqueName("SmokeNoWasm");
  await page.goto("/send");
  await expect(page.getByTestId("preview-unavailable")).toBeVisible();
  await page.getByLabel("Alert name").fill(name);
  await page.getByRole("button", { name: "Send to the router" }).click();
  const server = page.getByTestId("server-answer");
  await expect(server.getByText("HTTP 200")).toBeVisible();
  await expect(server.getByTestId("outcome-reason")).toHaveText("critical_alert");
});

test("the policies page explains each team's policy", async ({ page }) => {
  await page.goto("/policies");
  await expect(page.getByTestId("fingerprint")).not.toBeEmpty();
  await expect(page.getByRole("tab", { name: "checkout" })).toBeVisible();
  await expect(page.getByText("checkout.alerts").first()).toBeVisible();
  await expect(page.getByRole("cell", { name: /critical_alert/ }).first()).toBeVisible();
});

test("the teams page lists the directory", async ({ page }) => {
  await page.goto("/teams");
  await expect(page.getByTestId("team-row")).toHaveCount(2);
  await expect(page.getByRole("cell", { name: "checkout-primary" })).toBeVisible();
});
