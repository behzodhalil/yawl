import { describe, expect, it } from "vitest";
import { fireEvent, render, screen, within } from "@testing-library/react";
import { fakeApi } from "./test-api";
import { Pager, SortTh, TableBar, useTable } from "./table";
import { Shell } from "./Shell";

type R = { name: string; n: number };
const ROWS: R[] = [{ name: "beta", n: 2 }, { name: "alpha", n: 3 }, { name: "gamma", n: 1 }];

function List({ rows = ROWS, size }: { rows?: R[]; size?: number }) {
  const t = useTable(rows, { text: (r) => r.name, sort: { name: (r) => r.name, n: (r) => r.n }, size });
  return (
    <>
      <TableBar t={t} noun="rows" find="Search rows" />
      <table><thead><tr><SortTh t={t} k="name">Name</SortTh><SortTh t={t} k="n">N</SortTh></tr></thead>
        <tbody>{t.rows.map((r) => <tr key={r.name}><td>{r.name}</td></tr>)}</tbody></table>
      <Pager page={t.page} pages={t.pages} onPage={t.setPage} />
    </>
  );
}
const names = () => screen.getAllByRole("cell").map((c) => c.textContent);

describe("table", () => {
  it("a head sorts up, then down, then back to how the rows came", () => {
    render(<List />);
    const head = screen.getByRole("button", { name: "Name" });
    fireEvent.click(head);
    expect(names()).toEqual(["alpha", "beta", "gamma"]);
    expect(head.closest("th")!.getAttribute("aria-sort")).toBe("ascending");
    fireEvent.click(head);
    expect(names()).toEqual(["gamma", "beta", "alpha"]);
    fireEvent.click(head);
    expect(names()).toEqual(["beta", "alpha", "gamma"]);
  });
  it("search narrows the rows and the count, and goes back to page one", () => {
    render(<List size={1} />);
    fireEvent.click(screen.getByRole("button", { name: /Next/ }));
    expect(names()).toEqual(["alpha"]);
    fireEvent.change(screen.getByRole("searchbox", { name: "Search rows" }), { target: { value: "GAM" } });
    expect(names()).toEqual(["gamma"]);
    expect(screen.getByText("1 rows")).toBeTruthy();
    expect(screen.queryByText(/Page/)).toBeNull();
  });
  it("pages hold size rows each", () => {
    render(<List size={2} />);
    expect(names()).toEqual(["beta", "alpha"]);
    expect(screen.getByText("Page 1 of 2")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: /Next/ }));
    expect(names()).toEqual(["gamma"]);
    expect((screen.getByRole("button", { name: /Next/ }) as HTMLButtonElement).disabled).toBe(true);
  });
});

describe("shell", () => {
  it("Cmd+K opens the jump list, typing narrows it, Escape closes it", async () => {
    fakeApi({ "GET /api/projects": [{ name: "acme", envs: [{ name: "production", server: "hel1", services: [{ project: "acme", env: "production", service: "web", kind: "web", state: "live", health: "up" }] }] }], "GET /api/servers": [] });
    render(<Shell crumb={["x"]} active="/"><p>body</p></Shell>);
    fireEvent.keyDown(window, { key: "k", metaKey: true });
    const box = screen.getByRole("combobox", { name: "Go to" });
    fireEvent.change(box, { target: { value: "web" } });
    await screen.findByText("web");
    const opts = within(screen.getByRole("listbox")).getAllByRole("option");
    expect(opts.map((o) => o.textContent)).toEqual(["webacme / production", "Deploy webacme / production", "Open logs: webacme / production", "Restart webacme / production"]);
    fireEvent.keyDown(box, { key: "Escape" });
    expect(screen.queryByRole("listbox")).toBeNull();
  });
  it("the theme button flips the theme and keeps it", () => {
    document.documentElement.dataset.theme = "dark";
    render(<Shell crumb={["x"]} active="/"><p>body</p></Shell>);
    fireEvent.click(screen.getByRole("button", { name: "Light theme" }));
    expect(document.documentElement.dataset.theme).toBe("light");
    expect(localStorage.getItem("yawl.theme")).toBe("light");
    fireEvent.click(screen.getByRole("button", { name: "Dark theme" }));
    expect(document.documentElement.dataset.theme).toBe("dark");
  });
});
