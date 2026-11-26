import { createElement, type ComponentType } from "react";
import {
  ArrowClockwiseIcon, ArrowUpIcon, BellIcon, BellSimpleIcon, BracketsCurlyIcon, BroadcastIcon, FoldersIcon, GearSixIcon, ListMagnifyingGlassIcon, PuzzlePieceIcon, SirenIcon, UsersThreeIcon, CaretDownIcon, CaretRightIcon, CaretUpIcon, CheckCircleIcon, CheckIcon,
  CircleNotchIcon, ClockCounterClockwiseIcon, ClockIcon, CodeIcon, CopyIcon, CornersOutIcon, DatabaseIcon, DotsThreeIcon,
  EnvelopeIcon, FolderIcon, GearIcon, GlobeIcon, HardDrivesIcon, HeartbeatIcon, InfoIcon, KeyIcon, LightningIcon,
  MagnifyingGlassIcon, MinusCircleIcon, MoonIcon, PackageIcon, PaperPlaneTiltIcon, PencilSimpleIcon, PlugIcon, PlusIcon, PulseIcon,
  RocketLaunchIcon, SquaresFourIcon, StackIcon, SunIcon, TextAlignLeftIcon, TreeStructureIcon, UserIcon, UsersIcon,
  WarningCircleIcon, WarningIcon, WebhooksLogoIcon, WifiSlashIcon, WrenchIcon, XCircleIcon, XIcon, type IconProps,
} from "@phosphor-icons/react";

type IconComponent = ComponentType<IconProps>;

const ENV_STROKE = 1.5;

function envMark(bars: [number, number, number, number][]): IconComponent {
  return ({ size = 16, className }: IconProps) =>
    createElement("svg", { width: size, height: size, viewBox: "0 0 24 24", className, "aria-hidden": true, fill: "none", stroke: "currentColor", strokeWidth: ENV_STROKE },
      createElement("rect", { x: 3.75, y: 3.75, width: 16.5, height: 16.5 }),
      ...bars.map(([x, y, w, h], i) => createElement("rect", { key: i, x, y, width: w, height: h, fill: "currentColor", stroke: "none" })));
}

export const ICONS: Record<string, IconComponent> = {
  users: UsersIcon, server: HardDrivesIcon, bell: BellIcon, log: PulseIcon, layers: StackIcon, key: KeyIcon, code: CodeIcon,
  folder: FolderIcon, check: CheckIcon, cross: XIcon, x: XIcon, chev: CaretRightIcon, plus: PlusIcon, search: MagnifyingGlassIcon,
  globe: GlobeIcon, db: DatabaseIcon, cog: GearIcon, worker: TreeStructureIcon, copy: CopyIcon, send: PaperPlaneTiltIcon,
  theme: MoonIcon, sun: SunIcon, down: CaretDownIcon, up: CaretUpIcon, more: DotsThreeIcon, box: PackageIcon, cron: ClockIcon,
  template: SquaresFourIcon, build: WrenchIcon, mail: EnvelopeIcon, webhook: WebhooksLogoIcon,
  "log-info": InfoIcon, "log-alert": WarningIcon, flash: LightningIcon, "to-top": ArrowUpIcon, fullscreen: CornersOutIcon,
  "status-ok": CheckCircleIcon, "status-busy": CircleNotchIcon, "status-bad": XCircleIcon, "status-warn": WarningCircleIcon,
  "status-none": WifiSlashIcon, "status-off": MinusCircleIcon,
  pulse: HeartbeatIcon, person: UserIcon, alert: WarningIcon, plug: PlugIcon, history: ClockCounterClockwiseIcon,
  rocket: RocketLaunchIcon, logs: TextAlignLeftIcon, restart: ArrowClockwiseIcon, pencil: PencilSimpleIcon,
  folders: FoldersIcon, braces: BracketsCurlyIcon, siren: SirenIcon, broadcast: BroadcastIcon, puzzle: PuzzlePieceIcon,
  "bell-simple": BellSimpleIcon, "users-three": UsersThreeIcon, "activity-list": ListMagnifyingGlassIcon, "gear-six": GearSixIcon,
  "env-production": envMark([[6, 6, 12, 3], [6, 10.5, 12, 3], [6, 15, 12, 3]]),
  "env-staging": envMark([[6, 10.5, 12, 3], [6, 15, 12, 3]]),
  "env-development": envMark([[6, 15, 12, 3]]),
  "env-preview": envMark([[6, 6, 3, 12]]),
};

export function envIcon(env: string): string {
  if (env === "production") return "env-production";
  if (env.startsWith("pr-")) return "env-preview";
  return env.startsWith("dev") ? "env-development" : "env-staging";
}
