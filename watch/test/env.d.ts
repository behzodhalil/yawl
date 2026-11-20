import type { D1Migration } from "@cloudflare/vitest-plugin";
import type { Env as WatchEnv } from "../src/types";

declare global {
  namespace Cloudflare {
    interface Env extends WatchEnv {
      TEST_MIGRATIONS: D1Migration[];
    }
  }
}
