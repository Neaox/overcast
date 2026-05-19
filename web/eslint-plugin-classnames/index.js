/**
 * eslint-plugin-classnames — enforce idiomatic Tailwind className construction
 * in React components that use the cn() utility (clsx + tailwind-merge).
 *
 * These rules ensure that className attributes are written in a way that:
 *   - tailwind-merge can resolve class conflicts correctly
 *   - prettier-plugin-tailwindcss can sort classes
 *   - Tailwind's JIT content scanner sees all class tokens
 *   - Conditional styling is readable and maintainable
 *
 * Rules:
 *   classnames/no-template-literal  — use cn() instead of template literals
 *   classnames/no-concatenation      — use cn() instead of string concatenation
 *   classnames/no-bare-ternary      — wrap conditional classNames in cn()
 *   classnames/no-redundant-cn      — don't use cn() for a single static string
 *   classnames/no-dup-ternary       — hoist shared classes out of ternary branches
 */

import noTemplateLiteral from "./rules/no-template-literal.js"
import noConcatenation from "./rules/no-concatenation.js"
import noBareTernary from "./rules/no-bare-ternary.js"
import noRedundantCn from "./rules/no-redundant-cn.js"
import noDupTernary from "./rules/no-dup-ternary.js"
import preferCva from "./rules/prefer-cva.js"

export default {
  rules: {
    "no-template-literal": noTemplateLiteral,
    "no-concatenation": noConcatenation,
    "no-bare-ternary": noBareTernary,
    "no-redundant-cn": noRedundantCn,
    "no-dup-ternary": noDupTernary,
    "prefer-cva": preferCva,
  },
}
