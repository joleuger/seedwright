// Package contracts defines the shared Step and Card interfaces that let
// any extension record a sequence of actions — small capability interfaces,
// not a struct hierarchy.
//
// Core defines the interfaces. Core does not define a table for Cards,
// does not provide a way to register step handlers, does not dispatch
// anything or decide which steps run or in what order. Each extension
// with step-shaped data stores its own Card+Step records in its own
// S3/SQLite storage — the same precedent Batch's position/status columns
// already established.
//
// SimpleStep and SimpleCard are ready-to-use implementations for an
// extension with nothing fancier to say (Photobooth, for instance).
// Extensions with their own richer type (Batch's BatchItem, say)
// satisfy the interfaces via thin accessor methods instead, keeping
// their own storage and orchestration exactly as they are.
//
// This is not a wiring mechanism. Step and Card only give whoever is
// orchestrating a common place to write down what happened.
package contracts

import "time"

// ElementRef points to the Element a step produced, if any.
// Always singular — one Step produces at most one Element.
type ElementRef struct {
	// ElementID is the Element this step produced.
	ElementID string `json:"element_id"`
}

// Step is the minimal shared contract for one tracked unit of work.
// Extensions satisfy it structurally — via their own types and thin
// accessor methods — rather than embedding a shared struct.
type Step interface {
	// Type is extension-defined, e.g. "photobooth.capture", "batch.item".
	Type() string

	// Status is the step's current status. Exact, extension-specific.
	Status() string

	// CanonicalStatus is the step's coarse-grained status. Optional in
	// the sense that extensions that don't need a generic view may return
	// their exact Status() as-is. Extensions that do canonicalize return
	// one of five well-known states:
	//
	//	"running"  — in progress (maps: generating, processing, etc.)
	//	"success"  — completed successfully (maps: completed, done, etc.)
	//	"failed"   — terminal failure (maps: timeout, wrong format,
	//	           limit reached, internal error, etc.)
	//	"cancelled"— user- or system-initiated cancellation
	//	"waiting"  — queued, waiting to start (maps: pending, enqueued,
	//	           waiting for resources, etc.)
	//
	// The abstraction is lossy only on sub-reasons: timeout, wrong format,
	// and limit reached all map to "failed", but "failed" stays distinct
	// from "cancelled" and "success". A dashboard can show "running" with
	// a spinner, "waiting" with a clock, "success" with a checkmark,
	// "failed" in red with a retry, "cancelled" in dim with a label —
	// without knowing each extension's vocabulary.
	//
	// Generic code checks with a type assertion:
	//
	//	if cs, ok := step.(interface{ CanonicalStatus() string }); ok {
	//		state := cs.CanonicalStatus()
	//	}
	CanonicalStatus() string

	// Output is the Element this step produced, if any.
	// Always singular — a batch producing N elements has N Steps
	// (one per item), each with its own singular output.
	Output() *ElementRef
}

// Card is the minimal contract for an ordered sequence of Steps.
type Card interface {
	// ID returns the card's unique identifier.
	ID() string

	// Project returns the project this card belongs to.
	Project() string

	// Steps returns the ordered list of steps in this card.
	Steps() []Step
}

// Job is an empty marker type. It has no fields, no methods, and does
// not participate in any interface. Its sole purpose is documentation:
// embedding it in a concrete job type signals which package a Job belongs
// to, and makes the embedding pattern obvious to readers.
//
//	type GenerationJob struct {
//		Job
//		Prompt string
//		// ...
//	}
//
// This is the Transition side of a Petri net — active, not passive.
// Transitions don't hold state; they fire, deposit a Token (Element)
// into a Place (Step), and move on. A marker type is all they need.
type Job struct{}

// SimpleStep is a no-frills Step implementation for extensions whose
// steps are just type/status/output and nothing else.
type SimpleStep struct {
	// Type is extension-defined, e.g. "photobooth.capture".
	Type_ string `json:"type"`

	// Status is the step's current status.
	Status_ string `json:"status"`

	// CanonicalStatus is the coarse-grained status, optional.
	// When empty, CanonicalStatus() returns Status_ as-is.
	// When set, it's one of: "running", "success", "failed", "cancelled", "waiting".
	CanonicalStatus_ string `json:"canonical_status,omitempty"`

	// Output is the Element this step produced, if any.
	Output_ *ElementRef `json:"output,omitempty"`
}

// Type implements Step.
func (s SimpleStep) Type() string { return s.Type_ }

// Status implements Step.
func (s SimpleStep) Status() string { return s.Status_ }

// CanonicalStatus implements Step.
// Returns CanonicalStatus_ when set; falls back to Status_ when empty.
func (s SimpleStep) CanonicalStatus() string {
	if s.CanonicalStatus_ != "" {
		return s.CanonicalStatus_
	}
	return s.Status_
}

// Output implements Step.
func (s SimpleStep) Output() *ElementRef { return s.Output_ }

// SimpleCard is a no-frills Card implementation for extensions whose
// cards are just an ID, project, created time, and ordered steps.
type SimpleCard struct {
	// ID is the card's unique identifier.
	ID_ string `json:"id"`

	// Project is the project this card belongs to.
	Project_ string `json:"project"`

	// CreatedAt is when the card was created.
	CreatedAt time.Time `json:"created_at"`

	// Steps is the ordered list of steps.
	Steps_ []SimpleStep `json:"steps"`
}

// ID implements Card.
func (c SimpleCard) ID() string { return c.ID_ }

// Project implements Card.
func (c SimpleCard) Project() string { return c.Project_ }

// Steps implements Card — returns a shallow copy of the steps
// as []Step so callers can iterate without knowing the concrete type.
func (c SimpleCard) Steps() []Step {
	out := make([]Step, len(c.Steps_))
	for i, s := range c.Steps_ {
		out[i] = s
	}
	return out
}

// CanonicalStatus implements the optional Card-level canonical state.
// Returns "running" if any Step is "running"; otherwise iterates in
// reverse to find the most specific terminal state ("failed" or
// "cancelled" take priority over "success").
func (c SimpleCard) CanonicalStatus() string {
	steps := c.Steps()
	if len(steps) == 0 {
		return "success"
	}
	for i := len(steps) - 1; i >= 0; i-- {
		if cs, ok := steps[i].(interface{ CanonicalStatus() string }); ok {
			switch cs.CanonicalStatus() {
			case "running":
				return "running"
			case "failed":
				return "failed"
			case "cancelled":
				return "cancelled"
			}
		}
	}
	return "success"
}
