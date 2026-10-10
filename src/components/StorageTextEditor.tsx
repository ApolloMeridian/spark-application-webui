import { useEffect, useRef, useState } from "react";
import { Button, Space, Tag } from "antd";
import * as monaco from "monaco-editor";
import EditorWorker from "monaco-editor/editor/editor.worker.js?worker";
import JSONWorker from "monaco-editor/language/json/json.worker.js?worker";
import TSWorker from "monaco-editor/language/typescript/ts.worker.js?worker";
import HTMLWorker from "monaco-editor/language/html/html.worker.js?worker";
import { storageDiagnostics, storageLanguage } from "./storageText";

// All editor assets/workers ship with the frontend image; no CDN or remote LSP.
(
  self as typeof self & { MonacoEnvironment: monaco.Environment }
).MonacoEnvironment = {
  getWorker: (_id, label) => {
    if (label === "json") return new JSONWorker();
    if (label === "typescript" || label === "javascript") return new TSWorker();
    if (["html", "handlebars", "razor"].includes(label))
      return new HTMLWorker();
    return new EditorWorker();
  },
};
monaco.json.jsonDefaults.setDiagnosticsOptions({
  validate: false,
  enableSchemaRequest: false,
});
monaco.typescript.javascriptDefaults.setDiagnosticsOptions({
  noSemanticValidation: true,
  noSyntaxValidation: false,
});
monaco.typescript.typescriptDefaults.setDiagnosticsOptions({
  noSemanticValidation: true,
  noSyntaxValidation: false,
});

export default function StorageTextEditor({
  filename,
  value,
  readOnly,
  truncated = false,
  onChange,
  en,
}: {
  filename: string;
  value: string;
  readOnly: boolean;
  truncated?: boolean;
  onChange: (value: string) => void;
  en: boolean;
}) {
  const host = useRef<HTMLDivElement>(null);
  const instance = useRef<monaco.editor.IStandaloneCodeEditor>();
  const callback = useRef(onChange);
  callback.current = onChange;
  const [problems, setProblems] = useState<monaco.editor.IMarker[]>([]);
  const language = storageLanguage(filename);
  useEffect(() => {
    if (!host.current) return;
    const model = monaco.editor.createModel(value, language);
    const editor = monaco.editor.create(host.current, {
      model,
      automaticLayout: true,
      readOnly,
      ariaLabel: en ? "File content" : "文件内容",
      theme: "vs",
      fontSize: 13,
      lineNumbers: "on",
      minimap: { enabled: false },
      wordWrap: "on",
      scrollBeyondLastLine: false,
      tabSize: 2,
      autoClosingOvertype: "always",
      multiCursorModifier: "alt",
      find: { addExtraSpaceOnTop: false },
      renderValidationDecorations: "on",
      padding: { top: 12 },
      // Use the established input path for browser/IME compatibility.
      editContext: false,
    });
    instance.current = editor;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const validate = () => {
      if (model.isDisposed()) return;
      const markers = truncated
        ? []
        : storageDiagnostics(filename, model.getValue()).map((d) => ({
            severity: monaco.MarkerSeverity.Error,
            message: d.message,
            startLineNumber: d.line,
            endLineNumber: d.line,
            startColumn: d.column,
            endColumn: d.column + 1,
          }));
      monaco.editor.setModelMarkers(model, "storage-format", markers);
    };
    validate();
    const changed = editor.onDidChangeModelContent(() => {
      callback.current(model.getValue());
      clearTimeout(timer);
      timer = setTimeout(validate, 350);
    });
    const markers = monaco.editor.onDidChangeMarkers((uris) => {
      if (uris.some((uri) => uri.toString() === model.uri.toString()))
        setProblems(monaco.editor.getModelMarkers({ resource: model.uri }));
    });
    setProblems(monaco.editor.getModelMarkers({ resource: model.uri }));
    return () => {
      clearTimeout(timer);
      changed.dispose();
      markers.dispose();
      editor.dispose();
      model.dispose();
      instance.current = undefined;
    };
    // A file has one model; ordinary edits must preserve undo and multi-cursors.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [filename, language, truncated]);
  useEffect(() => {
    instance.current?.updateOptions({
      readOnly,
      ariaLabel: en ? "File content" : "文件内容",
    });
  }, [readOnly, en]);
  // Keep the model as the source of truth while this file is open, preserving
  // undo and auto-close state. A different filename creates a fresh model.
  const action = (id: string) => {
    const editor = instance.current;
    editor?.focus();
    void editor?.getAction(id)?.run();
  };
  return (
    <div>
      <Space wrap style={{ marginBottom: 8 }}>
        <Tag>{language}</Tag>
        {readOnly && <Tag color="orange">{en ? "Read-only" : "只读"}</Tag>}
        <Button size="small" onClick={() => action("actions.find")}>
          {en ? "Find · Ctrl+F" : "搜索 · Ctrl+F"}
        </Button>
        <Button
          size="small"
          disabled={readOnly}
          onClick={() => action("editor.action.startFindReplaceAction")}
        >
          {en ? "Replace · Ctrl+H" : "替换 · Ctrl+H"}
        </Button>
      </Space>
      <div
        ref={host}
        data-testid="storage-code-editor"
        style={{
          height: "max(360px, calc(100vh - 320px))",
          border: "1px solid #e6e9ef",
          borderRadius: 6,
          overflow: "hidden",
        }}
      />
      {!truncated && problems.length > 0 && (
        <div
          role="status"
          data-testid="storage-format-problems"
          style={{ maxHeight: 100, overflow: "auto", marginTop: 8 }}
        >
          {problems.slice(0, 20).map((p, index) => (
            <div key={index}>
              <Button
                type="link"
                size="small"
                danger
                onClick={() => {
                  instance.current?.setPosition({
                    lineNumber: p.startLineNumber,
                    column: p.startColumn,
                  });
                  instance.current?.revealLineInCenter(p.startLineNumber);
                  instance.current?.focus();
                }}
              >
                {p.startLineNumber}:{p.startColumn} {p.message}
              </Button>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
