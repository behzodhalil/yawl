import { CodeHighlighted, ShikiProvider } from "@cloudflare/kumo/code";

const VISIBLE_SCROLLBAR = "[&_.overflow-x-auto::-webkit-scrollbar]:h-2 [&_.overflow-x-auto::-webkit-scrollbar-thumb]:rounded-full [&_.overflow-x-auto::-webkit-scrollbar-thumb]:bg-kumo-line";

export default function YamlView({ code, highlightLines }: { code: string; highlightLines?: number[] }) {
  return (
    <ShikiProvider engine="javascript" languages={["yaml"]}>
      <CodeHighlighted className={VISIBLE_SCROLLBAR} code={code} lang="yaml" showLineNumbers highlightLines={highlightLines} showCopyButton labels={{ copy: "Copy yawl.yaml", copied: "Copied" }} />
    </ShikiProvider>
  );
}
