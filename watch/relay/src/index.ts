import { handle } from "./api";
import type { Env } from "./types";

export default {
  // fetch is wrapped: called as d.fetch it would lose its this, and Workers throws "Illegal invocation"
  fetch(req: Request, env: Env): Promise<Response> {
    return handle(req, env, { now: Date.now(), fetch: (input, init) => fetch(input, init), sleep: (ms) => new Promise((r) => setTimeout(r, ms)) });
  },
} satisfies ExportedHandler<Env>;
