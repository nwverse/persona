// The feature-module boundary (#13434, #13430 section 6; docs/web/web.md "Modules"), over all of src (`npm run lint`'s
// second pass): Persona Core never reaches into a module, and a module reaches another only when its descriptor names it
// in `needs`. Only the module list (src/modules/index.ts) and the module host (src/core/module-host.ts) join them.
//
//   - outside src/modules: no import of a module's files (`modules/<id>/…`), static or dynamic; the list itself
//     (`./modules`, `../modules`) is the way in. Tests may reach a module's files to test them.
//   - src/core: not even the list: the host knows no module.
//   - src/modules/<id>: no import of another module's files, unless its module.ts names that module in `needs`, and
//     never of the list.
//
// The disables of these rules are counted by scripts/ci/web_module_boundary_ratchet.py: the count can only go down.
import { readdirSync, readFileSync } from "node:fs";
import parser from "@typescript-eslint/parser";
import hooks from "eslint-plugin-react-hooks";

const MODULES_DIR = new URL("./src/modules/", import.meta.url);

/** The modules (src/modules/<id>/module.ts) and the modules each names in `needs`. */
export function moduleNeeds() {
  return readdirSync(MODULES_DIR, { withFileTypes: true })
    .filter((e) => e.isDirectory() && readdirSync(new URL(`${e.name}/`, MODULES_DIR)).includes("module.ts"))
    .map((e) => {
      const source = readFileSync(new URL(`${e.name}/module.ts`, MODULES_DIR), "utf8");
      const needs = /\bneeds:\s*\[([^\]]*)\]/.exec(source)?.[1] ?? "";
      return { id: e.name, needs: [...needs.matchAll(/["']([a-z][a-z0-9-]*)["']/g)].map((m) => m[1]) };
    });
}

const INTO_A_MODULE = "(^|/)modules/(?!index$)[^/]+";
const outside = "Persona Core reaches a module only through the module list (src/modules/index.ts) and the module host (src/core/module-host.ts): docs/web/web.md \"Modules\".";
const inCore = "src/core knows no module: the module list registers them with the host (docs/web/web.md \"Modules\").";

/** no-restricted-imports and the same paths through a dynamic import(). */
function forbid(regexes, message) {
  const source = regexes.map((r) => `(${r})`).join("|");
  return {
    "no-restricted-imports": ["error", { patterns: regexes.map((regex) => ({ regex, message })) }],
    "no-restricted-syntax": ["error", { selector: `ImportExpression[source.value=/${source.replace(/\//g, "\\/")}/]`, message }],
  };
}

// the plugin only so the code's own disable comments for its rules resolve; none of its rules is on here
const base = {
  plugins: { "react-hooks": hooks },
  linterOptions: { reportUnusedDisableDirectives: false },
  languageOptions: { parser, parserOptions: { ecmaVersion: "latest", sourceType: "module", ecmaFeatures: { jsx: true } } },
};

export default [
  { ...base, files: ["src/**/*.ts", "src/**/*.tsx"], ignores: ["src/modules/**", "src/**/*.test.ts", "src/**/*.test.tsx"], rules: forbid([INTO_A_MODULE], outside) },
  { ...base, files: ["src/core/**/*.ts", "src/core/**/*.tsx"], rules: forbid([INTO_A_MODULE, "(^|/)modules$", "(^|/)modules/index$"], inCore) },
  ...moduleNeeds().map(({ id, needs }) => {
    const allowed = [id, ...needs].join("|");
    const message = `module "${id}" may import only the modules its module.ts names in \`needs\` (docs/web/web.md "Modules").`;
    return {
      ...base,
      files: [`src/modules/${id}/**/*.ts`, `src/modules/${id}/**/*.tsx`],
      rules: forbid([`^\\.\\./(?!(${allowed})(/|$))[^./][^/]*(/|$)`, `(^|/)modules/(?!(${allowed})(/|$))[^/]+`, "^\\.\\./index$", "^\\.\\.$"], message),
    };
  }),
];
