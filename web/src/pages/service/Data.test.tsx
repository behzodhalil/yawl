import { describe, expect, it } from "vitest";
import { render, screen, fireEvent, within } from "@testing-library/react";
import { fail, fakeApi } from "../../test-api";
import { Data } from "./Data";

const D = "/api/services/acme/production/db/data";
const tables = { tables: [{ schema: "public", name: "users" }, { schema: "audit", name: "events" }] };

describe("Data", () => {
  it("lists the tables and pages through one's rows, NULL apart from empty", async () => {
    const calls = fakeApi({
      "GET /api/me": { role: "developer" },
      [`POST ${D}/tables`]: tables,
      [`POST ${D}/rows`]: { columns: ["id", "email"], rows: [["1", "a@acme.uz"], ["2", null], ["3", ""]], more: true },
    });
    render(<Data project="acme" env="production" service="db" />);
    expect(await screen.findByText("audit.events")).toBeTruthy();
    fireEvent.click(screen.getByText("users"));
    expect(await screen.findByText("a@acme.uz")).toBeTruthy();
    expect(screen.getByText("NULL")).toBeTruthy();
    expect(calls.find((c) => c.url === `${D}/rows`)!.body).toEqual({ schema: "public", table: "users", page: 0 });
    fireEvent.click(screen.getByRole("button", { name: /Next/ }));
    await screen.findByText("Page 2");
    expect(calls.filter((c) => c.url === `${D}/rows`)[1].body).toEqual({ schema: "public", table: "users", page: 1 });
    expect(screen.queryByLabelText("Writes")).toBeNull();
  });

  it("tables open from the keyboard", async () => {
    const calls = fakeApi({ "GET /api/me": { role: "developer" }, [`POST ${D}/tables`]: tables, [`POST ${D}/rows`]: { columns: ["id"], rows: [["1"]] } });
    render(<Data project="acme" env="production" service="db" />);
    const btn = await screen.findByRole("button", { name: "users" });
    fireEvent.click(btn);
    await screen.findByText("users · 1 row");
    expect(calls.filter((c) => c.url === `${D}/rows`)).toHaveLength(1);
  });

  it("a skeleton shows while tables load", () => {
    fakeApi({ "GET /api/me": { role: "developer" }, [`POST ${D}/tables`]: () => new Promise(() => {}) });
    render(<Data project="acme" env="production" service="db" />);
    expect(screen.getByRole("status", { name: "Loading" })).toBeTruthy();
  });

  it("runs SQL and shows the rows, what it changed, or the database's words", async () => {
    let answer: unknown = { columns: ["n"], rows: [["1"], ["2"]] };
    const calls = fakeApi({ "GET /api/me": { role: "developer" }, [`POST ${D}/tables`]: tables, [`POST ${D}/query`]: () => answer });
    render(<Data project="acme" env="production" service="db" />);
    fireEvent.change(await screen.findByLabelText("SQL"), { target: { value: "select n from t" } });
    fireEvent.click(screen.getByRole("button", { name: "Run" }));
    expect(await screen.findByText("2 rows")).toBeTruthy();
    expect(calls.find((c) => c.url === `${D}/query`)!.body).toEqual({ sql: "select n from t" });
    answer = { columns: null, rows: null, changed: 3 };
    fireEvent.keyDown(screen.getByLabelText("SQL"), { key: "Enter", metaKey: true });
    expect(await screen.findByText("Done. 3 rows changed.")).toBeTruthy();
    answer = fail(422, 'ERROR:  relation "t" does not exist');
    fireEvent.click(screen.getByRole("button", { name: "Run" }));
    expect(await screen.findByText(/^ERROR: {2}relation "t" does not exist$/, { normalizer: (t) => t })).toBeTruthy();
    answer = { columns: ["n"], rows: Array.from({ length: 1000 }, (_, i) => [String(i)]), more: true };
    fireEvent.click(screen.getByRole("button", { name: "Run" }));
    expect(await screen.findByText("Showing the first 1000 rows")).toBeTruthy();
  });

  it("asks an Admin before a run with writes, every time", async () => {
    const calls = fakeApi({ "GET /api/me": { role: "admin" }, [`POST ${D}/tables`]: tables, [`POST ${D}/write`]: { columns: null, rows: null, changed: 1 } });
    render(<Data project="acme" env="production" service="db" />);
    fireEvent.change(await screen.findByLabelText("SQL"), { target: { value: "delete from users where id = 2" } });
    fireEvent.click(await screen.findByLabelText("Writes"));
    fireEvent.click(screen.getByRole("button", { name: "Run" }));
    const dialog = await screen.findByRole("dialog", { name: "Run with writes on db?" });
    expect(within(dialog).getByText("delete from users where id = 2")).toBeTruthy();
    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(calls.some((c) => c.url === `${D}/write`)).toBe(false);
    fireEvent.click(screen.getByRole("button", { name: "Run" }));
    fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Run with writes" }));
    expect(await screen.findByText("Done. 1 row changed.")).toBeTruthy();
    expect(calls.find((c) => c.url === `${D}/write`)!.body).toEqual({ sql: "delete from users where id = 2" });
  });
});
