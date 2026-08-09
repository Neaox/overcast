// compat/suites/node-js-sdk/run.js
//
// Entry point for the node-js-sdk compat suite.
//
// The suite is written in TypeScript and is executed by Node directly, using
// the built-in type stripping that landed in Node 22.18 (22.x line) and 23.6.
// There is no build step and no loader — `tsx` used to be one, and a machine
// that had not run `npm install` got `ERR_MODULE_NOT_FOUND: Cannot find
// package 'tsx'` before a single test ran (issue #795).
//
// This file is plain JavaScript on purpose. A Node too old to strip types
// cannot even parse `src/runner.ts`; it dies with "Unknown file extension
// .ts" from deep inside the module loader, which reads as a broken suite
// rather than an out-of-date toolchain. Being .js means this always parses,
// so the version check below is what the user sees instead.
//
// Deliberately no top-level `await` and no other recent syntax here: a parse
// error would fire before the check and defeat the whole point.

const MIN_TWENTY_TWO = 18; // 22.18.0 backported unflagged type stripping
const MIN_TWENTY_THREE = 6; // 23.6.0 unflagged it on the current line

const parts = process.versions.node.split(".");
const major = Number(parts[0]);
const minor = Number(parts[1]);

const supported =
  major > 23 ||
  (major === 23 && minor >= MIN_TWENTY_THREE) ||
  (major === 22 && minor >= MIN_TWENTY_TWO);

if (!supported) {
  process.stderr.write(
    "compat/node-js-sdk needs Node >= 22.18.0 (or >= 23.6.0); this is v" +
      process.versions.node +
      ".\n" +
      "The suite runs its TypeScript sources directly through Node's built-in " +
      "type stripping, which older releases do not have.\n",
  );
  process.exit(1);
}

import("./src/runner.ts").catch((err) => {
  console.error(err);
  process.exit(1);
});
