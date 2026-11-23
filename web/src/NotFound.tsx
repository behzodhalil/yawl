import { OPEN_SEARCH } from "./Shell";
import { Icon, Link } from "./ui";

export function nearestPage(path: string): [string, string] | null {
  const [head, a, b] = path.split("/").filter(Boolean);
  if (head === "servers" && a) return [`/servers/${a}`, "Server overview"];
  if (head === "p" && a && b) return [`/p/${a}/${b}`, `${a} / ${b}`];
  return null;
}

export function NotFound({ path }: { path: string }) {
  const near = nearestPage(path);
  return (
    <>
      <h1>Nothing here</h1>
      <p className="muted">This address is not a page of this panel.</p>
      <div className="row">
        <Link to="/" className="btn pri">Back to Projects</Link>
        <button type="button" className="btn" onClick={() => window.dispatchEvent(new Event(OPEN_SEARCH))}><Icon name="search" />Search</button>
      </div>
      {near && <p className="muted">Closest page: <Link to={near[0]} className="l">{near[1]}</Link></p>}
    </>
  );
}
