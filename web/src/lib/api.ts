// Typed fetch wrapper for the Lineup API. Attaches a fresh Firebase ID
// token per call (the SDK caches and refreshes internally, so this is
// cheap) and normalizes the API's {"error":"<code>"} bodies to ApiError.

import { auth } from "@/lib/firebase";
import { config } from "@/lib/config";

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;

  constructor(status: number, code: string) {
    super(`api ${status}: ${code}`);
    this.status = status;
    this.code = code;
  }
}

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const user = auth.currentUser;
  if (!user) {
    throw new ApiError(401, "no_session");
  }
  const token = await user.getIdToken();
  const headers = new Headers(init.headers);
  headers.set("Authorization", `Bearer ${token}`);
  if (!headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  const res = await fetch(`${config.apiUrl}${path}`, { ...init, headers });
  if (!res.ok) {
    let code = "unknown";
    try {
      const body: unknown = await res.json();
      if (body && typeof body === "object" && "error" in body && typeof body.error === "string") {
        code = body.error;
      }
    } catch {
      // non-JSON error body; keep "unknown"
    }
    throw new ApiError(res.status, code);
  }
  return (await res.json()) as T;
}
