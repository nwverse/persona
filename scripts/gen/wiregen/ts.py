"""TypeScript: interfaces, a union of its variants' interfaces, string enums open to values a newer hub sends."""

from __future__ import annotations

import json
import re

from .model import DEFAULT_HEADER, Call, Def, Field, TypeRef
from .names import comment, upper_snake, plural


def ts_calls(calls: list[Call]) -> list[str]:
    if not calls:
        return []
    out = ["/** One call or event of an extension point's protocol: who sends it, and the definitions its params and",
           " * its answer hold to (no result: any JSON; an event has no answer). */",
           "export interface Call {", '  from: "hub" | "program";', "  params: string;", "  result?: string;",
           "  event?: boolean;", "}", "", "/** The calls and events the schemas describe, by name as on the wire. */",
           "export const CALLS: Record<string, Call> = {"]
    for c in calls:
        parts = [f"from: {json.dumps(c.sender)}", f"params: {json.dumps(c.params)}"]
        if c.result:
            parts.append(f"result: {json.dumps(c.result)}")
        if c.event:
            parts.append("event: true")
        out.append(f"  {json.dumps(c.name)}: {{ {', '.join(parts)} }},")
    return out + ["};", ""]



def gen_ts(defs: dict[str, Def], calls: list[Call] | None = None, header: list[str] | None = None) -> str:
    out = [*(header or DEFAULT_HEADER), ""]

    def ttype(t: TypeRef) -> str:
        k = t.kind
        if k in ("string", "time"):
            return "string"
        if k in ("int", "int64", "number"):
            return "number"
        if k == "bool":
            return "boolean"
        if k == "any":
            return "unknown"
        if k == "null":
            return "null"
        if k == "array":
            inner = ttype(t.of)
            return f"{inner}[]" if re.fullmatch(r"\w+", inner) else f"({inner})[]"
        if k == "map":
            return f"Record<string, {ttype(t.of)}>"
        return t.ref

    def fields(fs: list[Field]) -> list[str]:
        lines = []
        for f in fs:
            if f.doc and len(f.doc) <= 108:
                lines.append(f"  /** {f.doc} */")
            elif f.doc:
                lines += ["  /**"] + comment("   * ", f.doc) + ["   */"]
            typ = json.dumps(f.literal) if f.literal is not None else ttype(f.type)
            lines.append(f"  {f.name}{'' if f.required else '?'}: {typ};")
        return lines

    def doc(text: str) -> list[str]:
        if not text:
            return []
        return ["/**"] + comment(" * ", text) + [" */"]

    for d in defs.values():
        if d.kind == "enum":
            out += doc(d.doc)
            out.append(f"export type {d.name} = {' | '.join(json.dumps(v) for v in d.values)} | (string & {{}});")
            out.append(f"export const {upper_snake(plural(d.name))} = [{', '.join(json.dumps(v) for v in d.values)}] "
                       "as const;")
            out.append("")
        elif d.kind == "any":
            typ = " | ".join(ttype(t) for t in d.json_types) if d.json_types else "unknown"
            out += doc(d.doc) + [f"export type {d.name} = {typ};", ""]
        elif d.kind == "struct":
            out += doc(d.doc)
            out.append(f"export type {d.name} = {{" if d.exclusive else f"export interface {d.name} {{")
            if d.union:
                out.append(f"  {defs[d.union].discriminator}: {json.dumps(d.tag)};")
            out += fields([f for f in d.fields if f.name not in d.exclusive])
            if d.extra:
                out += ["  /** Extra keys are opaque JSON; declared properties keep their own types. */",
                        "  [key: string]: unknown;"]
            if d.exclusive:
                out += ["} & ("]
                for payload in d.exclusive:
                    parts = [f"{f.name}{'' if f.name == payload else '?'}: "
                             + (ttype(f.type) if f.name == payload else "never")
                             for f in d.fields if f.name in d.exclusive]
                    out.append("  | { " + "; ".join(parts) + " }")
                out += [");", ""]
            else:
                out += ["}", ""]
        elif d.kind == "union":
            out += doc(d.doc)
            out.append(f"export type {d.name} =")
            for i, v in enumerate(d.variants):
                out.append(f"  | {v}{';' if i == len(d.variants) - 1 else ''}")
            tags = ", ".join(json.dumps(defs[v].tag) for v in d.variants)
            out += [f"export const {upper_snake(d.name)}_{upper_snake(plural(d.discriminator))} = [{tags}] as const;"]
            if any(defs[v].since for v in d.variants):
                out.append(f"export const {upper_snake(d.name)}_SINCE: Record<string, number> = {{ "
                           + ", ".join(f"{defs[v].tag}: {defs[v].since}" for v in d.variants) + " };")
            out.append("")
    out += ts_calls(calls or [])
    return "\n".join(out)

