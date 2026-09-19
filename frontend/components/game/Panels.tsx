"use client";

import { useState } from "react";

import {
  type PlayerView,
  RANK_JACK,
  RANK_KING,
  RANK_QUEEN,
  RANK_TEN,
  powerLabel,
  printedValue,
  rankLabel,
  suitLabel,
} from "@/lib/protocol";
import type { Actions } from "@/lib/store";
import { FaceUpCard } from "./Card";

const btn =
  "rounded-lg px-4 py-2 text-sm font-semibold transition focus:outline-none focus-visible:ring-2 focus-visible:ring-amber-300 disabled:cursor-not-allowed disabled:opacity-40";
const primary = `${btn} bg-amber-300 text-ink hover:bg-amber-200`;
const ghost = `${btn} bg-white/10 text-white hover:bg-white/20`;

/**
 * The memory phase: pick exactly the number of positions the server asked for.
 * The count comes from `game_started`; this component does not decide it.
 */
export function MemoryPanel({
  view,
  inspect,
  actions,
}: {
  view: PlayerView;
  inspect: number;
  actions: Actions;
}) {
  const [picked, setPicked] = useState<number[]>([]);
  const toggle = (pos: number) =>
    setPicked((p) => (p.includes(pos) ? p.filter((x) => x !== pos) : [...p, pos]));

  return (
    <div className="space-y-3 text-center">
      <p className="text-sm text-white/80">
        Memory phase — choose {inspect} of your {view.my_cards.length} cards to look at. You get one
        look, so commit them to memory.
      </p>
      <div className="flex flex-wrap justify-center gap-2">
        {view.my_cards.map((h) => (
          <button
            key={h.id}
            type="button"
            onClick={() => toggle(h.position)}
            aria-pressed={picked.includes(h.position)}
            className={`${ghost} ${picked.includes(h.position) ? "!bg-amber-300 !text-ink" : ""}`}
          >
            Position {h.position + 1}
          </button>
        ))}
      </div>
      <button
        type="button"
        className={primary}
        disabled={picked.length !== inspect}
        onClick={() => actions.inspectMemory([...picked].sort((a, b) => a - b))}
      >
        Look at {picked.length}/{inspect}
      </button>
    </div>
  );
}

/** Draw, then keep-or-discard. Selection is local; legality is the server's. */
export function TurnPanel({
  view,
  actions,
  removeIndex,
  setRemoveIndex,
  insertMode,
  setInsertMode,
  onDeclare,
}: {
  view: PlayerView;
  actions: Actions;
  removeIndex: number | null;
  setRemoveIndex: (i: number | null) => void;
  insertMode: boolean;
  setInsertMode: (v: boolean) => void;
  onDeclare?: () => void;
}) {
  const drawn = view.my_drawn_card;

  if (view.phase === "turn_start" && view.my_turn) {
    return (
      <div className="flex flex-wrap items-center justify-center gap-3">
        <button type="button" className={primary} onClick={() => actions.drawCard()}>
          Draw a card
        </button>
        {view.declare_available && onDeclare ? <CombioButton onDeclare={onDeclare} /> : null}
      </div>
    );
  }

  if (view.phase !== "draw_decision" || !drawn) return null;

  if (view.my_cards.length === 0) {
    return (
      <div className="space-y-3 text-center">
        <DrawnCard card={drawn} />
        <p className="text-sm text-white/80">
          You have no cards. Keep it as your first card, or throw it away.
        </p>
        <div className="flex justify-center gap-3">
          <button type="button" className={primary} onClick={() => actions.keepDrawnAsFirstCard()}>
            Keep it
          </button>
          <button type="button" className={ghost} onClick={() => actions.discardDrawn()}>
            Discard it
          </button>
        </div>
      </div>
    );
  }

  if (insertMode && removeIndex !== null) {
    return (
      <div className="space-y-2 text-center">
        <DrawnCard card={drawn} />
        <p className="text-sm text-white/80">
          Position {removeIndex + 1} goes to the pile. Pick where the new card slots in.
        </p>
        <button
          type="button"
          className={ghost}
          onClick={() => {
            setInsertMode(false);
            setRemoveIndex(null);
          }}
        >
          Back
        </button>
      </div>
    );
  }

  return (
    <div className="space-y-3 text-center">
      <DrawnCard card={drawn} />
      <p className="text-sm text-white/80">
        {removeIndex === null
          ? "Tap one of your cards to swap it out, or discard the drawn card."
          : `Swapping out position ${removeIndex + 1}.`}
      </p>
      <div className="flex flex-wrap justify-center gap-3">
        <button
          type="button"
          className={primary}
          disabled={removeIndex === null}
          onClick={() => setInsertMode(true)}
        >
          Keep it
        </button>
        <button type="button" className={ghost} onClick={() => actions.discardDrawn()}>
          Discard it
          {drawn.rank === RANK_TEN ||
          drawn.rank === RANK_JACK ||
          drawn.rank === RANK_QUEEN ||
          drawn.rank === RANK_KING
            ? " (fires its power)"
            : ""}
        </button>
      </div>
    </div>
  );
}

function DrawnCard({ card }: { card: { rank: number; suit: number; deck: number; id: number } }) {
  return (
    <div className="flex flex-col items-center gap-1">
      <FaceUpCard
        card={{ id: card.id, rank: card.rank, suit: card.suit as 0 | 1 | 2 | 3, deck: card.deck as 0 | 1 }}
        size="lg"
        label={`You drew the ${rankLabel(card.rank)} of ${suitLabel(card.suit as 0 | 1 | 2 | 3)}`}
      />
      <span className="text-[11px] text-white/60">
        Only you can see this · counts {printedValue(card.rank)}
      </span>
    </div>
  );
}

/**
 * Power resolution. Which power is live comes from `my_pending_power`; the
 * server sent it and the server validates every choice made here.
 */
export function PowerPanel({
  view,
  actions,
  nameOf,
  targetPrompt,
  setTargetPrompt,
}: {
  view: PlayerView;
  actions: Actions;
  nameOf: (id: string) => string;
  targetPrompt: "power-target" | null;
  setTargetPrompt: (v: "power-target" | null) => void;
}) {
  const power = view.my_pending_power;
  const [ownIndex, setOwnIndex] = useState<number | null>(null);
  if (power === 0) return null;

  const targetable = view.opponents.filter((o) => !o.removed && o.count > 0);

  return (
    <div className="space-y-3 rounded-xl bg-sky-500/20 p-4 text-center ring-1 ring-sky-300/40">
      <p className="font-display font-bold text-white">{powerLabel(power)}</p>

      {view.phase === "king_decision" ? (
        <div className="space-y-2">
          <p className="text-sm text-white/80">
            Take that card into your hand, or leave it. Pick which of your cards to give up.
          </p>
          <div className="flex flex-wrap justify-center gap-2">
            {view.my_cards.map((h) => (
              <button
                key={h.id}
                type="button"
                className={`${ghost} ${ownIndex === h.position ? "!bg-amber-300 !text-ink" : ""}`}
                onClick={() => setOwnIndex(h.position)}
              >
                Position {h.position + 1}
              </button>
            ))}
          </div>
          <div className="flex justify-center gap-3">
            <button
              type="button"
              className={primary}
              disabled={ownIndex === null}
              onClick={() => ownIndex !== null && actions.kingSwap(ownIndex)}
            >
              Swap
            </button>
            <button type="button" className={ghost} onClick={() => actions.cancelKingSwap()}>
              Cancel the swap
            </button>
          </div>
        </div>
      ) : null}

      {view.phase === "power" && power === RANK_TEN ? (
        <div>
          <p className="mb-2 text-sm text-white/80">Peek at one of your own cards.</p>
          <div className="flex flex-wrap justify-center gap-2">
            {view.my_cards.map((h) => (
              <button
                key={h.id}
                type="button"
                className={ghost}
                onClick={() => actions.peek10(h.position)}
              >
                Position {h.position + 1}
              </button>
            ))}
            {view.my_cards.length === 0 ? (
              <span className="text-sm text-white/60">You have no cards, so this fizzles.</span>
            ) : null}
          </div>
        </div>
      ) : null}

      {view.phase === "power" && (power === RANK_JACK || power === RANK_KING) ? (
        <div>
          <p className="mb-2 text-sm text-white/80">Choose an opponent card to look at.</p>
          <OpponentPicker
            view={view}
            nameOf={nameOf}
            onPick={(target, index) =>
              power === RANK_JACK ? actions.jackPeek(target, index) : actions.kingPeek(target, index)
            }
          />
          {targetable.length === 0 ? (
            <span className="text-sm text-white/60">Nobody has a card to look at.</span>
          ) : null}
        </div>
      ) : null}

      {view.phase === "power" && power === RANK_QUEEN ? (
        <div className="space-y-2">
          <p className="text-sm text-white/80">
            Blind swap — neither card is shown, not even to you. This one is optional.
          </p>
          <div className="flex flex-wrap justify-center gap-2">
            {view.my_cards.map((h) => (
              <button
                key={h.id}
                type="button"
                className={`${ghost} ${ownIndex === h.position ? "!bg-amber-300 !text-ink" : ""}`}
                onClick={() => {
                  setOwnIndex(h.position);
                  setTargetPrompt("power-target");
                }}
              >
                Your position {h.position + 1}
              </button>
            ))}
          </div>
          {ownIndex !== null && targetPrompt === "power-target" ? (
            <OpponentPicker
              view={view}
              nameOf={nameOf}
              onPick={(target, index) => {
                actions.queenSwap(ownIndex, target, index);
                setTargetPrompt(null);
                setOwnIndex(null);
              }}
            />
          ) : null}
          <button type="button" className={ghost} onClick={() => actions.skipPower()}>
            Skip the Queen
          </button>
        </div>
      ) : null}
    </div>
  );
}

function OpponentPicker({
  view,
  nameOf,
  onPick,
}: {
  view: PlayerView;
  nameOf: (id: string) => string;
  onPick: (target: string, index: number) => void;
}) {
  return (
    <div className="space-y-2">
      {view.opponents
        .filter((o) => !o.removed && o.count > 0)
        .map((o) => (
          <div key={o.id} className="flex flex-wrap items-center justify-center gap-2">
            <span className="text-xs text-white/70">{nameOf(o.id)}</span>
            {o.cards.map((h) => (
              <button
                key={h.id}
                type="button"
                className={ghost}
                onClick={() => onPick(o.id, h.position)}
                aria-label={`${nameOf(o.id)} position ${h.position + 1}`}
              >
                {h.position + 1}
              </button>
            ))}
          </div>
        ))}
    </div>
  );
}

/**
 * COMBIO.
 *
 * This button appears when and only when the server's view says
 * `declare_available`. The client does not know its own score and does not
 * count rounds — both would be re-implementing engine rules.
 *
 * It takes a plain handler rather than the action surface so the caller can put
 * a confirmation in front of it: declaring is irreversible and sits next to the
 * other turn buttons, which makes it easy to hit by accident.
 */
export function CombioButton({ onDeclare }: { onDeclare: () => void }) {
  return (
    <button
      type="button"
      onClick={onDeclare}
      className={`${btn} bg-rose-500 text-white hover:bg-rose-400`}
    >
      Declare COMBIO
    </button>
  );
}

export function ScoreBoard({
  view,
  nameOf,
  onLeave,
  onRematch,
  canRematch = true,
}: {
  view: PlayerView;
  nameOf: (id: string) => string;
  onLeave: () => void;
  /** Keeps everyone in this room and returns it to the lobby. */
  onRematch?: () => void;
  canRematch?: boolean;
}) {
  const scores = view.scores ?? {};
  const winners = new Set(view.winners ?? []);
  const rows = Object.entries(scores).sort((a, b) => a[1] - b[1]);

  return (
    <div className="mx-auto w-full max-w-lg space-y-4 rounded-xl bg-felt-dark p-5 ring-1 ring-white/10">
      <h2 className="text-center font-display text-2xl font-bold text-white">Game over</h2>
      <ul className="space-y-2">
        {rows.map(([id, score]) => (
          <li
            key={id}
            className={`flex items-center gap-3 rounded-lg px-3 py-2 ${
              winners.has(id) ? "bg-amber-300 text-ink" : "bg-black/25 text-white"
            }`}
          >
            <span className="w-6 text-center" aria-hidden="true">
              {winners.has(id) ? "\u2605" : ""}
            </span>
            <span className="flex-1 font-semibold">{nameOf(id)}</span>
            <span className="tabular-nums">{score}</span>
            <span className="flex gap-1">
              {(view.reveal?.[id] ?? []).map((c) => (
                <FaceUpCard key={c.id} card={c} size="sm" />
              ))}
            </span>
          </li>
        ))}
      </ul>
      {view.declared ? (
        <p className="text-center text-sm text-white/70">
          {nameOf(view.declarer)} declared COMBIO
          {winners.has(view.declarer) ? " and made it." : " and did not make it."}
        </p>
      ) : null}
      <div className="flex flex-col gap-2 sm:flex-row">
        {onRematch ? (
          <button
            type="button"
            onClick={onRematch}
            disabled={!canRematch}
            className={`${primary} flex-1`}
            title={canRematch ? "" : "Only the host can start the next hand"}
          >
            Play again
          </button>
        ) : null}
        <button type="button" onClick={onLeave} className={`${ghost} flex-1`}>
          Leave the room
        </button>
      </div>
      {onRematch ? (
        <p className="text-center text-xs text-white/50">
          Play again keeps everyone here and deals a fresh hand \u2014 no need to share the code
          again.
        </p>
      ) : null}
    </div>
  );
}
