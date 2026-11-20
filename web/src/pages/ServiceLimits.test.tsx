import { describe, expect, it } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { ServiceFields } from "./NewService";
import { emptyForm } from "../serviceForm";
import type { ServiceForm } from "../types";

function Harness({ kind, seen }: { kind: string; seen: (f: ServiceForm) => void }) {
  const [f, setF] = useState<ServiceForm>({ ...emptyForm(kind), name: "api" });
  return <ServiceFields form={f} set={(n) => (setF(n), seen(n))} />;
}

describe("ServiceFields limits", () => {
  for (const kind of ["web", "worker", "cron", "postgres"]) {
    it(`asks CPU and Memory for ${kind}`, () => {
      let last: ServiceForm | null = null;
      render(<Harness kind={kind} seen={(f) => (last = f)} />);
      const cpu = screen.getByLabelText("CPU") as HTMLInputElement;
      const mem = screen.getByLabelText("Memory") as HTMLInputElement;
      expect(cpu.placeholder).toBe("cores");
      expect(mem.placeholder).toBe("512MB");
      fireEvent.change(cpu, { target: { value: "0.5" } });
      fireEvent.change(mem, { target: { value: "512MB" } });
      expect(last!.cpu).toBe(0.5);
      expect(last!.memory).toBe("512MB");
      fireEvent.change(cpu, { target: { value: "" } });
      fireEvent.change(mem, { target: { value: "" } });
      expect(last!.cpu).toBe(0);
      expect(last!.memory).toBe("");
    });
  }

  it("keeps the CPU text while it is typed", () => {
    let last: ServiceForm | null = null;
    render(<Harness kind="web" seen={(f) => (last = f)} />);
    const cpu = screen.getByLabelText("CPU") as HTMLInputElement;
    for (const v of ["0", "0.", "0.5"]) {
      fireEvent.change(cpu, { target: { value: v } });
      expect(cpu.value).toBe(v);
    }
    expect(last!.cpu).toBe(0.5);
    fireEvent.change(cpu, { target: { value: "1,5" } });
    expect(last!.cpu).toBe(1.5);
    fireEvent.change(cpu, { target: { value: "lots" } });
    expect(cpu.value).toBe("lots");
    expect(last!.cpu).toBe(-1);
  });
});
