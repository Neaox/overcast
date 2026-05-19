import {
  CheckCircle2,
  XCircle,
  MinusCircle,
  Ban,
  Loader2,
  Slash,
  CircleOff,
  Clock,
} from "lucide-react";
import { tv } from "tailwind-variants";
import { cn } from "../lib/cn";
import type { Status } from "../types/index";

// ─── Config ───────────────────────────────────────────────────────────────────

export const STATUS_CONFIG: Record<
  Status,
  { icon: typeof CheckCircle2; iconCls: string; pillCls: string; label: string }
> = {
  pass: {
    icon: CheckCircle2,
    iconCls: "text-green-500",
    pillCls:
      "bg-green-50 text-green-700 ring-green-200 dark:bg-green-950 dark:text-green-300 dark:ring-green-800",
    label: "Pass",
  },
  fail: {
    icon: XCircle,
    iconCls: "text-red-500",
    pillCls:
      "bg-red-50 text-red-700 ring-red-200 dark:bg-red-950 dark:text-red-300 dark:ring-red-800",
    label: "Fail",
  },
  skip: {
    icon: MinusCircle,
    iconCls: "text-amber-400",
    pillCls:
      "bg-amber-50 text-amber-700 ring-amber-200 dark:bg-amber-950 dark:text-amber-300 dark:ring-amber-800",
    label: "Skip",
  },
  unimplemented: {
    icon: Ban,
    iconCls: "text-gray-400",
    pillCls:
      "bg-gray-50 text-gray-500 ring-gray-200 dark:bg-gray-800 dark:text-gray-400 dark:ring-gray-700",
    label: "Not impl.",
  },
  running: {
    icon: Loader2,
    iconCls: "text-blue-400",
    pillCls:
      "bg-blue-50 text-blue-500 ring-blue-200 dark:bg-blue-950 dark:text-blue-400 dark:ring-blue-800",
    label: "Running",
  },
  na: {
    icon: Slash,
    iconCls: "text-gray-300 dark:text-gray-600",
    pillCls:
      "bg-gray-50 text-gray-400 ring-gray-200 dark:bg-gray-800 dark:text-gray-500 dark:ring-gray-700",
    label: "N/A",
  },
  cancelled: {
    icon: CircleOff,
    iconCls: "text-gray-400 dark:text-gray-500",
    pillCls:
      "bg-gray-50 text-gray-500 ring-gray-200 dark:bg-gray-800 dark:text-gray-400 dark:ring-gray-700",
    label: "Cancelled",
  },
  queued: {
    icon: Clock,
    iconCls: "text-blue-300 dark:text-blue-600",
    pillCls:
      "bg-blue-50 text-blue-400 ring-blue-200 dark:bg-blue-950 dark:text-blue-500 dark:ring-blue-800",
    label: "Queued",
  },
};

// ─── StatusIcon ───────────────────────────────────────────────────────────────

export function StatusIcon({
  status,
  size = 16,
}: {
  status: Status;
  size?: number;
}) {
  const { icon: Icon, iconCls } = STATUS_CONFIG[status];
  return <Icon size={size} className={iconCls} strokeWidth={2} />;
}

// ─── StatusBadge ─────────────────────────────────────────────────────────────

export function StatusBadge({
  status,
  expandable,
  active,
}: {
  status: Status;
  expandable?: boolean;
  active?: boolean;
}) {
  const { icon: Icon, iconCls } = STATUS_CONFIG[status];
  return (
    <span className={cn(badge({ expandable, active }), iconCls)}>
      {status === "running" ? (
        <span className="inline-block w-2.5 h-2.5 rounded-full bg-blue-400 animate-pulse" />
      ) : status === "queued" ? (
        <span className="inline-block w-2.5 h-2.5 rounded-full bg-blue-300 dark:bg-blue-600 animate-pulse opacity-60" />
      ) : status === "cancelled" ? (
        <Icon size={16} strokeWidth={2.2} className="line-through" />
      ) : (
        <Icon size={16} strokeWidth={2.2} />
      )}
    </span>
  );
}

// ─── CountChip ────────────────────────────────────────────────────────────────

export function CountChip({
  status,
  count,
}: {
  status: Status;
  count: number;
}) {
  if (count === 0) return null;
  const { pillCls, label } = STATUS_CONFIG[status];
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium ring-1",
        pillCls,
      )}
    >
      <StatusIcon status={status} size={11} />
      {count}
      <span className="hidden sm:inline">{label}</span>
    </span>
  );
}

const badge = tv({
  base: "inline-flex items-center justify-center w-7 h-7 rounded-full transition-all",
  variants: {
    expandable: {
      true: "ring-2 ring-offset-1 ring-current/40 cursor-pointer hover:ring-current/70",
    },
    active: {
      true: "ring-2 ring-offset-1 ring-current cursor-pointer",
    },
  },
  // active takes priority over expandable ring style
  compoundVariants: [
    {
      expandable: true,
      active: true,
      class: "ring-2 ring-offset-1 ring-current cursor-pointer",
    },
  ],
});
