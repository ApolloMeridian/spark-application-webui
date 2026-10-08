import { validateDAG, type WorkflowNode } from "./workflows";

export const NODE_WIDTH = 210;
export const NODE_HEIGHT = 74;
export type PortSide = "top" | "right" | "bottom" | "left";
export type Point = { x: number; y: number };
const normals: Record<PortSide, Point> = {
  top: { x: 0, y: -1 },
  right: { x: 1, y: 0 },
  bottom: { x: 0, y: 1 },
  left: { x: -1, y: 0 },
};
export function portPoint(node: WorkflowNode, side: PortSide): Point {
  const center = { x: node.x + NODE_WIDTH / 2, y: node.y + NODE_HEIGHT / 2 };
  const normal = normals[side];
  return {
    x: center.x + (normal.x * NODE_WIDTH) / 2,
    y: center.y + (normal.y * NODE_HEIGHT) / 2,
  };
}
export function curvePath(
  start: Point,
  end: Point,
  sourceSide: PortSide,
  targetSide?: PortSide,
) {
  const length = Math.max(
    40,
    Math.min(180, Math.hypot(end.x - start.x, end.y - start.y) * 0.45),
  );
  const out = normals[sourceSide];
  const incoming = targetSide ? normals[targetSide] : { x: -out.x, y: -out.y };
  return `M${start.x},${start.y} C${start.x + out.x * length},${start.y + out.y * length} ${end.x + incoming.x * length},${end.y + incoming.y * length} ${end.x},${end.y}`;
}
// Choose facing borders from relative centers; vertical and reverse layouts
// no longer route through the interiors of the cards.
export function edgeGeometry(source: WorkflowNode, target: WorkflowNode) {
  const dx = target.x - source.x,
    dy = target.y - source.y;
  const horizontal = Math.abs(dx) / NODE_WIDTH >= Math.abs(dy) / NODE_HEIGHT;
  const sourceSide: PortSide = horizontal
    ? dx >= 0
      ? "right"
      : "left"
    : dy >= 0
      ? "bottom"
      : "top";
  const targetSide: PortSide = horizontal
    ? dx >= 0
      ? "left"
      : "right"
    : dy >= 0
      ? "top"
      : "bottom";
  const start = portPoint(source, sourceSide),
    end = portPoint(target, targetSide);
  return {
    sourceSide,
    targetSide,
    start,
    end,
    path: curvePath(start, end, sourceSide, targetSide),
  };
}
export function nodeAt(nodes: WorkflowNode[], point: Point) {
  return [...nodes]
    .reverse()
    .find(
      (n) =>
        point.x >= n.x &&
        point.x <= n.x + NODE_WIDTH &&
        point.y >= n.y &&
        point.y <= n.y + NODE_HEIGHT,
    );
}
export function connectNodes(
  nodes: WorkflowNode[],
  source: string,
  target: string,
): {
  nodes: WorkflowNode[];
  error?: "self" | "duplicate" | "cycle" | "missing";
} {
  if (
    !nodes.some((n) => n.id === source) ||
    !nodes.some((n) => n.id === target)
  )
    return { nodes, error: "missing" };
  if (source === target) return { nodes, error: "self" };
  if (nodes.find((n) => n.id === target)!.dependsOn.includes(source))
    return { nodes, error: "duplicate" };
  const next = nodes.map((n) =>
    n.id === target ? { ...n, dependsOn: [...n.dependsOn, source] } : n,
  );
  if (validateDAG(next)) return { nodes, error: "cycle" };
  return { nodes: next };
}
