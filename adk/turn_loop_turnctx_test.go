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
	"time"

	"github.com/stretchr/testify/require"

	"github.com/cloudwego/eino/schema"
)

// ctxCapturingAgent records the context it receives from Run and only
// terminates once that context is cancelled (a cooperative agent).
type ctxCapturingAgent struct {
	name  string
	ctxCh chan context.Context
}

func (a *ctxCapturingAgent) Name(_ context.Context) string        { return a.name }
func (a *ctxCapturingAgent) Description(_ context.Context) string { return "ctx capturing agent" }

func (a *ctxCapturingAgent) Run(ctx context.Context, _ *AgentInput, _ ...AgentRunOption) *AsyncIterator[*AgentEvent] {
	iter, gen := NewAsyncIteratorPair[*AgentEvent]()
	a.ctxCh <- ctx
	go func() {
		<-ctx.Done()
		gen.Close()
	}()
	return iter
}

// Regression test: when the event callback fails, the turn-owned context must
// be cancelled on exit so cooperative agent work cannot outlive the loop. The
// original callback error must be preserved.
func TestTurnLoop_AgentContextCancelledAfterCallbackError(t *testing.T) {
	ctx := context.Background()
	errDelivery := errors.New("delivery failed")
	agentCtxCh := make(chan context.Context, 1)

	agent := &ctxCapturingAgent{name: "ctx-capturing", ctxCh: agentCtxCh}

	var capturedCtx context.Context
	loop := NewTurnLoop(TurnLoopConfig[string, *schema.Message]{
		GenInput: genInputConsumeAll,
		PrepareAgent: func(_ context.Context, _ *TurnLoop[string, *schema.Message], _ []string) (Agent, error) {
			return agent, nil
		},
		OnAgentEvents: func(_ context.Context, _ *TurnContext[string, *schema.Message], _ *AsyncIterator[*AgentEvent]) error {
			select {
			case capturedCtx = <-agentCtxCh:
			case <-time.After(2 * time.Second):
				return errors.New("timeout waiting for agent context")
			}
			// Return without draining the event iterator.
			return errDelivery
		},
	})
	loop.Push("item-1")
	loop.Run(ctx)
	exit := loop.Wait()

	require.ErrorIs(t, exit.ExitReason, errDelivery,
		"the original callback error must be preserved")

	require.NotNil(t, capturedCtx, "agent context was never captured")

	select {
	case <-capturedCtx.Done():
		// The turn-owned context has been cancelled on exit.
	case <-time.After(2 * time.Second):
		t.Fatal("agent context is still live after Wait returned")
	}
}
