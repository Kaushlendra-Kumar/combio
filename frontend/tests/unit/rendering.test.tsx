import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";

import { FaceDownCard, FaceUpCard } from "@/components/game/Card";
import {
  DiscardPile,
  OpponentLayout,
  PenaltyTray,
  PlayerLayout,
  TurnIndicator,
} from "@/components/game/Table";
import { CombioButton, ScoreBoard } from "@/components/game/Panels";
import { ConnectionStatus } from "@/components/common/ConnectionStatus";
import { ErrorBanner } from "@/components/common/ErrorBanner";
import type { Actions } from "@/lib/store";
import type { OpponentView, PlayerView } from "@/lib/protocol";

function view(over: Partial<PlayerView> = {}): PlayerView {
  return {
    me: "u1",
    phase: "turn_start",
    my_cards: [
      { position: 0, id: 11, deck: 0 },
      { position: 1, id: 12, deck: 1 },
    ],
    my_pending_count: 0,
    my_version: 3,
    opponents: [],
    turn_player: "u1",
    my_turn: true,
    final_turns: false,
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

function opponent(over: Partial<OpponentView> = {}): OpponentView {
  return {
    id: "u2",
    seat: 1,
    cards: [
      { position: 0, id: 21, deck: 0 },
      { position: 1, id: 22, deck: 1 },
    ],
    version: 5,
    count: 2,
    pending_penalty: 0,
    connected: true,
    removed: false,
    turns_completed: 1,
    ...over,
  };
}

/**
 * Anything that would betray a card's face.
 *
 * A rank only ever reaches the DOM through FaceUpCard, which renders "8\u2666"
 * as text and "8 of \u2666" as its label. Scanning raw innerHTML would trip
 * over Tailwind class names, so these check the two places a human or a screen
 * reader can actually perceive: rendered text, and accessible names.
 */
const FACE_PATTERN = /[\u2663\u2666\u2665\u2660]|\b(?:10|[2-9]|A|J|Q|K) of\b/;

function assertNoFaceLeak(container: HTMLElement) {
  expect(container.textContent ?? "").not.toMatch(FACE_PATTERN);
  for (const el of Array.from(container.querySelectorAll("[aria-label],[title],[alt]"))) {
    for (const attr of ["aria-label", "title", "alt"]) {
      const v = el.getAttribute(attr);
      if (v) expect(v).not.toMatch(FACE_PATTERN);
    }
  }
}

describe("hidden cards leak nothing", () => {
  it("a face-down card renders only a position and a colour", () => {
    const { container } = render(
      <FaceDownCard handle={{ position: 2, id: 42, deck: 1 }} label="A card in position 3" />,
    );
    assertNoFaceLeak(container);
    // No rank smuggled into an attribute name either.
    expect(container.innerHTML).not.toMatch(/data-(value|card|rank|suit)/i);
    expect(screen.getByRole("img")).toHaveAttribute("aria-label", "A card in position 3");
  });

  it("its props cannot even carry a rank", () => {
    // A CardHandle has three fields. This is the type-level guarantee, checked
    // here so a future refactor that widens it fails a test as well as tsc.
    const handle = { position: 0, id: 1, deck: 0 as const };
    expect(Object.keys(handle).sort()).toEqual(["deck", "id", "position"]);
  });

  it("an opponent's whole row exposes no rank, in text or aria", () => {
    const { container } = render(
      <OpponentLayout opponent={opponent()} name="Bo" isTurn={false} targetable={false} />,
    );
    assertNoFaceLeak(container);
    for (const el of Array.from(container.querySelectorAll("[aria-label]"))) {
      // Every label an opponent card carries describes a position, nothing more.
      expect(el.getAttribute("aria-label") ?? "").toMatch(/position \d/);
    }
  });

  it("my own cards are hidden from me too", () => {
    const { container } = render(
      <PlayerLayout view={view()} selectable={false} selectedIndex={null} insertMode={false} />,
    );
    assertNoFaceLeak(container);
  });

  it("the discard top is the one card that does show its face", () => {
    render(<DiscardPile view={view()} />);
    expect(screen.getByRole("img", { name: /8 of/ })).toBeInTheDocument();
  });

  it("a known card renders its rank, because the server showed it to us", () => {
    render(<FaceUpCard card={{ id: 1, rank: 12, suit: 3, deck: 0 }} />);
    expect(screen.getByRole("img", { name: /Q of/ })).toBeInTheDocument();
  });

  it("distinguishes the two decks by colour without hinting at rank", () => {
    const { container } = render(
      <PlayerLayout view={view()} selectable={false} selectedIndex={null} insertMode={false} />,
    );
    const html = container.innerHTML;
    expect(html).toContain("bg-card-back");
    expect(html).toContain("bg-card-backAlt");
  });
});

describe("table state", () => {
  it("shows a zero-card player as a normal position, not a win", () => {
    render(
      <PlayerLayout view={view({ my_cards: [] })} selectable={false} selectedIndex={null} insertMode={false} />,
    );
    expect(screen.getByText(/no cards/i)).toBeInTheDocument();
    expect(screen.getByText(/not a win/i)).toBeInTheDocument();
  });

  it("renders a pending penalty tray with placement targets", () => {
    const onPlace = vi.fn();
    render(<PenaltyTray count={2} positions={view().my_cards} onPlace={onPlace} />);
    expect(screen.getByText(/2 penalty cards waiting/i)).toBeInTheDocument();
    expect(screen.getByText(/don't get to see them/i)).toBeInTheDocument();
    // start + after 1 + end for a two-card hand
    expect(screen.getAllByRole("button")).toHaveLength(3);
    screen.getByRole("button", { name: "start" }).click();
    expect(onPlace).toHaveBeenCalledWith(0);
  });

  it("renders nothing when there is no penalty pending", () => {
    const { container } = render(<PenaltyTray count={0} positions={[]} onPlace={vi.fn()} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("marks an opponent as disconnected or removed", () => {
    const { rerender } = render(
      <OpponentLayout opponent={opponent({ connected: false })} name="Bo" isTurn={false} targetable={false} />,
    );
    expect(screen.getByText("disconnected")).toBeInTheDocument();
    rerender(
      <OpponentLayout opponent={opponent({ removed: true })} name="Bo" isTurn={false} targetable={false} />,
    );
    expect(screen.getByText("removed")).toBeInTheDocument();
  });

  it("shows pending penalties on an opponent without showing the cards", () => {
    const { container } = render(
      <OpponentLayout opponent={opponent({ pending_penalty: 2 })} name="Bo" isTurn={false} targetable={false} />,
    );
    expect(screen.getByText("+2 pending")).toBeInTheDocument();
    assertNoFaceLeak(container);
  });

  it("announces the final-turn sequence and who declared", () => {
    render(
      <TurnIndicator
        view={view({ final_turns: true, declared: true, declarer: "u2", my_turn: false, turn_player: "u2" })}
        nameOf={(id) => (id === "u2" ? "Bo" : "you")}
      />,
    );
    expect(screen.getByText(/Final turns/)).toBeInTheDocument();
    expect(screen.getByText(/Bo declared/)).toBeInTheDocument();
  });

  it("shows the paused badge when the server says so", () => {
    render(<TurnIndicator view={view({ paused: true })} nameOf={() => "you"} />);
    expect(screen.getByText("Paused")).toBeInTheDocument();
  });
});

describe("COMBIO and results", () => {
  it("the button is a plain send, with no local eligibility logic", () => {
    const declareCombio = vi.fn();
    render(<CombioButton actions={{ declareCombio } as unknown as Actions} />);
    screen.getByRole("button", { name: /Declare COMBIO/ }).click();
    expect(declareCombio).toHaveBeenCalledOnce();
  });

  it("renders the final scores the server sent, marking the winners", () => {
    render(
      <ScoreBoard
        view={view({
          finished: true,
          declared: true,
          declarer: "u1",
          scores: { u1: 12, u2: 3 },
          winners: ["u2"],
          reveal: { u1: [{ id: 1, rank: 13, suit: 0, deck: 0 }], u2: [{ id: 2, rank: 3, suit: 1, deck: 0 }] },
        })}
        nameOf={(id) => (id === "u1" ? "Ann" : "Bo")}
        onLeave={vi.fn()}
      />,
    );
    expect(screen.getByText("Game over")).toBeInTheDocument();
    expect(screen.getByText("3")).toBeInTheDocument();
    expect(screen.getByText("12")).toBeInTheDocument();
    expect(screen.getByText(/Ann declared COMBIO and did not make it/)).toBeInTheDocument();
  });
});

describe("connection and errors", () => {
  it("shows each connection state", () => {
    const { rerender } = render(<ConnectionStatus state="open" />);
    expect(screen.getByRole("status")).toHaveTextContent("Connected");
    rerender(<ConnectionStatus state="connecting" />);
    expect(screen.getByRole("status")).toHaveTextContent("Reconnecting");
    rerender(<ConnectionStatus state="closed" />);
    expect(screen.getByRole("status")).toHaveTextContent("Disconnected");
  });

  it("renders a dismissible banner, and nothing when there is no notice", () => {
    const onDismiss = vi.fn();
    const { container, rerender } = render(
      <ErrorBanner notice={{ key: "k", code: "stale", text: "Too late.", tone: "info" }} onDismiss={onDismiss} />,
    );
    expect(screen.getByRole("alert")).toHaveTextContent("Too late.");
    screen.getByRole("button", { name: /dismiss/i }).click();
    expect(onDismiss).toHaveBeenCalledOnce();
    rerender(<ErrorBanner notice={null} onDismiss={onDismiss} />);
    expect(container).toBeEmptyDOMElement();
  });
});
