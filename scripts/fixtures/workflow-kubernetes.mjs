// Kubernetes HTTP test double for the container smoke test; no real Spark jobs.
import http from "node:http";
const applications = new Map();
let submissions = 0;
let revision = 1;
const watchers = new Set();
function changed(type, app) {
  app.metadata.resourceVersion = String(++revision);
  for (const stream of watchers)
    stream.write(JSON.stringify({ type, object: app }) + "\n");
}
http
  .createServer(async (request, response) => {
    const url = new URL(request.url, "http://fixture");
    const send = (value, code = 200) => {
      response.writeHead(code, { "Content-Type": "application/json" });
      response.end(JSON.stringify(value));
    };
    let text = "";
    for await (const chunk of request) text += chunk;
    const body = text ? JSON.parse(text) : {};
    if (url.pathname === "/test/state") {
      for (const app of applications.values()) {
        app.status.applicationState.state = body.state;
        changed("MODIFIED", app);
      }
      send({ submissions, applications: [...applications.values()] });
      return;
    }
    if (url.pathname === "/test/stats") {
      send({ submissions, applications: [...applications.values()] });
      return;
    }
    if (url.pathname.includes("/sparkapplications")) {
      const name = url.pathname.split("/sparkapplications/")[1];
      if (url.searchParams.get("watch") === "true") {
        response.writeHead(200, { "Content-Type": "application/json" });
        watchers.add(response);
        response.write(
          JSON.stringify({
            type: "BOOKMARK",
            object: { metadata: { resourceVersion: "1" } },
          }) + "\n",
        );
        const timer = setInterval(() => response.write("\n"), 20000);
        response.on("close", () => {
          clearInterval(timer);
          watchers.delete(response);
        });
        return;
      }
      if (request.method === "GET") {
        send(
          name
            ? applications.get(name) || { message: "not found" }
            : {
                metadata: { resourceVersion: String(revision) },
                items: [...applications.values()],
              },
          name && !applications.has(name) ? 404 : 200,
        );
        return;
      }
      if (request.method === "POST") {
        const app = structuredClone(body);
        app.metadata.creationTimestamp = new Date().toISOString();
        app.metadata.uid = crypto.randomUUID();
        app.status = {
          applicationState: { state: "RUNNING" },
          driverInfo: { podName: `${app.metadata.name}-driver` },
          lastSubmissionAttemptTime: new Date().toISOString(),
        };
        if (!url.searchParams.has("dryRun")) {
          if (applications.has(app.metadata.name)) {
            send({ message: "already exists" }, 409);
            return;
          }
          applications.set(app.metadata.name, app);
          submissions++;
          changed("ADDED", app);
        }
        send(app, 201);
        return;
      }
      if (request.method === "PATCH") {
        const app = applications.get(name);
        if (app && Array.isArray(body)) {
          for (const op of body) {
            if (
              op.op === "test" &&
              op.path.startsWith("/metadata/") &&
              app.metadata[op.path.split("/")[2]] !== op.value
            ) {
              send({ message: "precondition failed" }, 409);
              return;
            }
            if (op.path === "/metadata/annotations")
              app.metadata.annotations = op.value;
            if (op.path === "/spec/restartPolicy")
              app.spec.restartPolicy = op.value;
          }
        } else if (app) app.spec.restartPolicy = { type: "Never" };
        if (app) changed("MODIFIED", app);
        send(app || {}, app ? 200 : 404);
        return;
      }
      if (request.method === "DELETE") {
        const app = applications.get(name);
        if (
          app &&
          body.preconditions?.uid &&
          body.preconditions.uid !== app.metadata.uid
        ) {
          send({ message: "precondition failed" }, 409);
          return;
        }
        applications.delete(name);
        if (app) changed("DELETED", app);
        send({});
        return;
      }
    }
    if (url.pathname.endsWith("/pods")) {
      send({
        items: [...applications.values()].map((app) => ({
          metadata: {
            uid: `${app.metadata.uid}-driver`,
            name: `${app.metadata.name}-driver`,
            namespace: "spark",
            labels: {
              "spark-role": "driver",
              "sparkoperator.k8s.io/app-name": app.metadata.name,
            },
          },
          spec: {},
          status: { phase: "Running" },
        })),
      });
      return;
    }
    if (url.pathname.includes("/pods/") && request.method === "PATCH") {
      const podName = url.pathname.split("/pods/")[1];
      const app = applications.get(podName.replace(/-driver$/, ""));
      if (app) {
        app.status.applicationState.state = "FAILED";
        changed("MODIFIED", app);
      }
      send({});
      return;
    }
    if (url.pathname.endsWith("/log")) {
      if (url.pathname.includes("logs-regression")) {
        const tail = Number(url.searchParams.get("tailLines") || 12050);
        for (let line = Math.max(1, 12051 - tail); line <= 12050; line++)
          response.write(
            `2026-10-20T00:00:00Z INFO driver-line-${String(line).padStart(5, "0")}\n`,
          );
        response.end();
        return;
      }
      response.end("INFO test fixture driver log\n");
      return;
    }
    if (url.pathname.startsWith("/api/v1/query")) {
      send({ status: "success", data: { resultType: "vector", result: [] } });
      return;
    }
    send({ items: [] });
  })
  .listen(8080, "0.0.0.0");
