"use client";

import {
  type Card as CardData,
  type CardHandle,
  type DeckColor,
  rankLabel,
  suitIsRed,
  suitLabel,
} from "@/lib/protocol";

/**
 * A card back.
 *
 * Its props are a `CardHandle` and a label. A handle has no rank and no suit,
 * so this component has nothing to leak — not through text, not through a
 * title, not through an aria-label, not through a data attribute. That is the
 * point: hidden cards are a different type from known ones.
 */
export function FaceDownCard({
  handle,
  label,
  selected = false,
  highlighted = false,
  onClick,
  disabled = false,
  size = "md",
}: {
  handle: CardHandle;
  /** Screen-reader text. Must never describe the card's value. */
  label: string;
  selected?: boolean;
  highlighted?: boolean;
  onClick?: () => void;
  disabled?: boolean;
  size?: "sm" | "md";
}) {
  const back: Record<DeckColor, string> = {
    0: "bg-card-back",
    1: "bg-card-backAlt",
  };
  const dims = size === "sm" ? "h-12 w-9 text-[10px]" : "h-20 w-14 text-xs sm:h-24 sm:w-16";
  const interactive = !!onClick && !disabled;

  const content = (
    <>
      <span
        aria-hidden="true"
        className="absolute inset-1 rounded-[3px] border border-white/25 bg-[repeating-linear-gradient(45deg,transparent,transparent_4px,rgba(255,255,255,0.12)_4px,rgba(255,255,255,0.12)_8px)]"
      />
      <span className="relative font-semibold text-white/85">{handle.position + 1}</span>
    </>
  );

  const classes = [
    "relative flex items-center justify-center rounded-md shadow-sm ring-1 ring-black/20 transition",
    dims,
    back[handle.deck] ?? back[0],
    selected ? "ring-2 ring-amber-300 -translate-y-2" : "",
    highlighted ? "ring-2 ring-sky-300" : "",
    interactive ? "cursor-pointer hover:-translate-y-1" : "",
    disabled && onClick ? "opacity-60" : "",
  ].join(" ");

  if (!onClick) {
    return (
      <div className={classes} role="img" aria-label={label}>
        {content}
      </div>
    );
  }
  return (
    <button
      type="button"
      className={`${classes} focus:outline-none focus-visible:ring-2 focus-visible:ring-amber-200`}
      aria-label={label}
      aria-pressed={selected}
      disabled={disabled}
      onClick={onClick}
    >
      {content}
    </button>
  );
}

/** A card the server has shown us. Only ever rendered for known cards. */
export function FaceUpCard({
  card,
  caption,
  size = "md",
  onClick,
  label,
}: {
  card: CardData;
  caption?: string;
  size?: "sm" | "md" | "lg";
  onClick?: () => void;
  label?: string;
}) {
  const dims =
    size === "sm"
      ? "h-12 w-9 text-sm"
      : size === "lg"
        ? "h-28 w-20 text-3xl"
        : "h-20 w-14 text-xl sm:h-24 sm:w-16";
  const red = suitIsRed(card.suit);
  const a11y = label ?? `${rankLabel(card.rank)} of ${suitLabel(card.suit)}`;

  const body = (
    <>
      <span className={red ? "text-rose-600" : "text-ink"}>
        {rankLabel(card.rank)}
        {suitLabel(card.suit)}
      </span>
      <span
        aria-hidden="true"
        title={card.deck === 1 ? "blue deck" : "red deck"}
        className={`absolute right-1 top-1 h-2 w-2 rounded-full ${
          card.deck === 1 ? "bg-card-backAlt" : "bg-card-back"
        }`}
      />
      {caption ? (
        <span className="absolute -bottom-5 left-0 right-0 text-center text-[10px] text-white/70">
          {caption}
        </span>
      ) : null}
    </>
  );

  const classes = `relative flex items-center justify-center rounded-md bg-card-face font-display font-bold shadow-md ring-1 ring-black/20 ${dims}`;

  if (onClick) {
    return (
      <button
        type="button"
        onClick={onClick}
        aria-label={a11y}
        className={`${classes} transition hover:-translate-y-1 focus:outline-none focus-visible:ring-2 focus-visible:ring-amber-300`}
      >
        {body}
      </button>
    );
  }
  return (
    <div className={classes} role="img" aria-label={a11y}>
      {body}
    </div>
  );
}

/** An insertion point between cards. */
export function CardSlot({
  label,
  onClick,
  active = false,
}: {
  label: string;
  onClick?: () => void;
  active?: boolean;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-label={label}
      className={`h-20 w-6 rounded border-2 border-dashed transition sm:h-24 ${
        active
          ? "border-amber-300 bg-amber-300/20"
          : "border-white/25 hover:border-amber-200 hover:bg-white/10"
      } focus:outline-none focus-visible:ring-2 focus-visible:ring-amber-300`}
    >
      <span aria-hidden="true" className="text-white/50">
        +
      </span>
    </button>
  );
}
