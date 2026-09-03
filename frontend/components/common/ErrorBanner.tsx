"use client";

import type { Notice } from "@/lib/game-state";

export function ErrorBanner({ notice, onDismiss }: { notice: Notice | null; onDismiss: () => void }) {
  if (!notice) return null;
  const tone = notice.tone === "info" ? "bg-sky-500/90" : "bg-rose-600/95";
  return (
    <div
      role="alert"
      className={`animate-slideUp pointer-events-auto flex items-center gap-3 rounded-lg px-4 py-2 text-sm text-white shadow-lg ${tone}`}
    >
      <span className="flex-1">{notice.text}</span>
      <button
        type="button"
        onClick={onDismiss}
        aria-label="Dismiss message"
        className="rounded px-2 py-0.5 text-white/80 hover:bg-white/20 focus:outline-none focus-visible:ring-2 focus-visible:ring-white"
      >
        &times;
      </button>
    </div>
  );
}
