# Syntax highlighting

A TextMate grammar and a language configuration for DQL, shipped with the
language so an editor does not have to guess.

| File | What it is |
|------|------------|
| `dql.tmLanguage.json` | TextMate grammar, scope `source.dql` |
| `language-configuration.json` | Comments, brackets, auto-closing and surrounding pairs |

TextMate is the format VS Code, Sublime Text, GitHub Linguist and most other
editors read, so one grammar covers nearly everything. It highlights by pattern
matching, without parsing — fast, and correct on a half-typed file, which is
the state a file being edited is usually in.

The grammar covers the textual pipe syntax — `source events | filter … | limit 10`.
Document-form queries are YAML or JSON, and are highlighted by whatever support
the editor already has for those.

## VS Code

Reference them from an extension's `package.json`:

```json
{
  "contributes": {
    "languages": [{
      "id": "dql",
      "extensions": [".dql"],
      "configuration": "./syntaxes/language-configuration.json"
    }],
    "grammars": [{
      "language": "dql",
      "scopeName": "source.dql",
      "path": "./syntaxes/dql.tmLanguage.json"
    }]
  }
}
```

Pair it with [`cmd/dql-lsp`](../cmd/dql-lsp) and you have highlighting,
completion, hover and diagnostics with no bespoke client code.

## Other editors

- **Sublime Text** — reads `.tmLanguage.json` directly; drop it in a package.
- **Neovim / Helix / Zed** — prefer tree-sitter. This grammar will not load
  there; see below.
- **GitHub** — syntax colouring for a new language goes through
  [Linguist](https://github.com/github-linguist/linguist), which accepts a
  TextMate grammar. This one is the file to submit.
- **Anything embedding Monaco** — the same grammar works via
  `monaco-textmate`.

## Highlighting from the language itself

Pattern matching does not know what a name refers to — a local, a builtin and
a user-defined function all look alike to a regular expression.

The accurate alternative is LSP **semantic tokens**, where the server
classifies each token using the parser and the operator catalog it already has. That is
not implemented yet; it belongs in `cmd/dql-lsp`, which already holds the
document text and everything needed to answer. Until then this grammar is what
provides colour, and it is sufficient for reading code.
