"use client";

import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useReducer,
  useRef,
  type ReactNode,
} from "react";

import { A, C, type ActionArgs, type ActionKind, type PlayerId } from "./protocol";
import { GameClient, type ConnectionState } from "./websocket";
import { env } from "./env";
import { type Session } from "./auth";
import { initialState, reduce, type GameState } from "./game-state";

/**
 * Every action the UI can send.
 *
 * Note what is absent from every call: an actor. The server takes that from
 * the authenticated socket, so there is nothing here for a client to forge.
 */
export interface Actions {
  createRoom(opts: { public: boolean; cardsPerPlayer: number; maxPlayers: number }): void;
  joinRoom(code: string): void;
  reconnectRoom(code: string): void;
  leaveRoom(): void;
  setReady(ready: boolean): void;
  startGame(): void;
  /** Return a finished table to the lobby with the same people in it. */
  rematch(): void;

  inspectMemory(positions: number[]): void;
  drawCard(): void;
  keepDrawn(removeIndex: number, insertIndex: number): void;
  keepDrawnAsFirstCard(): void;
  discardDrawn(): void;

  attemptOwnMatch(index: number, version: number): void;
  attemptOpponentMatch(args: {
    target: PlayerId;
    targetIndex: number;
    targetVersion: number;
    giveIndex: number;
    giveVersion: number;
  }): void;
  insertPenalty(trayIndex: number, position: number): void;

  peek10(index: number): void;
  jackPeek(target: PlayerId, targetIndex: number): void;
  queenSwap(ownIndex: number, target: PlayerId, targetIndex: number): void;
  kingPeek(target: PlayerId, targetIndex: number): void;
  kingSwap(ownIndex: number): void;
  cancelKingSwap(): void;
  skipPower(): void;

  declareCombio(): void;
  dismissNotice(): void;
  consumeReveal(key: string): void;
}

type LocalDispatch = (
  a: { type: "dismiss-notice" } | { type: "consume-reveal"; key: string } | { type: "leave" },
) => void;

/**
 * Binds the action surface to a client that may not exist yet. The getter is
 * read at call time, so these closures stay valid across reconnects and never
 * capture a stale socket.
 */
export function makeActions(getClient: () => GameClient | null, dispatch: LocalDispatch): Actions {
  const raw = { send: (t: Parameters<GameClient["send"]>[0], d?: Parameters<GameClient["send"]>[1]) => getClient()?.send(t, d) };
  const send = (kind: ActionKind, args?: ActionArgs) => {
    raw.send(C.Action, args && Object.keys(args).length > 0 ? { kind, args } : { kind });
  };
  return {
    createRoom: (o) =>
      raw.send(C.CreateRoom, {
        public: o.public,
        cards_per_player: o.cardsPerPlayer,
        max_players: o.maxPlayers,
      }),
    joinRoom: (code) => raw.send(C.JoinRoom, { code }),
    reconnectRoom: (code) => raw.send(C.Reconnect, { code }),
    leaveRoom: () => {
      raw.send(C.LeaveRoom);
      dispatch({ type: "leave" });
    },
    setReady: (ready) => raw.send(C.Ready, { ready }),
    startGame: () => raw.send(C.StartGame),
    rematch: () => raw.send(C.Rematch),

    inspectMemory: (positions) => send(A.InspectMemory, { positions }),
    drawCard: () => send(A.DrawCard),
    keepDrawn: (remove_index, insert_index) => send(A.KeepDrawn, { remove_index, insert_index }),
    keepDrawnAsFirstCard: () => send(A.KeepDrawn),
    discardDrawn: () => send(A.DiscardDrawn),

    attemptOwnMatch: (index, version) => send(A.AttemptOwnMatch, { index, version }),
    attemptOpponentMatch: (a) =>
      send(A.AttemptOpponentMatch, {
        target: a.target,
        target_index: a.targetIndex,
        target_version: a.targetVersion,
        give_index: a.giveIndex,
        give_version: a.giveVersion,
      }),
    insertPenalty: (tray_index, position) => send(A.InsertPenalty, { tray_index, position }),

    peek10: (index) => send(A.Peek10, { index }),
    jackPeek: (target, target_index) => send(A.JackPeek, { target, target_index }),
    queenSwap: (own_index, target, target_index) =>
      send(A.QueenSwap, { own_index, target, target_index }),
    kingPeek: (target, target_index) => send(A.KingPeek, { target, target_index }),
    kingSwap: (own_index) => send(A.KingSwap, { own_index }),
    cancelKingSwap: () => send(A.CancelKingSwap),
    skipPower: () => send(A.SkipPower),

    declareCombio: () => send(A.DeclareCombio),
    dismissNotice: () => dispatch({ type: "dismiss-notice" }),
    consumeReveal: (key) => dispatch({ type: "consume-reveal", key }),
  };
}

interface Store {
  state: GameState;
  actions: Actions;
  connection: ConnectionState;
}

const StoreContext = createContext<Store | null>(null);

export function GameProvider({
  session,
  children,
}: {
  session: Session;
  children: ReactNode;
}) {
  const [state, dispatch] = useReducer(reduce, initialState);
  const clientRef = useRef<GameClient | null>(null);

  useEffect(() => {
    // One client per session. Strict Mode mounts twice; `connect()` is
    // idempotent and the cleanup disposes the socket for good.
    const client = new GameClient({
      url: env.wsUrl,
      token: session.token,
      handlers: {
        onMessage: (msg) => dispatch({ type: "server", msg }),
        onState: (s) => dispatch({ type: "connection", state: s }),
        onProtocolError: (err) =>
          dispatch({ type: "protocol-error", kind: err.kind, message: err.message }),
      },
    });
    clientRef.current = client;
    client.connect();
    return () => {
      client.close();
      clientRef.current = null;
    };
  }, [session.token]);

  const actions = useMemo(
    () => makeActions(() => clientRef.current, dispatch),
    [],
  );

  const value = useMemo<Store>(
    () => ({ state, actions, connection: state.connection }),
    [state, actions],
  );

  return <StoreContext.Provider value={value}>{children}</StoreContext.Provider>;
}

export function useGame(): Store {
  const ctx = useContext(StoreContext);
  if (!ctx) throw new Error("useGame must be used inside <GameProvider>");
  return ctx;
}
