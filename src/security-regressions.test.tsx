import { act, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ApplicationChange } from "./types";

const events = vi.hoisted(() => ({
  listener: undefined as ((change: ApplicationChange) => void) | undefined,
  stop: vi.fn(),
}));
vi.mock("./runtimeConfig", async (original) => {
  const actual = await original<typeof import("./runtimeConfig")>();
  return {
    ...actual,
    runtimeConfig: { ...actual.runtimeConfig, dataMode: "api" },
  };
});
vi.mock("./service", async (original) => {
  const actual = await original<typeof import("./service")>();
  return {
    ...actual,
    subscribeApplicationEvents: (
      listener: (change: ApplicationChange) => void,
    ) => {
      events.listener = listener;
      return events.stop;
    },
  };
});
import { useApplicationEvents } from "./useApplicationEvents";
import { operationsService } from "./service";
import { workflowRequest } from "./workflows";

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  events.stop.mockClear();
});
describe("security and request-storm regressions", () => {
  it("coalesces a Watch burst and cleans up the pending callback", () => {
    vi.useFakeTimers();
    const callback = vi.fn();
    const hook = renderHook(() => useApplicationEvents(callback));
    act(() => {
      for (let i = 0; i < 50; i++)
        events.listener?.({
          namespace: "spark",
          name: `app-${i}`,
          type: "ADDED",
        } as ApplicationChange);
      vi.advanceTimersByTime(749);
    });
    expect(callback).not.toHaveBeenCalled();
    act(() => vi.advanceTimersByTime(1));
    expect(callback).toHaveBeenCalledTimes(1);
    act(() =>
      events.listener?.({
        namespace: "spark",
        name: "last",
        type: "MODIFIED",
      } as ApplicationChange),
    );
    hook.unmount();
    act(() => vi.advanceTimersByTime(1000));
    expect(callback).toHaveBeenCalledTimes(1);
    expect(events.stop).toHaveBeenCalledTimes(1);
  });
  it("management requests notify session expiry and carry timeout/explicit API headers", async () => {
    const request = vi.fn(
      async (_url: unknown, _init?: RequestInit) =>
        new Response(JSON.stringify({ message: "expired" }), { status: 401 }),
    );
    vi.stubGlobal("fetch", request);
    const dispatch = vi.spyOn(window, "dispatchEvent");
    await expect(operationsService.listTemplates()).rejects.toThrow("expired");
    expect(dispatch).toHaveBeenCalledWith(
      expect.objectContaining({ type: "spark-console:unauthorized" }),
    );
    expect(request.mock.calls[0][1]).toEqual(
      expect.objectContaining({
        signal: expect.anything(),
        headers: expect.objectContaining({
          "X-Requested-With": "SparkControlCenter",
        }),
      }),
    );
  });
  it("workflow requests notify session expiry", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(
        async () =>
          new Response(JSON.stringify({ message: "expired" }), { status: 401 }),
      ),
    );
    const dispatch = vi.spyOn(window, "dispatchEvent");
    await expect(workflowRequest("/projects")).rejects.toThrow("expired");
    expect(dispatch).toHaveBeenCalledWith(
      expect.objectContaining({ type: "spark-console:unauthorized" }),
    );
  });
});
