import { cloudflareTest, readD1Migrations } from "@cloudflare/vitest-plugin";
import { configDefaults, defineConfig } from "vitest/config";

export default defineConfig(async () => ({
  plugins: [
    cloudflareTest({
      main: "./src/index.ts",
      miniflare: {
        compatibilityDate: "2026-10-01",
        d1Databases: ["DB"],
        bindings: {
          TEST_MIGRATIONS: await readD1Migrations("./migrations"),
          PANEL_KEY: "07".repeat(32),
          BOT_TOKEN: "bot-token",
          CHAT_ID: "42",
          TELEGRAM_API: "http://telegram.test",
        },
      },
    }),
  ],
  test: { setupFiles: ["./test/apply-migrations.ts"], exclude: [...configDefaults.exclude, "relay/**"] },
}));
