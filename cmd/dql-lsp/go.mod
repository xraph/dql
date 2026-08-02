module github.com/xraph/dql/cmd/dql-lsp

go 1.26.0

require (
	github.com/xraph/dql v0.0.0
	github.com/xraph/langserver v0.0.0
)

replace github.com/xraph/dql => ../..

replace github.com/xraph/langserver => ../../../langserver
