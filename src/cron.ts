export type CronMode =
  | "custom"
  | "seconds"
  | "minutes"
  | "hourly"
  | "daily"
  | "weekly"
  | "monthly"
  | "yearly";
export type CronSettings = {
  mode: CronMode;
  interval: number;
  second: number;
  minute: number;
  hour: number;
  weekdays: number[];
  days: number[];
  month: number;
};
export const defaultCronSettings: CronSettings = {
  mode: "daily",
  interval: 5,
  second: 0,
  minute: 0,
  hour: 2,
  weekdays: [1],
  days: [1],
  month: 1,
};
export function buildCron(s: CronSettings) {
  const time = `${s.second} ${s.minute} ${s.hour}`;
  switch (s.mode) {
    case "seconds":
      return `*/${s.interval} * * * * *`;
    case "minutes":
      return `${s.second} */${s.interval} * * * *`;
    case "hourly":
      return `${s.second} ${s.minute} * * * *`;
    case "daily":
      return `${time} * * *`;
    case "weekly":
      return `${time} * * ${[...s.weekdays].sort((a, b) => a - b).join(",")}`;
    case "monthly":
      return `${time} ${[...s.days].sort((a, b) => a - b).join(",")} * *`;
    case "yearly":
      return `${time} ${s.days[0]} ${s.month} *`;
    default:
      return "";
  }
}
export function readCron(expression: string): CronSettings {
  const parts = expression.trim().split(/\s+/);
  if (parts.length === 5) parts.unshift("0");
  const custom = { ...defaultCronSettings, mode: "custom" as const };
  if (parts.length !== 6) return custom;
  const [second, minute, hour, day, month, weekday] = parts;
  const fixed = (v: string, max: number, min = 0) =>
    /^\d+$/.test(v) && +v >= min && +v <= max;
  const list = (v: string, max: number, min = 0) =>
    v.split(",").every((x) => fixed(x, max, min));
  const settings = {
    ...defaultCronSettings,
    second: +second,
    minute: +minute,
    hour: +hour,
  };
  const seconds = /^\*\/(\d+)$/.exec(second);
  if (
    seconds &&
    +seconds[1] >= 1 &&
    +seconds[1] <= 59 &&
    parts.slice(1).every((v) => v === "*")
  )
    return { ...defaultCronSettings, mode: "seconds", interval: +seconds[1] };
  const minutes = /^\*\/(\d+)$/.exec(minute);
  if (
    fixed(second, 59) &&
    minutes &&
    +minutes[1] >= 1 &&
    +minutes[1] <= 59 &&
    parts.slice(2).every((v) => v === "*")
  )
    return {
      ...defaultCronSettings,
      mode: "minutes",
      second: +second,
      interval: +minutes[1],
    };
  if (!fixed(second, 59) || !fixed(minute, 59)) return custom;
  if (hour === "*" && day === "*" && month === "*" && weekday === "*")
    return { ...settings, mode: "hourly", hour: 2 };
  if (!fixed(hour, 23)) return custom;
  if (day === "*" && month === "*" && weekday === "*")
    return { ...settings, mode: "daily" };
  if (day === "*" && month === "*" && list(weekday, 6))
    return {
      ...settings,
      mode: "weekly",
      weekdays: weekday.split(",").map(Number),
    };
  if (list(day, 31, 1) && month === "*" && weekday === "*")
    return { ...settings, mode: "monthly", days: day.split(",").map(Number) };
  if (fixed(day, 31, 1) && fixed(month, 12, 1) && weekday === "*")
    return { ...settings, mode: "yearly", days: [+day], month: +month };
  return custom;
}
