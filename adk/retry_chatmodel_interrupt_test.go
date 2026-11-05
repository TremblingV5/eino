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
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// newInterruptError produces a real graph interruption error, matching what a model
// adapter sees when it forwards an interrupted sub-graph through a stream.
func newInterruptError(t *testing.T) error {
	t.Helper()

	ctx := context.Background()
	g := compose.NewGraph[string, string]()
	err := g.AddLambdaNode("interrupt", compose.InvokableLambda(func(ctx context.Context, _ string) (string, error) {
		return "", compose.Interrupt(ctx, "waiting for input")
	}))
	require.NoError(t, err)
	require.NoError(t, g.AddEdge(compose.START, "interrupt"))
	require.NoError(t, g.AddEdge("interrupt", compose.END))

	r, err := g.Compile(ctx)
	require.NoError(t, err)

	_, err = r.Invoke(ctx, "in")
	require.Error(t, err)
	_, ok := compose.ExtractInterruptInfo(err)
	require.True(t, ok)

	return err
}

// midStreamInterruptModel emits a chunk and then the interrupt error on its first
// stream call, and a normal successful stream afterwards. If a retry happens, the
// second attempt succeeds and the original interruption is lost.
type midStreamInterruptModel struct {
	calls        int32
	interruptErr error
}

func (m *midStreamInterruptModel) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	return schema.AssistantMessage("generated", nil), nil
}

func (m *midStreamInterruptModel) Stream(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	attempt := atomic.AddInt32(&m.calls, 1)

	sr, sw := schema.Pipe[*schema.Message](2)
	go func() {
		defer sw.Close()
		if attempt == 1 {
			sw.Send(schema.AssistantMessage("partial", nil), nil)
			sw.Send(nil, m.interruptErr)
			return
		}
		sw.Send(schema.AssistantMessage("retried", nil), nil)
	}()

	return sr, nil
}

func (m *midStreamInterruptModel) BindTools(_ []*schema.ToolInfo) error { return nil }

func TestStreamRetry_MidStreamInterruptPreserved_Legacy(t *testing.T) {
	interruptErr := newInterruptError(t)
	m := &midStreamInterruptModel{interruptErr: interruptErr}
	w := newTypedRetryModelWrapper[*schema.Message](m, &ModelRetryConfig{
		MaxRetries:  1,
		BackoffFunc: instantBackoff,
	})

	stream, err := w.Stream(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	require.Nil(t, stream)
	require.ErrorIs(t, err, interruptErr)
	require.Equal(t, int32(1), atomic.LoadInt32(&m.calls), "the interrupted stream must not be retried")
}

func TestStreamRetry_MidStreamInterruptPreserved_ShouldRetry(t *testing.T) {
	interruptErr := newInterruptError(t)
	m := &midStreamInterruptModel{interruptErr: interruptErr}
	var policyCalls int32
	w := newTypedRetryModelWrapper[*schema.Message](m, &ModelRetryConfig{
		MaxRetries:  1,
		BackoffFunc: instantBackoff,
		ShouldRetry: func(_ context.Context, retryCtx *RetryContext) *RetryDecision {
			atomic.AddInt32(&policyCalls, 1)
			return &RetryDecision{Retry: retryCtx.Err != nil}
		},
	})

	stream, err := w.Stream(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	require.Nil(t, stream)
	require.ErrorIs(t, err, interruptErr)
	require.Equal(t, int32(1), atomic.LoadInt32(&m.calls), "the interrupted stream must not be retried")
	require.Equal(t, int32(0), atomic.LoadInt32(&policyCalls), "the retry policy must not decide on an interruption")
}

func TestGenErrWrapper_InterruptIsNotWrappedAsWillRetry(t *testing.T) {
	interruptErr := newInterruptError(t)
	wrapped := genErrWrapper(context.Background(), 1, 0, defaultIsRetryAble)(interruptErr)
	require.ErrorIs(t, wrapped, interruptErr)

	_, ok := compose.ExtractInterruptInfo(wrapped)
	require.True(t, ok)

	var willRetryErr *WillRetryError
	require.False(t, errors.As(wrapped, &willRetryErr))
}
