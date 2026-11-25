"""Swift: legacy Codable structs tolerate missing/mistyped fields; new stream envelopes reject them. Unions decode
unreadable variants as .unknown; enums retain string constants (newer hub values pass through)."""

from __future__ import annotations

import json

from .model import DEFAULT_HEADER, Def, SchemaError, TypeRef, reserved_keys, unknown_doc
from .names import comment, lower_camel, plural


SWIFT_KEYWORDS = {"protocol", "extension", "default", "func", "class", "struct", "enum", "var", "let", "in", "is", "as",
                  "for", "if", "return", "switch", "case", "where", "import", "init", "operator", "static", "public",
                  "private", "internal", "repeat", "while", "do", "try", "throw", "nil", "true", "false", "super",
                  "self", "Self", "Any", "inout", "subscript", "typealias", "associatedtype", "deinit", "fileprivate",
                  "else", "guard", "defer", "break", "continue", "fallthrough", "throws", "rethrows"}


def swift_ident(s: str) -> str:
    return f"`{s}`" if s in SWIFT_KEYWORDS else s


def gen_swift(defs: dict[str, Def], header: list[str] | None = None, prefix: str = "Extension") -> str:
    out = [*(header or DEFAULT_HEADER), "", "import Foundation", ""]
    helper = (prefix or "Generated") + "WireJSON"

    def swift_name(name: str) -> str:
        return name if name.startswith(prefix) else prefix + name


    if any(d.extra and d.fields for d in defs.values()):
        out += ["// Open objects keep dictionary keys verbatim, independent of the caller's key-conversion strategy.",
                f"private nonisolated enum {helper} {{",
                "    static func decode<T: Decodable>(_ type: T.Type, from value: JSONValue?) -> T? {",
                "        guard let value else { return nil }",
                "        return try? JSONDecoder().decode(type, from: JSONEncoder().encode(value))", "    }", "",
                "    static func encode<T: Encodable>(_ value: T) throws -> JSONValue {",
                "        try JSONDecoder().decode(JSONValue.self, from: JSONEncoder().encode(value))", "    }", "}", ""]

    def stype(t: TypeRef) -> str:
        k = t.kind
        if k in ("string", "time"):
            return "String"
        if k == "int":
            return "Int"
        if k == "int64":
            return "Int64"
        if k == "number":
            return "Double"
        if k == "bool":
            return "Bool"
        if k == "any":
            return "JSONValue"
        if k == "array":
            return f"[{stype(t.of)}]"
        if k == "map":
            return f"[String: {stype(t.of)}]"
        d = defs[t.ref]
        return "String" if d.kind == "enum" else swift_name(t.ref)

    def zero(t: TypeRef) -> str:
        k = t.kind
        if k in ("string", "time"):
            return '""'
        if k in ("int", "int64", "number"):
            return "0"
        if k == "bool":
            return "false"
        if k == "any":
            return ".null"
        if k == "array":
            return "[]"
        if k == "map":
            return "[:]"
        d = defs[t.ref]
        if d.kind == "any":
            return ".null"
        if d.kind == "enum":
            return '""'
        if d.kind == "union":
            raise SchemaError(f"a required field of union type {t.ref}: not in the subset")
        return f"{swift_name(t.ref)}()"

    def struct(d: Def) -> list[str]:
        name = swift_name(d.name)
        lines = comment("/// ", d.doc)
        lines.append(f"public nonisolated struct {name}: Codable, Hashable, Sendable {{")
        for f in d.fields:
            lines += comment("    /// ", f.doc)
            typ = stype(f.type) + ("" if f.required else "?")
            lines.append(f"    public var {swift_ident(lower_camel(f.name))}: {typ}")
        if d.extra:
            lines += ["    /// Extra keys stay at this object's level on the wire; declared fields always win.",
                      "    public var additionalProperties: [String: JSONValue]"]
        if d.fields:
            lines.append("")
            params = ", ".join(f"{swift_ident(lower_camel(f.name))}: {stype(f.type)}"
                               + (f" = {zero(f.type)}" if f.required else "? = nil") for f in d.fields)
            if d.extra:
                params += ", additionalProperties: [String: JSONValue] = [:]"
            lines.append(f"    public init({params}) {{")
            for f in d.fields:
                n = lower_camel(f.name)
                lines.append(f"        self.{n} = {swift_ident(n)}")
            if d.extra:
                lines.append("        self.additionalProperties = additionalProperties")
            lines += ["    }", "", "    enum CodingKeys: String, CodingKey {"]
            for f in d.fields:
                n = lower_camel(f.name)
                lines.append(f"        case {swift_ident(n)}" + (f' = "{f.name}"' if n != f.name else ""))
            lines += ["    }", "", "    public init(from decoder: Decoder) throws {"]
            if d.extra:
                lines.append("        let raw = try decoder.singleValueContainer().decode([String: JSONValue].self)")
            else:
                lines.append("        let c = try decoder.container(keyedBy: CodingKeys.self)")
            for f in d.fields:
                n = lower_camel(f.name)
                key = swift_ident(n)
                if d.extra:
                    value = f"{helper}.decode({stype(f.type)}.self, from: raw[{json.dumps(f.name)}])"
                    lines.append(f"        self.{n} = {value}" + (f" ?? {zero(f.type)}" if f.required else ""))
                elif d.preserve_required:
                    # New stream envelopes cannot normalize malformed fields into valid operations. Required JSON
                    # values decode null explicitly; optional typed fields reject a present null or wrong type.
                    decode = f"try c.decode({stype(f.type)}.self, forKey: .{key})"
                    lines.append(f"        self.{n} = {decode}" if f.required
                                 else f"        self.{n} = c.contains(.{key}) ? {decode} : nil")
                elif f.required:
                    lines.append(f"        self.{n} = (try? c.decodeIfPresent({stype(f.type)}.self, forKey: .{key})) "
                                 f"?? {zero(f.type)}")
                else:
                    lines.append(f"        self.{n} = try? c.decodeIfPresent({stype(f.type)}.self, forKey: .{key})")
            if d.extra:
                keys = ", ".join(json.dumps(k) for k in reserved_keys(defs, d))
                lines += [f"        let known: Set<String> = [{keys}]",
                          "        additionalProperties = raw.filter { !known.contains($0.key) }"]
            lines.append("    }")
            if d.extra:
                lines += ["", "    public func encode(to encoder: Encoder) throws {",
                          f"        let known: Set<String> = [{keys}]",
                          "        var fields = additionalProperties.filter { !known.contains($0.key) }"]
                for f in d.fields:
                    n = swift_ident(lower_camel(f.name))
                    key = json.dumps(f.name)
                    if f.required:
                        lines.append(f"        fields[{key}] = try {helper}.encode({n})")
                    else:
                        lines.append(f"        if let value = {n} {{ fields[{key}] = try {helper}.encode(value) }}")
                lines += ["        var container = encoder.singleValueContainer()",
                          "        try container.encode(fields)", "    }"]
        elif d.extra:
            lines += ["", "    public init(additionalProperties: [String: JSONValue] = [:]) {",
                      "        self.additionalProperties = additionalProperties", "    }", "",
                      "    public init(from decoder: Decoder) throws {",
                      "        additionalProperties = try decoder.singleValueContainer().decode([String: JSONValue].self)", "    }", "",
                      "    public func encode(to encoder: Encoder) throws {",
                      "        var container = encoder.singleValueContainer()",
                      "        try container.encode(additionalProperties)", "    }"]
        else:
            lines += ["    public init() {}", "", "    public init(from decoder: Decoder) throws {}", "",
                      "    public func encode(to encoder: Encoder) throws {}"]
        lines += ["}", ""]
        return lines

    for d in defs.values():
        if d.kind == "enum":
            out += comment("/// ", d.doc or f"Values of {d.name}.")
            out.append(f"public nonisolated enum {swift_name(d.name)} {{")
            for v in d.values:
                # A value named like the list of values ("all") takes a suffix: Swift has one namespace for both.
                n = lower_camel(v) + ("Value" if lower_camel(v) == "all" else "")
                out.append(f'    public static let {swift_ident(n)} = "{v}"')
            out.append(f"    public static let all = [{', '.join(json.dumps(v) for v in d.values)}]")
            out += ["}", ""]
        elif d.kind == "any":
            out += comment("/// ", d.doc) + [f"public typealias {swift_name(d.name)} = JSONValue", ""]
        elif d.kind == "struct":
            out += struct(d)
        elif d.kind == "union":
            name, disc = swift_name(d.name), d.discriminator
            out += comment("/// ", d.doc)
            out.append(f"public nonisolated enum {name}: Codable, Hashable, Sendable {{")
            for v in d.variants:
                out.append(f"    case {swift_ident(lower_camel(defs[v].tag))}({swift_name(v)})")
            out += [f"    /// {unknown_doc(d)}",
                    f"    case unknown({disc}: String)", "",
                    f"    public static let {lower_camel(plural(disc))} = "
                    f"[{', '.join(json.dumps(defs[v].tag) for v in d.variants)}]", ""]
            if any(defs[v].since for v in d.variants):
                out += [f"    /// The UI version that brought each {disc}.",
                        "    public static let since: [String: Int] = ["
                        + ", ".join(f"{json.dumps(defs[v].tag)}: {defs[v].since}" for v in d.variants) + "]", ""]
            out += ["    private enum TagKey: String, CodingKey {", f"        case tag = \"{disc}\"", "    }", "",
                    f"    public var {swift_ident(disc)}: String {{", "        switch self {"]
            for v in d.variants:
                out.append(f"        case .{swift_ident(lower_camel(defs[v].tag))}: {json.dumps(defs[v].tag)}")
            out += [f"        case .unknown(let t): t", "        }", "    }", "",
                    "    public init(from decoder: Decoder) throws {",
                    "        let t = (try? decoder.container(keyedBy: TagKey.self).decode(String.self, forKey: .tag)) "
                    "?? \"\"",
                    "        switch t {"]
            for v in d.variants:
                out.append(f"        case {json.dumps(defs[v].tag)}: self = (try? {swift_name(v)}(from: decoder))"
                           f".map {{ .{swift_ident(lower_camel(defs[v].tag))}($0) }} ?? .unknown({disc}: t)")
            out += [f"        default: self = .unknown({disc}: t)", "        }", "    }", "",
                    "    public func encode(to encoder: Encoder) throws {", "        switch self {"]
            for v in d.variants:
                out.append(f"        case .{swift_ident(lower_camel(defs[v].tag))}(let v): try v.encode(to: encoder)")
            out += ["        case .unknown: break", "        }",
                    "        var c = encoder.container(keyedBy: TagKey.self)",
                    f"        try c.encode({swift_ident(disc)}, forKey: .tag)", "    }", "}", ""]
    return "\n".join(out)

