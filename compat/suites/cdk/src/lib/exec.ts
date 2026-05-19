import { spawn } from "node:child_process";

export class ExecError extends Error {
  readonly code: number | null;
  readonly stdout: string;
  readonly stderr: string;

  constructor(message: string, code: number | null, stdout: string, stderr: string) {
    super(message);
    this.name = "ExecError";
    this.code = code;
    this.stdout = stdout;
    this.stderr = stderr;
  }
}

export interface ExecOptions {
  cwd: string;
  env: NodeJS.ProcessEnv;
}

export async function execCmd(
  command: string,
  args: string[],
  opts: ExecOptions,
): Promise<{ stdout: string; stderr: string }> {
  return await new Promise((resolve, reject) => {
    const cp = spawn(command, args, {
      cwd: opts.cwd,
      env: opts.env,
      stdio: ["ignore", "pipe", "pipe"],
    });

    let stdout = "";
    let stderr = "";

    cp.stdout.on("data", (chunk: Buffer) => {
      stdout += chunk.toString("utf8");
    });

    cp.stderr.on("data", (chunk: Buffer) => {
      stderr += chunk.toString("utf8");
    });

    cp.on("error", (err) => {
      reject(new ExecError(`failed to execute ${command}: ${String(err)}`, null, stdout, stderr));
    });

    cp.on("close", (code) => {
      if (code === 0) {
        resolve({ stdout, stderr });
        return;
      }
      const trimmedOut = stdout.trim();
      const trimmedErr = stderr.trim();
      const detail = [trimmedErr, trimmedOut].filter(Boolean).join("\n");
      reject(
        new ExecError(
          `${command} ${args.join(" ")} exited with ${String(code)}${detail ? `\n${detail}` : ""}`,
          code,
          stdout,
          stderr,
        ),
      );
    });
  });
}
