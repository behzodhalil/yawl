import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { fileURLToPath } from "node:url";

const here = (p: string) => fileURLToPath(new URL(p, import.meta.url));
const appRouter = here("../src/router.ts"), demoRouter = here("./router.ts");

export default defineConfig({
  root: here("."),
  base: "./",
  plugins: [
    react(),
    {
      name: "demo-router",
      enforce: "pre",
      async resolveId(source, importer, opts) {
        if (!importer || importer === demoRouter || !/router(\.ts)?$/.test(source)) return null;
        const r = await this.resolve(source, importer, { ...opts, skipSelf: true });
        return r?.id === appRouter ? demoRouter : null;
      },
    },
  ],
  build: { outDir: process.env.DEMO_OUT || here("dist"), emptyOutDir: true, target: "es2022" },
});
