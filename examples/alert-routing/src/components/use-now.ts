"use client";

import { useEffect, useState } from "react";

/** The current time, updated every few seconds so relative times stay true. */
export function useNow(intervalMs = 5000): Date {
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const t = setInterval(() => setNow(new Date()), intervalMs);
    return () => clearInterval(t);
  }, [intervalMs]);
  return now;
}
