import { useEffect, type ReactNode } from "react";
import { Button, ButtonGroup } from "@cloudflare/kumo";
import { Icon, Menu, MenuItem } from "./ui";

export function UnsavedBar({ detail, onReset, onSave, canDeploy = false, deployNow = false, label = "Save", deployLabel = "Save and deploy", busy, children }: {
  detail?: string; onReset: () => void; onSave: (deploy: boolean) => void; canDeploy?: boolean; deployNow?: boolean;
  label?: string; deployLabel?: string; busy?: boolean; children?: ReactNode;
}) {
  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "s") {
        e.preventDefault();
        if (!busy) onSave(deployNow);
      }
    };
    window.addEventListener("keydown", key);
    return () => window.removeEventListener("keydown", key);
  }, [busy, deployNow, onSave]);
  return (
    <div role="region" aria-label="Unsaved changes" className="sticky bottom-4 z-30 flex w-full max-w-xl flex-col gap-2 self-center mx-auto transition-[translate,opacity] duration-200 starting:translate-y-3 starting:opacity-0 motion-reduce:transition-none">
      {children}
      <div className="flex flex-wrap items-center gap-3 rounded-sm bg-kumo-elevated py-2 pr-2 pl-4 text-kumo-default shadow-lg ring ring-kumo-line">
        <span className="text-kumo-warning"><Icon name="log-alert" /></span>
        <span className="min-w-0 flex-1">
          <span className="block font-medium">Unsaved changes</span>
          {detail && <span className="block truncate text-xs text-kumo-subtle">{detail}</span>}
        </span>
        <Button variant="secondary" size="sm" disabled={busy} onClick={onReset}>Reset</Button>
        <ButtonGroup>
          <Button variant="primary" size="sm" disabled={busy} onClick={() => onSave(deployNow)}>{deployNow ? deployLabel : canDeploy ? `${label} only` : label}</Button>
          {canDeploy && (
            <Menu label="Save options" variant="primary" size="sm" trigger={<Icon name="down" />}>
              <MenuItem icon="rocket" description="The latest commit is deployed so your changes take effect now." onClick={() => onSave(true)}>Save and deploy</MenuItem>
              <MenuItem icon="check" description="Changes take effect on the next deploy." onClick={() => onSave(false)}>Save only</MenuItem>
            </Menu>
          )}
        </ButtonGroup>
      </div>
    </div>
  );
}
