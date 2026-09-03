"use client";

import { useRouter } from "next/navigation";
import { useEffect, useState } from "react";

import { loadSession, signInAsGuest, type Session } from "@/lib/auth";
import { Home } from "@/components/lobby/Home";

export default function Page() {
  const router = useRouter();
  const [session, setSession] = useState<Session | null>(null);
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    setSession(loadSession());
  }, []);

  const signIn = async () => {
    setBusy(true);
    setError(null);
    try {
      setSession(await signInAsGuest(name.trim() || "Player"));
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not reach the server.");
    } finally {
      setBusy(false);
    }
  };

  if (!session) {
    return (
      <main className="mx-auto flex min-h-[100dvh] max-w-md flex-col items-center justify-center gap-6 p-6">
        <div className="text-center">
          <h1 className="font-display text-5xl font-black tracking-tight text-amber-300">COMBIO</h1>
          <p className="mt-2 text-sm text-white/70">
            You can&apos;t see your own cards. Remember them, or lose.
          </p>
        </div>
        <div className="w-full space-y-3 rounded-xl bg-black/25 p-5">
          <label htmlFor="name" className="block text-sm text-white/80">
            What should we call you?
          </label>
          <input
            id="name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && signIn()}
            maxLength={24}
            placeholder="Player"
            className="w-full rounded-lg bg-white/10 px-3 py-2 text-white placeholder:text-white/40 focus:outline-none focus-visible:ring-2 focus-visible:ring-amber-300"
          />
          <button
            type="button"
            onClick={signIn}
            disabled={busy}
            className="w-full rounded-lg bg-amber-300 py-2 font-semibold text-ink hover:bg-amber-200 disabled:opacity-50 focus:outline-none focus-visible:ring-2 focus-visible:ring-white"
          >
            {busy ? "Connecting\u2026" : "Continue"}
          </button>
          {error ? <p className="text-sm text-rose-300">{error}</p> : null}
          <p className="rounded-lg bg-amber-400/15 px-3 py-2 text-[11px] text-amber-100">
            <strong>Development sign-in.</strong> There is no password and no account. Anyone who
            can reach this server can claim any name. Real accounts come later.
          </p>
        </div>
      </main>
    );
  }

  return <Home session={session} onEnterRoom={(code) => router.push(`/room/${code}`)} />;
}
