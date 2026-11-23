import { describe, expect, it } from "vitest";
import { render } from "@testing-library/react";
import { Icon } from "./ui";
import { envIcon } from "./icons";

describe("Icon", () => {
  it("a brand name is its mark, drawn in the text colour", () => {
    const { container } = render(<Icon name="github" />);
    const mark = container.querySelector(".ico.brandmark svg")!;
    expect(mark).toBeTruthy();
    expect(mark.outerHTML).toContain('fill="currentColor"');
  });
  it("every mark is one colour, ours: no paint of its own is left", () => {
    for (const name of ["gitlab", "slack", "postgres", "lovable", "mysql"]) {
      const svg = render(<Icon name={name} />).container.querySelector(".brandmark svg")!.outerHTML.replace(/<mask[\s\S]*?<\/mask>/g, "");
      expect(svg, name).not.toMatch(/(fill|stroke)(="|:)\s*(#|rgb|url)/i);
    }
  });
  it("any other name is an interface icon, and an unknown name draws nothing", () => {
    expect(render(<Icon name="plus" />).container.querySelector("svg.ico")).toBeTruthy();
    expect(render(<Icon name="no-such-icon" />).container.innerHTML).toBe("");
  });
});

describe("envIcon", () => {
  it("production is full, previews stand apart, dev is one step, the rest sit between", () => {
    expect(envIcon("production")).toBe("env-production");
    expect(envIcon("pr-42")).toBe("env-preview");
    expect(envIcon("dev")).toBe("env-development");
    expect(envIcon("development")).toBe("env-development");
    expect(envIcon("staging")).toBe("env-staging");
    expect(envIcon("uat")).toBe("env-staging");
  });
  it("every environment icon draws", () => {
    for (const env of ["production", "pr-1", "dev", "qa"]) {
      expect(render(<Icon name={envIcon(env)} />).container.querySelector("svg.ico rect"), env).toBeTruthy();
    }
  });
});
