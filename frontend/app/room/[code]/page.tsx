"use client";

import { useParams, useRouter } from "next/navigation";
import { useEffect, useState } from "react";

import { loadSession, type Session } from "@/lib/auth";
import { RoomScreen } from "@/components/lobby/RoomScreen";

export default function RoomPage() {
  const params = useParams<{ code: string }>();
  const router = useRouter();
  const [session, setSession] = useState<Session | null>(null);
  const [ready, setReady] = useState(false);

  useEffect(() => {
    const s = loadSession();
    setSession(s);
    setReady(true);
    if (!s) router.replace("/");
  }, [router]);

  const code = (params?.code ?? "").toString().toUpperCase();

  if (!ready) return null;
  if (!session) return null;

  return <RoomScreen session={session} code={code} onLeave={() => router.push("/")} />;
}
