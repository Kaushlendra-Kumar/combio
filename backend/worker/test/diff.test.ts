// Differential test against the Go engine.
//
// `go run ./cmd/difftrace` plays random games — legal moves, illegal moves,
// stale clicks, timers, disconnects, garbage arguments — and records every
// player's view and events after each step. This replays the same inputs
// through the TypeScript engine and requires byte-for-byte the same results.
//
// Skipped when Go is not installed.

import { execFileSync, spawnSync } from "node:child_process";
import { deepStrictEqual } from "node:assert";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import type { Action, TimerKind } from "../src/engine/actions";
import { StackedShuffler } from "../src/engine/deck";
import { filterEvents, type GameEvent } from "../src/engine/events";
import { newGame, type State } from "../src/engine/state";
import { apply } from "../src/engine/transition";
import { viewFor } from "../src/engine/view";
import { buildAction, encodeEvents, errorCode } from "../src/protocol";

const backendDir = fileURLToPath(new URL("../..", import.meta.url));
const haveGo = spawnSync("go", ["version"]).status === 0;

interface WireAction {
  actor?: string;
  kind?: string;
  args?: unknown;
  system?: string;
  player?: string;
  timer?: TimerKind;
}

function render(st: State, evs: GameEvent[]) {
  const views: Record<string, unknown> = {};
  const events: Record<string, unknown> = {};
  for (const p of st.players) {
    views[p.id] = viewFor(st, p.id);
    events[p.id] = encodeEvents(filterEvents(evs, p.id));
  }
  // A JSON round trip compares exactly what would go on the wire.
  return JSON.parse(JSON.stringify({ views, events }));
}

function systemAction(a: WireAction): Action {
  switch (a.system) {
    case "timer_expired":
      return { kind: "timer_expired", player: "", timer: a.timer! };
    case "player_disconnected":
      return { kind: "player_disconnected", player: a.player! };
    case "player_reconnected":
      return { kind: "player_reconnected", player: a.player! };
    default:
      return { kind: "remove_player", player: a.player! };
  }
}

function replay(seed: number, games: number): number {
  const out = execFileSync(
    "go",
    ["run", "./cmd/difftrace", "-games", String(games), "-seed", String(seed)],
    { cwd: backendDir, maxBuffer: 1 << 30, encoding: "utf8" },
  );
  let st: State | null = null;
  let steps = 0;
  out.split("\n").forEach((line, n) => {
    if (!line.trim()) return;
    const rec = JSON.parse(line);
    const where = `seed ${seed}, trace line ${n + 1}`;
    if (rec.type === "game") {
      const dealt = newGame(
        { cardsPerPlayer: rec.cards, numDecks: 0 },
        rec.players,
        new StackedShuffler(rec.order),
      );
      st = dealt.state;
      deepStrictEqual(render(st, dealt.events), { views: rec.views, events: rec.events }, where);
      return;
    }
    steps++;
    const wa: WireAction = rec.action;
    let action: Action;
    if (wa.system) {
      action = systemAction(wa);
    } else {
      try {
        action = buildAction(wa.kind ?? "", wa.args, wa.actor!);
      } catch (e) {
        deepStrictEqual(
          { code: errorCode(e), message: (e as Error).message },
          rec.error,
          `${where}: build ${JSON.stringify(wa)}`,
        );
        return;
      }
    }
    const res = apply(st!, action);
    if (!res.ok) {
      deepStrictEqual(
        { code: errorCode(res.error), message: res.error.message },
        rec.error ?? "no error",
        `${where}: ${JSON.stringify(wa)}`,
      );
      return;
    }
    expect(rec.error, `${where}: Go rejected ${JSON.stringify(wa)}`).toBeUndefined();
    st = res.state;
    deepStrictEqual(render(st, res.events), { views: rec.views, events: rec.events }, `${where}: ${JSON.stringify(wa)}`);
  });
  return steps;
}

describe.skipIf(!haveGo)("matches the Go engine step for step", () => {
  for (const seed of [1, 2, 3, 4, 5, 6, 7, 8]) {
    it(`random games, seed ${seed}`, { timeout: 300_000 }, () => {
      expect(replay(seed, 150)).toBeGreaterThan(1000);
    });
  }
});
