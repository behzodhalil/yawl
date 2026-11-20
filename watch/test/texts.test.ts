import { describe, expect, it } from "vitest";
import { clock, lasted, when } from "../src/texts";

const MIN = 60_000;

describe("texts", () => {
  it("says how long", () => {
    expect(lasted(30_000)).toBe("less than a minute");
    expect(lasted(MIN)).toBe("1 minute");
    expect(lasted(7 * MIN)).toBe("7 minutes");
    expect(lasted(60 * MIN)).toBe("1 hour");
    expect(lasted(120 * MIN)).toBe("2 hours");
    expect(lasted(125 * MIN)).toBe("2 hours 5 minutes");
    expect(lasted(61 * MIN)).toBe("1 hour 1 minute");
  });

  it("tells the time in the panel's zone", () => {
    const t = Date.UTC(2026, 9, 27, 9, 2);
    expect(clock(t, "UTC")).toBe("09:02");
    expect(clock(t, "Asia/Tashkent")).toBe("14:02");
  });

  it("names a past moment", () => {
    const now = Date.UTC(2026, 9, 27, 12, 0);
    expect(when(undefined, now, "UTC")).toBe("never");
    expect(when(Date.UTC(2026, 9, 27, 3, 0), now, "UTC")).toBe("03:00");
    expect(when(Date.UTC(2026, 9, 26, 3, 0), now, "UTC")).toBe("26 Oct 03:00");
  });
});
