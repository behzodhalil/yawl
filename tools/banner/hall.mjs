import { readFileSync, writeFileSync } from "node:fs";
import { gzipSync } from "node:zlib";

const T = JSON.parse(readFileSync(new URL("text.json", import.meta.url), "utf8"));
const OUT = new URL("../../.github/assets/banner.svg", import.meta.url);
const [W, H, VX, VY, F, CAM] = [1200, 630, 792, 312, 300, 0.9];
const BG = "#15140d", FG = "#f3f1e6", MUTE = "#aba891", PRI = "#cdf75a";
const n = (v) => (Math.round(v * 10) / 10).toString();
const P = (x, y, z) => [VX + x * F / z, VY - (y - CAM) * F / z];
const pt = (p) => `${n(p[0])} ${n(p[1])}`;
const seg = (a, b) => `M${pt(a)}L${pt(b)}`;
const alpha = (z) => Math.max(0.12, Math.min(0.95, 1.15 - Math.log(z) / Math.log(14)));

const layers = [];
function add(d, a, w = 1) { layers.push({ d, a, w }); }

const RACK_H = 1.72, UNITS = 12, DEPTH = 0.5;
function wall(side, x0) {
  const back = x0 + side * DEPTH;
  let z = 0.62;
  const leds = [];
  while (z < 14) {
    const len = 0.62, z1 = z + len, a = alpha(z);
    let d = "";
    for (const zz of [z, z1]) d += seg(P(x0, 0, zz), P(x0, RACK_H, zz));
    d += seg(P(x0, 0, z), P(x0, 0, z1)) + seg(P(x0, RACK_H, z), P(x0, RACK_H, z1));
    d += seg(P(x0, RACK_H, z), P(back, RACK_H, z)) + seg(P(x0, 0, z), P(back, 0, z)) + seg(P(back, 0, z), P(back, RACK_H, z));
    add(d, a, z < 1.6 ? 1.3 : 1);
    let g = "";
    for (let u = 1; u < UNITS; u++) { const y = RACK_H * u / UNITS; g += seg(P(x0, y, z), P(x0, y, z1)); }
    g += seg(P(x0, 0, z + len * 0.5), P(x0, RACK_H, z + len * 0.5));
    for (let u = 1; u < 4; u++) { const y = RACK_H * u / 4; g += seg(P(x0, y, z), P(back, y, z)); }
    add(g, a * 0.45);
    if (z < 7) for (let u = 0; u < UNITS; u++) leds.push({ x: x0, y: RACK_H * (u + 0.5) / UNITS, z: z + len * 0.12, a });
    z = z1 + 0.06;
  }
  return leds;
}
const leds = [...wall(-1, -1.15), ...wall(1, 1.25)];

let floor = "";
for (let k = 0; k <= 8; k++) { const x = -1.15 + k * (2.4 / 8); floor += seg(P(x, 0, 0.5), P(x, 0, 30)); }
for (let z = 0.5; z < 14; z *= 1.16) floor += seg(P(-1.15, 0, z), P(1.25, 0, z));
add(floor, 0.42);
add(seg([660, 0], P(-0.45, 3.2, 40)) + seg([920, 0], P(0.55, 3.2, 40)), 0.45);

const hash = (a, b) => { const v = Math.sin(a * 127.1 + b * 311.7) * 43758.5453; return v - Math.floor(v); };
const STEPS = 16, LOOP = 6;
let ledSvg = "";
leds.forEach((l, i) => {
  if (hash(i, 1) > 0.42) return;
  const [x, y] = P(l.x, l.y, l.z), s = Math.max(1.4, 5.5 / l.z);
  const vals = Array.from({ length: STEPS }, (_, k) => (hash(i, k + 7) > 0.35 ? 1 : 0.15));
  const warn = hash(i, 3) > 0.94;
  ledSvg += `<rect x="${n(x - s / 2)}" y="${n(y - s / 2)}" width="${n(s)}" height="${n(s * 0.7)}" fill="${warn ? "#e9b44c" : PRI}" opacity="${n(vals[0] * l.a)}"><animate attributeName="opacity" dur="${LOOP}s" begin="${n(-hash(i, 5) * LOOP)}s" repeatCount="indefinite" calcMode="discrete" values="${vals.map((v) => n(v * l.a)).join(";")}"/></rect>`;
});

const svg = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${W} ${H}" width="${W}" height="${H}">
<title>Yawl. Open source PaaS. Deploy to your own servers. Push to Git. Yawl builds, deploys and rolls back on failure.</title>
<defs>
<linearGradient id="fade" x1="0" x2="640" y1="0" y2="0" gradientUnits="userSpaceOnUse"><stop offset="0" stop-color="${BG}" stop-opacity=".97"/><stop offset=".5" stop-color="${BG}" stop-opacity=".88"/><stop offset="1" stop-color="${BG}" stop-opacity="0"/></linearGradient>
<radialGradient id="end" cx="${VX}" cy="${VY}" r="70" gradientUnits="userSpaceOnUse"><stop offset="0" stop-color="${BG}"/><stop offset="1" stop-color="${BG}" stop-opacity="0"/></radialGradient>
</defs>
<rect width="${W}" height="${H}" fill="${BG}"/>
${layers.map((l) => `<path d="${l.d}" fill="none" stroke="${FG}" stroke-opacity="${n(l.a)}" stroke-width="${l.w}"/>`).join("\n")}
<g>${ledSvg}</g>
<circle cx="${VX}" cy="${VY}" r="70" fill="url(#end)"/>
<rect width="640" height="${H}" fill="url(#fade)"/>
<rect x="72" y="74" width="30" height="30" rx="2" fill="${FG}"/>
<path d="${T.Y}" fill="${BG}"/>
<path d="${T.brand}" fill="${FG}"/>
<path d="${T.eyebrow}" fill="${MUTE}"/>
<path d="${T.h1}" fill="${FG}"/>
<path d="${T.sub}" fill="${FG}"/>
</svg>
`;
writeFileSync(OUT, svg);
console.log(`${OUT.pathname}: ${(svg.length / 1024).toFixed(0)} KB, gzip ${(gzipSync(svg).length / 1024).toFixed(0)} KB`);
