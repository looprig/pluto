// Package packfile is the strict, versioned trust boundary between the YAML
// pack corpus and qual. Decoding rejects unknown fields, bounds sizes, and
// never executes anything; building (Task 6) turns validated documents into
// qual.Pack values via the evaluator registry.
package packfile
