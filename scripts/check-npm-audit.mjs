import { spawnSync } from "node:child_process";
import { readFileSync } from "node:fs";

const run = spawnSync(process.execPath, [process.env.npm_execpath, "audit", "--json"], {
  encoding: "utf8",
});
if (run.error || run.signal) throw new Error("npm audit did not finish");
const report = JSON.parse(run.stdout);
if (report.error || !report.vulnerabilities || !report.metadata) {
  throw new Error("npm audit did not return a vulnerability report");
}

const packages = JSON.parse(readFileSync("package-lock.json", "utf8")).packages;
const blocking = new Set(["high", "critical"]);
// CVE-2026-112366 has no published fix as of this release. Vite/Vitest use
// repository-controlled glob patterns during build/test. The Nginx image only
// ships the built assets. Keep the production audit independent and strict.
const buildOnlyAdvisory = "https://github.com/advisories/GHSA-fc5q-39xc-vjpf";
const blockers = [];
let accepted = false;
for (const item of Object.values(report.vulnerabilities)) {
  if (!blocking.has(item.severity)) continue;
  if (item.nodes.some((node) => packages[node]?.dev !== true)) {
    blockers.push(item.name);
    continue;
  }
  for (const advisory of item.via) {
    if (typeof advisory === "string") continue;
    if (item.name === "picomatch" && advisory.url === buildOnlyAdvisory) accepted = true;
    else blockers.push(advisory.url ?? advisory.title ?? item.name);
  }
}
if (blockers.length) throw new Error("Blocking npm advisories: " + blockers.join(", "));
if (accepted) console.log("Build-only exception:", buildOnlyAdvisory);
console.log("No unexcepted high or critical npm advisories.");
