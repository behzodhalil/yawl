import { describe, expect, it } from "vitest";
import { fireEvent, render } from "@testing-library/react";
import { LogLines, isFailureLine } from "./LogView";

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

  const spring = (extra: object) => JSON.stringify({ "@timestamp": "2026-11-23T03:43:25.688Z", message: "GET /x -> 401 Unauthorized", level: "INFO", ...extra });

  it("shows a JSON line as level pill, coloured status and message, with the raw JSON toggled by click or Enter", () => {
    const text = spring({ statusCode: "401" });
    const { container } = render(<div><LogLines rows={[{ text }]} /></div>);
    const ln = container.querySelector(".ln")!;
    expect(ln.className).toContain("s4");
    expect(ln.querySelector(".pill")!.textContent).toBe("INFO");
    expect(ln.querySelector(".st")!.className).toContain("warn");
    expect(ln.querySelector(".st")!.textContent).toBe("401");
    expect(ln.querySelector(".tx")!.textContent).toBe("INFO401GET /x -> 401 Unauthorized");
    expect(container.querySelector(".raw")).toBeNull();
    fireEvent.click(ln);
    expect(container.querySelector(".raw")!.textContent).toBe(text);
    fireEvent.keyDown(ln, { key: "Enter" });
    expect(container.querySelector(".raw")).toBeNull();
  });

  it("colours 2xx ok and tints 5xx bad", () => {
    const { container } = render(<div><LogLines rows={[{ text: spring({ status: 200 }) }, { text: spring({ statusCode: 503, level: "ERROR" }) }]} /></div>);
    const [ok, bad] = [...container.querySelectorAll(".ln")];
    expect(ok.querySelector(".st")!.className).toContain("ok");
    expect(ok.className).not.toMatch(/s[45]/);
    expect(bad.className).toContain("s5");
    expect(bad.querySelector(".pill")!.className).toContain("bad");
  });

  it("leaves a plain line and non-object JSON exactly as plain text", () => {
    const { container } = render(<div><LogLines rows={[{ text: "hello" }, { text: "[1,2]" }, { text: "{broken" }]} /></div>);
    const lines = [...container.querySelectorAll(".ln")];
    expect(lines.every((l) => l.className === "ln" && !l.hasAttribute("role"))).toBe(true);
    expect(lines.map((l) => l.querySelector(".tx")!.textContent)).toEqual(["hello", "[1,2]", "{broken"]);
  });

  it("counts parsed 5xx and error level as failures, besides stderr", () => {
    expect(isFailureLine(spring({ statusCode: "500" }))).toBe(true);
    expect(isFailureLine(spring({ level: "ERROR" }))).toBe(true);
    expect(isFailureLine(spring({ statusCode: "401" }))).toBe(false);
    expect(isFailureLine("plain")).toBe(false);
    expect(isFailureLine("plain", true)).toBe(true);
  });

  it("falls back to the raw line when a JSON object has no message", () => {
    const text = JSON.stringify({ level: "INFO", path: "/x" });
    const { container } = render(<div><LogLines rows={[{ text }]} /></div>);
    expect(container.querySelector(".tx")!.textContent).toBe("INFO" + text);
  });
});
