"use client";

import { useEffect, useRef, type ReactNode } from "react";

export function Modal({
  open,
  title,
  children,
  onClose,
  closeLabel = "Close",
}: {
  open: boolean;
  title: string;
  children: ReactNode;
  onClose?: () => void;
  closeLabel?: string;
}) {
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (open) ref.current?.focus();
  }, [open]);

  if (!open) return null;
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4">
      <div
        ref={ref}
        tabIndex={-1}
        role="dialog"
        aria-modal="true"
        aria-label={title}
        className="animate-pop w-full max-w-md rounded-xl bg-felt-dark p-5 text-white shadow-2xl ring-1 ring-white/10 focus:outline-none"
      >
        <h2 className="mb-3 font-display text-lg font-bold">{title}</h2>
        {children}
        {onClose ? (
          <button
            type="button"
            onClick={onClose}
            className="mt-4 w-full rounded-lg bg-white/10 py-2 text-sm hover:bg-white/20 focus:outline-none focus-visible:ring-2 focus-visible:ring-amber-300"
          >
            {closeLabel}
          </button>
        ) : null}
      </div>
    </div>
  );
}
