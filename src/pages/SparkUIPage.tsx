import { Alert, Button, Skeleton } from "antd";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useParams } from "react-router-dom";
import { PageHeader } from "../components/PageHeader";
import { useI18n } from "../i18n";
import { apiUrl } from "../runtimeConfig";
import { sparkService } from "../service";
import type { SparkApplication } from "../types";

export function SparkUIPage() {
  const { namespace = "", name = "" } = useParams();
  const { t } = useI18n();
  const [app, setApp] = useState<SparkApplication>();
  const [error, setError] = useState("");
  const frame = useRef<HTMLIFrameElement>(null);
  const lastView = useRef({ base: "", path: "" });
  const [frameVersion, setFrameVersion] = useState(0);
  const base = apiUrl(
    `/v1/namespaces/${encodeURIComponent(namespace)}/applications/${encodeURIComponent(app?.id ?? name)}/spark-ui/`,
  );
  const source = useMemo(
    () => base + (lastView.current.base === base ? lastView.current.path : ""),
    [base, frameVersion],
  );
  useEffect(() => {
    lastView.current = { base, path: "" };
    const receive = (event: MessageEvent) => {
      if (
        event.source !== frame.current?.contentWindow ||
        event.origin !== "null" ||
        event.data?.type !== "spark-ui-location" ||
        typeof event.data.path !== "string" ||
        event.data.path.length > 4096
      )
        return;
      try {
        const u = new URL(event.data.path, "https://spark-ui.invalid/");
        if (u.origin === "https://spark-ui.invalid")
          lastView.current = {
            base,
            path: u.pathname.slice(1) + u.search + u.hash,
          };
      } catch {
        /* ignore invalid frame messages */
      }
    };
    window.addEventListener("message", receive);
    const timer = window.setInterval(
      () => setFrameVersion((v) => v + 1),
      10 * 60 * 1000,
    );
    return () => {
      window.removeEventListener("message", receive);
      window.clearInterval(timer);
    };
  }, [base]);
  const load = useCallback(async () => {
    setError("");
    try {
      setApp(await sparkService.getApplication(namespace, name));
    } catch (caught) {
      setError(
        caught instanceof Error ? caught.message : "Failed to load application",
      );
    }
  }, [namespace, name]);
  useEffect(() => {
    void load();
  }, [load]);
  if (error)
    return (
      <Alert
        type="error"
        showIcon
        message={error}
        action={<Button onClick={load}>{t("retry")}</Button>}
      />
    );
  if (!app) return <Skeleton active paragraph={{ rows: 10 }} />;
  if (app.state !== "RUNNING" || !app.sparkUiAvailable)
    return <Alert type="warning" showIcon message={t("sparkUiUnavailable")} />;
  return (
    <>
      <PageHeader
        back={{
          label: app.name,
          to: `/applications/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}`,
        }}
        title={`${app.name} · Spark UI`}
        subtitle={t("sparkUiProxyHint")}
        extra={
          <Button onClick={() => setFrameVersion((v) => v + 1)}>
            {t("refresh")}
          </Button>
        }
      />
      <div className="spark-ui-frame-wrap">
        <iframe
          ref={frame}
          key={frameVersion}
          title={`${app.name} Spark UI`}
          src={source}
          className="spark-ui-frame"
          sandbox="allow-scripts"
          referrerPolicy="no-referrer"
        />
      </div>
    </>
  );
}
