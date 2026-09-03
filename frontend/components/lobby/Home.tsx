"use client";

import { useEffect, useState } from "react";

import { type Session } from "@/lib/auth";
import { GameProvider, useGame } from "@/lib/store";
import { ConnectionStatus } from "../common/ConnectionStatus";
import { ErrorBanner } from "../common/ErrorBanner";
import { Modal } from "../common/Modal";
import { RoomLobby } from "./RoomLobby";
import { GameTable } from "../game/GameTable";

const btn =
  "rounded-lg px-4 py-2 text-sm font-semibold transition focus:outline-none focus-visible:ring-2 focus-visible:ring-amber-300 disabled:cursor-not-allowed disabled:opacity-40";

/** Home: create a room or join one by code. */
export function Home({
  session,
  onEnterRoom,
}: {
  session: Session;
  onEnterRoom: (code: string) => void;
}) {
  return (
    <GameProvider session={session}>
      <HomeInner name={session.name} onEnterRoom={onEnterRoom} />
    </GameProvider>
  );
}

function HomeInner({
  name,
  onEnterRoom,
}: {
  name: string;
  onEnterRoom: (code: string) => void;
}) {
  const { state, actions, connection } = useGame();
  const [code, setCode] = useState("");
  const [cards, setCards] = useState(4);
  const [isPublic, setIsPublic] = useState(true);
  const [maxPlayers, setMaxPlayers] = useState(8);

  // The server assigns the code, so we follow its room_state rather than
  // guessing one locally.
  useEffect(() => {
    if (state.room?.code) onEnterRoom(state.room.code);
  }, [state.room?.code, onEnterRoom]);

  return (
    <main className="mx-auto flex min-h-[100dvh] max-w-md flex-col justify-center gap-6 p-6">
      <header className="text-center">
        <h1 className="font-display text-5xl font-black tracking-tight text-amber-300">COMBIO</h1>
        <p className="mt-1 text-sm text-white/70">Hi {name}. Lowest total wins. Ace is &minus;1.</p>
        <div className="mt-2 flex justify-center">
          <ConnectionStatus state={connection} />
        </div>
      </header>

      <section className="space-y-3 rounded-xl bg-black/25 p-5">
        <h2 className="font-display text-lg font-bold">Start a table</h2>
        <div className="flex flex-wrap items-center gap-3 text-sm">
          <label htmlFor="cards" className="text-white/80">
            Cards each
          </label>
          <select
            id="cards"
            value={cards}
            onChange={(e) => setCards(Number(e.target.value))}
            className="rounded bg-white/10 px-2 py-1 focus:outline-none focus-visible:ring-2 focus-visible:ring-amber-300"
          >
            <option value={4}>4</option>
            <option value={6}>6</option>
            <option value={8}>8</option>
          </select>
          <label htmlFor="max" className="text-white/80">
            Max players
          </label>
          <select
            id="max"
            value={maxPlayers}
            onChange={(e) => setMaxPlayers(Number(e.target.value))}
            className="rounded bg-white/10 px-2 py-1 focus:outline-none focus-visible:ring-2 focus-visible:ring-amber-300"
          >
            {[2, 3, 4, 5, 6, 7, 8].map((n) => (
              <option key={n} value={n}>
                {n}
              </option>
            ))}
          </select>
          <label className="flex items-center gap-2 text-white/80">
            <input
              type="checkbox"
              checked={isPublic}
              onChange={(e) => setIsPublic(e.target.checked)}
              className="h-4 w-4"
            />
            Public
          </label>
        </div>
        <button
          type="button"
          className={`${btn} w-full bg-amber-300 text-ink`}
          disabled={connection !== "open"}
          onClick={() =>
            actions.createRoom({ public: isPublic, cardsPerPlayer: cards, maxPlayers })
          }
        >
          Create room
        </button>
      </section>

      <section className="space-y-3 rounded-xl bg-black/25 p-5">
        <h2 className="font-display text-lg font-bold">Join a table</h2>
        <div className="flex gap-2">
          <label htmlFor="code" className="sr-only">
            Room code
          </label>
          <input
            id="code"
            value={code}
            onChange={(e) => setCode(e.target.value.toUpperCase())}
            onKeyDown={(e) => e.key === "Enter" && code && actions.joinRoom(code)}
            placeholder="ABCD"
            maxLength={5}
            className="flex-1 rounded-lg bg-white/10 px-3 py-2 text-center font-display text-xl tracking-[0.3em] focus:outline-none focus-visible:ring-2 focus-visible:ring-amber-300"
          />
          <button
            type="button"
            className={`${btn} bg-white/10 text-white`}
            disabled={!code || connection !== "open"}
            onClick={() => actions.joinRoom(code)}
          >
            Join
          </button>
        </div>
      </section>

      <div className="pointer-events-none fixed inset-x-0 bottom-4 z-40 mx-auto flex max-w-md justify-center px-4">
        <ErrorBanner notice={state.notice} onDismiss={actions.dismissNotice} />
      </div>
    </main>
  );
}

/** The room route: one provider, switching between lobby and table. */
export function RoomScreen({
  session,
  code,
  onLeave,
}: {
  session: Session;
  code: string;
  onLeave: () => void;
}) {
  return (
    <GameProvider session={session}>
      <RoomInner code={code} onLeave={onLeave} />
    </GameProvider>
  );
}

function RoomInner({ code, onLeave }: { code: string; onLeave: () => void }) {
  const { state, actions, connection } = useGame();
  const [asked, setAsked] = useState(false);

  // Ask to join once the socket is up. `join_room` doubles as reconnect on the
  // server, so a refresh or a dropped connection lands us back in our seat.
  useEffect(() => {
    if (connection === "open" && !asked) {
      actions.joinRoom(code);
      setAsked(true);
    }
    if (connection === "closed") setAsked(false);
  }, [connection, asked, actions, code]);

  // A reconnect after a real drop: ask again once we are back.
  useEffect(() => {
    if (connection === "open" && state.reconnectRequired) {
      actions.reconnectRoom(code);
    }
  }, [connection, state.reconnectRequired, actions, code]);

  const inGame = state.started && state.view !== null;

  return (
    <main className="min-h-[100dvh]">
      <div className="flex items-center justify-between gap-3 px-4 py-2 text-xs">
        <span className="font-display font-bold tracking-widest text-amber-300">
          COMBIO · {code}
        </span>
        <ConnectionStatus state={connection} />
      </div>

      {inGame ? <GameTable onLeave={onLeave} /> : <RoomLobby onLeave={onLeave} />}

      <Modal open={connection === "connecting" || connection === "closed"} title="Connection lost">
        <p className="text-sm text-white/80">
          {connection === "closed"
            ? "We couldn't get back to the server. Reload the page to try again."
            : "Reconnecting\u2026 your game is paused for everyone until you're back."}
        </p>
        <p className="mt-2 text-xs text-white/50">
          The server owns the reconnect countdown. If it runs out you&apos;ll be removed from the game
          and your cards go to the bottom of the discard pile.
        </p>
      </Modal>

      <div className="pointer-events-none fixed inset-x-0 bottom-4 z-40 mx-auto flex max-w-md justify-center px-4">
        <ErrorBanner notice={state.notice} onDismiss={actions.dismissNotice} />
      </div>
    </main>
  );
}
