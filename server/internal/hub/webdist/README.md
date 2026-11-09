scripts/release/publish.sh puts the built web client (web/dist) here before building a published personad, and
`go run ./internal/webasset/precompress internal/hub/webdist` keeps its hashed assets and third-party-licenses.txt
gzipped; the first screen's brotli copies (name.br) come from the web build as they are (docs/web/web.md
"Precompressed assets").
