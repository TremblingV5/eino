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

package reduction

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/filesystem"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

var errTruncHandlerFailed = errors.New("trunc handler failed")

func newCustomTruncTestConfig(name string, handler func(_ context.Context, detail *ToolDetail) (*TruncResult, error)) *Config {
	backend := filesystem.NewInMemoryBackend()
	return &Config{
		Backend: backend,
		ToolConfig: map[string]*ToolReductionConfig{
			name: {
				Backend:      backend,
				TruncHandler: handler,
			},
		},
	}
}

// Regression tests for the custom TruncHandler streamable wrappers: when the
// handler returns NeedTrunc:false or an error, the analysis copy handed to the
// handler must be closed so cancellation propagates to the upstream producer.

func TestCustomTruncPassthroughClosesAnalysisCopy(t *testing.T) {
	ctx := context.Background()
	config := newCustomTruncTestConfig("plain_tool", func(_ context.Context, _ *ToolDetail) (*TruncResult, error) {
		// Do not consume the analysis copy.
		return &TruncResult{NeedTrunc: false}, nil
	})
	mw, err := New(ctx, config)
	require.NoError(t, err)

	sr, sw := schema.Pipe[string](4)
	endpoint := func(_ context.Context, _ string, _ ...tool.Option) (*schema.StreamReader[string], error) {
		return sr, nil
	}
	tCtx := &adk.ToolContext{Name: "plain_tool", CallID: "cid-1"}

	wrapped, err := mw.WrapStreamableToolCall(ctx, endpoint, tCtx)
	require.NoError(t, err)

	require.False(t, sendStreamChunk(t, sw, "c1"), "receiver should be open before the tool call")

	resp, err := wrapped(ctx, `{"k":"v"}`)
	require.NoError(t, err)
	got, err := resp.Recv()
	require.NoError(t, err)
	assert.Equal(t, "c1", got, "passthrough content must be unchanged")
	resp.Close()

	assert.True(t, sendStreamChunk(t, sw, "c2"),
		"upstream receiver should be closed once the returned reader is closed")
}

func TestCustomTruncHandlerErrorClosesAnalysisCopy(t *testing.T) {
	ctx := context.Background()
	config := newCustomTruncTestConfig("plain_tool", func(_ context.Context, _ *ToolDetail) (*TruncResult, error) {
		return nil, errTruncHandlerFailed
	})
	mw, err := New(ctx, config)
	require.NoError(t, err)

	sr, sw := schema.Pipe[string](4)
	endpoint := func(_ context.Context, _ string, _ ...tool.Option) (*schema.StreamReader[string], error) {
		return sr, nil
	}
	tCtx := &adk.ToolContext{Name: "plain_tool", CallID: "cid-2"}

	wrapped, err := mw.WrapStreamableToolCall(ctx, endpoint, tCtx)
	require.NoError(t, err)

	require.False(t, sendStreamChunk(t, sw, "c1"), "receiver should be open before the tool call")

	_, err = wrapped(ctx, `{"k":"v"}`)
	require.ErrorIs(t, err, errTruncHandlerFailed, "handler error identity must be preserved")

	assert.True(t, sendStreamChunk(t, sw, "c2"),
		"upstream receiver should be closed after the handler error")
}

func TestCustomEnhancedTruncPassthroughClosesAnalysisCopy(t *testing.T) {
	ctx := context.Background()
	config := newCustomTruncTestConfig("enh_tool", func(_ context.Context, _ *ToolDetail) (*TruncResult, error) {
		// Do not consume the analysis copy.
		return &TruncResult{NeedTrunc: false}, nil
	})
	mw, err := New(ctx, config)
	require.NoError(t, err)

	sr, sw := schema.Pipe[*schema.ToolResult](4)
	endpoint := func(_ context.Context, _ *schema.ToolArgument, _ ...tool.Option) (*schema.StreamReader[*schema.ToolResult], error) {
		return sr, nil
	}
	tCtx := &adk.ToolContext{Name: "enh_tool", CallID: "cid-3"}

	wrapped, err := mw.WrapEnhancedStreamableToolCall(ctx, endpoint, tCtx)
	require.NoError(t, err)

	c1 := &schema.ToolResult{Parts: []schema.ToolOutputPart{{Type: schema.ToolPartTypeText, Text: "c1"}}}
	require.False(t, sendStreamChunk(t, sw, c1), "receiver should be open before the tool call")

	resp, err := wrapped(ctx, &schema.ToolArgument{Text: `{"k":"v"}`})
	require.NoError(t, err)
	got, err := resp.Recv()
	require.NoError(t, err)
	assert.Equal(t, "c1", got.Parts[0].Text, "passthrough content must be unchanged")
	resp.Close()

	assert.True(t, sendStreamChunk(t, sw, c1),
		"upstream receiver should be closed once the returned reader is closed")
}

func TestCustomEnhancedTruncHandlerErrorClosesAnalysisCopy(t *testing.T) {
	ctx := context.Background()
	config := newCustomTruncTestConfig("enh_tool", func(_ context.Context, _ *ToolDetail) (*TruncResult, error) {
		return nil, errTruncHandlerFailed
	})
	mw, err := New(ctx, config)
	require.NoError(t, err)

	sr, sw := schema.Pipe[*schema.ToolResult](4)
	endpoint := func(_ context.Context, _ *schema.ToolArgument, _ ...tool.Option) (*schema.StreamReader[*schema.ToolResult], error) {
		return sr, nil
	}
	tCtx := &adk.ToolContext{Name: "enh_tool", CallID: "cid-4"}

	wrapped, err := mw.WrapEnhancedStreamableToolCall(ctx, endpoint, tCtx)
	require.NoError(t, err)

	c1 := &schema.ToolResult{Parts: []schema.ToolOutputPart{{Type: schema.ToolPartTypeText, Text: "c1"}}}
	require.False(t, sendStreamChunk(t, sw, c1), "receiver should be open before the tool call")

	_, err = wrapped(ctx, &schema.ToolArgument{Text: `{"k":"v"}`})
	require.ErrorIs(t, err, errTruncHandlerFailed, "handler error identity must be preserved")

	assert.True(t, sendStreamChunk(t, sw, c1),
		"upstream receiver should be closed after the handler error")
}

// TestCustomTruncReplacementKeepsAnalysisCopyOwnership guards the truncation
// path: when the handler returns the analysis copy as the truncation
// replacement, the wrapper must not close it and the replacement must remain
// fully readable.
func TestCustomTruncReplacementKeepsAnalysisCopyOwnership(t *testing.T) {
	ctx := context.Background()
	config := newCustomTruncTestConfig("plain_tool", func(_ context.Context, detail *ToolDetail) (*TruncResult, error) {
		require.NotNil(t, detail.StreamToolResult)
		// Return the analysis copy itself as the replacement.
		return &TruncResult{NeedTrunc: true, StreamToolResult: detail.StreamToolResult}, nil
	})
	mw, err := New(ctx, config)
	require.NoError(t, err)

	sr, sw := schema.Pipe[string](4)
	endpoint := func(_ context.Context, _ string, _ ...tool.Option) (*schema.StreamReader[string], error) {
		return sr, nil
	}
	tCtx := &adk.ToolContext{Name: "plain_tool", CallID: "cid-5"}

	wrapped, err := mw.WrapStreamableToolCall(ctx, endpoint, tCtx)
	require.NoError(t, err)

	require.False(t, sendStreamChunk(t, sw, "c1"))
	sw.Close() // finish the producer side

	resp, err := wrapped(ctx, `{"k":"v"}`)
	require.NoError(t, err)

	got, err := resp.Recv()
	require.NoError(t, err)
	assert.Equal(t, "c1", got, "replacement content must stay readable")
	for {
		_, err = resp.Recv()
		if err != nil {
			require.ErrorIs(t, err, io.EOF)
			break
		}
	}
	resp.Close()

	assert.True(t, sendStreamChunk(t, sw, "c2"),
		"upstream receiver should be closed after the replacement stream is drained and closed")
}
