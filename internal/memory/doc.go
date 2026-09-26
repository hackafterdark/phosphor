// Package memory implements Phosphor's first-party memory subsystem: a
// markdown vault that is the source of truth, a disposable SQLite/FTS5 index
// derived from it, and the agent-facing tools that write through a governed
// gate.
//
// The design invariants that this package exists to protect are:
//
//   - Markdown is truth, the database is cache. Deleting memory.db and
//     rescanning the vault reproduces the index byte-for-byte.
//   - No embeddings, no external service, no background model pipeline.
//     Everything runs in-process and is deterministic.
//   - The memory tool is the only writer. edit/write cannot touch the vault,
//     which is what lets schema, sanitization and indexing be enforced.
//   - Nothing is ever hard-deleted; supersede and retire are tombstones, so a
//     wrong automatic decision stays recoverable.
//   - Memory records asserted truth. An inferred statement can only ever land
//     as a pending Tier B draft, never in the always-injected Tier A window.
package memory

import (
	"strings"
	"time"
)

// Scope distinguishes the project vault from the global vault. Project memory
// only surfaces inside its project; only global memory crosses projects.
type Scope string

const (
	ScopeGlobal  Scope = "global"
	ScopeProject Scope = "project"
)

// Type is the taxonomy of an entry. The set is closed so retrieval can filter
// on it and so the write gate can reason about blast radius.
type Type string

const (
	TypeDecision     Type = "decision"
	TypeConstraint   Type = "constraint"
	TypeRequirement  Type = "requirement"
	TypeOpenQuestion Type = "open_question"
	TypeReference    Type = "reference"
	TypePlan         Type = "plan"
	TypePreference   Type = "preference"
	TypeFact         Type = "fact"
	TypePattern      Type = "pattern"
	TypeEnvironment  Type = "environment"
	TypeTask         Type = "task"
	TypePolicy       Type = "policy"
)

// AllTypes is the closed menu of entry types.
var AllTypes = []Type{
	TypeDecision, TypeConstraint, TypeRequirement, TypeOpenQuestion,
	TypeReference, TypePlan, TypePreference, TypeFact,
	TypePattern, TypeEnvironment, TypeTask, TypePolicy,
}

// ValidType reports whether s names an entry type in the closed menu.
func ValidType(s string) bool {
	if s == "" {
		return false
	}
	for _, t := range AllTypes {
		if string(t) == strings.ToLower(s) {
			return true
		}
	}
	return false
}

// NormalType canonicalizes a user/agent supplied type string, returning
// TypeFact for anything unrecognized so a typo degrades to the least
// consequential kind instead of failing the write.
func NormalType(s string) Type {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, t := range AllTypes {
		if string(t) == s {
			return t
		}
	}
	return TypeFact
}

// DecisionTypes are the types whose utterances the post-turn nudge watches
// for, and the types the write gate treats as high blast radius.
var DecisionTypes = []Type{TypeDecision, TypeConstraint, TypeRequirement, TypePreference, TypePolicy}

// IsDecisionLike reports whether a type is one the gate should be careful
// about (it is always injected, or it constrains future behavior).
func (t Type) IsDecisionLike() bool {
	for _, d := range DecisionTypes {
		if d == t {
			return true
		}
	}
	return false
}

// Status is the lifecycle state of an entry.
type Status string

const (
	StatusActive  Status = "active"
	StatusCold    Status = "cold"
	StatusRetired Status = "retired"
	StatusMerged  Status = "merged"
	StatusPending Status = "pending"
)

// Op is the outcome of classifying a candidate against its neighbours, or the
// operation the agent explicitly requested.
type Op string

const (
	OpAdd       Op = "add"
	OpRefine    Op = "refine"
	OpQualify   Op = "qualify"
	OpSupersede Op = "supersede"
	OpMerge     Op = "merge"
	OpRetire    Op = "retire"
	OpPin       Op = "pin"
	OpUnpin     Op = "unpin"
	OpNote      Op = "note"
	OpConfirm   Op = "confirm"
	OpIgnore    Op = "ignore"
	OpNoOp      Op = "no_op"
)

// Owner marks who authored a region of an entry. Human body edits are
// authoritative; system fields are recomputed on re-index.
type Owner string

const (
	OwnerAgent  Owner = "agent"
	OwnerHuman  Owner = "human"
	OwnerSystem Owner = "system"
)

// Entry is one atomic fact: one file in the vault, one row in the index.
// Identity is ID, never the path, so Obsidian renames and moves cannot break
// references.
type Entry struct {
	ID      string `yaml:"id"`
	Thread  string `yaml:"thread,omitempty"`
	Type    Type   `yaml:"type"`
	Summary string `yaml:"summary,omitempty"`
	// Title is the human display label, written for Obsidian's benefit: the
	// agent derives it from the summary once, when an entry is written without
	// one, and from then on the file is authoritative, so a label a person
	// overwrites in Obsidian is kept verbatim by every later system rewrite.
	Title string `yaml:"title,omitempty"`
	// Tags and Links are human-editable frontmatter and are deliberately left out of
	// the tamper seal (see macCanonical). tags is Obsidian's native tag property and
	// the most hand-edited field in the vault; links is free-form a person may prune.
	// Sealing them would quarantine a note the moment anyone edited them in Obsidian,
	// and the entries row carries neither, so a re-seal could not restate them.
	Tags    []string `yaml:"tags,omitempty"`
	Links   []string `yaml:"links,omitempty"`
	Source  string   `yaml:"source,omitempty"`
	Expires string   `yaml:"expires,omitempty"`

	// System-owned fields live under the phosphor.* namespace in the file so a
	// human's own Obsidian properties can never collide with the machine's.
	Trust        float64 `yaml:"phosphor.trust,omitempty"`
	Status       Status  `yaml:"phosphor.status,omitempty"`
	Pinned       bool    `yaml:"phosphor.pinned,omitempty"`
	RecallCount  int     `yaml:"phosphor.recall_count,omitempty"`
	HelpfulCount int     `yaml:"phosphor.helpful_count,omitempty"`
	LastUsed     string  `yaml:"phosphor.last_used,omitempty"`
	Created      string  `yaml:"phosphor.created,omitempty"`
	Updated      string  `yaml:"phosphor.updated,omitempty"`
	Supersedes   string  `yaml:"phosphor.supersedes,omitempty"`
	FromDecision string  `yaml:"phosphor.from_decision,omitempty"`
	Owner        Owner   `yaml:"phosphor.owner,omitempty"`
	Asserted     *bool   `yaml:"phosphor.asserted,omitempty"`
	// Mac is the keyed digest over the entry's machine-authored content. It is
	// empty when the vault runs without integrity, so an unsigned vault stays
	// byte-identical to one written before the seal existed.
	Mac string `yaml:"phosphor.mac,omitempty"`
	// Sanitized is the §9 audit stamp: the digest of the defanged projection
	// (sanitize(body) + sanitize(notes)) — the exact bytes the render path would
	// inject. It is recomputed at every index pass and written by every system
	// write, so /memory fsck can check the invariant "every indexed row's stamp
	// equals the hash of its file's sanitized bytes" without reopening the
	// sanitizer. Like the lifecycle bookkeeping it is deliberately outside the
	// tamper seal: it is derived from the content, which the seal already covers.
	Sanitized string `yaml:"phosphor.sanitized,omitempty"`

	// Derived at load time, never authored in the frontmatter.
	Body        string  `yaml:"-"`
	Notes       string  `yaml:"-"`
	Quarantined bool    `yaml:"-"`
	Path        string  `yaml:"-"`
	Scope       Scope   `yaml:"-"`
	HotScore    float64 `yaml:"-"`

	// ContentHash of the raw file, used by the indexer to skip unchanged work.
	ContentHash string `yaml:"-"`
}

// Normalized fills in the defaults an entry needs to be a valid row, so
// callers cannot produce a half-formed record.
func (e *Entry) Normalized(now time.Time) {
	if e.Type == "" {
		e.Type = TypeFact
	}
	if e.Status == "" {
		e.Status = StatusActive
	}
	if e.Owner == "" {
		e.Owner = OwnerAgent
	}
	if e.Trust == 0 {
		e.Trust = 0.5
	}
	if e.Created == "" {
		e.Created = now.UTC().Format(time.RFC3339)
	}
	e.Updated = now.UTC().Format(time.RFC3339)
	for i, tag := range e.Tags {
		e.Tags[i] = CanonicalTag(tag)
	}
	e.Tags = dedupe(e.Tags)
}

// Inferred reports whether the entry is a guess rather than an assertion. An
// entry with no explicit verdict is treated as inferred, because the safe
// default is the one that cannot pollute the hot window.
func (e *Entry) Inferred() bool {
	return e.Asserted == nil || !*e.Asserted
}

// InjectBytes is the cost this entry imposes when it is rendered into the
// always-injected block, measured in bytes the way the rest of the budget
// system measures them.
func (e *Entry) InjectBytes() int {
	return len(e.Summary) + len(e.Body) + len(e.ID) + len(e.Thread) + len(strings.Join(e.Tags, ","))
}

func dedupe(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
