// compat/suites/cdk/run.js
//
// Entry point for the cdk compat suite. See the twin in
// compat/suites/node-js-sdk/run.js for why this file is plain JavaScript and
// what it is guarding against — the two suites share the arrangement because
// they share the constraint: TypeScript sources run directly by Node, no
// build step, no loader.
//
// Deliberately no top-level `await` and no other recent syntax here: a parse
// error would fire before the version check and defeat the whole point.

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
    "compat/cdk needs Node >= 22.18.0 (or >= 23.6.0); this is v" +
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
