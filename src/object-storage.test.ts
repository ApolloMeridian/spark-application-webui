import { describe, it, expect, beforeEach } from "vitest";
import {
  storageAllowed,
  storageService,
  validStorageKey,
  TEXT_LIMIT,
  formatStorageSize,
  type StorageConnection,
} from "./objectStorage";
import type { Session } from "./auth";
const admin: Session = {
  id: "mock-admin",
  username: "admin",
  displayName: "Admin",
  email: "",
  role: "admin",
  namespaces: [],
  authSource: "mock",
  disabled: false,
  createdAt: "",
  updatedAt: "",
};
const c: StorageConnection = {
  id: "demo",
  name: "S3",
  provider: "s3",
  editable: true,
  roots: [
    {
      bucket: "bucket",
      prefix: "team/a/",
      actions: ["list", "read", "download", "edit"],
    },
  ],
};
describe("storage permissions and mock operations", () => {
  beforeEach(() => localStorage.clear());
  it("matches exact directory boundaries and rejects ambiguous keys", () => {
    expect(storageAllowed(admin, c, "bucket", "team/a/file.txt", "read")).toBe(
      true,
    );
    expect(storageAllowed(admin, c, "bucket", "team/ab/file.txt", "read")).toBe(
      false,
    );
    expect(storageAllowed(admin, c, "bucket", "team/a/../b.txt", "read")).toBe(
      false,
    );
    expect(validStorageKey("team/a/百分号%20 空格.txt")).toBe(true);
    expect(validStorageKey("a\\b")).toBe(false);
    expect(
      storageAllowed(
        { ...admin, role: "viewer" },
        c,
        "bucket",
        "team/a/file.txt",
        "edit",
      ),
    ).toBe(true);
    expect(
      storageAllowed(admin, undefined, "bucket", "team/a/file.txt", "read"),
    ).toBe(false);
  });
  it("bounds large read-only previews and rejects stale or oversized saves", async () => {
    const service = storageService(admin);
    const t = {
      connection: "demo",
      bucket: "spark-artifacts",
      key: "team/analytics/config.yaml",
    };
    const before = (await service.operations()).length;
    const first = await service.text(t);
    expect((await service.operations()).length).toBe(before);
    await service.save(t, "updated", first.object.version);
    await expect(
      service.save(t, "stale", first.object.version),
    ).rejects.toThrow("version_conflict");
    const large = await service.text({ ...t, key: "team/analytics/large.log" });
    expect(large.truncated).toBe(true);
    expect(large.readOnly).toBe(true);
    expect(large.previewBytes).toBeLessThanOrEqual(TEXT_LIMIT);
    await expect(
      service.save(
        { ...t, key: "team/analytics/large.log" },
        "small",
        large.object.version,
      ),
    ).rejects.toThrow("text_too_large");
    const updated = await service.text(t);
    await expect(
      service.save(t, "x".repeat(TEXT_LIMIT + 1), updated.object.version),
    ).rejects.toThrow("text_too_large");
    expect((await service.operations()).length).toBe(before + 1);
  });
  it("defaults to read-only, but honors admin grants for viewer writes", async () => {
    const service = storageService({ ...admin, role: "viewer" });
    await expect(
      service.delete(
        {
          connection: "demo",
          bucket: "spark-artifacts",
          key: "team/analytics/config.yaml",
        },
        "1",
      ),
    ).rejects.toThrow("access_denied");
    const adminService = storageService(admin);
    await adminService.saveGrant(
      {
        userId: admin.id,
        connection: "demo",
        bucket: "spark-artifacts",
        prefix: "team/analytics/",
        actions: ["list", "read", "upload", "edit"],
      },
      false,
    );
    const viewerService = storageService({ ...admin, role: "viewer" });
    const target = {
      connection: "demo",
      bucket: "spark-artifacts",
      key: "team/analytics/new.json",
    };
    await viewerService.create(target, "{}");
    await expect(viewerService.create(target, "[]")).rejects.toThrow(
      "version_conflict",
    );
    await viewerService.save(target, "[]", "1");
    expect((await viewerService.text(target)).content).toBe("[]");
    await expect(viewerService.saveGrant({}, false)).rejects.toThrow(
      "access_denied",
    );
  });
  it("uses adaptive decimal byte units", () => {
    expect([0, 999, 1000, 1000000, 1e9, 1e12].map(formatStorageSize)).toEqual([
      "0 B",
      "999 B",
      "1.00 KB",
      "1.00 MB",
      "1.00 GB",
      "1.00 TB",
    ]);
  });
});
