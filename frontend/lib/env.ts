/**
 * All backend endpoints come from here. Nothing else in the app may hardcode a
 * host, so the same build points at a local server or a deployed one.
 */
const API = process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080";
const WS = process.env.NEXT_PUBLIC_WS_URL ?? "ws://localhost:8080/ws";

export const env = {
  apiUrl: API.replace(/\/$/, ""),
  wsUrl: WS,
};
