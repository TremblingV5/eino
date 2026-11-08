/*
 * Copyright 2026 CloudWeGo Authors
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
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cloudwego/eino/schema"
)

// readOnlyFaultStore serves Get failures while keeping Set and Delete
// operational, simulating a transient read fault on the checkpoint store.
type readOnlyFaultStore struct {
	turnLoopCheckpointStore
	getErr       error
	deleteCalled bool
}

func (s *readOnlyFaultStore) Get(_ context.Context, _ string) ([]byte, bool, error) {
	return nil, false, s.getErr
}

func (s *readOnlyFaultStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteCalled = true
	delete(s.m, key)
	return nil
}

// Regression test: a transient checkpoint read error must not cause the saved
// checkpoint to be deleted. The "loaded" marker is recorded only after the
// checkpoint has been read successfully.
func TestTurnLoop_CheckpointLoadError_KeepsCheckpoint(t *testing.T) {
	ctx := context.Background()
	store := &deletableCheckpointStore{
		turnLoopCheckpointStore: turnLoopCheckpointStore{m: make(map[string][]byte)},
	}
	cpID := "load-fault-session"

	// Loop 1: push an item, then Stop so the pending item is checkpointed.
	loop1 := NewTurnLoop(TurnLoopConfig[string, *schema.Message]{
		Store:        store,
		CheckpointID: cpID,
		GenInput:     genInputConsumeAll,
		PrepareAgent: prepareTestAgent,
	})
	loop1.Push("a")
	loop1.Stop()
	loop1.Run(ctx)
	loop1.Wait()

	store.mu.Lock()
	_, exists := store.m[cpID]
	store.mu.Unlock()
	require.True(t, exists, "checkpoint should exist after the first loop saves it")

	// Loop 2: the same store/ID, but Get fails with a transient error while
	// Set and Delete stay operational.
	faulty := &readOnlyFaultStore{
		turnLoopCheckpointStore: turnLoopCheckpointStore{m: store.m},
		getErr:                  errors.New("transient read failure"),
	}
	loop2 := NewTurnLoop(TurnLoopConfig[string, *schema.Message]{
		Store:        faulty,
		CheckpointID: cpID,
		GenInput:     genInputConsumeAll,
		PrepareAgent: prepareTestAgent,
	})
	loop2.Push("b")
	loop2.Run(ctx)
	exit := loop2.Wait()

	require.Error(t, exit.ExitReason)
	assert.Contains(t, exit.ExitReason.Error(), "transient read failure",
		"the read error should be returned as-is")

	faulty.mu.Lock()
	deleteCalled := faulty.deleteCalled
	_, inStore := faulty.m[cpID]
	faulty.mu.Unlock()

	assert.False(t, deleteCalled, "a failed checkpoint read must not trigger Delete")
	assert.True(t, inStore, "the saved checkpoint should remain available for a later loop")
}
