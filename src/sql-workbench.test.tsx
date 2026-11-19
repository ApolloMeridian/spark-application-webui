import "@testing-library/jest-dom/vitest";
import { act, cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { App } from "antd";
import { afterEach, expect, it, vi } from "vitest";
import { I18nProvider } from "./i18n";
import SqlPage from "./pages/SqlPage";

vi.mock("./auth", () => ({ useAuth: () => ({ session: null }) }));
vi.mock("./components/SqlEditor", () => ({ default: () => <textarea aria-label="SQL" /> }));

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  localStorage.clear();
});

function renderWorkspace() {
  localStorage.setItem("spark-console-locale", "en-US");
  render(
    <I18nProvider>
      <App>
        <SqlPage />
      </App>
    </I18nProvider>,
  );
}

it("keeps the workspace usable when the catalog has no root namespaces", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => Response.json(null)));
  await act(async () => renderWorkspace());
  expect(screen.getByRole("button", { name: "Catalog polaris" })).toBeEnabled();
  expect(screen.getByRole("button", { name: "Run query" })).toBeEnabled();
});

it("keeps the workspace usable when the selected namespace has no child namespaces", async () => {
  const user = userEvent.setup();
  const fetchCatalog = vi.fn(async (input: RequestInfo | URL) => {
    const url = new URL(String(input), window.location.origin);
    if (url.pathname.endsWith("/namespaces")) {
      return Response.json(url.searchParams.has("parent") ? null : ["polaris.analytics"]);
    }
    if (url.pathname.endsWith("/tables")) {
      return Response.json([{ table: "polaris.analytics.events", scope: "table" }]);
    }
    if (url.pathname.endsWith("/views")) {
      return Response.json([{ table: "polaris.analytics.events_view", scope: "view" }]);
    }
    return new Response(null, { status: 404 });
  });
  vi.stubGlobal("fetch", fetchCatalog);
  renderWorkspace();

  expect(fetchCatalog.mock.calls.some(([url]) => String(url).includes("/tables"))).toBe(false);
  await user.click(await screen.findByRole("button", { name: "Namespace polaris.analytics" }));
  expect(await screen.findByText("events", { exact: true })).toBeVisible();
  expect(screen.getByRole("button", { name: "View polaris.analytics.events_view" })).toBeVisible();
  expect(screen.getByRole("button", { name: "Run query" })).toBeEnabled();
});
