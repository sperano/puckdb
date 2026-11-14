package simulation

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// The supported pool shape. The system prompt states one category list
// and one roster (v1FixedCategories, v1FixedRoster in prompts.go) and the
// standings score the same categories whatever a pool stores, so a pool
// created with any other shape would tell its agents about slots the
// validators reject. Pool creation refuses such a pool (ValidateCategories,
// ValidateRosterPositions). Whether categories and the roster become
// configurable per pool is an open product decision (wiki
// puckdb/simulation.md > "Open decisions"); until it is taken, these
// checks keep configured pools and prompts identical.

// rosterSlotOrder is the display order of the roster slots, the order
// of the prompt's "Roster:" line.
var rosterSlotOrder = []RosterSlot{SlotC, SlotLW, SlotRW, SlotD, SlotUtil, SlotG, SlotBN, SlotIR}

// ErrUnsupportedPoolShape marks a category list or roster other than the
// supported one.
var ErrUnsupportedPoolShape = errors.New("unsupported pool shape")

// SupportedCategories returns the roto categories the simulator scores,
// in prompt order.
func SupportedCategories() []string {
	return append([]string(nil), v1FixedCategories...)
}

// SupportedRosterPositions returns the roster slot counts the simulator
// supports.
func SupportedRosterPositions() map[RosterSlot]int {
	out := make(map[RosterSlot]int, len(v1FixedRoster))
	for slot, n := range v1FixedRoster {
		out[slot] = n
	}
	return out
}

// IsRosterSlot reports whether s names a roster slot.
func IsRosterSlot(s RosterSlot) bool {
	for _, slot := range rosterSlotOrder {
		if slot == s {
			return true
		}
	}
	return false
}

// RosterCapacity is the number of players a roster with these slot counts
// holds, every slot (bench and IR included) counted.
func RosterCapacity(positions map[RosterSlot]int) int {
	total := 0
	for _, n := range positions {
		total += n
	}
	return total
}

// ValidateCategories accepts exactly the supported categories, each once,
// in any order.
func ValidateCategories(categories []string) error {
	supported := make(map[string]bool, len(v1FixedCategories))
	for _, c := range v1FixedCategories {
		supported[c] = true
	}
	seen := make(map[string]bool, len(categories))
	var problems []string
	for _, c := range categories {
		switch {
		case !supported[c]:
			problems = append(problems, fmt.Sprintf("unknown category %q", c))
		case seen[c]:
			problems = append(problems, fmt.Sprintf("duplicate category %q", c))
		}
		seen[c] = true
	}
	for _, c := range v1FixedCategories {
		if !seen[c] {
			problems = append(problems, fmt.Sprintf("missing category %q", c))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s; the simulator scores exactly [%s]",
		ErrUnsupportedPoolShape, strings.Join(problems, ", "), strings.Join(v1FixedCategories, ", "))
}

// ValidateRosterPositions accepts exactly the supported slot counts. A
// slot absent from positions counts as 0.
func ValidateRosterPositions(positions map[RosterSlot]int) error {
	var problems []string
	var unknown []string
	for slot := range positions {
		if !IsRosterSlot(slot) {
			unknown = append(unknown, string(slot))
		}
	}
	sort.Strings(unknown)
	for _, slot := range unknown {
		problems = append(problems, fmt.Sprintf("unknown slot %q", slot))
	}
	for _, slot := range rosterSlotOrder {
		if got, want := positions[slot], v1FixedRoster[slot]; got != want {
			problems = append(problems, fmt.Sprintf("%s count %d, want %d", slot, got, want))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s; the simulator supports exactly %s",
		ErrUnsupportedPoolShape, strings.Join(problems, ", "), formatRoster(v1FixedRoster))
}

// formatRoster renders slot counts in display order, as "2 C, 2 LW, ...".
func formatRoster(positions map[RosterSlot]int) string {
	parts := make([]string, 0, len(rosterSlotOrder))
	for _, slot := range rosterSlotOrder {
		parts = append(parts, fmt.Sprintf("%d %s", positions[slot], slot))
	}
	return strings.Join(parts, ", ")
}

// ValidateProvider reports whether an agent of this provider can be
// built (the same check NewAgent makes when the workflow first runs it).
func ValidateProvider(provider string) error {
	_, err := resolveProvider(provider)
	return err
}

// keyedProviderAPIHosts is the only host an agent's apiBase may name for
// a provider whose requests carry a workspace API key: NewAgent replaces
// the provider's base URL with apiBase and sends that key to it, so any
// other host would receive the key. Google routes through the OpenAI
// transport (resolveProvider), so its requests carry the OpenAI key.
// Ollama sends no key and is not listed: any http(s) host is accepted.
var keyedProviderAPIHosts = map[string]string{
	PricingProviderAnthropic: "api.anthropic.com",
	PricingProviderOpenAI:    "api.openai.com",
	PricingProviderGoogle:    "generativelanguage.googleapis.com",
}

// ErrDisallowedAPIBase marks an agent apiBase that is not an https URL
// on a keyed provider's own host (a different host, scheme or port), or
// a missing apiBase for Google, which has no default base URL.
var ErrDisallowedAPIBase = errors.New("disallowed api base")

// ValidateAPIBase accepts any absolute http(s) URL without credentials
// for Ollama, and only an https URL on the provider's own host (no port,
// no credentials) for a keyed provider. An empty apiBase keeps the
// provider's default base URL, so it is accepted for every provider but
// Google, whose requests would otherwise go to the OpenAI endpoint.
// provider must already pass ValidateProvider. The errors never quote
// apiBase: it can hold a credential and they reach the API log.
func ValidateAPIBase(provider, apiBase string) error {
	canonical := canonicalProviderName(provider)
	if apiBase == "" {
		if canonical == PricingProviderGoogle {
			return fmt.Errorf("%w: google has no default base URL, so apiBase must be an https URL on %s",
				ErrDisallowedAPIBase, keyedProviderAPIHosts[PricingProviderGoogle])
		}
		return nil
	}
	u, err := url.Parse(apiBase)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("must be an absolute http(s) URL")
	}
	if u.User != nil {
		return errors.New("must not carry credentials")
	}
	host, keyed := keyedProviderAPIHosts[canonical]
	if !keyed {
		return nil
	}
	if u.Scheme != "https" || !strings.EqualFold(u.Host, host) {
		return fmt.Errorf("%w: %s requests carry the workspace API key, so apiBase must be an https URL on %s",
			ErrDisallowedAPIBase, canonical, host)
	}
	return nil
}
