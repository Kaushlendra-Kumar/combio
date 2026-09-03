"use client";

import type { ConnectionState } from "@/lib/websocket";

const LABEL: Record<ConnectionState, string> = {
  idle: "Starting\u2026",
  connecting: "Reconnecting\u2026",
  open: "Connected",
  closing: "Disconnecting\u2026",
  closed: "Disconnected",
};

const TONE: Record<ConnectionState, string> = {
  idle: "bg-slate-400",
  connecting: "bg-amber-400 animate-pulse",
  open: "bg-emerald-400",
  closing: "bg-amber-400",
  closed: "bg-rose-500",
};

export function ConnectionStatus({ state }: { state: ConnectionState }) {
  return (
    <div className="flex items-center gap-2 text-xs text-white/70" role="status" aria-live="polite">
      <span aria-hidden="true" className={`h-2 w-2 rounded-full ${TONE[state]}`} />
      {LABEL[state]}
    </div>
  );
}
