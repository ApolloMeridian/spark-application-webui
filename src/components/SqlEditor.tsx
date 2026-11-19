import { useEffect, useRef } from "react";
import * as monaco from "monaco-editor";
import EditorWorker from "monaco-editor/editor/editor.worker.js?worker";
import { SQL_RELATION_DRAG_TYPE } from "./SqlCatalog";

const browser = self as typeof self & { MonacoEnvironment?: monaco.Environment };
browser.MonacoEnvironment ??= { getWorker: () => new EditorWorker() };

export default function SqlEditor({ value, onChange, onRun }: {
  value: string;
  onChange: (sql: string) => void;
  onRun: () => void;
}) {
  const host = useRef<HTMLDivElement>(null);
  const instance = useRef<monaco.editor.IStandaloneCodeEditor>();
  const current = useRef({ value, onChange, onRun });
  current.current = { value, onChange, onRun };

  useEffect(() => {
    if (!host.current) return;
    const model = monaco.editor.createModel(current.current.value, "sql");
    const editor = monaco.editor.create(host.current, {
      model,
      ariaLabel: "SQL",
      automaticLayout: true,
      minimap: { enabled: false },
      scrollBeyondLastLine: false,
      fontSize: 13,
      tabSize: 2,
      padding: { top: 8 },
      editContext: false,
    });
    instance.current = editor;
    const listener = model.onDidChangeContent(() => current.current.onChange(model.getValue()));
    editor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.Enter, () => current.current.onRun());
    return () => {
      instance.current = undefined;
      listener.dispose();
      editor.dispose();
      model.dispose();
    };
  }, []);

  useEffect(() => {
    const editor = instance.current;
    if (editor && editor.getValue() !== value) editor.setValue(value);
  }, [value]);

  return <div className="sql-editor" ref={host}
    onDragOver={(event) => {
      if (!event.dataTransfer.types.includes(SQL_RELATION_DRAG_TYPE)) return;
      event.preventDefault();
      event.dataTransfer.dropEffect = "copy";
      const editor = instance.current;
      const position = editor?.getTargetAtClientPoint(event.clientX, event.clientY)?.position;
      if (position) {
        editor?.focus();
        editor?.setPosition(position);
      }
    }}
    onDrop={(event) => {
      const text = event.dataTransfer.getData(SQL_RELATION_DRAG_TYPE);
      if (!text) return;
      event.preventDefault();
      const editor = instance.current;
      const position = editor?.getTargetAtClientPoint(event.clientX, event.clientY)?.position ?? editor?.getPosition();
      if (!editor || !position) return;
      editor.pushUndoStop();
      editor.executeEdits("catalog", [{ range: monaco.Range.fromPositions(position), text }]);
      editor.pushUndoStop();
      editor.focus();
    }} />;
}
