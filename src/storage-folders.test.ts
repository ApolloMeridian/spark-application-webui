import { describe, it, expect } from "vitest";
import {
  draftFolderPath,
  draftFolderRows,
  materializeDraftFolders,
} from "./storageFolders";

describe("draft object-storage folders", () => {
  it("creates relative nested prefixes without object writes", () => {
    expect(draftFolderPath("team/a/", "new folder/child")).toBe(
      "team/a/new folder/child/",
    );
    expect(draftFolderPath("team/a/", "新目录/")).toBe("team/a/新目录/");
    for (const name of [
      "",
      "/",
      "../x",
      "a/../b",
      "/absolute",
      "a//b",
      "a\\b",
      "a\u0000b",
      "x".repeat(1024),
    ])
      expect(() => draftFolderPath("team/a/", name)).toThrow();
  });
  it("merges only visible direct children and prefers persisted provider rows", () => {
    const drafts = [
      "team/a/new/child/",
      "team/a/new/other/",
      "team/b/private/",
    ];
    expect(
      draftFolderRows(drafts, "team/a/", [], () => true).map((x) => x.key),
    ).toEqual(["team/a/new/"]);
    expect(
      draftFolderRows(drafts, "team/a/new/", [], () => true).map((x) => x.key),
    ).toEqual(["team/a/new/child/", "team/a/new/other/"]);
    const remote = [
      { key: "team/a/new/", directory: true, size: 0, version: "" },
    ];
    expect(draftFolderRows(drafts, "team/a/", remote, () => true)).toEqual([]);
    expect(draftFolderRows(drafts, "team/a/", [], () => false)).toEqual([]);
  });
  it("materializes only ancestors of a successfully written file", () => {
    const drafts = ["team/a/new/", "team/a/new/child/", "team/a/other/"];
    expect(
      materializeDraftFolders(drafts, "team/a/new/child/config.yml"),
    ).toEqual(["team/a/other/"]);
    expect(materializeDraftFolders(drafts, "team/ab/file.txt")).toEqual(drafts);
  });
});
