import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createServer } from "node:http";
import { chromium } from "playwright-core";

const csp = JSON.parse(
  '"' +
    readFileSync("backend/internal/api/server.go", "utf8").match(
      /Header\.Set\("Content-Security-Policy", "([^"\n]+)"\)/,
    )[1] +
    '"',
);
let cookiesSeen = false;
const server = createServer((req, res) => {
  if (req.url === "/") {
    res.setHeader(
      "Set-Cookie",
      "spark-console-session=fixture; HttpOnly; SameSite=Lax; Path=/",
    );
    res.setHeader("Content-Type", "text/html");
    return res.end(
      `<script>window.result=null;addEventListener('message',e=>{if(e.source===document.querySelector('iframe').contentWindow&&e.origin==='null')window.result=e.data})</script><iframe sandbox="allow-scripts" src="/ui/t/read-only-fixture/"></iframe>`,
    );
  }
  if (req.url === "/ui/t/read-only-fixture/") {
    res.setHeader("Content-Type", "text/html");
    res.setHeader("Content-Security-Policy", csp);
    return res.end(
      `<html><head></head><body><script>(async function(){const out={};try{parent.document.body.innerHTML='attacked';out.parentBlocked=false}catch{out.parentBlocked=true}try{localStorage.setItem('attack','1');out.storageBlocked=false}catch{out.storageBlocked=true}try{out.json=await(await fetch('json')).json()}catch(e){out.jsonError=String(e)}try{await(await fetch('/api/private')).text();out.privateBlocked=false}catch{out.privateBlocked=true}out.writeStatus=(await fetch('/api/change',{method:'POST',headers:{'Content-Type':'application/json'},body:'{}'}).catch(()=>({status:0}))).status;parent.postMessage(out,'*')})();</script></body></html>`,
    );
  }
  if (req.url === "/ui/t/read-only-fixture/json") {
    cookiesSeen = Boolean(
      req.headers.cookie?.includes("spark-console-session=fixture"),
    );
    res.setHeader("Access-Control-Allow-Origin", "null");
    res.setHeader("Access-Control-Allow-Credentials", "true");
    res.setHeader("Content-Type", "application/json");
    return res.end(JSON.stringify({ authorized: true }));
  }
  if (req.method === "OPTIONS") {
    res.writeHead(403);
    return res.end();
  }
  res.setHeader("Content-Type", "application/json");
  res.end('{"private":true}');
});
await new Promise((r) => server.listen(0, "127.0.0.1", r));
const browser = await chromium.launch({
  executablePath:
    "C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe",
  headless: true,
});
try {
  const page = await browser.newPage();
  await page.goto(`http://127.0.0.1:${server.address().port}/`);
  await page.waitForFunction(() => window.result !== null);
  const result = await page.evaluate(() => window.result);
  assert.equal(result.parentBlocked, true);
  assert.equal(result.storageBlocked, true);
  assert.equal(result.privateBlocked, true);
  assert.equal(result.json?.authorized, true, JSON.stringify(result));
  assert.equal(
    cookiesSeen,
    false,
    "opaque UI must not depend on the console session cookie",
  );
  assert.equal(result.writeStatus, 0);
  console.log(
    "PASS: opaque Spark UI cannot access parent/storage/private API; scoped credentialed JSON works and writes are blocked.",
  );
} finally {
  await browser.close();
  await new Promise((r) => server.close(r));
}
