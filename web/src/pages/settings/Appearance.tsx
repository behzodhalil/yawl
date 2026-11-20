import { ACCENTS, pickAccent, pickTheme, useLook, type Theme } from "../../Shell";
import { Row } from "../../ui";

const THEMES: [Theme, string][] = [["light", "Light"], ["dark", "Dark"], ["system", "System"]];

export function Appearance() {
  const { theme, accent } = useLook();
  return (
    <div>
      <Row title="Theme" hint="Kept in this browser.">
        <div className="seg" data-u="start" role="group" aria-label="Theme">
          {THEMES.map(([k, label]) => (
            <button key={k} type="button" className={theme === k ? "on" : ""} aria-pressed={theme === k} onClick={() => pickTheme(k)}>{label}</button>
          ))}
        </div>
      </Row>
      <Row title="Accent" hint="The colour of primary buttons and the chosen menu item.">
        <div className="swatches" role="radiogroup" aria-label="Accent">
          {ACCENTS.map(([k, label]) => (
            <label key={k} className="swatch">
              <input type="radio" name="accent" value={k} checked={accent === k} onChange={() => pickAccent(k)} />
              <span className="sw" data-accent={k} aria-hidden="true" />
              {label}
            </label>
          ))}
        </div>
      </Row>
    </div>
  );
}
