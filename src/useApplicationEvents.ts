import { useEffect, useRef } from "react";
import { subscribeApplicationEvents } from "./service";
import type { ApplicationChange } from "./types";

export function useApplicationEvents(
  callback: (change: ApplicationChange) => void,
) {
  const callbackRef = useRef(callback);
  callbackRef.current = callback;
  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined;
    let latest: ApplicationChange;
    const stop = subscribeApplicationEvents((change) => {
      latest = change;
      if (timer) return;
      timer = setTimeout(() => {
        timer = undefined;
        callbackRef.current(latest);
      }, 750);
    });
    return () => {
      stop();
      if (timer) clearTimeout(timer);
    };
  }, []);
}
