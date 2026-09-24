import { useEffect, useRef } from 'react';
import { subscribeApplicationEvents } from './service';
import type { ApplicationChange } from './types';

export function useApplicationEvents(callback: (change: ApplicationChange) => void) {
  const callbackRef = useRef(callback);
  callbackRef.current = callback;
  useEffect(() => subscribeApplicationEvents((change) => callbackRef.current(change)), []);
}
