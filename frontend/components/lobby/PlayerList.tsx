"use client";

import type { RoomPlayer } from "@/lib/protocol";

export function PlayerList({ players, meId }: { players: RoomPlayer[]; meId: string }) {
  return (
    <ul className="space-y-2" aria-label="Players in this room">
      {players.map((p) => (
        <li
          key={p.id}
          className="flex items-center gap-3 rounded-lg bg-black/25 px-3 py-2 text-sm text-white"
        >
          <span className="flex-1 font-semibold">
            {p.name}
            {p.id === meId ? <span className="ml-1 text-white/50">(you)</span> : null}
          </span>
          {p.host ? (
            <span className="rounded bg-white/15 px-2 py-0.5 text-[10px] uppercase">host</span>
          ) : null}
          {!p.connected ? (
            <span className="rounded bg-amber-500/30 px-2 py-0.5 text-[10px] uppercase text-amber-100">
              offline
            </span>
          ) : null}
          <span
            className={`rounded px-2 py-0.5 text-[10px] uppercase ${
              p.ready ? "bg-emerald-500/30 text-emerald-100" : "bg-white/10 text-white/60"
            }`}
          >
            {p.ready ? "ready" : "not ready"}
          </span>
        </li>
      ))}
    </ul>
  );
}
