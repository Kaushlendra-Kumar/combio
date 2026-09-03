"use client";

import { useState } from "react";

import { useGame } from "@/lib/store";
import { PlayerList } from "./PlayerList";

const btn =
  "rounded-lg px-4 py-2 text-sm font-semibold transition focus:outline-none focus-visible:ring-2 focus-visible:ring-amber-300 disabled:cursor-not-allowed disabled:opacity-40";

/**
 * The lobby.
 *
 * Start is enabled when the server's room status says READY. That is a hint
 * only: pressing it always asks the server, and a refusal shows the server's
 * error rather than being pre-empted here.
 */
export function RoomLobby({ onLeave }: { onLeave: () => void }) {
  const { state, actions } = useGame();
  const [copied, setCopied] = useState(false);
  const room = state.room;

  if (!room) {
    return <p className="p-8 text-center text-white/70">Joining\u2026</p>;
  }

  const me = room.players.find((p) => p.id === state.session?.playerId);
  const isHost = me?.host ?? false;

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(room.code);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      setCopied(false);
    }
  };

  return (
    <div className="mx-auto w-full max-w-lg space-y-5 p-6">
      <header className="text-center">
        <p className="text-xs uppercase tracking-widest text-white/50">Room code</p>
        <div className="flex items-center justify-center gap-3">
          <span className="font-display text-4xl font-bold tracking-[0.3em] text-amber-300">
            {room.code}
          </span>
          <button type="button" onClick={copy} className={`${btn} bg-white/10 text-white`}>
            {copied ? "Copied" : "Copy"}
          </button>
        </div>
        <p className="mt-1 text-xs text-white/60">
          {room.public ? "Public" : "Private"} · {room.cards_per_player} cards each · up to{" "}
          {room.max_players} players · status {room.status}
        </p>
      </header>

      <PlayerList players={room.players} meId={state.session?.playerId ?? ""} />

      <div className="flex flex-wrap justify-center gap-3">
        <button
          type="button"
          className={`${btn} ${me?.ready ? "bg-white/10 text-white" : "bg-emerald-400 text-ink"}`}
          onClick={() => actions.setReady(!me?.ready)}
        >
          {me?.ready ? "Not ready" : "I'm ready"}
        </button>
        <button
          type="button"
          className={`${btn} bg-amber-300 text-ink`}
          disabled={!isHost || room.status !== "READY"}
          onClick={() => actions.startGame()}
          title={isHost ? "" : "Only the host can start"}
        >
          Start game
        </button>
        <button
          type="button"
          className={`${btn} bg-white/10 text-white`}
          onClick={() => {
            actions.leaveRoom();
            onLeave();
          }}
        >
          Leave
        </button>
      </div>

      <p className="text-center text-xs text-white/50">
        Open this page in another tab, paste the code, and you can play both seats yourself.
      </p>
    </div>
  );
}
