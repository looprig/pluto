module github.com/looprig/mpqt

go 1.26.4

replace (
	github.com/looprig/core => ../core
	github.com/looprig/eval => ../eval
	github.com/looprig/inference => ../inference
	github.com/looprig/llm => ../llm
)

require github.com/looprig/eval v0.0.0-00010101000000-000000000000

require (
	github.com/looprig/core v0.2.0
	github.com/looprig/inference v0.3.1-0.20260718005749-13e4d7f173b3
	github.com/looprig/llm v0.0.0-00010101000000-000000000000
)

require (
	github.com/google/go-tdx-guest v0.3.1 // indirect
	github.com/google/logger v1.1.1 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/crypto v0.52.0 // indirect
	golang.org/x/sys v0.45.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)
