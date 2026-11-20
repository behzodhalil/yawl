import { handle } from "./routes";
import { tick } from "./tick";
import type { Env } from "./types";

export default {
  fetch(req: Request, env: Env): Promise<Response> {
    return handle(req, env, Date.now());
  },
  async scheduled(_c: ScheduledController, env: Env): Promise<void> {
    await tick(env.DB, env, Date.now(), fetch);
  },
} satisfies ExportedHandler<Env>;
