/*
 * Copyright 2025 CloudWeGo Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package adk

import (
	"fmt"
	"sync"
	"testing"
)

type designateAgentTestConfig struct {
	value string
}

func newDesignateAgentTestOption(value string) AgentRunOption {
	return WrapImplSpecificOptFn(func(c *designateAgentTestConfig) {
		c.value = value
	})
}

func containsName(names []string, target string) bool {
	for _, name := range names {
		if name == target {
			return true
		}
	}
	return false
}

// TestAgentRunOptionDesignateAgent_NoAliasing covers the case where the base
// option has spare capacity in its agentNames slice: appending to one derived
// option must not overwrite the agents designated by another derived option.
func TestAgentRunOptionDesignateAgent_NoAliasing(t *testing.T) {
	base := newDesignateAgentTestOption("configured")
	base = base.DesignateAgent("shared1").DesignateAgent("shared2").DesignateAgent("shared3")

	first := base.DesignateAgent("first")
	second := base.DesignateAgent("second")

	if !containsName(first.agentNames, "first") {
		t.Fatalf("first option lost its designated agent: %v", first.agentNames)
	}
	if containsName(first.agentNames, "second") {
		t.Fatalf("first option was polluted by second option: %v", first.agentNames)
	}
	if !containsName(second.agentNames, "second") {
		t.Fatalf("second option lost its designated agent: %v", second.agentNames)
	}
	if containsName(second.agentNames, "first") {
		t.Fatalf("second option was polluted by first option: %v", second.agentNames)
	}

	for _, name := range []string{"shared1", "shared2", "shared3"} {
		if !containsName(base.agentNames, name) {
			t.Fatalf("base option lost shared agent %q: %v", name, base.agentNames)
		}
	}
	if containsName(base.agentNames, "first") || containsName(base.agentNames, "second") {
		t.Fatalf("base option was mutated by derived options: %v", base.agentNames)
	}

	// The implementation specific option function must still be applied to the
	// agent designated by the first option only.
	firstCfg := GetImplSpecificOptions[designateAgentTestConfig](nil, filterOptions("first", []AgentRunOption{first})...)
	if firstCfg.value != "configured" {
		t.Fatalf("first agent should see the configured option, got %q", firstCfg.value)
	}
	secondCfg := GetImplSpecificOptions[designateAgentTestConfig](nil, filterOptions("second", []AgentRunOption{first})...)
	if secondCfg.value != "" {
		t.Fatalf("second agent should not see the option designated to first, got %q", secondCfg.value)
	}
}

// TestAgentRunOptionDesignateAgent_EmptyArgs verifies that branching with no
// arguments never duplicates or mutates the designation list.
func TestAgentRunOptionDesignateAgent_EmptyArgs(t *testing.T) {
	base := newDesignateAgentTestOption("configured").DesignateAgent("agent1")
	derived := base.DesignateAgent()

	if len(base.agentNames) != 1 || base.agentNames[0] != "agent1" {
		t.Fatalf("unexpected base agentNames: %v", base.agentNames)
	}
	if len(derived.agentNames) != 1 || derived.agentNames[0] != "agent1" {
		t.Fatalf("unexpected derived agentNames: %v", derived.agentNames)
	}
}

// TestAgentRunOptionDesignateAgent_ConcurrentDerivation races several derived
// options created from the same base option. Each derived option must keep only
// its own designation plus the shared ones.
func TestAgentRunOptionDesignateAgent_ConcurrentDerivation(t *testing.T) {
	base := newDesignateAgentTestOption("configured").DesignateAgent("shared")

	const n = 100
	derived := make([]AgentRunOption, n)

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			derived[i] = base.DesignateAgent(fmt.Sprintf("agent-%d", i))
		}(i)
	}
	wg.Wait()

	for i := 0; i < n; i++ {
		own := fmt.Sprintf("agent-%d", i)
		if !containsName(derived[i].agentNames, own) {
			t.Fatalf("derived option %d lost its own agent: %v", i, derived[i].agentNames)
		}
		if len(derived[i].agentNames) != 2 {
			t.Fatalf("derived option %d has unexpected agents: %v", i, derived[i].agentNames)
		}
		if !containsName(base.agentNames, "shared") || len(base.agentNames) != 1 {
			t.Fatalf("base option was corrupted: %v", base.agentNames)
		}
	}
}
