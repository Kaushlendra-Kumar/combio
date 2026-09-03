"use client";

import { useEffect, useState } from "react";

import { useGame } from "@/lib/store";
import { isMatchingOpen, playerName } from "@/lib/game-state";
import { rankLabel } from "@/lib/protocol";
import { FaceUpCard } from "./Card";
import {
  DiscardPile,
  DrawPile,
  MatchWindow,
  OpponentLayout,
  PenaltyTray,
  PlayerLayout,
  TurnIndicator,
} from "./Table";
import { CombioButton, MemoryPanel, PowerPanel, ScoreBoard, TurnPanel } from "./Panels";
import { Modal } from "../common/Modal";
import { Timer } from "../common/Timer";

/**
 * The table.
 *
 * Every decision it makes is presentational: which panel to render, which card
 * is selected, whether a button is worth showing. Legality is never decided
 * here — actions go to the server and the next `state_update` is the truth.
 */
export function GameTable({ onLeave }: { onLeave: () => void }) {
  const { state, actions } = useGame();
  const view = state.view;

  const [removeIndex, setRemoveIndex] = useState<number | null>(null);
  const [insertMode, setInsertMode] = useState(false);
  const [matchGive, setMatchGive] = useState<{ target: string; index: number } | null>(null);
  const [targetPrompt, setTargetPrompt] = useState<"power-target" | null>(null);

  // Any authoritative update clears local selections: a half-made choice
  // against a layout that has since changed is meaningless.
  useEffect(() => {
    setRemoveIndex(null);
    setInsertMode(false);
    setMatchGive(null);
  }, [view?.phase, view?.my_version]);

  if (!view) {
    return <p className="p-8 text-center text-white/70">Waiting for the table\u2026</p>;
  }

  if (view.finished) {
    return (
      <ScoreBoard view={view} nameOf={(id) => playerName(state, id)} onLeave={onLeave} />
    );
  }

  const nameOf = (id: string) => playerName(state, id);
  const matching = isMatchingOpen(state);

  const seats = view.opponents.length + 1;
  const columns = seats <= 3 ? "grid-cols-1 sm:grid-cols-2" : seats <= 5 ? "sm:grid-cols-3" : "sm:grid-cols-4";

  const reveal = state.pendingReveals[0];

  return (
    <div className="flex min-h-[100dvh] flex-col gap-4 p-3 sm:p-6">
      {/* opponents, wrapped so 2 through 8 players all fit */}
      <section aria-label="Opponents" className={`grid gap-2 ${columns}`}>
        {view.opponents.map((o) => (
          <OpponentLayout
            key={o.id}
            opponent={o}
            name={nameOf(o.id)}
            isTurn={view.turn_player === o.id}
            targetable={matching && !o.removed && o.count > 0 && view.my_cards.length > 0}
            onTarget={(index) => setMatchGive({ target: o.id, index })}
          />
        ))}
      </section>

      {/* middle */}
      <section
        aria-label="Table centre"
        className="flex flex-col items-center gap-3 rounded-2xl bg-felt py-5 ring-1 ring-black/25"
      >
        <TurnIndicator view={view} nameOf={nameOf} />
        <div className="flex items-end gap-8">
          <DrawPile
            count={view.deck_count}
            canDraw={view.my_turn && view.phase === "turn_start" && !view.paused}
            onDraw={() => actions.drawCard()}
          />
          <DiscardPile view={view} />
        </div>
        <Timer label="Server-timed" />
        {matching ? <MatchWindow activeRank={view.active_rank} /> : null}
      </section>

      {/* your side */}
      <section aria-label="Your hand" className="space-y-3">
        <PlayerLayout
          view={view}
          selectable={
            (view.phase === "draw_decision" && !!view.my_drawn_card) || matching
          }
          selectedIndex={removeIndex}
          insertMode={insertMode}
          onInsert={(index) => {
            if (removeIndex !== null) actions.keepDrawn(removeIndex, index);
            setInsertMode(false);
            setRemoveIndex(null);
          }}
          onSelect={(index) => {
            if (matching) {
              actions.attemptOwnMatch(index, view.my_version);
              return;
            }
            setRemoveIndex(index);
          }}
        />

        <PenaltyTray
          count={view.my_pending_count}
          positions={view.my_cards}
          onPlace={(position) => actions.insertPenalty(0, position)}
        />

        <div className="space-y-3">
          {view.phase === "memory" ? (
            <MemoryPanel view={view} inspect={state.inspectCount || view.my_cards.length / 2} actions={actions} />
          ) : null}

          <TurnPanel
            view={view}
            actions={actions}
            removeIndex={removeIndex}
            setRemoveIndex={setRemoveIndex}
            insertMode={insertMode}
            setInsertMode={setInsertMode}
          />

          <PowerPanel
            view={view}
            actions={actions}
            nameOf={nameOf}
            targetPrompt={targetPrompt}
            setTargetPrompt={setTargetPrompt}
          />

          {view.declare_available && view.phase === "turn_start" ? (
            <div className="flex justify-center">
              <CombioButton actions={actions} />
            </div>
          ) : null}
        </div>
      </section>

      {/* event feed */}
      <section aria-label="Recent events" className="rounded-lg bg-black/25 p-3">
        <ul className="max-h-28 space-y-0.5 overflow-y-auto text-xs text-white/70">
          {state.feed.slice(-8).map((l) => (
            <li key={l.key}>{l.text}</li>
          ))}
        </ul>
      </section>

      {/* choosing which of your cards to hand over after targeting */}
      <Modal
        open={matchGive !== null}
        title={`Take ${nameOf(matchGive?.target ?? "")}'s card`}
        onClose={() => setMatchGive(null)}
        closeLabel="Never mind"
      >
        <p className="mb-3 text-sm text-white/80">
          If you&apos;re right, that card leaves the game and you hand one of yours over in its
          place. If you&apos;re wrong, you take two penalty cards.
        </p>
        <div className="flex flex-wrap gap-2">
          {view.my_cards.map((h) => (
            <button
              key={h.id}
              type="button"
              className="rounded-lg bg-white/10 px-3 py-2 text-sm hover:bg-white/25 focus:outline-none focus-visible:ring-2 focus-visible:ring-amber-300"
              onClick={() => {
                const target = view.opponents.find((o) => o.id === matchGive?.target);
                if (matchGive && target) {
                  actions.attemptOpponentMatch({
                    target: matchGive.target,
                    targetIndex: matchGive.index,
                    targetVersion: target.version,
                    giveIndex: h.position,
                    giveVersion: view.my_version,
                  });
                }
                setMatchGive(null);
              }}
            >
              Give position {h.position + 1}
            </button>
          ))}
        </div>
      </Modal>

      {/* a private reveal, shown once and then gone */}
      <Modal
        open={!!reveal}
        title={reveal ? reveal.label : ""}
        onClose={reveal ? () => actions.consumeReveal(reveal.key) : undefined}
        closeLabel="Got it"
      >
        {reveal ? (
          <div className="flex flex-col items-center gap-2">
            <FaceUpCard card={reveal.card} size="lg" />
            <p className="text-sm text-white/80">
              {reveal.owner === "me"
                ? reveal.position >= 0
                  ? `Your position ${reveal.position + 1}`
                  : "Only you can see this"
                : `${nameOf(reveal.owner)}'s position ${reveal.position + 1}`}
            </p>
            <p className="text-xs text-white/50">
              Remember it — nothing on this screen will remind you later.
            </p>
          </div>
        ) : null}
      </Modal>

      {/* paused / reconnecting */}
      <Modal open={view.paused} title="Game paused">
        <p className="text-sm text-white/80">
          Someone lost their connection. Every timer is frozen and nobody can act until they come
          back or the server drops them. The server owns that countdown.
        </p>
      </Modal>

      <p className="text-center text-[10px] text-white/40">
        Active rank {view.active_rank === 0 ? "\u2014" : rankLabel(view.active_rank)} · deck{" "}
        {view.deck_count} · your layout v{view.my_version}
      </p>
    </div>
  );
}
