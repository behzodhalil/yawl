import type { D1Migration } from "@cloudflare/vitest-plugin";
import type { Env as RelayEnv } from "../src/types";

declare global {
  namespace Cloudflare {
    interface Env extends RelayEnv {
      TEST_MIGRATIONS: D1Migration[];
    }
    interface GlobalProps {
      mainModule: typeof import("../src/index");
    }
  }
}
