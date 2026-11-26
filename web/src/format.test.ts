import { describe, expect, it } from "vitest";
import { actorName, ago, commitSubject, backupWord, bytes, dateTime, ms, spaced, dayTime, deployWord, niceTicks, outsideLine, serviceWord, shortSha, stateKind, stepName, took, parseDay, urlParts } from "./format";

process.env.TZ = "Asia/Tashkent";

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
  it("says the answer time when up", () => {
    expect(outsideLine({ ok: true, ms: 120, detail: "", since: 0 })).toBe("From outside: 120 ms");
  });
  it("says down since when", () => {
    expect(outsideLine({ ok: false, ms: 10_000, detail: "502", since: new Date(2026, 9, 27, 14, 2).getTime() })).toBe("From outside: down since 14:02");
  });
  it("says nothing without a check", () => {
    expect(outsideLine(undefined)).toBe("");
  });
});

describe("bytes and the forecast", () => {
  it("formats bytes", () => {
    expect(bytes(1288490188)).toBe("1.3 GB");
    expect(bytes(300e6)).toBe("300 MB");
    expect(bytes(512)).toBe("512 B");
    expect(bytes(1234)).toBe("1.2 kB");
  });
  it("formats milliseconds", () => {
    expect(ms(1204.4)).toBe("1,204 ms");
    expect(ms(120)).toBe("120 ms");
  });
  it("puts a space between a spec size and its unit", () => {
    expect(spaced("512MB")).toBe("512 MB");
    expect(spaced("1.5 GB")).toBe("1.5 GB");
    expect(spaced("No limit")).toBe("No limit");
  });
});

describe("dates", () => {
  it("writes a moment as day, month, year and 24-hour time in the browser's zone", () => {
    expect(dateTime(Date.UTC(2026, 10, 23, 3, 43))).toBe("23 Nov 2026 08:43");
    expect(dateTime(Date.UTC(2026, 0, 1, 18, 5))).toBe("1 Jan 2026 23:05");
    expect(dateTime(Date.UTC(2026, 11, 31, 19, 0))).toBe("1 Jan 2027 00:00");
  });

  it("reads a day written either way as local midnight", () => {
    const midnight = new Date(2026, 10, 23).getTime();
    expect(parseDay("23 Nov 2026")).toBe(midnight);
    expect(parseDay(" 23 nov 2026 ")).toBe(midnight);
    expect(parseDay("2026-11-23")).toBe(midnight);
    expect(parseDay("5 Jan 2026")).toBe(new Date(2026, 0, 5).getTime());
    expect(new Date(parseDay("23 Nov 2026")!).toISOString()).toBe("2026-11-22T19:00:00.000Z");
  });

  it("refuses anything that is not a real day", () => {
    for (const s of ["", "Nov 23 2026", "11/23/2026", "31 Feb 2026", "2026-02-30", "23 Foo 2026", "2026-13-01", "23 Nov", "tomorrow"]) {
      expect(parseDay(s)).toBeNull();
    }
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

describe("dayTime", () => {
  const now = new Date(2026, 10, 23, 12, 0).getTime();
  const at = (y: number, m: number, d: number, h: number, min: number) => new Date(y, m, d, h, min).getTime();
  it("words today and yesterday", () => {
    expect(dayTime(at(2026, 10, 23, 8, 40), now)).toBe("Today, 08:40");
    expect(dayTime(at(2026, 10, 22, 23, 59), now)).toBe("Yesterday, 23:59");
  });
  it("gives the date, and the year only when it is not this one", () => {
    expect(dayTime(at(2026, 10, 21, 8, 40), now)).toBe("21 Nov, 08:40");
    expect(dayTime(at(2025, 10, 21, 8, 40), now)).toBe("21 Nov 2025, 08:40");
  });
});

describe("commitSubject", () => {
  it("shortens a merge commit to its PR number and branch", () => {
    expect(commitSubject("Merge pull request #42 from acme/fix/invoice-rounding")).toBe("PR #42 · fix/invoice-rounding");
  });
  it("leaves other subjects alone", () => {
    expect(commitSubject("Fix invoice rounding")).toBe("Fix invoice rounding");
  });
});

describe("actorName", () => {
  it("drops the email in parentheses", () => {
    expect(actorName("Dilnoza (dilnoza@example.com)")).toBe("Dilnoza");
    expect(actorName("dilnoza")).toBe("dilnoza");
  });
});

describe("urlParts", () => {
  it("splits a database URL into the fields a GUI client asks for", () => {
    expect(urlParts("postgres://app:pw@db:5432/app?sslmode=disable")).toEqual([["Host", "db"], ["Port", "5432"], ["User", "app"], ["Database", "app"]]);
    expect(urlParts("redis://default:pw@cache:6379")).toEqual([["Host", "cache"], ["Port", "6379"], ["User", "default"]]);
    expect(urlParts("not a url")).toEqual([]);
  });
});
