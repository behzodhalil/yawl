export interface Schedule {
  min: Set<number>;
  hour: Set<number>;
  dom: Set<number>;
  mon: Set<number>;
  dow: Set<number>;
  domStar: boolean;
  dowStar: boolean;
}

const FIELDS: [string, number, number][] = [
  ["minute", 0, 59],
  ["hour", 0, 23],
  ["day of month", 1, 31],
  ["month", 1, 12],
  ["day of week", 0, 7],
];

const num = (s: string) => (/^\d+$/.test(s) ? Number(s) : NaN);

function field(item: string, lo: number, hi: number): number[] | null {
  let rng = item;
  let step = 1;
  const slash = item.indexOf("/");
  if (slash >= 0) {
    step = num(item.slice(slash + 1));
    if (!(step >= 1)) return null;
    rng = item.slice(0, slash);
  }
  let a = lo;
  let b = hi;
  if (rng !== "*") {
    if (rng.includes("-")) {
      const [x, y] = rng.split("-");
      a = num(x);
      b = num(y);
      if (Number.isNaN(a) || Number.isNaN(b) || a > b) return null;
    } else {
      a = num(rng);
      if (Number.isNaN(a)) return null;
      if (step === 1) b = a;
    }
  }
  if (a < lo || b > hi) return null;
  const out: number[] = [];
  for (let v = a; v <= b; v += step) out.push(v);
  return out;
}

export function parse(expr: string): Schedule | string {
  const parts = expr.trim().split(/\s+/).filter(Boolean);
  if (parts.length !== 5) return `must be five fields (minute hour day month weekday), got "${expr}"`;
  const sets: Set<number>[] = [];
  for (let i = 0; i < 5; i++) {
    const [name, lo, hi] = FIELDS[i];
    const set = new Set<number>();
    for (const item of parts[i].split(",")) {
      const vs = field(item, lo, hi);
      if (!vs) return `${name} "${item}" is not a number from ${lo} to ${hi}`;
      vs.forEach((v) => set.add(v));
    }
    sets.push(set);
  }
  if (sets[4].has(7)) sets[4].add(0);
  // As in Vixie cron, a field starting with * (like */2) is a star for the day rule
  return { min: sets[0], hour: sets[1], dom: sets[2], mon: sets[3], dow: sets[4], domStar: parts[2].startsWith("*"), dowStar: parts[4].startsWith("*") };
}

export interface Parts {
  minute: number;
  hour: number;
  day: number;
  month: number;
  weekday: number;
}

const DAYS: Record<string, number> = { Sun: 0, Mon: 1, Tue: 2, Wed: 3, Thu: 4, Fri: 5, Sat: 6 };

// formatter is the one Intl formatter a tick needs per zone (CPU is 10 ms).
export function formatter(tz: string): Intl.DateTimeFormat {
  return new Intl.DateTimeFormat("en-GB", {
    timeZone: tz, minute: "numeric", hour: "numeric", day: "numeric", month: "numeric", weekday: "short", hourCycle: "h23",
  });
}

export function partsIn(ms: number, f: Intl.DateTimeFormat): Parts {
  const p = Object.fromEntries(f.formatToParts(ms).map((x) => [x.type, x.value]));
  return { minute: Number(p.minute), hour: Number(p.hour), day: Number(p.day), month: Number(p.month), weekday: DAYS[p.weekday] };
}

export function matches(s: Schedule, t: Parts): boolean {
  if (!s.min.has(t.minute) || !s.hour.has(t.hour) || !s.mon.has(t.month)) return false;
  const dom = s.dom.has(t.day);
  const dow = s.dow.has(t.weekday);
  return !s.domStar && !s.dowStar ? dom || dow : dom && dow;
}

export function split(target: string): { tz: string; expr: string } {
  const i = target.indexOf(" ");
  return { tz: target.slice(0, i), expr: target.slice(i + 1) };
}
