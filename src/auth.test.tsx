import { StrictMode } from "react";
import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { AuthProvider, useAuth } from "./auth";
import { runtimeConfig } from "./runtimeConfig";

const originalMode = runtimeConfig.auth.mode;
afterEach(() => {
  cleanup();
  runtimeConfig.auth.mode = originalMode;
  vi.unstubAllGlobals();
});

it("ignores aborted session checks during StrictMode remount without prematurely redirecting", async () => {
  runtimeConfig.auth.mode = "local";
  let resolveSession!: (value: unknown) => void;
  const pending = new Promise((resolve) => {
    resolveSession = resolve;
  });
  let checks = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn((_url, options: { signal: AbortSignal }) => {
      if (++checks === 1)
        return new Promise((_resolve, reject) =>
          options.signal.addEventListener("abort", () =>
            reject(new DOMException("Aborted", "AbortError")),
          ),
        );
      return pending;
    }),
  );
  function Probe() {
    const auth = useAuth();
    return (
      <span>
        {auth.loading ? "loading" : auth.session?.username || "logged out"}
      </span>
    );
  }
  render(
    <StrictMode>
      <AuthProvider>
        <Probe />
      </AuthProvider>
    </StrictMode>,
  );
  await act(async () => {
    await Promise.resolve();
  });
  expect(screen.getByText("loading")).toBeTruthy();
  await act(async () =>
    resolveSession({
      ok: true,
      json: async () => ({ id: "test", username: "admin", role: "admin" }),
    }),
  );
  expect(screen.getByText("admin")).toBeTruthy();
});
