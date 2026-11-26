import { CodeHighlighted, ShikiProvider } from "@cloudflare/kumo/code";
import { Copy } from "../ui";

export default function Command({ code }: { code: string }) {
  return (
    <div className="flex flex-col items-end gap-2">
      <Copy text={code} />
      <ShikiProvider engine="javascript" languages={["bash"]}>
        <CodeHighlighted className="w-full" code={code} lang="bash" />
      </ShikiProvider>
    </div>
  );
}
