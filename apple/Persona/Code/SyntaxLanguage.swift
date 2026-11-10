import Foundation

/// What a run of source is, for its color (`Theme.Syntax`, mapped in `SyntaxStyle`).
nonisolated enum SyntaxKind: UInt8, Sendable {
    case plain, keyword, type, string, number, function, comment, tag, attribute, variable
    // Markdown and diffs.
    case heading, emphasis, strong, link, code, quote, added, removed, meta
}

/// One colored run of a line: byte offsets in the line's UTF-8.
nonisolated struct SyntaxToken: Sendable, Equatable {
    var start: Int32
    var length: Int32
    var kind: SyntaxKind
}

/// A string literal's shape: what opens and closes it, whether a backslash escapes, whether it runs over lines,
/// and the letters that may come right before it (r"…", f"…", b'…').
nonisolated struct SyntaxString: Sendable {
    var open: [UInt8]
    var close: [UInt8]
    var escapes = true
    var multiline = false
    var prefixes: Set<String> = []

    init(_ open: String, _ close: String? = nil, escapes: Bool = true, multiline: Bool = false, prefixes: Set<String> = []) {
        self.open = Array(open.utf8)
        self.close = Array((close ?? open).utf8)
        self.escapes = escapes
        self.multiline = multiline
        self.prefixes = prefixes
    }
}

/// A language the file viewer colors (docs/apple/ios.md「File viewer」): one generic lexer reads them all from these tables;
/// `mode` picks the few that are not C-like (markup, YAML, Markdown, diffs…).
nonisolated struct SyntaxLanguage: Sendable, Identifiable {
    enum Mode: Sendable { case code, shell, yaml, toml, json, markup, css, dockerfile, makefile, markdown, diff }

    var id: String
    var mode: Mode = .code
    var lineComments: [String] = []
    /// `#` starts a comment only after a space or at the line's start (shell, YAML): `$#` and `a#b` are not comments.
    var hashNeedsSpace = false
    var blockComment: (open: String, close: String)? = nil
    var strings: [SyntaxString] = []
    var keywords = ""
    var types = ""
    var constants = ""
    /// Called names that are the language's own (print, len, echo): colored as functions.
    var builtins = ""
    var caseInsensitive = false
    /// A word that starts with a capital is a type (Swift, Go, Kotlin…).
    var capitalizedTypes = false
    /// `@name` is an attribute / decorator / annotation.
    var annotations = false
    /// `#…` at a line's start is a preprocessor line (C).
    var preprocessor = false
    /// `$name`, `${…}`, `$(…)`, `$1` are variables (shell, Makefile, PHP).
    var dollarVariables = false
    /// `name!` is a macro call (Rust).
    var macroBang = false
    /// `'a` is a lifetime unless it closes like a character ('a') (Rust).
    var lifetimes = false
    /// `#[…]` / `#![…]` is an attribute (Rust).
    var hashAttributes = false
    /// Characters an identifier may hold besides letters, digits and `_` (`$` in JS, `-` in CSS).
    var identifierExtras = ""
    /// A string followed by `:` is a key (JSON).
    var keyedStrings = false

    /// Words → kind, hashed from their bytes (`SyntaxLexer.hash`), so the lexer never makes a String per word.
    let words: [UInt64: SyntaxKind]
    let longestWord: Int
    let identifierExtraBytes: Set<UInt8>

    init(id: String, mode: Mode = .code, lineComments: [String] = [], hashNeedsSpace: Bool = false,
         blockComment: (open: String, close: String)? = nil, strings: [SyntaxString] = [], keywords: String = "",
         types: String = "", constants: String = "", builtins: String = "", caseInsensitive: Bool = false,
         capitalizedTypes: Bool = false, annotations: Bool = false, preprocessor: Bool = false,
         dollarVariables: Bool = false, macroBang: Bool = false, lifetimes: Bool = false, hashAttributes: Bool = false,
         identifierExtras: String = "", keyedStrings: Bool = false) {
        self.id = id
        self.mode = mode
        self.lineComments = lineComments
        self.hashNeedsSpace = hashNeedsSpace
        self.blockComment = blockComment
        self.strings = strings
        self.keywords = keywords
        self.types = types
        self.constants = constants
        self.builtins = builtins
        self.caseInsensitive = caseInsensitive
        self.capitalizedTypes = capitalizedTypes
        self.annotations = annotations
        self.preprocessor = preprocessor
        self.dollarVariables = dollarVariables
        self.macroBang = macroBang
        self.lifetimes = lifetimes
        self.hashAttributes = hashAttributes
        self.identifierExtras = identifierExtras
        self.keyedStrings = keyedStrings
        var words: [UInt64: SyntaxKind] = [:]
        var longest = 0
        for (list, kind) in [(builtins, SyntaxKind.function), (types, .type), (constants, .number), (keywords, .keyword)] {
            for w in list.split(separator: " ") where !w.isEmpty {
                let bytes = Array(w.utf8)
                longest = max(longest, bytes.count)
                bytes.withUnsafeBufferPointer { words[SyntaxLexer.hash($0, 0, $0.count, lower: caseInsensitive)] = kind }
            }
        }
        self.words = words
        longestWord = longest
        identifierExtraBytes = Set(identifierExtras.utf8)
    }
}

// MARK: The languages

nonisolated extension SyntaxLanguage {
    private static let cStrings = [SyntaxString("\""), SyntaxString("'")]
    private static let cComments = (open: "/*", close: "*/")

    static let swift = SyntaxLanguage(
        id: "swift", lineComments: ["//"], blockComment: cComments,
        strings: [SyntaxString("#\"\"\"", "\"\"\"#", escapes: false, multiline: true), SyntaxString("\"\"\"", multiline: true),
                  SyntaxString("#\"", "\"#", escapes: false), SyntaxString("\"")],
        keywords: "actor any as associatedtype async await break case catch class consume continue convenience default defer deinit didSet do dynamic else enum extension fallthrough fileprivate final for func get guard if import in indirect init inout internal is isolated lazy let macro mutating nonisolated nonmutating open operator optional override package private protocol public repeat required rethrows return set some static struct subscript super switch throw throws try typealias unowned var weak where while willSet borrowing consuming sending",
        types: "Self Any AnyObject Int Int8 Int16 Int32 Int64 UInt UInt8 UInt16 UInt32 UInt64 Double Float CGFloat Bool String Character Void Never",
        constants: "true false nil self", capitalizedTypes: true, annotations: true, preprocessor: true)

    static let go = SyntaxLanguage(
        id: "go", lineComments: ["//"], blockComment: cComments,
        strings: [SyntaxString("`", escapes: false, multiline: true), SyntaxString("\""), SyntaxString("'")],
        keywords: "break case chan const continue default defer else fallthrough for func go goto if import interface map package range return select struct switch type var",
        types: "bool byte complex64 complex128 error float32 float64 int int8 int16 int32 int64 rune string uint uint8 uint16 uint32 uint64 uintptr any comparable",
        constants: "true false nil iota",
        builtins: "append cap clear close complex copy delete imag len make max min new panic print println real recover",
        capitalizedTypes: false)

    static let python = SyntaxLanguage(
        id: "python", lineComments: ["#"],
        strings: [SyntaxString("\"\"\"", multiline: true, prefixes: pyPrefixes), SyntaxString("'''", multiline: true, prefixes: pyPrefixes),
                  SyntaxString("\"", prefixes: pyPrefixes), SyntaxString("'", prefixes: pyPrefixes)],
        keywords: "and as assert async await break class continue def del elif else except finally for from global if import in is lambda nonlocal not or pass raise return try while with yield match case type",
        types: "int float str bool bytes list dict set tuple object complex frozenset bytearray",
        constants: "True False None self cls",
        builtins: "print len range open isinstance issubclass enumerate zip map filter sorted reversed sum min max abs any all iter next repr super hasattr getattr setattr input format round",
        capitalizedTypes: true, annotations: true)
    private static let pyPrefixes: Set<String> = ["r", "b", "f", "u", "rb", "br", "fr", "rf", "R", "B", "F", "U", "Rb", "bR", "RB", "BR", "Fr", "fR", "FR", "RF"]

    static let javascript = SyntaxLanguage(
        id: "javascript", lineComments: ["//"], blockComment: cComments,
        strings: [SyntaxString("`", multiline: true), SyntaxString("\""), SyntaxString("'")],
        keywords: "async await break case catch class const continue debugger default delete do else export extends finally for from function get if import in instanceof let new of return set static super switch this throw try typeof var void while with yield",
        types: "Array Boolean Date Error Map Number Object Promise RegExp Set String Symbol",
        constants: "true false null undefined NaN Infinity",
        builtins: "console require module exports window document JSON Math parseInt parseFloat setTimeout",
        annotations: true, identifierExtras: "$")

    static let typescript: SyntaxLanguage = {
        let l = javascript
        return SyntaxLanguage(
            id: "typescript", lineComments: l.lineComments, blockComment: l.blockComment, strings: l.strings,
            keywords: l.keywords + " abstract as declare enum implements interface keyof namespace private protected public readonly satisfies type infer is asserts override unique",
            types: l.types + " any boolean never number object string symbol unknown void bigint Record Partial Readonly Pick Omit",
            constants: l.constants, builtins: l.builtins, capitalizedTypes: true, annotations: true, identifierExtras: "$")
    }()

    static let rust = SyntaxLanguage(
        id: "rust", lineComments: ["//"], blockComment: cComments,
        strings: [SyntaxString("r#\"", "\"#", escapes: false, multiline: true), SyntaxString("\"", multiline: true, prefixes: ["b", "c"])],
        keywords: "as async await break const continue crate dyn else enum extern fn for if impl in let loop match mod move mut pub ref return static struct super trait type unsafe use where while yield",
        types: "Self bool char str String i8 i16 i32 i64 i128 isize u8 u16 u32 u64 u128 usize f32 f64 Vec Option Result Box Rc Arc HashMap",
        constants: "true false self None Some Ok Err",
        capitalizedTypes: true, macroBang: true, lifetimes: true, hashAttributes: true)

    static let shell = SyntaxLanguage(
        id: "shell", mode: .shell, lineComments: ["#"], hashNeedsSpace: true,
        strings: [SyntaxString("\"", multiline: true), SyntaxString("'", escapes: false, multiline: true), SyntaxString("`")],
        keywords: "if then else elif fi for in do done case esac while until function return select time local export readonly declare typeset unset shift break continue exit source trap set",
        constants: "true false",
        builtins: "echo printf cd pwd ls cat grep sed awk find xargs test read eval exec mkdir rm cp mv chmod chown curl git ssh sudo make go python3 node npm brew kill sleep wait",
        dollarVariables: true, identifierExtras: "-")

    static let yaml = SyntaxLanguage(
        id: "yaml", mode: .yaml, lineComments: ["#"], hashNeedsSpace: true,
        strings: [SyntaxString("\""), SyntaxString("'", escapes: false)],
        constants: "true false null yes no on off True False Null Yes No TRUE FALSE NULL ~")

    static let toml = SyntaxLanguage(
        id: "toml", mode: .toml, lineComments: ["#"],
        strings: [SyntaxString("\"\"\"", multiline: true), SyntaxString("'''", escapes: false, multiline: true),
                  SyntaxString("\""), SyntaxString("'", escapes: false)],
        constants: "true false inf nan")

    static let ini = SyntaxLanguage(
        id: "ini", mode: .toml, lineComments: ["#", ";"],
        strings: [SyntaxString("\""), SyntaxString("'", escapes: false)],
        constants: "true false yes no on off")

    static let json = SyntaxLanguage(
        id: "json", mode: .json, lineComments: ["//"], blockComment: cComments, strings: [SyntaxString("\"")],
        constants: "true false null", keyedStrings: true)

    static let sql = SyntaxLanguage(
        id: "sql", lineComments: ["--"], blockComment: cComments,
        strings: [SyntaxString("'", multiline: true), SyntaxString("\"")],
        keywords: "select from where and or not in is null as on join left right inner outer full cross group by order having limit offset insert into values update set delete create table index view drop alter add column primary key foreign references unique default check constraint distinct union all exists between like ilike case when then else end returning with recursive begin commit rollback transaction if cascade asc desc using conflict do nothing grant revoke trigger function returns language",
        types: "int integer bigint smallint serial bigserial text varchar char boolean bool real double precision numeric decimal date time timestamp timestamptz interval json jsonb uuid bytea",
        constants: "true false",
        builtins: "count sum avg min max coalesce now lower upper length substring concat cast extract",
        caseInsensitive: true)

    static let c = SyntaxLanguage(
        id: "c", lineComments: ["//"], blockComment: cComments, strings: cStrings,
        keywords: "auto break case const continue default do else enum extern for goto if inline register restrict return sizeof static struct switch typedef union volatile while _Bool _Atomic",
        types: "char double float int long short signed unsigned void bool size_t ssize_t int8_t int16_t int32_t int64_t uint8_t uint16_t uint32_t uint64_t FILE",
        constants: "true false NULL", preprocessor: true)

    static let cpp = SyntaxLanguage(
        id: "cpp", lineComments: ["//"], blockComment: cComments, strings: cStrings,
        keywords: c.keywords + " alignas alignof and asm catch class concept consteval constexpr constinit co_await co_return co_yield decltype delete dynamic_cast explicit export friend mutable namespace new noexcept not operator or private protected public reinterpret_cast requires static_assert static_cast template this thread_local throw try typeid typename using virtual override final",
        types: c.types + " string vector map unordered_map set shared_ptr unique_ptr optional auto wchar_t char8_t char16_t char32_t",
        constants: "true false nullptr NULL", preprocessor: true)

    static let objc = SyntaxLanguage(
        id: "objc", lineComments: ["//"], blockComment: cComments,
        strings: [SyntaxString("@\"", "\""), SyntaxString("\""), SyntaxString("'")],
        keywords: c.keywords + " self super in out inout bycopy byref oneway nonatomic atomic strong weak copy assign readonly readwrite nullable nonnull",
        types: c.types + " id instancetype BOOL NSInteger NSUInteger CGFloat SEL Class",
        constants: "YES NO nil Nil NULL true false", capitalizedTypes: true, annotations: true, preprocessor: true)

    static let kotlin = SyntaxLanguage(
        id: "kotlin", lineComments: ["//"], blockComment: cComments,
        strings: [SyntaxString("\"\"\"", multiline: true), SyntaxString("\""), SyntaxString("'")],
        keywords: "abstract actual annotation as break by catch class companion const constructor continue crossinline data delegate do dynamic else enum expect external field file final finally for fun get if import in infix init inline inner interface internal is lateinit noinline object open operator out override package param private property protected public receiver reified return sealed set setparam suspend tailrec this throw try typealias typeof val value var vararg when where while",
        types: "Any Unit Nothing Int Long Short Byte Double Float Boolean Char String Array List MutableList Map Set",
        constants: "true false null", capitalizedTypes: true, annotations: true)

    static let java = SyntaxLanguage(
        id: "java", lineComments: ["//"], blockComment: cComments,
        strings: [SyntaxString("\"\"\"", multiline: true), SyntaxString("\""), SyntaxString("'")],
        keywords: "abstract assert break case catch class const continue default do else enum extends final finally for goto if implements import instanceof interface native new package private protected public record return sealed static strictfp super switch synchronized this throw throws transient try var void volatile while yield def",
        types: "boolean byte char double float int long short String Object Integer Long List Map",
        constants: "true false null", capitalizedTypes: true, annotations: true)

    static let csharp = SyntaxLanguage(
        id: "csharp", lineComments: ["//"], blockComment: cComments,
        strings: [SyntaxString("@\"", "\"", escapes: false, multiline: true), SyntaxString("$\"", "\""), SyntaxString("\""), SyntaxString("'")],
        keywords: "abstract as async await base break case catch checked class const continue default delegate do else enum event explicit extern finally fixed for foreach get goto if implicit in interface internal is lock namespace new operator out override params private protected public readonly record ref return sealed set sizeof stackalloc static struct switch this throw try typeof unchecked unsafe using var virtual void volatile when where while yield",
        types: "bool byte char decimal double float int long object sbyte short string uint ulong ushort dynamic",
        constants: "true false null", capitalizedTypes: true, preprocessor: true)

    static let ruby = SyntaxLanguage(
        id: "ruby", lineComments: ["#"],
        strings: [SyntaxString("\""), SyntaxString("'"), SyntaxString("`")],
        keywords: "alias and begin break case class def defined? do else elsif end ensure for if in module next not or redo rescue retry return self super then undef unless until when while yield require require_relative attr_accessor attr_reader private protected public",
        constants: "true false nil",
        builtins: "puts print p raise lambda proc include extend",
        capitalizedTypes: true, annotations: true, dollarVariables: true)

    static let php = SyntaxLanguage(
        id: "php", lineComments: ["//", "#"], blockComment: cComments, strings: cStrings,
        keywords: "abstract and array as break callable case catch class clone const continue declare default do echo else elseif empty enddeclare endfor endforeach endif endswitch endwhile enum extends final finally fn for foreach function global goto if implements include include_once instanceof insteadof interface isset list match namespace new or print private protected public readonly require require_once return static switch throw trait try unset use var while yield",
        constants: "true false null TRUE FALSE NULL", dollarVariables: true)

    static let lua = SyntaxLanguage(
        id: "lua", lineComments: ["--"], blockComment: (open: "--[[", close: "]]"),
        strings: [SyntaxString("[[", "]]", escapes: false, multiline: true), SyntaxString("\""), SyntaxString("'")],
        keywords: "and break do else elseif end for function goto if in local not or repeat return then until while",
        constants: "true false nil", builtins: "print pairs ipairs require type tostring tonumber setmetatable getmetatable")

    static let dart = SyntaxLanguage(
        id: "dart", lineComments: ["//"], blockComment: cComments,
        strings: [SyntaxString("'''", multiline: true), SyntaxString("\"\"\"", multiline: true), SyntaxString("\""), SyntaxString("'")],
        keywords: "abstract as assert async await break case catch class const continue covariant default deferred do dynamic else enum export extends extension external factory final finally for get hide if implements import in interface is late library mixin new on operator part required rethrow return sealed set show static super switch sync this throw try typedef var void when while with yield",
        types: "int double num String bool List Map Set Future Stream Object dynamic",
        constants: "true false null", capitalizedTypes: true, annotations: true)

    static let proto = SyntaxLanguage(
        id: "proto", lineComments: ["//"], blockComment: cComments, strings: cStrings,
        keywords: "syntax package import option message enum service rpc returns repeated optional required oneof map reserved extend stream",
        types: "double float int32 int64 uint32 uint64 sint32 sint64 fixed32 fixed64 sfixed32 sfixed64 bool string bytes",
        constants: "true false", capitalizedTypes: true)

    static let graphql = SyntaxLanguage(
        id: "graphql", lineComments: ["#"], strings: [SyntaxString("\"\"\"", multiline: true), SyntaxString("\"")],
        keywords: "query mutation subscription fragment on type interface union enum input scalar schema extend implements directive",
        types: "Int Float String Boolean ID", constants: "true false null", capitalizedTypes: true, annotations: true, dollarVariables: true)

    static let hcl = SyntaxLanguage(
        id: "hcl", lineComments: ["#", "//"], blockComment: cComments, strings: [SyntaxString("\"")],
        keywords: "resource data variable output locals module provider terraform for in if each count depends_on lifecycle dynamic",
        types: "string number bool list map set object tuple any", constants: "true false null")

    static let cmake = SyntaxLanguage(
        id: "cmake", lineComments: ["#"], strings: [SyntaxString("\"", multiline: true)],
        keywords: "if else elseif endif foreach endforeach while endwhile function endfunction macro endmacro set unset option project cmake_minimum_required add_executable add_library target_link_libraries include_directories find_package install message",
        caseInsensitive: true, dollarVariables: true)

    static let html = SyntaxLanguage(id: "html", mode: .markup, strings: [SyntaxString("\""), SyntaxString("'")])
    static let xml = SyntaxLanguage(id: "xml", mode: .markup, strings: [SyntaxString("\""), SyntaxString("'")])

    static let css = SyntaxLanguage(
        id: "css", mode: .css, blockComment: cComments, strings: cStrings,
        constants: "inherit initial unset none auto transparent", identifierExtras: "-")
    static let scss = SyntaxLanguage(
        id: "scss", mode: .css, lineComments: ["//"], blockComment: cComments, strings: cStrings,
        constants: "inherit initial unset none auto transparent", dollarVariables: true, identifierExtras: "-")

    static let dockerfile = SyntaxLanguage(
        id: "dockerfile", mode: .dockerfile, lineComments: ["#"], hashNeedsSpace: true,
        strings: [SyntaxString("\""), SyntaxString("'", escapes: false)],
        keywords: shell.keywords, builtins: shell.builtins, dollarVariables: true)

    static let makefile = SyntaxLanguage(
        id: "makefile", mode: .makefile, lineComments: ["#"], hashNeedsSpace: true,
        strings: [SyntaxString("\""), SyntaxString("'", escapes: false)],
        keywords: "ifeq ifneq ifdef ifndef else endif include define endef export override", builtins: shell.builtins,
        dollarVariables: true, identifierExtras: "-")

    static let markdown = SyntaxLanguage(id: "markdown", mode: .markdown)
    static let diff = SyntaxLanguage(id: "diff", mode: .diff)

    static let all: [SyntaxLanguage] = [swift, go, python, javascript, typescript, rust, shell, yaml, toml, ini, json, sql, c, cpp,
                                        objc, kotlin, java, csharp, ruby, php, lua, dart, proto, graphql, hcl, cmake, html,
                                        xml, css, scss, dockerfile, makefile, markdown, diff]

    private static let byExtension: [String: SyntaxLanguage] = {
        var m: [String: SyntaxLanguage] = [:]
        func add(_ l: SyntaxLanguage, _ exts: String) { for e in exts.split(separator: " ") { m[String(e)] = l } }
        add(swift, "swift swiftinterface")
        add(go, "go")
        add(python, "py pyw pyi pyx")
        add(javascript, "js mjs cjs jsx")
        add(typescript, "ts mts cts tsx")
        add(rust, "rs")
        add(shell, "sh bash zsh ksh fish command bats")
        add(yaml, "yml yaml")
        add(toml, "toml")
        add(ini, "ini cfg conf properties env editorconfig gitconfig")
        add(json, "json jsonc json5 geojson webmanifest ipynb jsonl ndjson")
        add(sql, "sql psql")
        add(c, "c h")
        add(cpp, "cc cpp cxx c++ hh hpp hxx h++ ino cu")
        add(objc, "m mm")
        add(kotlin, "kt kts")
        add(java, "java gradle groovy scala sc")
        add(csharp, "cs")
        add(ruby, "rb rake gemspec podspec")
        add(php, "php")
        add(lua, "lua")
        add(dart, "dart")
        add(proto, "proto")
        add(graphql, "graphql gql")
        add(hcl, "tf tfvars hcl nomad")
        add(cmake, "cmake")
        add(html, "html htm xhtml vue svelte astro")
        add(xml, "xml plist svg xib storyboard csproj xcscheme entitlements strings xsd rss atom")
        add(css, "css")
        add(scss, "scss sass less")
        add(dockerfile, "dockerfile containerfile")
        add(makefile, "mk mak")
        add(markdown, "md markdown mdx mdown")
        add(diff, "diff patch")
        return m
    }()

    /// A language an extension names (docs/extensions/extensions.md「UI version 4: code hosting」 `code.language`): a linguist name ("Go",
    /// "TypeScript", "Objective-C"), an extension ("ts", ".py") or one of the lexer's own ids. Nil: plain text.
    static func named(_ name: String) -> SyntaxLanguage? {
        let n = name.trimmingCharacters(in: .whitespaces).lowercased()
        guard !n.isEmpty else { return nil }
        let aliases: [String: SyntaxLanguage] = [
            "golang": go, "objective-c": objc, "objectivec": objc, "c++": cpp, "c#": csharp, "shell": shell, "bash": shell,
            "zsh": shell, "sh": shell, "js": javascript, "jsx": javascript, "tsx": typescript, "yml": yaml, "terraform": hcl,
            "protocol buffer": proto, "protobuf": proto, "docker": dockerfile, "make": makefile,
        ]
        if let l = aliases[n] ?? all.first(where: { $0.id == n }) { return l }
        let ext = n.hasPrefix(".") ? String(n.dropFirst()) : n
        return byExtension[ext]
    }

    /// The language of a file by its name (extension, or a known name like Dockerfile), else by its first line
    /// (`#!/usr/bin/env python3`, `<?xml`). Nil: plain text.
    static func detect(name: String, firstLine: String? = nil) -> SyntaxLanguage? {
        let base = (name as NSString).lastPathComponent
        let lower = base.lowercased()
        if lower == "dockerfile" || lower.hasPrefix("dockerfile.") || lower.hasSuffix(".dockerfile") || lower == "containerfile" { return dockerfile }
        if ["makefile", "gnumakefile", "bsdmakefile"].contains(lower) { return makefile }
        if ["podfile", "gemfile", "rakefile", "fastfile", "appfile", "brewfile", "vagrantfile", "guardfile"].contains(lower) { return ruby }
        if lower == "cmakelists.txt" { return cmake }
        if lower == "jenkinsfile" { return java }
        // .envrc is direnv's shell script (below), not a .env file.
        if (lower.hasPrefix(".env") && lower != ".envrc") || [".gitignore", ".dockerignore", ".npmrc", ".gitattributes", ".gitmodules"].contains(lower) { return ini }
        if [".bashrc", ".zshrc", ".profile", ".bash_profile", ".zprofile", ".zshenv", ".bash_aliases", ".envrc"].contains(lower) { return shell }
        if ["go.mod", "go.sum", "cargo.lock", "package-lock.json"].contains(lower) {
            return lower.hasSuffix(".json") ? json : (lower == "cargo.lock" ? toml : nil)
        }
        let ext = (lower as NSString).pathExtension
        if let l = byExtension[ext] { return l }
        guard let first = firstLine?.trimmingCharacters(in: .whitespaces) else { return nil }
        if first.hasPrefix("#!") {
            for (needle, l) in [("python", python), ("node", javascript), ("deno", typescript), ("bun", typescript), ("ruby", ruby),
                                ("php", php), ("lua", lua), ("bash", shell), ("zsh", shell), ("/sh", shell), ("env sh", shell)]
            where first.contains(needle) { return l }
            return shell
        }
        if first.hasPrefix("<?xml") || first.hasPrefix("<!DOCTYPE") || first.hasPrefix("<html") { return first.hasPrefix("<?xml") ? xml : html }
        if first.hasPrefix("diff --git") || first.hasPrefix("--- ") { return diff }
        return nil
    }
}
