import { useEffect, useRef, useState } from "react";
import {
  ChevronDown,
  ChevronUp,
  Pause,
  Play,
  Trash2,
  Radio,
} from "lucide-react";
import { useOvercastEvents } from "../hooks/use-overcast-events";
import { cn } from "../lib/cn";

export function ServerLogPanel({ endpoint }: { endpoint: string }) {
  const [open, setOpen] = useState(false);
  const [paused, setPaused] = useState(false);
  const { events, clear } = useOvercastEvents(endpoint, paused);
  const scrollRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open || paused) return;
    const el = scrollRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [events, open, paused]);

  if (!endpoint) return null;

  return (
    <div className="fixed bottom-0 left-0 right-0 z-40 border-t border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-900 shadow-lg">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="w-full flex items-center gap-2 px-4 py-1.5 text-[11px] uppercase tracking-widest font-semibold text-gray-500 dark:text-gray-400 hover:bg-gray-50 dark:hover:bg-gray-800"
        title="Toggle overcast bus event stream"
      >
        <Radio
          size={12}
          className={cn(paused ? "text-gray-400" : "text-emerald-500")}
        />
        <span>Overcast bus</span>
        <span className="font-mono text-gray-400 dark:text-gray-500 normal-case tracking-normal">
          {events.length} events
        </span>
        <span className="ml-auto flex items-center gap-1">
          {open ? <ChevronDown size={14} /> : <ChevronUp size={14} />}
        </span>
      </button>
      {open && (
        <div className="border-t border-gray-100 dark:border-gray-800">
          <div className="flex items-center gap-1 px-4 py-1.5 border-b border-gray-100 dark:border-gray-800 bg-gray-50/60 dark:bg-gray-800/40">
            <button
              type="button"
              onClick={() => setPaused((v) => !v)}
              title={paused ? "Resume" : "Pause"}
              className="p-1 text-gray-500 hover:text-blue-600 dark:text-gray-400 dark:hover:text-blue-400"
            >
              {paused ? <Play size={13} /> : <Pause size={13} />}
            </button>
            <button
              type="button"
              onClick={clear}
              title="Clear"
              className="p-1 text-gray-500 hover:text-red-600 dark:text-gray-400 dark:hover:text-red-400"
            >
              <Trash2 size={13} />
            </button>
            <span className="ml-auto text-[10px] font-mono text-gray-400 dark:text-gray-500 truncate">
              {endpoint}/_events
            </span>
          </div>
          <div
            ref={scrollRef}
            className="h-48 overflow-y-auto font-mono text-[10px] leading-snug px-4 py-2"
          >
            {events.length === 0 ? (
              <div className="text-gray-400 dark:text-gray-600 text-center py-4">
                waiting for events…
              </div>
            ) : (
              events.map((e) => (
                <div key={e.id} className="flex gap-2 py-px">
                  <span className="text-gray-400 dark:text-gray-600 shrink-0">
                    {formatTs(e.ts)}
                  </span>
                  <span className="text-violet-600 dark:text-violet-400 shrink-0 w-16 truncate">
                    {e.source}
                  </span>
                  <span className="text-blue-600 dark:text-blue-400 shrink-0 truncate">
                    {e.type}
                  </span>
                </div>
              ))
            )}
          </div>
        </div>
      )}
    </div>
  );
}

function formatTs(ts: string): string {
  if (!ts) return "--:--:--";
  const d = new Date(ts);
  if (isNaN(d.getTime())) return "--:--:--";
  return d.toTimeString().slice(0, 8);
}
