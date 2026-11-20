import { describe, expect, it } from "vitest";
import { ago, backupWord, bytes, deployWord, forecastLine, niceTicks, outsideLine, serviceWord, shortSha, stateKind, stepName, took } from "./format";

describe("format", () => {
  it("tones", () => {
    expect(stateKind("live")).toBe("ok");
    expect(stateKind("failed")).toBe("bad");
    expect(stateKind("building")).toBe("busy");
    expect(stateKind("rolled_back")).toBe("off");
    expect(stateKind("never deployed")).toBe("off");
  });
  it("ago", () => {
    const now = 1_000_000_000;
    expect(ago(now - 5_000, now)).toBe("just now");
    expect(ago(now - 3 * 60_000, now)).toBe("3 min ago");
    expect(ago(now - 2 * 3_600_000, now)).toBe("2 h ago");
    expect(ago(now - 3 * 86_400_000, now)).toBe("3 days ago");
    expect(ago(0, now)).toBe("never");
  });
  it("shortSha", () => expect(shortSha("a1f9c2e88aa")).toBe("a1f9c2e"));
});

describe("outsideLine", () => {
  const now = new Date(2026, 9, 27, 14, 30).getTime();
  it("says up with the answer time and how long ago", () => {
    expect(outsideLine({ ok: true, ms: 120, detail: "", since: 0 }, now - 40_000, now)).toBe("From outside: up, 120 ms, checked 40 s ago");
  });
  it("says down since when", () => {
    expect(outsideLine({ ok: false, ms: 10_000, detail: "502", since: new Date(2026, 9, 27, 14, 2).getTime() }, now, now)).toBe("From outside: down since 14:02");
  });
  it("uses the result's own check time when it has one", () => {
    expect(outsideLine({ ok: true, ms: 120, detail: "", since: 0, at: now - 5 * 60_000 }, now, now)).toBe("From outside: up, 120 ms, checked 5 min ago");
  });
  it("says nothing without a check", () => {
    expect(outsideLine(undefined, now, now)).toBe("");
  });
});

describe("bytes and the forecast", () => {
  it("formats bytes", () => {
    expect(bytes(1288490188)).toBe("1.3 GB");
    expect(bytes(300e6)).toBe("300 MB");
    expect(bytes(512)).toBe("512 B");
  });
  it("says the disk's course", () => {
    expect(forecastLine(61, { days: 23, gb_per_day: 2.1, growing: true })).toEqual(["Disk 61% full, growing 2.1 GB a day: full in about 23 days.", false]);
    expect(forecastLine(61, { days: null, gb_per_day: 0, growing: false })).toEqual(["Disk 61% full, not growing.", false]);
    expect(forecastLine(92, { days: 5, gb_per_day: 2.1, growing: true })).toEqual(["Disk 92% full, growing 2.1 GB a day: full in about 5 days.", true]);
  });
});

describe("utcStamp", () => {
  it("is the day, month and time in UTC", async () => {
    const { utcStamp } = await import("./format");
    expect(utcStamp(Date.UTC(2026, 9, 28, 3, 30))).toBe("28 Oct 03:30 UTC");
  });

  it("took", () => {
    expect(took(134_000)).toBe("2m 14s");
    expect(took(61_000)).toBe("1m 01s");
    expect(took(9_400)).toBe("9s");
    expect(took(0)).toBe("");
    expect(took(undefined)).toBe("");
  });
  it("niceTicks", () => {
    expect(niceTicks(94)).toEqual([0, 25, 50, 75, 100]);
    expect(niceTicks(16)).toEqual([0, 5, 10, 15, 20]);
    expect(niceTicks(180)).toEqual([0, 50, 100, 150, 200]);
    expect(niceTicks(257e6)).toEqual([0, 100e6, 200e6, 300e6]);
    expect(niceTicks(0.3)).toEqual([0, 0.1, 0.2, 0.3]);
    expect(niceTicks(0)).toEqual([0, 1]);
    expect(niceTicks(0.3, true)).toEqual([0, 1]);
    expect(niceTicks(2, true)).toEqual([0, 1, 2]);
    expect(niceTicks(10, true)).toEqual([0, 5, 10]);
    expect(niceTicks(94, true)).toEqual([0, 25, 50, 75, 100]);
  });
  it("deployWord", () => {
    expect(deployWord("live", true)).toEqual(["Deployed", "ok"]);
  });
  it("says a service's state in the word its kind earns", () => {
    expect(serviceWord("web", "live")).toEqual(["Deployed", "ok"]);
    expect(serviceWord("worker", "live")).toEqual(["Deployed", "ok"]);
    expect(serviceWord("postgres", "live")).toEqual(["Available", "ok"]);
    expect(serviceWord("valkey", "live")).toEqual(["Available", "ok"]);
    expect(serviceWord("cron", "live")).toEqual(["Scheduled", "ok"]);
    expect(serviceWord("web", "failed")).toEqual(["Failed", "bad"]);
    expect(serviceWord("web", "building")).toEqual(["Building", "busy"]);
    expect(serviceWord("web", "starting")).toEqual(["Deploying", "busy"]);
    expect(serviceWord("postgres", "starting")).toEqual(["Starting", "busy"]);
    expect(serviceWord("web", "")).toEqual(["Not deployed", "off"]);
    expect(backupWord("ok")).toEqual(["Completed", "ok"]);
    expect(backupWord("failed")).toEqual(["Failed", "bad"]);
    expect(backupWord("running")).toEqual(["Backing up", "busy"]);
    expect(deployWord("live", false)).toEqual(["Replaced", "off"]);
    expect(deployWord("failed", false)).toEqual(["Failed", "bad"]);
    expect(deployWord("superseded", false)).toEqual(["Skipped", "off"]);
    expect(deployWord("rolled_back", false)).toEqual(["Rolled back", "bad"]);
    expect(deployWord("checking", false)).toEqual(["Checking", "busy"]);
    expect(deployWord("live", false, true)).toEqual(["Rolled back", "bad"]);
  });
  it("stepName", () => {
    expect(stepName("check")).toBe("Health check");
    expect(stepName("pull")).toBe("Pull");
    expect(stepName("switch")).toBe("Route traffic");
    expect(stepName("drain")).toBe("Stop old version");
    expect(stepName("something_new")).toBe("Something new");
  });
});
