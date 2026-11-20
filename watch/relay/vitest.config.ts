import { cloudflareTest, readD1Migrations } from "@cloudflare/vitest-plugin";
import { defineConfig } from "vitest/config";

const sent: { method: string; body: unknown }[] = [];
async function telegram(req: Request): Promise<Response> {
  const url = new URL(req.url);
  if (url.host !== "telegram.test") return new Response("no network in tests", { status: 502 });
  if (url.pathname === "/sent") return Response.json(sent.splice(0));
  sent.push({ method: url.pathname.split("/").pop()!, body: await req.json() });
  return Response.json({ ok: true, result: true });
}

export default defineConfig(async () => ({
  plugins: [
    cloudflareTest({
      main: "./src/index.ts",
      miniflare: {
        compatibilityDate: "2026-10-01",
        d1Databases: ["DB"],
        outboundService: telegram,
        bindings: {
          TEST_MIGRATIONS: await readD1Migrations("./migrations"),
          BOT_TOKEN: "bot-token",
          WEBHOOK_SECRET: "hook-secret",
          BOT_USERNAME: "useyawl_bot",
          TELEGRAM_API: "http://telegram.test",
        },
      },
    }),
  ],
  test: { setupFiles: ["./test/apply-migrations.ts"] },
}));
