import { beforeEach, describe, expect, it, vi } from "vitest";
import { api, ApiError } from "./api";

function answer(status: number, body: string) {
  return vi.fn(async (_url: string, _init?: RequestInit) => new Response(status === 204 ? null : body, { status }));
}

describe("api", () => {
  beforeEach(() => history.replaceState(null, "", "/servers"));

  it("sends the request header and JSON", async () => {
    const f = answer(200, `{"ok":true}`);
    vi.stubGlobal("fetch", f);
    expect(await api("POST", "/api/projects", { name: "acme" })).toEqual({ ok: true });
    const init = f.mock.calls[0][1]!;
    expect((init.headers as Record<string, string>)["X-Yawl-Request"]).toBe("1");
    expect(init.credentials).toBe("same-origin");
    expect(init.body).toBe(`{"name":"acme"}`);
  });

  it("goes to sign-in on 401", async () => {
    vi.stubGlobal("fetch", answer(401, "Your session has ended. Sign in again."));
    await expect(api("GET", "/api/projects")).rejects.toBeInstanceOf(ApiError);
    expect(location.pathname).toBe("/sign-in");
  });

  it("throws the server's text", async () => {
    vi.stubGlobal("fetch", answer(409, "Project acme already exists.\n"));
    await expect(api("POST", "/api/projects", {})).rejects.toMatchObject({ status: 409, message: "Project acme already exists." });
  });

  it("returns nothing for 204", async () => {
    vi.stubGlobal("fetch", answer(204, ""));
    expect(await api("DELETE", "/api/tokens/t1")).toBeUndefined();
  });
});
