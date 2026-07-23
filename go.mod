module github.com/looprig/mpqt

go 1.26.4

replace (
	github.com/looprig/core => ../core
	github.com/looprig/eval => ../eval
	github.com/looprig/inference => ../inference
	github.com/looprig/llm => ../llm
)

require github.com/looprig/eval v0.0.0-00010101000000-000000000000

require github.com/looprig/core v0.2.0
