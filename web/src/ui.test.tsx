import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { fakeApi, fail } from "./test-api";
import { api } from "./api";
import { Ago, Confirm, Menu, MenuItem, Modal, POLL_MS, useBusy, useLoad, useUnsaved } from "./ui";

afterEach(() => vi.useRealTimers());

describe("Modal", () => {
  it("with dismissible=false ignores Escape and the backdrop", () => {
    const spy = vi.fn();
    const { container } = render(<Modal title="T" dismissible={false} onClose={spy}><button>Done</button></Modal>);
    fireEvent.keyDown(window, { key: "Escape" });
    fireEvent.click(container.querySelector(".modal-bg")!);
    expect(spy).not.toHaveBeenCalled();
  });
  it("still closes on Escape and the backdrop by default", () => {
    const spy = vi.fn();
    const { container } = render(<Modal title="T" onClose={spy}><button>Done</button></Modal>);
    fireEvent.keyDown(window, { key: "Escape" });
    fireEvent.click(container.querySelector(".modal-bg")!);
    expect(spy).toHaveBeenCalledTimes(2);
  });
  it("focuses its first field and gives focus back on close", () => {
    const Page = ({ open }: { open: boolean }) => (
      <><button>Open</button>{open && <Modal title="T" onClose={() => {}}><input aria-label="Name" /></Modal>}</>
    );
    const { rerender } = render(<Page open={false} />);
    screen.getByRole("button", { name: "Open" }).focus();
    rerender(<Page open />);
    expect(document.activeElement).toBe(screen.getByLabelText("Name"));
    rerender(<Page open={false} />);
    expect(document.activeElement).toBe(screen.getByRole("button", { name: "Open" }));
  });
  it("prefers an [autofocus] element", () => {
    render(<Modal title="T" onClose={() => {}}><input aria-label="Name" /><button data-testid="b" {...{ autofocus: "" }}>Go</button></Modal>);
    expect(document.activeElement).toBe(screen.getByTestId("b"));
  });
  it("opens with showModal so the page behind is inert", () => {
    const show = vi.spyOn(HTMLDialogElement.prototype, "showModal");
    render(<Modal title="T" onClose={() => {}}><button>A</button></Modal>);
    expect(show).toHaveBeenCalledTimes(1);
    expect((screen.getByRole("dialog", { name: "T" }) as HTMLDialogElement).open).toBe(true);
    vi.restoreAllMocks();
  });
  it("Escape closes only the top one of two stacked modals", () => {
    const under = vi.fn(), over = vi.fn();
    render(<><Modal title="Under" onClose={under}><button>A</button></Modal><Modal title="Over" onClose={over}><button>B</button></Modal></>);
    fireEvent.keyDown(document.activeElement!, { key: "Escape" });
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
    const { container } = render(<Confirm title="Stop" yes="Yes, stop" onYes={() => new Promise<void>((r) => (done = r))} onClose={onClose} />);
    const yes = screen.getByRole("button", { name: "Yes, stop" }) as HTMLButtonElement;
    fireEvent.click(yes);
    await waitFor(() => expect(yes.disabled).toBe(true));
    expect((screen.getByRole("button", { name: "Cancel" }) as HTMLButtonElement).disabled).toBe(true);
    fireEvent.keyDown(window, { key: "Escape" });
    fireEvent.click(container.querySelector(".modal-bg")!);
    expect(screen.getByRole("dialog")).toBeTruthy();
    expect(onClose).not.toHaveBeenCalled();
    done();
    await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1));
  });
});

describe("Modal links", () => {
  it("focuses a link when it is the first control", () => {
    render(<Modal title="T" onClose={() => {}}><a href="/x">Docs</a></Modal>);
    expect(document.activeElement).toBe(screen.getByRole("link", { name: "Docs" }));
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
  it("opens in a browser without popover support", () => {
    const show = HTMLElement.prototype.showPopover;
    const hide = HTMLElement.prototype.hidePopover;
    Object.assign(HTMLElement.prototype, { showPopover: undefined, hidePopover: undefined });
    try {
      render(<Menu label="Actions" trigger={<>Menu</>}><MenuItem onClick={() => {}}>Item 1</MenuItem></Menu>);
      const btn = screen.getByRole("button", { name: "Actions" });
      fireEvent.click(btn);
      const item = screen.getByRole("menuitem", { name: "Item 1", hidden: true });
      expect(btn.getAttribute("aria-expanded")).toBe("true");
      fireEvent.keyDown(item, { key: "Escape" });
      expect(btn.getAttribute("aria-expanded")).toBe("false");
    } finally {
      Object.assign(HTMLElement.prototype, { showPopover: show, hidePopover: hide });
    }
  });
  it("opens fixed under its trigger and closes on scroll", () => {
    const originalInnerWidth = window.innerWidth;
    Object.defineProperty(window, "innerWidth", { value: 1000, configurable: true });

    const { container } = render(
      <Menu label="Actions" trigger={<>Menu</>}>
        <MenuItem onClick={() => {}}>Item 1</MenuItem>
      </Menu>
    );

    const btn = container.querySelector("button")!;
    vi.spyOn(btn, "getBoundingClientRect").mockReturnValue({
      bottom: 100,
      right: 300,
      top: 60,
      left: 260,
      width: 40,
      height: 40,
      x: 260,
      y: 60,
      toJSON: () => ({})
    });

    fireEvent.click(btn);

    const menu = screen.getByRole("menu");
    expect(menu.style.position).toBe("fixed");
    expect(menu.style.top).toBe("106px");
    expect(menu.style.right).toBe("700px");

    fireEvent.scroll(window);
    expect(screen.queryByRole("menu")).toBeNull();

    Object.defineProperty(window, "innerWidth", { value: originalInnerWidth, configurable: true });
  });

  it("opens above its trigger near the bottom of the viewport", () => {
    Object.defineProperty(window, "innerHeight", { value: 600, configurable: true });
    vi.spyOn(HTMLElement.prototype, "offsetHeight", "get").mockReturnValue(200);
    const { container } = render(<Menu label="Actions" trigger={<>Menu</>}><MenuItem onClick={() => {}}>Item 1</MenuItem></Menu>);
    const btn = container.querySelector("button")!;
    vi.spyOn(btn, "getBoundingClientRect").mockReturnValue({ bottom: 560, right: 300, top: 520, left: 260, width: 40, height: 40, x: 260, y: 520, toJSON: () => ({}) });
    fireEvent.click(btn);
    const menu = screen.getByRole("menu");
    expect(menu.style.bottom).toBe("86px");
    expect(menu.style.top).toBe("");
    vi.restoreAllMocks();
  });

  it("A menu item that opens a Modal gives focus back to the menu button when it closes", () => {
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
    fireEvent.click(screen.getByRole("menuitem", { name: "Rename" }));
    fireEvent.keyDown(document, { key: "Escape" });
    expect(document.activeElement).toBe(screen.getByRole("button", { name: "More" }));
  });

  const three = () => render(
    <Menu label="Actions" trigger={<>Menu</>}>
      <MenuItem onClick={() => {}}>Restart</MenuItem>
      <MenuItem onClick={() => {}}>Stop</MenuItem>
      <MenuItem onClick={() => {}}>Delete</MenuItem>
    </Menu>
  );
  const item = (name: string) => screen.getByRole("menuitem", { name });

  it("focuses the first item on open and moves with arrows, Home, End and a letter", () => {
    three();
    fireEvent.click(screen.getByRole("button", { name: "Actions" }));
    expect(document.activeElement).toBe(item("Restart"));
    const menu = screen.getByRole("menu");
    fireEvent.keyDown(menu, { key: "ArrowDown" });
    expect(document.activeElement).toBe(item("Stop"));
    fireEvent.keyDown(menu, { key: "End" });
    expect(document.activeElement).toBe(item("Delete"));
    fireEvent.keyDown(menu, { key: "ArrowDown" });
    expect(document.activeElement).toBe(item("Restart"));
    fireEvent.keyDown(menu, { key: "ArrowUp" });
    expect(document.activeElement).toBe(item("Delete"));
    fireEvent.keyDown(menu, { key: "Home" });
    expect(document.activeElement).toBe(item("Restart"));
    fireEvent.keyDown(menu, { key: "s" });
    expect(document.activeElement).toBe(item("Stop"));
    fireEvent.keyDown(menu, { key: "D" });
    expect(document.activeElement).toBe(item("Delete"));
  });

  it.each(["Escape", "Tab"])("%s closes it and gives focus back to the trigger", (key) => {
    three();
    const btn = screen.getByRole("button", { name: "Actions" });
    fireEvent.click(btn);
    fireEvent.keyDown(item("Stop"), { key });
    expect(screen.queryByRole("menu")).toBeNull();
    expect(document.activeElement).toBe(btn);
  });

  it("follows a light dismiss, and a click on the open trigger leaves it closed", () => {
    three();
    const btn = screen.getByRole("button", { name: "Actions" });
    fireEvent.click(btn);
    fireEvent.pointerDown(btn);
    act(() => screen.getByRole("menu").hidePopover());
    expect(screen.queryByRole("menu")).toBeNull();
    fireEvent.click(btn);
    expect(screen.queryByRole("menu")).toBeNull();
    expect(btn.getAttribute("aria-expanded")).toBe("false");
    fireEvent.click(btn);
    expect(screen.getByRole("menu").hasAttribute("popover")).toBe(true);
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
    process.env.TZ = "Asia/Tashkent";
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
