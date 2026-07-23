// Package pricing implements list-price estimation for qualification runs.
// It is llm-free: token counting arrives behind Counter, supplied by the CLI
// module. Costs are estimates, never invoices (design: "Preflight token and
// cost estimate").
//
// Three things live here: Snapshot, a frozen models.dev price table with
// provenance (snapshot.go); Cost, the pure calculation that prices one
// Usage against one Rates row, honest about anything it cannot know
// (cost.go); and Preflight, which turns a set of runnable qual.TablePlan
// values into the call-count and token-cost plan a caller prints before
// spending a paid call (preflight.go).
package pricing
