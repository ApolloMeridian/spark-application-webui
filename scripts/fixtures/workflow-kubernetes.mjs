// Kubernetes HTTP test double for the container smoke test; no real Spark jobs.
import http from "node:http";
const applications = new Map();
let submissions = 0;
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
      for (const app of applications.values())
        app.status.applicationState.state = body.state;
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
        response.write(
          JSON.stringify({
            type: "BOOKMARK",
            object: { metadata: { resourceVersion: "1" } },
          }) + "\n",
        );
        const timer = setInterval(() => response.write("\n"), 20000);
        response.on("close", () => clearInterval(timer));
        return;
      }
      if (request.method === "GET") {
        send(
          name
            ? applications.get(name) || { message: "not found" }
            : { items: [...applications.values()] },
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
        }
        send(app, 201);
        return;
      }
      if (request.method === "PATCH") {
        const app = applications.get(name);
        if (app) app.spec.restartPolicy = { type: "Never" };
        send(app || {}, app ? 200 : 404);
        return;
      }
      if (request.method === "DELETE") {
        applications.delete(name);
        send({});
        return;
      }
    }
    if (url.pathname.endsWith("/pods")) {
      send({
        items: [...applications.values()].map((app) => ({
          metadata: {
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
      if (app) app.status.applicationState.state = "FAILED";
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
