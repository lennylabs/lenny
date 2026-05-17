// SPDX-License-Identifier: MIT

package agentcard_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/agentcard"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
)

func sampleInterface() runtimestore.AgentInterface {
	return runtimestore.AgentInterface{
		Description:            "Analyzes codebases and produces refactoring plans",
		InputModes:             []runtimestore.AgentInterfaceMode{{Type: "text/plain"}, {Type: "application/json"}},
		OutputModes:            []runtimestore.AgentInterfaceMode{{Type: "text/plain", Role: "primary"}},
		SupportsWorkspaceFiles: true,
		Skills:                 []runtimestore.AgentInterfaceSkill{{ID: "review", Name: "Code Review", Description: "Reviews code"}},
		Examples:               []runtimestore.AgentInterfaceExample{{Description: "Review auth", Input: "Review the auth module"}},
	}
}

func TestGenerateMapsAgentInterface(t *testing.T) {
	at := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	card := agentcard.Generate("refactorer", sampleInterface(), at, "1.4.0")

	if card.Name != "refactorer" {
		t.Errorf("name: %q", card.Name)
	}
	if card.Description != "Analyzes codebases and produces refactoring plans" {
		t.Errorf("description: %q", card.Description)
	}
	if len(card.DefaultInputModes) != 2 || card.DefaultInputModes[0] != "text/plain" ||
		card.DefaultInputModes[1] != "application/json" {
		t.Errorf("defaultInputModes: %+v", card.DefaultInputModes)
	}
	if len(card.DefaultOutputModes) != 1 || card.DefaultOutputModes[0] != "text/plain" {
		t.Errorf("defaultOutputModes: %+v", card.DefaultOutputModes)
	}
	if len(card.Skills) != 1 || card.Skills[0].ID != "review" || card.Skills[0].Name != "Code Review" {
		t.Errorf("skills: %+v", card.Skills)
	}
	if len(card.Examples) != 1 || card.Examples[0].Input != "Review the auth module" {
		t.Errorf("examples: %+v", card.Examples)
	}
}

func TestGenerateInjectsEnvelopeFields(t *testing.T) {
	// §5.1: every auto-generated card carries the two envelope fields
	// generatedAt (RFC 3339) and generatorVersion, injected by the
	// generator rather than drawn from agentInterface.
	at := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	card := agentcard.Generate("refactorer", sampleInterface(), at, "1.4.0")
	if card.GeneratedAt != "2026-05-17T12:00:00Z" {
		t.Errorf("generatedAt: %q, want RFC 3339 2026-05-17T12:00:00Z", card.GeneratedAt)
	}
	if card.GeneratorVersion != "1.4.0" {
		t.Errorf("generatorVersion: %q", card.GeneratorVersion)
	}
	// §21.1: the v1 A2A path has elicitation disabled.
	if card.Capabilities.Elicitation {
		t.Error("a generated card must advertise elicitation false for v1")
	}
}

func TestEntryProducesPublicAgentCardEntry(t *testing.T) {
	at := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	entry := agentcard.Entry("refactorer", sampleInterface(), at, "1.4.0")

	if agentcard.Key != "agent-card" {
		t.Errorf("the agent-card key constant drifted: %q", agentcard.Key)
	}
	if entry.Key != agentcard.Key {
		t.Errorf("entry key: %q, want %q", entry.Key, agentcard.Key)
	}
	if entry.ContentType != "application/json" {
		t.Errorf("entry content type: %q", entry.ContentType)
	}
	// §5.1: A2A cards are meant for cross-organization discovery and so
	// are stored with public visibility.
	if entry.Visibility != runtimestore.VisibilityPublic {
		t.Errorf("entry visibility: %q, want public", entry.Visibility)
	}
	var card agentcard.Card
	if err := json.Unmarshal([]byte(entry.Content), &card); err != nil {
		t.Fatalf("entry content is not the JSON card: %v", err)
	}
	if card.Name != "refactorer" || card.GeneratorVersion != "1.4.0" {
		t.Errorf("decoded card: %+v", card)
	}
}

func TestNeedsRegen(t *testing.T) {
	cases := []struct {
		stored, threshold string
		want              bool
	}{
		{"1.4.0", "", true},           // empty threshold regenerates all
		{"1.3.0", "1.4.0", true},      // stored strictly older
		{"1.4.0", "1.4.0", false},     // stored equal to threshold
		{"1.5.0", "1.4.0", false},     // stored newer
		{"1.9.0", "1.10.0", true},     // numeric, not lexical, ordering
		{"", "1.4.0", true},           // no stored card regenerates
		{"dev", "1.4.0", true},        // unparseable stored sorts oldest
		{"1.4.0-rc1", "1.4.0", false}, // pre-release suffix ignored
	}
	for _, tc := range cases {
		if got := agentcard.NeedsRegen(tc.stored, tc.threshold); got != tc.want {
			t.Errorf("NeedsRegen(%q, %q) = %v, want %v", tc.stored, tc.threshold, got, tc.want)
		}
	}
}

func TestGenerateEmptyInterface(t *testing.T) {
	// A runtime with an empty agentInterface still yields a card with
	// the envelope fields and name; the optional lists stay empty.
	at := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	card := agentcard.Generate("bare", runtimestore.AgentInterface{}, at, "1.4.0")
	if card.Name != "bare" || card.GeneratorVersion != "1.4.0" {
		t.Errorf("bare card: %+v", card)
	}
	if len(card.Skills) != 0 || len(card.DefaultInputModes) != 0 {
		t.Errorf("bare card must carry no skills or modes: %+v", card)
	}
}
