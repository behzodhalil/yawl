import { Tabs } from "@cloudflare/kumo";
import { pickTheme, useLook, type Theme } from "../../Shell";
import { Row } from "../../ui";

const THEMES: { value: Theme; label: string }[] = [{ value: "light", label: "Light" }, { value: "dark", label: "Dark" }, { value: "system", label: "System" }];

export function Appearance() {
  const { theme } = useLook();
  return (
    <div>
      <Row title="Theme" hint="Kept in this browser.">
        <Tabs variant="segmented" className="self-start" tabs={THEMES} value={theme} onValueChange={(v) => pickTheme(v as Theme)} />
      </Row>
    </div>
  );
}
