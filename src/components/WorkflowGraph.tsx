import { useId, useRef, useState } from "react";
import { Alert, Button, Space, Typography } from "antd";
import type { WorkflowNode } from "../workflows";
import {
  connectNodes,
  curvePath,
  edgeGeometry,
  nodeAt,
  portPoint,
  type Point,
  type PortSide,
} from "../workflowGraph";

type Connection = {
  source: string;
  side: PortSide;
  point: Point;
  target?: string;
};
export function WorkflowGraph({
  nodes,
  onChange,
  selected,
  onSelect,
  en,
}: {
  nodes: WorkflowNode[];
  onChange?: (nodes: WorkflowNode[]) => void;
  selected?: string;
  onSelect: (id: string) => void;
  en: boolean;
}) {
  const canvas = useRef<HTMLDivElement>(null);
  const drag = useRef<{
    id: string;
    x: number;
    y: number;
    left: number;
    top: number;
  }>();
  const link = useRef<Connection>();
  const [preview, setPreview] = useState<Connection>();
  const [keyboardSource, setKeyboardSource] = useState<string>();
  const [error, setError] = useState("");
  const [edge, setEdge] = useState<{ source: string; target: string }>();
  const marker = `workflow-arrow-${useId().replace(/:/g, "")}`;
  const width = Math.max(760, ...nodes.map((n) => n.x + 245));
  const height = Math.max(270, ...nodes.map((n) => n.y + 120));
  const errorText = {
    self: en ? "A task cannot depend on itself." : "任务不能连接到自身。",
    duplicate: en ? "This dependency already exists." : "该依赖已存在。",
    cycle: en
      ? "This connection would create a dependency cycle."
      : "该连接会产生循环依赖。",
    missing: en ? "The task no longer exists." : "任务已不存在。",
  };
  const connect = (source: string, target: string) => {
    if (!onChange) return;
    const next = connectNodes(nodes, source, target);
    if (next.error) setError(errorText[next.error]);
    else {
      setError("");
      onChange(next.nodes);
      onSelect(target);
    }
    setKeyboardSource(undefined);
  };
  const localPoint = (clientX: number, clientY: number) => {
    const box = canvas.current!.getBoundingClientRect();
    return { x: clientX - box.left, y: clientY - box.top };
  };
  const clearGesture = () => {
    drag.current = undefined;
    link.current = undefined;
    setPreview(undefined);
  };
  const source = preview && nodes.find((n) => n.id === preview.source);
  const target = preview && nodes.find((n) => n.id === preview.target);
  const candidate = preview?.target
    ? connectNodes(nodes, preview.source, preview.target)
    : undefined;
  return (
    <>
      <div className="workflow-graph-toolbar">
        <Typography.Text type="secondary">
          {onChange
            ? en
              ? "Drag a node to arrange. Drag any border port onto another task to connect; all upstream tasks must succeed."
              : "拖动节点调整布局；从边缘连接点拖到另一任务建立依赖，所有上游成功后才运行。"
            : en
              ? "All upstream tasks must succeed before a task runs."
              : "所有上游任务成功后才运行。"}
        </Typography.Text>
        {keyboardSource && (
          <Space>
            <Typography.Text>
              {en ? "Select a downstream task" : "请选择下游任务"}
            </Typography.Text>
            <Button size="small" onClick={() => setKeyboardSource(undefined)}>
              {en ? "Cancel connection" : "取消连线"}
            </Button>
          </Space>
        )}
        {edge && onChange && (
          <Button
            size="small"
            danger
            onClick={() => {
              onChange(
                nodes.map((n) =>
                  n.id === edge.target
                    ? {
                        ...n,
                        dependsOn: n.dependsOn.filter(
                          (id) => id !== edge.source,
                        ),
                      }
                    : n,
                ),
              );
              setEdge(undefined);
            }}
          >
            {en ? "Remove selected connection" : "删除选中连线"}
          </Button>
        )}
      </div>
      {error && (
        <Alert
          type="warning"
          showIcon
          closable
          message={error}
          onClose={() => setError("")}
          style={{ marginBottom: 10 }}
        />
      )}
      <div className="workflow-canvas-scroll">
        <div
          ref={canvas}
          className="workflow-canvas"
          style={{ width, height }}
          onKeyDown={(e) => {
            if (e.key === "Escape") {
              clearGesture();
              setKeyboardSource(undefined);
              setEdge(undefined);
            }
          }}
          onPointerMove={(e) => {
            if (link.current) {
              const point = localPoint(e.clientX, e.clientY);
              const next = {
                ...link.current,
                point,
                target: nodeAt(nodes, point)?.id,
              };
              link.current = next;
              setPreview(next);
              return;
            }
            const current = drag.current;
            if (current && onChange)
              onChange(
                nodes.map((n) =>
                  n.id === current.id
                    ? {
                        ...n,
                        x: Math.max(20, current.left + e.clientX - current.x),
                        y: Math.max(20, current.top + e.clientY - current.y),
                      }
                    : n,
                ),
              );
          }}
          onPointerUp={(e) => {
            const current = link.current;
            if (current) {
              const drop = nodeAt(nodes, localPoint(e.clientX, e.clientY));
              if (drop && drop.id !== current.source)
                connect(current.source, drop.id);
            }
            clearGesture();
          }}
          onPointerCancel={clearGesture}
          onLostPointerCapture={clearGesture}
        >
          <svg width={width} height={height} className="workflow-edges">
            <defs>
              <marker
                id={marker}
                markerWidth="9"
                markerHeight="9"
                refX="8"
                refY="4"
                orient="auto"
              >
                <path d="M0,0 L8,4 L0,8" fill="context-stroke" />
              </marker>
            </defs>
            {nodes.flatMap((n) =>
              n.dependsOn.map((dep) => {
                const parent = nodes.find((x) => x.id === dep);
                if (!parent) return null;
                const geometry = edgeGeometry(parent, n);
                const active = edge?.source === dep && edge.target === n.id;
                return (
                  <g
                    key={`${dep}-${n.id}`}
                    data-source={dep}
                    data-target={n.id}
                  >
                    <path
                      d={geometry.path}
                      fill="none"
                      stroke={active ? "#2868f0" : "#7a96ba"}
                      strokeWidth={active ? 3 : 2}
                      markerEnd={`url(#${marker})`}
                    />
                    {onChange && (
                      <path
                        role="button"
                        tabIndex={0}
                        aria-label={`${en ? "Select connection" : "选择连线"} ${parent.name} → ${n.name}`}
                        className="workflow-edge-hit"
                        d={geometry.path}
                        fill="none"
                        stroke="transparent"
                        strokeWidth={14}
                        onPointerDown={(e) => e.stopPropagation()}
                        onClick={() => setEdge({ source: dep, target: n.id })}
                        onKeyDown={(e) => {
                          if (e.key === "Enter" || e.key === " ") {
                            e.preventDefault();
                            setEdge({ source: dep, target: n.id });
                          }
                        }}
                      />
                    )}
                  </g>
                );
              }),
            )}
            {preview && source && (
              <path
                className="workflow-edge-preview"
                d={
                  target
                    ? edgeGeometry(source, target).path
                    : curvePath(
                        portPoint(source, preview.side),
                        preview.point,
                        preview.side,
                      )
                }
                fill="none"
                stroke={candidate?.error ? "#e44c55" : "#2868f0"}
                strokeWidth={2}
                strokeDasharray="6 4"
                markerEnd={`url(#${marker})`}
              />
            )}
          </svg>
          {nodes.map((n, i) => (
            <div
              key={n.id}
              data-workflow-node={n.id}
              className={`workflow-node ${selected === n.id ? "selected" : ""} ${preview?.target === n.id ? (candidate?.error ? "connection-invalid" : "connection-target") : ""}`}
              style={{ left: n.x, top: n.y }}
            >
              <button
                type="button"
                className="workflow-node-body"
                aria-label={`${en ? "Select task" : "选择任务"} ${n.name}`}
                onPointerDown={(e) => {
                  if (!onChange || keyboardSource || e.button !== 0) return;
                  drag.current = {
                    id: n.id,
                    x: e.clientX,
                    y: e.clientY,
                    left: n.x,
                    top: n.y,
                  };
                  e.currentTarget.setPointerCapture(e.pointerId);
                }}
                onClick={() => {
                  if (keyboardSource) connect(keyboardSource, n.id);
                  else {
                    setEdge(undefined);
                    onSelect(n.id);
                  }
                }}
              >
                <span className="workflow-node-number">
                  {String(i + 1).padStart(2, "0")}
                </span>
                <span>
                  <strong>{n.name}</strong>
                  <small>
                    {n.namespace} ·{" "}
                    {n.templateId
                      ? `Template v${n.templateVersion}`
                      : "SparkApplication"}
                  </small>
                </span>
              </button>
              {onChange &&
                (["top", "right", "bottom", "left"] as PortSide[]).map(
                  (side) => (
                    <button
                      key={side}
                      type="button"
                      className={`workflow-port workflow-port-${side}`}
                      title={
                        en
                          ? "Drag to connect; Enter then select a downstream task"
                          : "拖动建立连接；也可回车后选择下游任务"
                      }
                      aria-label={`${en ? "Connect from" : "从此任务连线"} ${n.name} (${side})`}
                      onPointerDown={(e) => {
                        if (e.button !== 0) return;
                        e.stopPropagation();
                        e.preventDefault();
                        setError("");
                        setEdge(undefined);
                        setKeyboardSource(undefined);
                        const next = {
                          source: n.id,
                          side,
                          point: portPoint(n, side),
                        };
                        link.current = next;
                        setPreview(next);
                        canvas.current!.setPointerCapture(e.pointerId);
                      }}
                      onClick={(e) => {
                        if (e.detail === 0) {
                          setKeyboardSource(n.id);
                          onSelect(n.id);
                          setError("");
                        }
                      }}
                    />
                  ),
                )}
            </div>
          ))}
        </div>
      </div>
    </>
  );
}
