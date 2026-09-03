"use client";

import { type CardHandle, type OpponentView, type PlayerView, rankLabel } from "@/lib/protocol";
import { CardSlot, FaceDownCard, FaceUpCard } from "./Card";

/**
 * Your own row of cards.
 *
 * Your cards are face down to you as well: COMBIO is a memory game and the
 * server never tells you what you are holding. What you get is a position and
 * a deck colour, exactly like everyone else sees.
 */
export function PlayerLayout({
  view,
  selectable,
  selectedIndex,
  onSelect,
  insertMode,
  onInsert,
}: {
  view: PlayerView;
  selectable: boolean;
  selectedIndex: number | null;
  onSelect?: (index: number) => void;
  /** When set, gaps become insertion points for a card being kept. */
  insertMode: boolean;
  onInsert?: (index: number) => void;
}) {
  const cards = view.my_cards;

  if (insertMode) {
    return (
      <div className="flex items-end justify-center gap-1" role="group" aria-label="Choose where to slot the new card">
        <CardSlot label="Insert at the far left" onClick={() => onInsert?.(0)} />
        {cards.map((h, i) => (
          <div key={h.id} className="flex items-end gap-1">
            <FaceDownCard handle={h} label={`Your card in position ${h.position + 1}`} />
            <CardSlot label={`Insert after position ${i + 1}`} onClick={() => onInsert?.(i + 1)} />
          </div>
        ))}
      </div>
    );
  }

  return (
    <div className="flex flex-wrap items-end justify-center gap-2" role="group" aria-label="Your cards">
      {cards.length === 0 ? (
        <p className="rounded-lg border border-dashed border-white/25 px-6 py-6 text-sm text-white/60">
          You have no cards. That is a normal position, not a win.
        </p>
      ) : null}
      {cards.map((h) => (
        <FaceDownCard
          key={h.id}
          handle={h}
          label={`Your card in position ${h.position + 1}`}
          selected={selectedIndex === h.position}
          onClick={selectable && onSelect ? () => onSelect(h.position) : undefined}
        />
      ))}
    </div>
  );
}

/** One opponent: their name, status and their card backs. */
export function OpponentLayout({
  opponent,
  name,
  isTurn,
  targetable,
  onTarget,
  compact = false,
}: {
  opponent: OpponentView;
  /** Display name comes from room_state, not from the game view. */
  name: string;
  isTurn: boolean;
  targetable: boolean;
  onTarget?: (index: number) => void;
  compact?: boolean;
}) {
  const status = opponent.removed
    ? "removed"
    : !opponent.connected
      ? "disconnected"
      : null;

  return (
    <div
      className={`rounded-xl px-3 py-2 transition ${
        isTurn ? "bg-amber-300/15 ring-1 ring-amber-300/60" : "bg-black/15"
      }`}
    >
      <div className="mb-1 flex items-center gap-2 text-xs">
        <span className="font-semibold text-white">{name}</span>
        <span className="text-white/60">{opponent.count} cards</span>
        {opponent.pending_penalty > 0 ? (
          <span className="rounded bg-rose-500/30 px-1.5 text-[10px] text-rose-100">
            +{opponent.pending_penalty} pending
          </span>
        ) : null}
        {status ? <span className="text-[10px] uppercase text-amber-200">{status}</span> : null}
      </div>
      <div className="flex flex-wrap gap-1">
        {opponent.cards.map((h) => (
          <FaceDownCard
            key={h.id}
            handle={h}
            size={compact ? "sm" : "sm"}
            label={`${name}'s card in position ${h.position + 1}`}
            onClick={targetable && onTarget ? () => onTarget(h.position) : undefined}
            highlighted={targetable}
          />
        ))}
        {opponent.cards.length === 0 ? (
          <span className="text-[11px] text-white/40">no cards</span>
        ) : null}
      </div>
    </div>
  );
}

export function DrawPile({
  count,
  canDraw,
  onDraw,
}: {
  count: number;
  canDraw: boolean;
  onDraw: () => void;
}) {
  return (
    <div className="flex flex-col items-center gap-1">
      <button
        type="button"
        onClick={onDraw}
        disabled={!canDraw}
        aria-label={canDraw ? "Draw a card" : `Draw pile, ${count} cards`}
        className={`relative h-24 w-16 rounded-md bg-felt-light ring-1 ring-black/30 transition ${
          canDraw
            ? "cursor-pointer shadow-lg hover:-translate-y-1 ring-2 ring-amber-300"
            : "opacity-70"
        } focus:outline-none focus-visible:ring-2 focus-visible:ring-amber-300`}
      >
        <span aria-hidden="true" className="text-2xl text-white/50">
          ?
        </span>
      </button>
      <span className="text-[11px] text-white/60">Draw · {count}</span>
    </div>
  );
}

export function DiscardPile({ view }: { view: PlayerView }) {
  return (
    <div className="flex flex-col items-center gap-1">
      {view.discard_top ? (
        <FaceUpCard card={view.discard_top} size="lg" />
      ) : (
        <div className="h-28 w-20 rounded-md border-2 border-dashed border-white/25" />
      )}
      <span className="text-[11px] text-white/60">Middle · {view.discard_count}</span>
    </div>
  );
}

export function TurnIndicator({
  view,
  nameOf,
}: {
  view: PlayerView;
  nameOf: (id: string) => string;
}) {
  const who = view.my_turn ? "Your turn" : `${nameOf(view.turn_player)}'s turn`;
  return (
    <div className="flex flex-wrap items-center justify-center gap-2 text-sm">
      <span
        className={`rounded-full px-3 py-1 font-semibold ${
          view.my_turn ? "bg-amber-300 text-ink" : "bg-black/25 text-white/80"
        }`}
      >
        {who}
      </span>
      {view.final_turns ? (
        <span className="rounded-full bg-rose-500/80 px-3 py-1 text-xs font-semibold text-white">
          Final turns · {nameOf(view.declarer)} declared
        </span>
      ) : null}
      {view.paused ? (
        <span className="rounded-full bg-amber-500/80 px-3 py-1 text-xs font-semibold text-white">
          Paused
        </span>
      ) : null}
    </div>
  );
}

/**
 * Pending penalty cards.
 *
 * They are unknown to you and are not part of your layout: they cannot be
 * matched or targeted, which is why they are drawn apart from the row. Placing
 * them is optional — if the window closes first, the server appends them.
 */
export function PenaltyTray({
  count,
  positions,
  onPlace,
}: {
  count: number;
  positions: CardHandle[];
  onPlace: (position: number) => void;
}) {
  if (count === 0) return null;
  return (
    <div className="rounded-lg border border-rose-400/50 bg-rose-500/15 p-3">
      <p className="mb-2 text-xs text-rose-100">
        {count} penalty card{count === 1 ? "" : "s"} waiting. You don&apos;t get to see them. Slot
        them in while the window is open, or the server will append them.
      </p>
      <div className="flex flex-wrap items-center gap-1">
        <span className="mr-2 text-[11px] text-white/60">Place at:</span>
        {Array.from({ length: positions.length + 1 }, (_, i) => (
          <button
            key={i}
            type="button"
            onClick={() => onPlace(i)}
            className="rounded bg-white/10 px-2 py-1 text-xs hover:bg-white/25 focus:outline-none focus-visible:ring-2 focus-visible:ring-amber-300"
          >
            {i === 0 ? "start" : i === positions.length ? "end" : `after ${i}`}
          </button>
        ))}
      </div>
    </div>
  );
}

export function MatchWindow({ activeRank }: { activeRank: number }) {
  return (
    <div className="animate-pop rounded-xl bg-amber-300 px-4 py-2 text-center text-ink shadow-lg">
      <p className="font-display text-lg font-bold">MATCH {rankLabel(activeRank)}!</p>
      <p className="text-xs">
        Throw any {rankLabel(activeRank)} you remember — yours, or one you know in someone
        else&apos;s hand. A wrong guess costs two cards.
      </p>
    </div>
  );
}
