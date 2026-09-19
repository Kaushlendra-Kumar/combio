import { beforeEach, describe, expect, it } from "vitest";

import {
  canAct,
  initialState,
  isMatchingOpen,
  myTurnPhase,
  playerName,
  reduce,
  resetKeys,
  type GameState,
} from "@/lib/game-state";
import type { PlayerView, ServerMessage } from "@/lib/protocol";

function view(over: Partial<PlayerView> = {}): PlayerView {
  return {
    me: "u1",
    phase: "turn_start",
    my_cards: [
      { position: 0, id: 11, deck: 0 },
      { position: 1, id: 12, deck: 0 },
    ],
    my_pending_count: 0,
    my_version: 3,
    opponents: [
      {
        id: "u2",
        seat: 1,
        cards: [{ position: 0, id: 21, deck: 0 }],
        version: 5,
        count: 1,
        pending_penalty: 0,
        connected: true,
        removed: false,
        turns_completed: 1,
      },
    ],
    turn_player: "u1",
    my_turn: true,
    deck_count: 30,
    discard_count: 2,
    discard_top: { id: 99, rank: 8, suit: 1, deck: 0 },
    active_rank: 0,
    my_drawn_card: null,
    my_pending_power: 0,
    declare_available: false,
    declared: false,
    declarer: "",
    paused: false,
    finished: false,
    ...over,
  };
}

function srv(msg: ServerMessage): { type: "server"; msg: ServerMessage } {
  return { type: "server", msg };
}

function withSession(): GameState {
  return reduce(
    initialState,
    srv({ v: 1, type: "welcome", data: { version: 1, player_id: "u1", name: "Ann" } }),
  );
}

beforeEach(() => resetKeys());

describe("reducer", () => {
  it("records the session the server assigns", () => {
    const s = withSession();
    expect(s.session).toEqual({ playerId: "u1", name: "Ann" });
  });

  it("replaces the view wholesale rather than merging", () => {
    let s = withSession();
    s = reduce(s, srv({ v: 1, type: "state_update", data: { view: view() } }));
    expect(s.view?.my_cards).toHaveLength(2);

    // The next update shows one card. The old second card must be gone, not
    // merged: a card the server stopped showing us cannot linger in state.
    const shrunk = view({ my_cards: [{ position: 0, id: 11, deck: 0 }], my_version: 4 });
    s = reduce(s, srv({ v: 1, type: "state_update", data: { view: shrunk } }));
    expect(s.view?.my_cards).toHaveLength(1);
    expect(JSON.stringify(s.view)).not.toContain('"id":12');
  });

  it("replaces the room roster wholesale", () => {
    let s = withSession();
    const room = (players: string[]) => ({
      code: "ABCD",
      public: true,
      status: "WAITING" as const,
      cards_per_player: 4,
      max_players: 8,
      players: players.map((id, i) => ({
        id,
        name: id.toUpperCase(),
        ready: false,
        connected: true,
        removed: false,
        host: i === 0,
      })),
    });
    s = reduce(s, srv({ v: 1, type: "room_state", data: room(["u1", "u2", "u3"]) }));
    expect(s.room?.players).toHaveLength(3);
    s = reduce(s, srv({ v: 1, type: "room_state", data: room(["u1"]) }));
    expect(s.room?.players).toHaveLength(1);
  });

  it("queues private reveals and drops them once consumed", () => {
    let s = withSession();
    s = reduce(
      s,
      srv({
        v: 1,
        type: "events",
        data: {
          events: [
            { name: "private_draw", data: { card: { id: 7, rank: 12, suit: 2, deck: 0 } } },
          ],
        },
      }),
    );
    expect(s.pendingReveals).toHaveLength(1);
    expect(s.pendingReveals[0]?.card.rank).toBe(12);

    const key = s.pendingReveals[0]!.key;
    s = reduce(s, { type: "consume-reveal", key });
    expect(s.pendingReveals).toHaveLength(0);
    // And the card is not retained anywhere else in state.
    expect(JSON.stringify(s)).not.toContain('"rank":12');
  });

  it("turns a memory reveal into one entry per card", () => {
    let s = withSession();
    s = reduce(
      s,
      srv({
        v: 1,
        type: "events",
        data: {
          events: [
            {
              name: "memory_revealed",
              data: {
                cards: [
                  { id: 1, rank: 3, suit: 0, deck: 0 },
                  { id: 2, rank: 9, suit: 1, deck: 0 },
                ],
                positions: [0, 2],
              },
            },
          ],
        },
      }),
    );
    expect(s.pendingReveals).toHaveLength(2);
    expect(s.pendingReveals[1]?.position).toBe(2);
  });

  it("shows a lost race as information, never as a penalty", () => {
    let s = withSession();
    s = reduce(s, srv({ v: 1, type: "error", data: { code: "stale", message: "gone" } }));
    expect(s.notice?.tone).toBe("info");
    expect(s.notice?.text).toContain("Too late");
    expect(s.notice?.text.toLowerCase()).not.toContain("wrong");
    expect(s.notice?.text.toLowerCase()).not.toContain("penalty");
  });

  it("does not stack duplicate error banners", () => {
    let s = withSession();
    const err = srv({ v: 1, type: "error", data: { code: "not_your_turn", message: "x" } });
    s = reduce(s, err);
    const first = s.notice;
    s = reduce(s, err);
    expect(s.notice).toBe(first);
  });

  it("maps a protocol error into a readable banner", () => {
    const s = reduce(withSession(), {
      type: "protocol-error",
      kind: "unsupported_version",
      message: "v99",
    });
    expect(s.notice?.text).toContain("out of date");
  });

  it("caps the event feed so a long game cannot grow without bound", () => {
    let s = withSession();
    for (let i = 0; i < 80; i++) {
      s = reduce(
        s,
        srv({ v: 1, type: "events", data: { events: [{ name: "card_drawn", data: { player: "u2" } }] } }),
      );
    }
    expect(s.feed.length).toBeLessThanOrEqual(40);
  });

  it("keeps the connection and session on leave but drops the game", () => {
    let s = withSession();
    s = reduce(s, { type: "connection", state: "open" });
    s = reduce(s, srv({ v: 1, type: "state_update", data: { view: view() } }));
    s = reduce(s, { type: "leave" });
    expect(s.view).toBeNull();
    expect(s.session?.playerId).toBe("u1");
    expect(s.connection).toBe("open");
  });
});

describe("selectors", () => {
  function ready(v: Partial<PlayerView> = {}): GameState {
    let s = withSession();
    s = reduce(s, { type: "connection", state: "open" });
    return reduce(s, srv({ v: 1, type: "state_update", data: { view: view(v) } }));
  }

  it("blocks acting while paused, finished or disconnected", () => {
    expect(canAct(ready())).toBe(true);
    expect(canAct(ready({ paused: true }))).toBe(false);
    expect(canAct(ready({ finished: true }))).toBe(false);
    const offline = reduce(ready(), { type: "connection", state: "closed" });
    expect(canAct(offline)).toBe(false);
  });

  it("detects an open matching window from the server view only", () => {
    expect(isMatchingOpen(ready())).toBe(false);
    expect(isMatchingOpen(ready({ phase: "matching", active_rank: 8 }))).toBe(true);
    // Phase without a rank is not a window.
    expect(isMatchingOpen(ready({ phase: "matching", active_rank: 0 }))).toBe(false);
  });

  it("reports which of my own panels applies", () => {
    expect(myTurnPhase(ready())).toBe("draw");
    expect(myTurnPhase(ready({ my_turn: false }))).toBeNull();
    expect(
      myTurnPhase(ready({ phase: "draw_decision", my_drawn_card: { id: 5, rank: 4, suit: 0, deck: 0 } })),
    ).toBe("decide");
    expect(myTurnPhase(ready({ phase: "power", my_pending_power: 11 }))).toBe("power");
    expect(myTurnPhase(ready({ phase: "king_decision", my_pending_power: 13 }))).toBe("king");
  });

  it("names players from the room roster, and says 'you' for me", () => {
    let s = withSession();
    s = reduce(
      s,
      srv({
        v: 1,
        type: "room_state",
        data: {
          code: "ABCD",
          public: true,
          status: "PLAYING",
          cards_per_player: 4,
          max_players: 8,
          players: [
            { id: "u1", name: "Ann", ready: true, connected: true, removed: false, host: true },
            { id: "u2", name: "Bo", ready: true, connected: true, removed: false, host: false },
          ],
        },
      }),
    );
    expect(playerName(s, "u1")).toBe("you");
    expect(playerName(s, "u2")).toBe("Bo");
    expect(playerName(s, "ghost")).toBe("ghost");
  });
});

describe("rematch", () => {
  it("clears the finished game when the room returns to the lobby", () => {
    let s = withSession();
    s = reduce(s, srv({ v: 1, type: "state_update", data: { view: view({ finished: true }) } }));
    s = reduce(
      s,
      srv({
        v: 1,
        type: "events",
        data: { events: [{ name: "private_draw", data: { card: { id: 7, rank: 12, suit: 2, deck: 0 } } }] },
      }),
    );
    expect(s.view).not.toBeNull();
    expect(s.pendingReveals).toHaveLength(1);

    const lobby = {
      code: "ABCD",
      public: true,
      status: "WAITING" as const,
      cards_per_player: 4,
      max_players: 8,
      players: [
        { id: "u1", name: "Ann", ready: false, connected: true, removed: false, host: true },
      ],
    };
    s = reduce(s, srv({ v: 1, type: "room_state", data: lobby }));

    // The old hand is gone, not merged into the next one.
    expect(s.view).toBeNull();
    expect(s.started).toBe(false);
    expect(s.pendingReveals).toHaveLength(0);
    expect(s.room?.status).toBe("WAITING");
  });

  it("does not clear a running game on a routine room_state", () => {
    let s = withSession();
    s = reduce(s, srv({ v: 1, type: "state_update", data: { view: view() } }));
    s = reduce(
      s,
      srv({
        v: 1,
        type: "room_state",
        data: {
          code: "ABCD",
          public: true,
          status: "PLAYING",
          cards_per_player: 4,
          max_players: 8,
          players: [
            { id: "u1", name: "Ann", ready: true, connected: true, removed: false, host: true },
          ],
        },
      }),
    );
    expect(s.view).not.toBeNull();
    expect(s.started).toBe(true);
  });
});
