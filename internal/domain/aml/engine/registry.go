package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

var (
	ErrDuplicateRule = errors.New("AML rule already registered")
	ErrRuleNotFound  = errors.New("AML rule not found")
)

// RegisteredRule associates an implementation with the version and operating mode that
// should be recorded in each decision snapshot.
type RegisteredRule struct {
	Rule       Rule
	Version    string
	Mode       Mode
	Category   Category
	Severity   string
	Action     string
	Enabled    bool
	Parameters json.RawMessage
}

type RuleMetadata struct {
	Severity, Action string
	Enabled          bool
	Parameters       json.RawMessage
}

// Registry stores rules by stable code. It is safe for concurrent reads and configuration
// updates; callers receive copies of registration metadata.
type Registry struct {
	mu    sync.RWMutex
	rules map[string]RegisteredRule
}

func NewRegistry() *Registry {
	return &Registry{rules: make(map[string]RegisteredRule)}
}

// Register adds one rule. Rule codes are unique within a registry; changing a rule's version
// or mode is done with SetMode or Replace so a duplicate cannot silently shadow a rule.
func (r *Registry) Register(rule Rule, version string, mode Mode, metadata ...RuleMetadata) error {
	if rule == nil {
		return errors.New("AML rule must not be nil")
	}
	code := rule.Code()
	if code == "" || strings.TrimSpace(code) != code {
		return errors.New("AML rule code must be non-empty and trimmed")
	}
	if version == "" || strings.TrimSpace(version) != version {
		return errors.New("AML rule version must be non-empty and trimmed")
	}
	if !rule.Category().Valid() {
		return fmt.Errorf("AML rule %q has invalid category %q", code, rule.Category())
	}
	if !mode.Valid() {
		return fmt.Errorf("AML rule %q has invalid mode %q", code, mode)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.rules[code]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateRule, code)
	}
	entry := RegisteredRule{Rule: rule, Version: version, Mode: mode, Category: rule.Category(), Enabled: true}
	if len(metadata) > 0 {
		entry.Severity, entry.Action, entry.Enabled = metadata[0].Severity, metadata[0].Action, metadata[0].Enabled
		entry.Parameters = append(json.RawMessage(nil), metadata[0].Parameters...)
	}
	r.rules[code] = entry
	return nil
}

// Get returns a registration by code.
func (r *Registry) Get(code string) (RegisteredRule, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.rules[code]
	entry.Parameters = append(json.RawMessage(nil), entry.Parameters...)
	return entry, ok
}

// SetMode updates a rule's mode without changing its implementation or version.
func (r *Registry) SetMode(code string, mode Mode) error {
	if !mode.Valid() {
		return fmt.Errorf("invalid AML rule mode %q", mode)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.rules[code]
	if !ok {
		return fmt.Errorf("%w: %s", ErrRuleNotFound, code)
	}
	entry.Mode = mode
	r.rules[code] = entry
	return nil
}

// Replace changes a registered implementation and version while retaining the configured mode.
func (r *Registry) Replace(rule Rule, version string) error {
	if rule == nil {
		return errors.New("AML rule must not be nil")
	}
	code := rule.Code()
	if code == "" || strings.TrimSpace(code) != code {
		return errors.New("AML rule code must be non-empty and trimmed")
	}
	if version == "" || strings.TrimSpace(version) != version {
		return errors.New("AML rule version must be non-empty and trimmed")
	}
	if !rule.Category().Valid() {
		return fmt.Errorf("AML rule %q has invalid category %q", code, rule.Category())
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.rules[code]
	if !ok {
		return fmt.Errorf("%w: %s", ErrRuleNotFound, code)
	}
	entry.Rule = rule
	entry.Version = version
	entry.Category = rule.Category()
	r.rules[code] = entry
	return nil
}

// List returns a deterministic copy of registrations, optionally filtered by category.
// An invalid non-empty category returns no entries.
func (r *Registry) List(category Category) []RegisteredRule {
	if category != "" && !category.Valid() {
		return nil
	}
	r.mu.RLock()
	entries := make([]RegisteredRule, 0, len(r.rules))
	for _, entry := range r.rules {
		if category == "" || entry.Category == category {
			entry.Parameters = append(json.RawMessage(nil), entry.Parameters...)
			entries = append(entries, entry)
		}
	}
	r.mu.RUnlock()
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Rule.Code() < entries[j].Rule.Code()
	})
	return entries
}
