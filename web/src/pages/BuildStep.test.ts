import { describe, expect, it } from "vitest";
import { choiceFor, stepsFor, yamlOf } from "./BuildStep";
import { emptyForm } from "../serviceForm";

describe("yamlOf", () => {
  const form = { ...emptyForm("web"), name: "storefront", repo: "https://github.com/acme/storefront", port: 3000 };

  it("writes no steps while the builder decides", () => {
    const y = yamlOf(form);
    expect(y.code).toBe("services:\n  - name: storefront\n    kind: web\n    repo: https://github.com/acme/storefront\n    port: 3000\n");
    expect(y.fresh).toEqual([]);
  });

  it("writes only the steps that are set, and marks them", () => {
    const y = yamlOf({ ...form, root: "apps/web", steps: { install: "", build: "npm run build:prod", output: "dist" } });
    expect(y.code).toContain("    root: apps/web\n    steps:\n      build: npm run build:prod\n      output: dist\n");
    expect(y.fresh).toEqual([7, 8, 9]);
  });

  it("Other writes the real builder line and nothing new", () => {
    expect(yamlOf({ ...form, builder: "railpack" })).toMatchObject({ fresh: [] });
  });
});

describe("stepsFor", () => {
  it("static takes the preset's output folder and drops the start command", () => {
    expect(stepsFor(choiceFor("vite"), { start: "npm run preview", build: "vite build" })).toEqual({ start: "", build: "vite build", output: "dist" });
  });
  it("keeps an output folder already typed", () => {
    expect(stepsFor(choiceFor("astro"), { output: "public" }).output).toBe("public");
  });
  it("server drops the output folder", () => {
    expect(stepsFor({ framework: "vite", render: "server" }, { output: "dist" })).toEqual({ output: "" });
  });
});
