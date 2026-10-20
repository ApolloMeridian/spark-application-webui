import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { App } from "antd";
import { afterEach, expect, it, vi } from "vitest";
import { DriverLogsPanel } from "./components/DriverLogsPanel";
import { I18nProvider } from "./i18n";
import { sparkService } from "./service";
import { seedApplications } from "./mockData";
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  localStorage.clear();
});

it("queries early lines and keeps absolute line numbers through filtering and paging", async () => {
  localStorage.setItem("spark-console-locale", "en-US");
  const get = vi
    .spyOn(sparkService, "getDriverLogs")
    .mockImplementation(async (_ns, _name, q) => ({
      source: q.source,
      mode: q.mode,
      lines:
        q.mode === "range"
          ? [`INFO line-${q.startLine}`, `ERROR line-${(q.startLine ?? 1) + 1}`]
          : ["INFO latest-only"],
      startLine: q.mode === "range" ? (q.startLine ?? 1) : 0,
      endLine: q.mode === "range" ? (q.startLine ?? 1) + 1 : 0,
      hasMore: true,
      byteLimit: false,
      limit: q.limit,
    }));
  render(
    <I18nProvider>
      <App>
        <DriverLogsPanel app={seedApplications[0]} />
      </App>
    </I18nProvider>,
  );
  await screen.findByText("INFO latest-only");
  await userEvent.click(screen.getByRole("combobox", { name: "Query mode" }));
  await userEvent.click(screen.getByText("Line range", { exact: true }));
  fireEvent.change(screen.getByRole("spinbutton", { name: "Start line" }), {
    target: { value: "6001" },
  });
  fireEvent.change(screen.getByRole("spinbutton", { name: "End line" }), {
    target: { value: "8000" },
  });
  await userEvent.click(screen.getByRole("button", { name: /Query logs/ }));
  await screen.findByText("INFO line-6001");
  await userEvent.type(
    screen.getByRole("textbox", { name: "Filter current logs" }),
    "ERROR",
  );
  expect(screen.getByText("6002", { exact: true })).toBeTruthy();
  expect(screen.queryByText("INFO line-6001")).toBeNull();
  await userEvent.clear(
    screen.getByRole("textbox", { name: "Filter current logs" }),
  );
  await userEvent.click(screen.getByRole("button", { name: "Next range" }));
  await waitFor(() =>
    expect(get).toHaveBeenLastCalledWith(
      expect.any(String),
      expect.any(String),
      expect.objectContaining({ startLine: 8001, endLine: 10000 }),
    ),
  );
});
