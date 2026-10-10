import { describe, expect, it } from "vitest";
import { storageDiagnostics, storageLanguage } from "./components/storageText";
import { isStorageText, textExtensions } from "./objectStorage";
describe("storage editor language and structural diagnostics", () => {
  it("recognizes all supported text extensions", () => {
    for (const ext of textExtensions)
      expect(isStorageText(`file.${ext.toUpperCase()}`)).toBe(true);
    expect(isStorageText("file.exe")).toBe(false);
    expect(storageLanguage("x.hql")).toBe("sql");
    expect(storageLanguage("x.cfg")).toBe("ini");
  });
  it("reports JSON, YAML and XML errors, with locations", () => {
    for (const [key, valid, invalid] of [
      ["a.json", "{}", '{"x":}'],
      ["a.yaml", "x: 1", "x: ["],
      ["a.xml", "<root/>", "<root>"],
    ]) {
      expect(storageDiagnostics(key, valid)).toEqual([]);
      const errors = storageDiagnostics(key, invalid);
      expect(errors.length).toBeGreaterThan(0);
      expect(errors[0].line).toBeGreaterThan(0);
      expect(errors[0].column).toBeGreaterThan(0);
    }
  });
  it("never executes script content", () => {
    expect(
      storageDiagnostics("x.js", "throw new Error('must not execute')"),
    ).toEqual([]);
  });
});
