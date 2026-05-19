// Allow side-effect CSS imports (e.g. `import "./index.css"`)
// Required for TypeScript 6+ which errors on unresolvable module imports.
declare module "*.css" {
  const _: Record<string, string>;
  export default _;
}
