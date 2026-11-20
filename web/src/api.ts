import { navigate } from "./router";

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message);
  }
}

export async function api<T = unknown>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { "X-Yawl-Request": "1" };
  if (body !== undefined) headers["Content-Type"] = "application/json";
  const res = await fetch(path, {
    method,
    headers,
    credentials: "same-origin",
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await res.text();
  if (res.status === 401 && path !== "/api/session") navigate("/sign-in");
  if (!res.ok) throw new ApiError(res.status, text.trim() || res.statusText);
  if (res.status === 204 || text === "") return undefined as T;
  return JSON.parse(text) as T;
}
