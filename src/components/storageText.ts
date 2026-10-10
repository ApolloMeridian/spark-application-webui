import { LineCounter, parseDocument } from "yaml";

const languages: Record<string, string> = {
  md: "markdown",
  txt: "plaintext",
  log: "plaintext",
  sh: "shell",
  bat: "bat",
  conf: "ini",
  cfg: "ini",
  ini: "ini",
  properties: "ini",
  py: "python",
  java: "java",
  xml: "xml",
  svg: "xml",
  sql: "sql",
  hql: "sql",
  json: "json",
  yml: "yaml",
  yaml: "yaml",
  js: "javascript",
  ts: "typescript",
  html: "html",
  toml: "ini",
  csv: "plaintext",
};
export function storageLanguage(key: string) {
  return languages[key.split(".").pop()?.toLowerCase() || ""] || "plaintext";
}
export interface TextDiagnostic {
  message: string;
  line: number;
  column: number;
}
// Pure data parsing only: never execute source code or resolve remote schemas.
export function storageDiagnostics(
  key: string,
  content: string,
): TextDiagnostic[] {
  const language = storageLanguage(key);
  if (language === "yaml") {
    const counter = new LineCounter();
    const doc = parseDocument(content, { lineCounter: counter });
    return doc.errors.map((error) => {
      const pos = counter.linePos(error.pos[0]);
      return { message: error.message, line: pos.line, column: pos.col };
    });
  }
  if (language === "json") {
    try {
      JSON.parse(content);
    } catch (error) {
      const message = (error as Error).message;
      const explicit = /line (\d+) column (\d+)/i.exec(message);
      const offset = Number(/position (\d+)/i.exec(message)?.[1] || 0);
      const lines = content.slice(0, offset).split("\n");
      return [
        {
          message,
          line: explicit ? Number(explicit[1]) : lines.length,
          column: explicit ? Number(explicit[2]) : lines.at(-1)!.length + 1,
        },
      ];
    }
  }
  if (language === "xml") {
    const doc = new DOMParser().parseFromString(content, "application/xml");
    const error = doc.querySelector("parsererror");
    if (error) {
      const message = error.textContent || "Invalid XML";
      const pos = /line (\d+).*column (\d+)/i.exec(message);
      return [
        { message, line: Number(pos?.[1] || 1), column: Number(pos?.[2] || 1) },
      ];
    }
  }
  return [];
}
