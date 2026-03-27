/** @type {import("prettier").Config} */
export default {
  semi: false,
  singleQuote: false,
  trailingComma: "all",
  printWidth: 100,
  tabWidth: 2,
  plugins: ["prettier-plugin-tailwindcss"],
  tailwindStylesheet: "./src/styles/global.css",
}
