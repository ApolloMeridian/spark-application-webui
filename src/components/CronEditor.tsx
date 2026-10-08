import { useEffect, useState } from "react";
import { Alert, Input, Select, Typography } from "antd";
import {
  buildCron,
  defaultCronSettings,
  readCron,
  type CronSettings,
  type CronMode,
} from "../cron";

export function CronEditor({
  value,
  onChange,
  en,
}: {
  value: string;
  onChange: (value: string) => void;
  en: boolean;
}) {
  const [settings, setSettings] = useState(() => readCron(value));
  useEffect(() => {
    setSettings(readCron(value));
  }, [value]);
  const update = (change: Partial<CronSettings>) => {
    const next = { ...settings, ...change };
    setSettings(next);
    if (next.mode !== "custom") onChange(buildCron(next));
  };
  const numbers = (max: number, start = 0) =>
    Array.from({ length: max - start + 1 }, (_, i) => ({
      value: i + start,
      label: String(i + start).padStart(2, "0"),
    }));
  const field = (
    key: "second" | "minute" | "hour" | "month",
    label: string,
    max: number,
    start = 0,
  ) => (
    <label className="cron-field">
      <span>{label}</span>
      <Select
        aria-label={label}
        value={settings[key]}
        options={numbers(max, start)}
        onChange={(v) => update({ [key]: v })}
      />
    </label>
  );
  const modes: { value: CronMode; label: string }[] = [
    { value: "seconds", label: en ? "Every N seconds" : "每隔 N 秒" },
    { value: "minutes", label: en ? "Every N minutes" : "每隔 N 分钟" },
    { value: "hourly", label: en ? "Hourly" : "每小时" },
    { value: "daily", label: en ? "Daily" : "每天" },
    { value: "weekly", label: en ? "Weekly" : "每周" },
    { value: "monthly", label: en ? "Monthly" : "每月" },
    { value: "yearly", label: en ? "Yearly" : "每年" },
    { value: "custom", label: en ? "Custom expression" : "自定义表达式" },
  ];
  return (
    <div className="cron-editor">
      <label className="cron-field">
        <span>{en ? "Frequency" : "执行周期"}</span>
        <Select
          aria-label={en ? "Frequency" : "执行周期"}
          value={settings.mode}
          options={modes}
          onChange={(mode) => {
            if (mode === "custom") {
              setSettings({ ...settings, mode });
              return;
            }
            // Custom expressions must not introduce NaN into point-and-click controls.
            const next = {
              ...(settings.mode === "custom" ? defaultCronSettings : settings),
              mode,
            };
            setSettings(next);
            onChange(buildCron(next));
          }}
        />
      </label>
      {settings.mode !== "custom" && (
        <div className="cron-builder-fields">
          {settings.mode === "seconds" || settings.mode === "minutes" ? (
            <label className="cron-field">
              <span>{en ? "Interval" : "间隔"}</span>
              <Select
                aria-label={en ? "Interval" : "间隔"}
                value={settings.interval}
                options={numbers(59, 1)}
                onChange={(interval) => update({ interval })}
              />
            </label>
          ) : null}
          {["daily", "weekly", "monthly", "yearly"].includes(settings.mode) &&
            field("hour", en ? "Hour" : "小时", 23)}
          {["hourly", "daily", "weekly", "monthly", "yearly"].includes(
            settings.mode,
          ) && field("minute", en ? "Minute" : "分钟", 59)}
          {settings.mode !== "seconds" &&
            field("second", en ? "Second" : "秒", 59)}
          {settings.mode === "weekly" && (
            <label className="cron-field cron-field-wide">
              <span>{en ? "Days of week" : "星期"}</span>
              <Select
                mode="multiple"
                aria-label={en ? "Days of week" : "星期"}
                value={settings.weekdays}
                options={(en
                  ? [
                      "Sunday",
                      "Monday",
                      "Tuesday",
                      "Wednesday",
                      "Thursday",
                      "Friday",
                      "Saturday",
                    ]
                  : ["周日", "周一", "周二", "周三", "周四", "周五", "周六"]
                ).map((label, value) => ({ label, value }))}
                onChange={(weekdays) => {
                  if (weekdays.length) update({ weekdays });
                }}
              />
            </label>
          )}
          {settings.mode === "yearly" &&
            field("month", en ? "Month" : "月份", 12, 1)}
          {["monthly", "yearly"].includes(settings.mode) && (
            <label className="cron-field cron-field-wide">
              <span>{en ? "Day of month" : "日期"}</span>
              <Select
                mode={settings.mode === "monthly" ? "multiple" : undefined}
                aria-label={en ? "Day of month" : "日期"}
                value={
                  settings.mode === "monthly" ? settings.days : settings.days[0]
                }
                options={numbers(31, 1)}
                onChange={(days) => {
                  const selected = Array.isArray(days) ? days : [days];
                  if (selected.length) update({ days: selected });
                }}
              />
            </label>
          )}
        </div>
      )}
      {["monthly", "yearly"].includes(settings.mode) && (
        <Typography.Text type="secondary">
          {en
            ? "Dates absent in a month are skipped (e.g. February 30)."
            : "月份中不存在的日期会跳过，例如 2 月 30 日。"}
        </Typography.Text>
      )}
      <label className="cron-field cron-expression">
        <span>{en ? "Cron expression" : "Cron 表达式"}</span>
        <Input
          aria-label={en ? "Cron expression" : "Cron 表达式"}
          value={value}
          onChange={(e) => {
            setSettings({ ...settings, mode: "custom" });
            onChange(e.target.value);
          }}
        />
      </label>
      <Typography.Text type="secondary">
        {en
          ? "Six fields: second · minute · hour · day · month · weekday. Existing five-field schedules run at second 0."
          : "六段：秒 · 分钟 · 小时 · 日 · 月 · 星期。兼容现有五段配置，秒默认为 0。"}
      </Typography.Text>
      {settings.mode === "seconds" && (
        <Alert
          showIcon
          type="info"
          message={
            en
              ? "Second-level occurrences are supported; dispatch latency depends on the backend scanInterval (default 5s). Set it to 1s for finer scanning, and review concurrency/misfire limits."
              : "支持秒级计划时间；实际派发延迟受后端 scanInterval 影响（默认 5 秒）。需要更细扫描时配置为 1 秒，并注意并发与补跑限制。"
          }
        />
      )}
    </div>
  );
}
