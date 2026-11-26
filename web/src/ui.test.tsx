import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { fakeApi, fail, pressBackdrop } from "./test-api";
import { api } from "./api";
import { Ago, ButtonLink, Confirm, Field, Input, Menu, MenuItem, Modal, NavTabs, POLL_MS, useBusy, useLoad, useUnsaved } from "./ui";

process.env.TZ = "Asia/Tashkent";

afterEach(() => vi.useRealTimers());

const escape = () => fireEvent.keyDown(document.activeElement!, { key: "Escape" });
const focused = (el: () => Element) => waitFor(() => expect(document.activeElement).toBe(el()));

describe("Modal", () => {
  it("with dismissible=false ignores Escape and the backdrop", async () => {
    const spy = vi.fn();
    render(<Modal title="T" dismissible={false} onClose={spy}><button>Done</button></Modal>);
    await focused(() => screen.getByRole("button", { name: "Done" }));
    escape();
    pressBackdrop();
    expect(spy).not.toHaveBeenCalled();
  });
  it("still closes on Escape and the backdrop by default", async () => {
    const spy = vi.fn();
    render(<Modal title="T" onClose={spy}><button>Done</button></Modal>);
    await focused(() => screen.getByRole("button", { name: "Done" }));
    escape();
    pressBackdrop();
    expect(spy).toHaveBeenCalledTimes(2);
  });
  it("is named by its title", () => {
    render(<Modal title="Rename" onClose={() => {}}><button>A</button></Modal>);
    expect(screen.getByRole("dialog", { name: "Rename" })).toBeTruthy();
  });
  it("focuses its first field and gives focus back on close", async () => {
    const Page = ({ open }: { open: boolean }) => (
      <><button>Open</button>{open && <Modal title="T" onClose={() => {}}><input aria-label="Name" /></Modal>}</>
    );
    const { rerender } = render(<Page open={false} />);
    screen.getByRole("button", { name: "Open" }).focus();
    rerender(<Page open />);
    await focused(() => screen.getByLabelText("Name"));
    rerender(<Page open={false} />);
    await focused(() => screen.getByRole("button", { name: "Open" }));
  });
  it("prefers an [autofocus] element", async () => {
    render(<Modal title="T" onClose={() => {}}><input aria-label="Name" /><button data-testid="b" autoFocus>Go</button></Modal>);
    await focused(() => screen.getByTestId("b"));
  });
  it("Escape closes only the top one of two stacked modals", async () => {
    const under = vi.fn(), over = vi.fn();
    render(<><Modal title="Under" onClose={under}><button>A</button></Modal><Modal title="Over" onClose={over}><button>B</button></Modal></>);
    await waitFor(() => expect(document.activeElement?.textContent).toBe("B"));
    escape();
    expect(over).toHaveBeenCalledTimes(1);
    expect(under).not.toHaveBeenCalled();
  });
});

describe("Confirm", () => {
  it("shows a failure inside and stays open", async () => {
    const onClose = vi.fn();
    render(<Confirm title="Stop" yes="Yes, stop" onYes={() => Promise.reject(new Error("nope"))} onClose={onClose} />);
    const yes = screen.getByRole("button", { name: "Yes, stop" });
    fireEvent.click(yes);
    const msg = await screen.findByText("nope");
    expect(screen.getByRole("dialog").contains(msg)).toBe(true);
    expect(onClose).not.toHaveBeenCalled();
    expect((yes as HTMLButtonElement).disabled).toBe(false);
  });
  it("closes after success and disables while running", async () => {
    const onClose = vi.fn();
    let done!: () => void;
    render(<Confirm title="Stop" yes="Yes, stop" onYes={() => new Promise<void>((r) => (done = r))} onClose={onClose} />);
    const yes = screen.getByRole("button", { name: "Yes, stop" }) as HTMLButtonElement;
    fireEvent.click(yes);
    await waitFor(() => expect(yes.disabled).toBe(true));
    done();
    await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1));
  });
});

describe("Confirm disabled", () => {
  it("keeps the yes button disabled while disabled is set", () => {
    const { rerender } = render(<Confirm title="Stop" yes="Yes, stop" disabled onYes={async () => {}} onClose={() => {}} />);
    expect((screen.getByRole("button", { name: "Yes, stop" }) as HTMLButtonElement).disabled).toBe(true);
    rerender(<Confirm title="Stop" yes="Yes, stop" onYes={async () => {}} onClose={() => {}} />);
    expect((screen.getByRole("button", { name: "Yes, stop" }) as HTMLButtonElement).disabled).toBe(false);
  });
});

describe("Confirm while running", () => {
  it("cannot be dismissed, and closes exactly once", async () => {
    const onClose = vi.fn();
    let done!: () => void;
    render(<Confirm title="Stop" yes="Yes, stop" onYes={() => new Promise<void>((r) => (done = r))} onClose={onClose} />);
    const yes = screen.getByRole("button", { name: "Yes, stop" }) as HTMLButtonElement;
    fireEvent.click(yes);
    await waitFor(() => expect(yes.disabled).toBe(true));
    expect((screen.getByRole("button", { name: "Cancel" }) as HTMLButtonElement).disabled).toBe(true);
    escape();
    pressBackdrop();
    expect(screen.getByRole("dialog")).toBeTruthy();
    expect(onClose).not.toHaveBeenCalled();
    done();
    await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1));
  });
});

describe("Modal links", () => {
  it("focuses a link when it is the first control", async () => {
    render(<Modal title="T" onClose={() => {}}><a href="/x">Docs</a></Modal>);
    await focused(() => screen.getByRole("link", { name: "Docs" }));
  });
});

describe("useBusy", () => {
  it("runs once for two clicks in one tick", async () => {
    const calls = fakeApi({ "POST /x": new Promise((r) => setTimeout(() => r(null), 20)) });
    const C = () => {
      const [, run] = useBusy();
      return <button onClick={() => run(() => api("POST", "/x"))}>go</button>;
    };
    render(<C />);
    const b = screen.getByRole("button");
    act(() => { b.click(); b.click(); });
    await new Promise((r) => setTimeout(r, 60));
    expect(calls.filter((c) => c.method === "POST")).toHaveLength(1);
  });
});

describe("useLoad", () => {
  it("polls and keeps data when a poll fails", async () => {
    vi.useFakeTimers();
    const answers: unknown[] = [[1], fail(500, "down"), [2]];
    fakeApi({ "GET /l": () => answers.shift() });
    let last: ReturnType<typeof useLoad<number[]>> | undefined;
    const C = () => { last = useLoad<number[]>("/l", POLL_MS); return null; };
    render(<C />);
    await act(() => vi.advanceTimersByTimeAsync(0));
    expect(last!.data).toEqual([1]);
    await act(() => vi.advanceTimersByTimeAsync(POLL_MS));
    expect(last!.error).toBe("down");
    expect(last!.data).toEqual([1]);
    await act(() => vi.advanceTimersByTimeAsync(POLL_MS));
    expect(last!.data).toEqual([2]);
    expect(last!.error).toBeUndefined();
  });
});

describe("useLoad polling guards", () => {
  it("skips a tick while the document is hidden", async () => {
    vi.useFakeTimers();
    const calls = fakeApi({ "GET /l": [1] });
    const C = () => { useLoad<number[]>("/l", POLL_MS); return null; };
    render(<C />);
    await act(() => vi.advanceTimersByTimeAsync(0));
    vi.spyOn(document, "hidden", "get").mockReturnValue(true);
    await act(() => vi.advanceTimersByTimeAsync(POLL_MS * 2));
    expect(calls.filter((c) => c.method === "GET")).toHaveLength(1);
    vi.restoreAllMocks();
  });
  it("does not start a poll while the last one is in flight", async () => {
    vi.useFakeTimers();
    const calls = fakeApi({ "GET /l": () => new Promise(() => {}) });
    const C = () => { useLoad<number[]>("/l", POLL_MS); return null; };
    render(<C />);
    await act(() => vi.advanceTimersByTimeAsync(POLL_MS * 2));
    expect(calls.filter((c) => c.method === "GET")).toHaveLength(1);
  });
});

describe("Menu", () => {
  it("A menu item that opens a Modal gives focus back to the menu button when it closes", async () => {
    function T() {
      const [open, setOpen] = useState(false);
      return (
        <>
          <Menu label="More" trigger={<>More</>}><MenuItem onClick={() => setOpen(true)}>Rename</MenuItem></Menu>
          {open && <Modal title="Rename" onClose={() => setOpen(false)}><button>Done</button></Modal>}
        </>
      );
    }
    render(<T />);
    fireEvent.click(screen.getByRole("button", { name: "More" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "Rename" }));
    await focused(() => screen.getByRole("button", { name: "Done" }));
    escape();
    await focused(() => screen.getByRole("button", { name: "More" }));
  });

  const three = () => render(
    <Menu label="Actions" trigger={<>Menu</>}>
      <MenuItem onClick={() => {}}>Restart</MenuItem>
      <MenuItem onClick={() => {}}>Stop</MenuItem>
      <MenuItem onClick={() => {}}>Delete</MenuItem>
    </Menu>
  );
  const item = (name: string) => () => screen.getByRole("menuitem", { name });
  const key = (k: string) => fireEvent.keyDown(document.activeElement!, { key: k });

  it("moves with arrows, Home, End and a letter", async () => {
    three();
    fireEvent.click(screen.getByRole("button", { name: "Actions" }));
    await screen.findByRole("menu");
    fireEvent.keyDown(screen.getByRole("menu"), { key: "End" });
    await focused(item("Delete"));
    key("Home");
    await focused(item("Restart"));
    key("ArrowDown");
    await focused(item("Stop"));
    key("d");
    await focused(item("Delete"));
  });

  it("Escape closes it and gives focus back to the trigger", async () => {
    three();
    const btn = screen.getByRole("button", { name: "Actions" });
    fireEvent.click(btn);
    fireEvent.keyDown(await screen.findByRole("menu"), { key: "End" });
    await focused(item("Delete"));
    key("Escape");
    await waitFor(() => expect(screen.queryByRole("menu")).toBeNull());
    await focused(() => btn);
  });

  it("runs the picked item and closes", async () => {
    const restart = vi.fn();
    render(<Menu label="Actions" trigger={<>Menu</>}><MenuItem onClick={restart}>Restart</MenuItem></Menu>);
    fireEvent.click(screen.getByRole("button", { name: "Actions" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "Restart" }));
    expect(restart).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(screen.queryByRole("menu")).toBeNull());
  });
});

describe("useUnsaved", () => {
  function T({ dirty }: { dirty: boolean }) { useUnsaved(dirty); return null; }
  it("prevents unload only while dirty", () => {
    const { rerender } = render(<T dirty={false} />);
    expect(window.dispatchEvent(new Event("beforeunload", { cancelable: true }))).toBe(true);
    rerender(<T dirty />);
    expect(window.dispatchEvent(new Event("beforeunload", { cancelable: true }))).toBe(false);
    rerender(<T dirty={false} />);
    expect(window.dispatchEvent(new Event("beforeunload", { cancelable: true }))).toBe(true);
  });
});

describe("Ago", () => {
  it("says how long ago and shows the exact time on hover", () => {
    const at = Date.UTC(2026, 10, 23, 3, 43);
    const { container } = render(<div><Ago ms={at} /></div>);
    const t = container.querySelector("time")!;
    expect(t.textContent).toMatch(/ago$/);
    expect(t.getAttribute("title")).toBe("23 Nov 2026 08:43");
    expect(t.getAttribute("datetime")).toBe("2026-11-23T03:43:00.000Z");
  });

  it("says never without a time", () => {
    const { container } = render(<div><Ago ms={undefined} /></div>);
    expect(container.textContent).toBe("never");
    expect(container.querySelector("time")).toBeNull();
  });
});

describe("ButtonLink", () => {
  it("navigates in the app and keeps the primary colours a plain class cannot carry", () => {
    render(<ButtonLink to="/servers" variant="primary">Go</ButtonLink>);
    const a = screen.getByRole("link", { name: "Go" });
    expect(a.getAttribute("style")).toContain("--kumo-button-emphasis-bg");
    fireEvent.click(a);
    expect(location.pathname).toBe("/servers");
  });
});

describe("MenuItem with a description", () => {
  it("is named by its title and described by the line under it", async () => {
    render(<Menu label="Add" trigger={<>Add</>}><MenuItem icon="db" description="Create or connect a database" onClick={() => {}}>Database</MenuItem></Menu>);
    fireEvent.click(screen.getByRole("button", { name: "Add" }));
    expect(await screen.findByRole("menuitem", { name: "Database", description: "Create or connect a database" })).toBeTruthy();
  });
});

describe("Field", () => {
  it("ties the hint to the control for screen readers", () => {
    render(<Field label="Branch" hint="A push to it deploys.">{(id) => <Input id={id} />}</Field>);
    expect(screen.getByRole("textbox", { name: "Branch", description: "A push to it deploys." })).toBeTruthy();
  });

  it("leaves the control undescribed without a hint", () => {
    render(<Field label="Branch">{(id) => <Input id={id} />}</Field>);
    expect(screen.getByRole("textbox", { name: "Branch" }).hasAttribute("aria-describedby")).toBe(false);
  });
});

describe("NavTabs", () => {
  const tabs = ["Overview", "Deploys", "Environment"].map((label, i) => ({ to: "/" + label, label, current: i === 2 }));

  it("scrolls the current tab into view", () => {
    const seen: Element[] = [];
    const spy = vi.spyOn(Element.prototype, "scrollIntoView").mockImplementation(function (this: Element) { seen.push(this); });
    render(<NavTabs label="Service" tabs={tabs} />);
    expect(seen.map((el) => el.textContent)).toEqual(["Environment"]);
    spy.mockRestore();
  });

  it("fades only the edge with hidden tabs", () => {
    render(<NavTabs label="Service" tabs={tabs} />);
    const strip = screen.getByRole("navigation", { name: "Service" });
    expect(strip.style.maskImage).toBe("");
    Object.defineProperties(strip, { scrollWidth: { value: 400 }, clientWidth: { value: 200 }, scrollLeft: { value: 0, writable: true } });
    fireEvent.scroll(strip);
    expect(strip.style.maskImage).toContain("black calc(100% - 16px), transparent)");
    expect(strip.style.maskImage).toMatch(/^linear-gradient\(to right, black,/);
  });
});
