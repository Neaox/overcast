/**
 * commands.ts — stdin command parser and dispatcher for interactive mode.
 *
 * Reads NDJSON commands from stdin and dispatches them to handlers.
 * Used when OVERCAST_COMPAT_INTERACTIVE=1 is set.
 */

import * as readline from "node:readline";

// ─── Command types matching the protocol ──────────────────────────────────

export interface RunCommand {
  command: "run";
  batch_id: string;
  tests?: Array<{ group: string; tests?: string[] }>;
}

export interface CancelCommand {
  command: "cancel";
  batch_id?: string;
  group?: string;
  test?: string;
}

export interface ShutdownCommand {
  command: "shutdown";
}

export interface PingCommand {
  command: "ping";
}

export type StdinCommand = RunCommand | CancelCommand | ShutdownCommand | PingCommand;

export interface CommandHandler {
  onRun: (cmd: RunCommand) => Promise<void>;
  onCancel: (cmd: CancelCommand) => void;
  onShutdown: () => Promise<void>;
  onPing: () => void;
}

/**
 * Start reading NDJSON commands from stdin.
 * Returns a cleanup function to close the readline interface.
 * Commands are dispatched to the handler as they arrive.
 */
export function startCommandLoop(handler: CommandHandler): () => void {
  const rl = readline.createInterface({
    input: process.stdin,
    terminal: false,
  });

  rl.on("line", async (line: string) => {
    const trimmed = line.trim();
    if (!trimmed) return;

    let cmd: StdinCommand;
    try {
      cmd = JSON.parse(trimmed);
    } catch {
      process.stderr.write(`[commands] invalid JSON: ${trimmed}\n`);
      return;
    }

    switch (cmd.command) {
      case "run":
        handler.onRun(cmd).catch((err) => {
          process.stderr.write(`[commands] run error: ${String(err)}\n`);
        });
        break;
      case "cancel":
        handler.onCancel(cmd);
        break;
      case "shutdown":
        await handler.onShutdown();
        break;
      case "ping":
        handler.onPing();
        break;
      default:
        process.stderr.write(
          `[commands] unknown command: ${(cmd as Record<string, unknown>).command}\n`,
        );
    }
  });

  // Safety net: if stdin closes unexpectedly, treat as shutdown
  rl.on("close", () => {
    handler.onShutdown().catch(() => {});
  });

  return () => rl.close();
}
