import { StatusIcon } from "./status";

export function AppFooter() {
  return (
    <footer className="mt-10 mb-6 flex flex-col items-center gap-2.5">
      <div className="flex flex-wrap justify-center items-center gap-x-5 gap-y-1.5 text-xs text-gray-400 dark:text-gray-500">
        {LEGEND.map(({ status, label }) => (
          <span key={status} className="flex items-center gap-1.5">
            <StatusIcon status={status} size={13} />
            {label}
          </span>
        ))}
      </div>
      <p className="text-xs text-gray-300 dark:text-gray-600 text-center leading-relaxed max-w-sm">
        Pass rate includes all statuses; 100% means fully implemented.
      </p>
    </footer>
  );
}

const LEGEND: {
  status: "pass" | "fail" | "unimplemented" | "skip" | "na";
  label: string;
}[] = [
  { status: "pass", label: "Pass" },
  { status: "fail", label: "Fail" },
  { status: "unimplemented", label: "Not implemented" },
  { status: "skip", label: "Skipped" },
  { status: "na", label: "N/A" },
];
