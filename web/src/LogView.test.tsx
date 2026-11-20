import { describe, expect, it } from "vitest";
import { render } from "@testing-library/react";
import { LogLines } from "./LogView";

describe("LogLines", () => {
  it("makes URLs links that open a new tab, leaving the sentence's full stop as text", () => {
    const { container } = render(<div><LogLines rows={[{ text: "Listening on http://localhost:3000. See https://docs.example.com/a?b=1" }]} /></div>);
    const a = [...container.querySelectorAll("a")];
    expect(a.map((x) => x.getAttribute("href"))).toEqual(["http://localhost:3000", "https://docs.example.com/a?b=1"]);
    expect(a.every((x) => x.target === "_blank" && x.rel.includes("noopener"))).toBe(true);
    expect(container.querySelector(".tx")!.textContent).toBe("Listening on http://localhost:3000. See https://docs.example.com/a?b=1");
  });

  it("never links a script URL", () => {
    const { container } = render(<div><LogLines rows={[{ text: "javascript:alert(1) <a href=x>y</a>" }]} /></div>);
    expect(container.querySelector("a")).toBeNull();
  });

  it("draws a step header bold with its marker, and a known error line with the alert icon", () => {
    const { container } = render(<div><LogLines rows={[{ text: "==> Build" }, { text: "npm ERR! x", bad: true }, { text: "plain" }]} /></div>);
    const [hd, er, plain] = [...container.querySelectorAll(".ln")];
    expect(hd.className).toContain("hd");
    expect(hd.querySelector(".mk")!.textContent).toBe("==>");
    expect(er.className).toContain("er");
    expect(plain.className).toBe("ln");
    expect(container.querySelector(".ts")).toBeNull();
  });

  it("marks a stderr line with the warning icon, and guesses nothing from a stdout line's words (ROADMAP 35)", () => {
    const { container } = render(<div><LogLines rows={[{ text: "ERROR in stdout" }, { text: "listening", stderr: true }]} /></div>);
    const [out, err] = [...container.querySelectorAll(".ln")];
    expect(out.className).toBe("ln");
    expect(err.className).toBe("ln se");
    expect(err.querySelector(".lv")!.innerHTML).not.toBe(out.querySelector(".lv")!.innerHTML);
  });
});
