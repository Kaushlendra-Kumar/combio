"use client";

import { useEffect, useState } from "react";

/**
 * A countdown, shown only when the server has told us when something expires.
 *
 * The Phase 3 protocol carries no deadline field, so in practice this renders
 * the idle form. That is deliberate: the server owns every timer and performs
 * every transition. Inventing a client countdown would put a second, wrong
 * clock on screen and would drift the moment a disconnect froze the real one.
 * If a deadline is ever added to the protocol, pass it here — even then this
 * only displays, it never acts on reaching zero.
 */
export function Timer({ deadlineMs, label }: { deadlineMs?: number; label: string }) {
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    if (deadlineMs === undefined) return;
    const t = setInterval(() => setNow(Date.now()), 250);
    return () => clearInterval(t);
  }, [deadlineMs]);

  if (deadlineMs === undefined) {
    return (
      <span className="text-xs text-white/50" title="The server owns all timers">
        {label}
      </span>
    );
  }
  const left = Math.max(0, Math.ceil((deadlineMs - now) / 1000));
  return (
    <span className="text-xs tabular-nums text-white/80">
      {label} {left}s
    </span>
  );
}
