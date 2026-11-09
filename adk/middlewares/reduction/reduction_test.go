(some characters truncated)...
al(t, schema.User, s.Messages[2].Role)
		assert.Equal(t, "<system-reminder>write_file and edit_file executed successfully</system-reminder>", s.Messages[2].Content)
		assert.Equal(t, schema.Assistant, s.Messages[3].Role)
		assert.Equal(t, "done", s.Messages[3].Content)
	})

	t.Run("test remove tool call and tool responses", func(t *testing.T) {
		config := &Config{
			SkipTruncation:     true,
			TokenCounter:       tokenCounter,
			MaxTokensForClear:  10,
			ClearAtLeastTokens: 0,
			ClearMessageRewriter: func(ctx context.Context, assistantMessage adk.Message, toolResponses []adk.Message) (messagesAfterRewrite []adk.Message, err error) {
				return []adk.Message{}, nil
			},
		}

		mw, err := New(ctx, config)
		assert.NoError(t, err)
		_, s, err := mw.BeforeModelRewriteState(ctx, &adk.ChatModelAgentState{
			Messages: []adk.Message{
				schema.SystemMessage("you are a helpful assistant"),
				schema.UserMessage("user request"),
				schema.AssistantMessage("", []schema.ToolCall{
					{
						ID:       "call_1",
						Type:     "function",
						Function: schema.FunctionCall{Name: "write_file", Arguments: `{"file": "test.txt", "content": "hello"}`},
					},
					{
						ID:       "call_2",
						Type:     "function",
						Function: schema.FunctionCall{Name: "edit_file", Arguments: `{"file": "test.txt", "changes": "add"}`},
					},
				}),
				schema.ToolMessage("write success", "call_1"),
				schema.ToolMessage("edit success", "call_2"),
				schema.AssistantMessage("", []schema.ToolCall{{ID: "dummy", Type: "function", Function: schema.FunctionCall{Name: "dummy_tool"}}}),
				schema.ToolMessage("dummy dummy", "dummy"),
				schema.AssistantMessage("done", nil),
			},
		}, &adk.ModelContext{
			Tools: toolsInfo,
		})
		assert.NoError(t, err)
		assert.Len(t, s.Messages, 5)
		assert.Equal(t, schema.Assistant, s.Messages[2].Role)
		assert.Equal(t, schema.Tool, s.Messages[3].Role)
		assert.Equal(t, "dummy dummy", s.Messages[3].Content)
		assert.Equal(t, schema.Assistant, s.Messages[4].Role)
		assert.Equal(t, "done", s.Messages[4].Content)
	})

	t.Run("test partial tools hit - keep unhit tools and rewrite hit ones", func(t *testing.T) {
		config := &Config{
			SkipTruncation:     true,
			TokenCounter:       tokenCounter,
			MaxTokensForClear:  10,
			ClearAtLeastTokens: 0,
			ClearExcludeTools:  []string{"get_weather"},
			ClearMessageRewriter: func(ctx context.Context, assistantMessage adk.Message, toolResponses []adk.Message) (messagesAfterRewrite []adk.Message, err error) {
				hitToolIDs := map[string]bool{"call_1": true}

				newToolCalls := make([]schema.ToolCall, 0)
				newToolResponses := make([]adk.Message, 0)

				for i, tc := range assistantMessage.ToolCalls {
					if !hitToolIDs[tc.ID] {
						newToolCalls = append(newToolCalls, tc)
						if i < len(toolResponses) {
							newToolResponses = append(newToolResponses, toolResponses[i])
						}
					}
				}

				result := []adk.Message{
					schema.UserMessage("<system-reminder>write_file executed successfully</system-reminder>"),
				}
				if len(newToolCalls) > 0 {
					result = append(result, schema.AssistantMessage("", newToolCalls))
					result = append(result, newToolResponses...)
				}
				return result, nil
			},
		}

		mw, err := New(ctx, config)
		assert.NoError(t, err)
		_, s, err := mw.BeforeModelRewriteState(ctx, &adk.ChatModelAgentState{
			Messages: []adk.Message{
				schema.SystemMessage("you are a helpful assistant"),
				schema.UserMessage("user request"),
				schema.AssistantMessage("", []schema.ToolCall{
					{
						ID:       "call_1",
						Type:     "function",
						Function: schema.FunctionCall{Name: "write_file", Arguments: `{"file": "test.txt", "content": "hello"}`},
					},
					{
						ID:       "call_2",
						Type:     "function",
						Function: schema.FunctionCall{Name: "get_weather", Arguments: `{"location": "London"}`},
					},
				}),
				schema.ToolMessage("write success", "call_1"),
				schema.ToolMessage("sunny", "call_2"),
				schema.AssistantMessage("", []schema.ToolCall{{ID: "dummy", Type: "function", Function: schema.FunctionCall{Name: "dummy_tool"}}}),
			},
		}, &adk.ModelContext{
			Tools: toolsInfo,
		})
		assert.NoError(t, err)
		assert.Len(t, s.Messages, 6)
		assert.Equal(t, schema.User, s.Messages[2].Role)
		assert.Equal(t, "<system-reminder>write_file executed successfully</system-reminder>", s.Messages[2].Content)
		assert.Equal(t, schema.Assistant, s.Messages[3].Role)
		assert.Len(t, s.Messages[3].ToolCalls, 1)
		assert.Equal(t, "call_2", s.Messages[3].ToolCalls[0].ID)
		assert.Equal(t, schema.Tool, s.Messages[4].Role)
		assert.Equal(t, "sunny", s.Messages[4].Content)
	})

	t.Run("test mixed messages with system/user before", func(t *testing.T) {
		config := &Config{
			SkipTruncation:     true,
			TokenCounter:       tokenCounter,
			MaxTokensForClear:  10,
			ClearAtLeastTokens: 0,
			ClearMessageRewriter: func(ctx context.Context, assistantMessage adk.Message, toolResponses []adk.Message) (messagesAfterRewrite []adk.Message, err error) {
				return []adk.Message{
					schema.UserMessage("<system-reminder>tool executed</system-reminder>"),
				}, nil
			},
		}

		mw, err := New(ctx, config)
		assert.NoError(t, err)
		_, s, err := mw.BeforeModelRewriteState(ctx, &adk.ChatModelAgentState{
			Messages: []adk.Message{
				schema.SystemMessage("you are a helpful assistant"),
				schema.UserMessage("first request"),
				schema.AssistantMessage("", []schema.ToolCall{
					{
						ID:       "call_1",
						Type:     "function",
						Function: schema.FunctionCall{Name: "write_file", Arguments: `{"file": "test.txt"}`},
					},
				}),
				schema.ToolMessage("success", "call_1"),
				schema.UserMessage("second request"),
				schema.AssistantMessage("", []schema.ToolCall{{ID: "dummy", Type: "function", Function: schema.FunctionCall{Name: "dummy_tool"}}}),
			},
		}, &adk.ModelContext{
			Tools: toolsInfo,
		})
		assert.NoError(t, err)
		assert.Len(t, s.Messages, 5)
		assert.Equal(t, schema.System, s.Messages[0].Role)
		assert.Equal(t, schema.User, s.Messages[1].Role)
		assert.Equal(t, schema.User, s.Messages[2].Role)
		assert.Equal(t, "<system-reminder>tool executed</system-reminder>", s.Messages[2].Content)
		assert.Equal(t, schema.User, s.Messages[3].Role)
	})

	t.Run("test mixed messages with system/user before", func(t *testing.T) {
		config := &Config{
			SkipTruncation:     true,
			TokenCounter:       tokenCounter,
			MaxTokensForClear:  10,
			ClearAtLeastTokens: 0,
			ClearMessageRewriter: func(ctx context.Context, assistantMessage adk.Message, toolResponses []adk.Message) (messagesAfterRewrite []adk.Message, err error) {
				return []adk.Message{
					schema.UserMessage("<system-reminder>tool executed</system-reminder>"),
				}, nil
			},
		}

		mw, err := New(ctx, config)
		assert.NoError(t, err)
		_, s, err := mw.BeforeModelRewriteState(ctx, &adk.ChatModelAgentState{
			Messages: []adk.Message{
				schema.SystemMessage("you are a helpful assistant"),
				schema.UserMessage("first request"),
				schema.AssistantMessage("", []schema.ToolCall{
					{
						ID:       "call_1",
						Type:     "function",
						Function: schema.FunctionCall{Name: "write_file", Arguments: `{"file": "test.txt"}`},
					},
				}),
				schema.ToolMessage("success", "call_1"),
				schema.UserMessage("second request"),
				schema.AssistantMessage("", []schema.ToolCall{{ID: "dummy", Type: "function", Function: schema.FunctionCall{Name: "dummy_tool"}}}),
			},
		}, &adk.ModelContext{
			Tools: toolsInfo,
		})
		assert.NoError(t, err)
		assert.Len(t, s.Messages, 5)
		assert.Equal(t, schema.System, s.Messages[0].Role)
		assert.Equal(t, schema.User, s.Messages[1].Role)
		assert.Equal(t, schema.User, s.Messages[2].Role)
		assert.Equal(t, "<system-reminder>tool executed</system-reminder>", s.Messages[2].Content)
		assert.Equal(t, schema.User, s.Messages[3].Role)
	})

	t.Run("test assistant message without tool calls - keep as is", func(t *testing.T) {
		handlerCalled := false
		config := &Config{
			SkipTruncation:     true,
			TokenCounter:       tokenCounter,
			MaxTokensForClear:  10,
			ClearAtLeastTokens: 0,
			ClearMessageRewriter: func(ctx context.Context, assistantMessage adk.Message, toolResponses []adk.Message) (messagesAfterRewrite []adk.Message, err error) {
				handlerCalled = true
				return nil, nil
			},
		}

		mw, err := New(ctx, config)
		assert.NoError(t, err)
		_, s, err := mw.BeforeModelRewriteState(ctx, &adk.ChatModelAgentState{
			Messages: []adk.Message{
				schema.SystemMessage("you are a helpful assistant"),
				schema.UserMessage("user request"),
				schema.AssistantMessage("regular response without tools", nil),
				schema.AssistantMessage("", []schema.ToolCall{{ID: "dummy", Type: "function", Function: schema.FunctionCall{Name: "dummy_tool"}}}),
			},
		}, &adk.ModelContext{
			Tools: toolsInfo,
		})
		assert.NoError(t, err)
		assert.False(t, handlerCalled)
		assert.Len(t, s.Messages, 4)
		assert.Equal(t, schema.Assistant, s.Messages[2].Role)
		assert.Equal(t, "regular response without tools", s.Messages[2].Content)
	})

	t.Run("test clear not meeting threshold - should keep original messages", func(t *testing.T) {
		originalTokenCount := int64(1000)
		rewrittenTokenCount := int64(999)
		callCount := 0

		config := &Config{
			SkipTruncation: true,
			TokenCounter: func(_ context.Context, msgs []adk.Message, _ []*schema.ToolInfo) (int64, error) {
				callCount++
				if callCount == 1 {
					return originalTokenCount, nil
				}
				return rewrittenTokenCount, nil
			},
			MaxTokensForClear:  10,
			ClearAtLeastTokens: 10,
			ClearMessageRewriter: func(ctx context.Context, assistantMessage adk.Message, toolResponses []adk.Message) (messagesAfterRewrite []adk.Message, err error) {
				return []adk.Message{
					schema.UserMessage("<system-reminder>tool executed</system-reminder>"),
				}, nil
			},
		}

		originalMessages := []adk.Message{
			schema.SystemMessage("you are a helpful assistant"),
			schema.UserMessage("user request"),
			schema.AssistantMessage("", []schema.ToolCall{
				{
					ID:       "call_1",
					Type:     "function",
					Function: schema.FunctionCall{Name: "write_file", Arguments: `{"file": "test.txt"}`},
				},
			}),
			schema.ToolMessage("success", "call_1"),
			schema.AssistantMessage("", []schema.ToolCall{{ID: "dummy", Type: "function", Function: schema.FunctionCall{Name: "dummy_tool"}}}),
		}

		mw, err := New(ctx, config)
		assert.NoError(t, err)
		_, s, err := mw.BeforeModelRewriteState(ctx, &adk.ChatModelAgentState{
			Messages: originalMessages,
		}, &adk.ModelContext{
			Tools: toolsInfo,
		})
		assert.NoError(t, err)
		assert.Len(t, s.Messages, len(originalMessages))
		for i := range originalMessages {
			assert.Equal(t, originalMessages[i].Role, s.Messages[i].Role)
			assert.Equal(t, originalMessages[i].Content, s.Messages[i].Content)
		}
	})

	t.Run("test applyClearRewrite with default role", func(t *testing.T) {
		config := &Config{
			SkipTruncation:     true,
			TokenCounter:       tokenCounter,
			MaxTokensForClear:  10,
			ClearAtLeastTokens: 0,
			ClearMessageRewriter: func(ctx context.Context, assistantMessage adk.Message, toolResponses []adk.Message) (messagesAfterRewrite []adk.Message, err error) {
				return []adk.Message{}, nil
			},
		}

		mw, err := New(ctx, config)
		assert.NoError(t, err)

		unknownRoleMsg := &schema.Message{
			Role:    "unknown_role",
			Content: "unknown",
		}

		_, s, err := mw.BeforeModelRewriteState(ctx, &adk.ChatModelAgentState{
			Messages: []adk.Message{
				schema.SystemMessage("you are a helpful assistant"),
				schema.UserMessage("user request"),
				unknownRoleMsg,
				schema.AssistantMessage("", []schema.ToolCall{{ID: "dummy", Type: "function", Function: schema.FunctionCall{Name: "dummy_tool"}}}),
			},
		}, &adk.ModelContext{
			Tools: toolsInfo,
		})
		assert.NoError(t, err)
		assert.NotNil(t, s)
	})

	t.Run("test applyClearRewrite with rewrite error", func(t *testing.T) {
		expectedErr := fmt.Errorf("rewrite error")
		config := &Config{
			SkipTruncation:     true,
			TokenCounter:       tokenCounter,
			MaxTokensForClear:  10,
			ClearAtLeastTokens: 0,
			ClearMessageRewriter: func(ctx context.Context, assistantMessage adk.Message, toolResponses []adk.Message) (messagesAfterRewrite []adk.Message, err error) {
				return nil, expectedErr
			},
		}

		mw, err := New(ctx, config)
		assert.NoError(t, err)

		_, _, err = mw.BeforeModelRewriteState(ctx, &adk.ChatModelAgentState{
			Messages: []adk.Message{
				schema.SystemMessage("you are a helpful assistant"),
				schema.UserMessage("user request"),
				schema.AssistantMessage("", []schema.ToolCall{
					{
						ID:       "call_1",
						Type:     "function",
						Function: schema.FunctionCall{Name: "write_file", Arguments: `{"file": "test.txt"}`},
					},
				}),
				schema.ToolMessage("success", "call_1"),
				schema.AssistantMessage("", []schema.ToolCall{{ID: "dummy", Type: "function", Function: schema.FunctionCall{Name: "dummy_tool"}}}),
			},
		}, &adk.ModelContext{
			Tools: toolsInfo,
		})
		assert.Error(t, err)
		assert.Equal(t, expectedErr, err)
	})

	t.Run("test applyClearRewrite with missing tool responses", func(t *testing.T) {
		config := &Config{
			SkipTruncation:     true,
			TokenCounter:       tokenCounter,
			MaxTokensForClear:  10,
			ClearAtLeastTokens: 0,
			ClearMessageRewriter: func(ctx context.Context, assistantMessage adk.Message, toolResponses []adk.Message) (messagesAfterRewrite []adk.Message, err error) {
				assert.Nil(t, toolResponses)
				return []adk.Message{
					schema.UserMessage("<system-reminder>tool executed</system-reminder>"),
				}, nil
			},
		}

		mw, err := New(ctx, config)
		assert.NoError(t, err)
		_, s, err := mw.BeforeModelRewriteState(ctx, &adk.ChatModelAgentState{
			Messages: []adk.Message{
				schema.SystemMessage("you are a helpful assistant"),
				schema.UserMessage("user request"),
				schema.AssistantMessage("", []schema.ToolCall{
					{
						ID:       "call_1",
						Type:     "function",
						Function: schema.FunctionCall{Name: "write_file", Arguments: `{"file": "test.txt"}`},
					},
				}),
				schema.AssistantMessage("", []schema.ToolCall{{ID: "dummy", Type: "function", Function: schema.FunctionCall{Name: "dummy_tool"}}}),
			},
		}, &adk.ModelContext{
			Tools: toolsInfo,
		})
		assert.NoError(t, err)
		assert.NotNil(t, s)
	})

	t.Run("test applyClearRewrite with clearAtLeastTokens > 0", func(t *testing.T) {
		config := &Config{
			SkipTruncation:     true,
			TokenCounter:       tokenCounter,
			MaxTokensForClear:  10,
			ClearAtLeastTokens: 10,
			ClearMessageRewriter: func(ctx context.Context, assistantMessage adk.Message, toolResponses []adk.Message) (messagesAfterRewrite []adk.Message, err error) {
				return []adk.Message{
					schema.UserMessage("<system-reminder>tool executed</system-reminder>"),
				}, nil
			},
		}

		mw, err := New(ctx, config)
		assert.NoError(t, err)
		_, s, err := mw.BeforeModelRewriteState(ctx, &adk.ChatModelAgentState{
			Messages: []adk.Message{
				schema.SystemMessage("you are a helpful assistant"),
				schema.UserMessage("user request"),
				schema.AssistantMessage("", []schema.ToolCall{
					{
						ID:       "call_1",
						Type:     "function",
						Function: schema.FunctionCall{Name: "write_file", Arguments: `{"file": "test.txt"}`},
					},
				}),
				schema.ToolMessage("success", "call_1"),
				schema.AssistantMessage("", []schema.ToolCall{{ID: "dummy", Type: "function", Function: schema.FunctionCall{Name: "dummy_tool"}}}),
			},
		}, &adk.ModelContext{
			Tools: toolsInfo,
		})
		assert.NoError(t, err)
		assert.NotNil(t, s)
	})
}

func TestNewTypedAgenticMessage(t *testing.T) {
	ctx := context.Background()
	mw, err := NewTyped(ctx, &TypedConfig[*schema.AgenticMessage]{
		SkipTruncation: true,
		SkipClear:      true,
	})
	assert.NoError(t, err)
	assert.NotNil(t, mw)

	var _ adk.TypedChatModelAgentMiddleware[*schema.AgenticMessage] = mw
}

func TestToolResultMediaSourceRoundTrip(t *testing.T) {
	for _, source := range []string{"file_id", "base64"} {
		t.Run(source, func(t *testing.T) {
			common := schema.MessagePartCommon{MIMEType: "application/octet-stream"}
			value := "provider-file-123"
			if source == "file_id" {
				common.FileID = &value
			} else {
				value = "YmluYXJ5"
				common.Base64Data = &value
			}
			parts := []schema.ToolOutputPart{
				{Type: schema.ToolPartTypeImage, Image: &schema.ToolOutputImage{MessagePartCommon: common}},
				{Type: schema.ToolPartTypeAudio, Audio: &schema.ToolOutputAudio{MessagePartCommon: common}},
				{Type: schema.ToolPartTypeVideo, Video: &schema.ToolOutputVideo{MessagePartCommon: common}},
				{Type: schema.ToolPartTypeFile, File: &schema.ToolOutputFile{MessagePartCommon: common}},
			}
			result := &schema.FunctionToolResult{}
			setToolResultFromOutputParts(result, parts)
			require.Equal(t, parts, toolResultToOutputParts(result))
		})
	}
}

// failNthWriteBackend fails exactly one Write call, determined by its 1-based index.
type failNthWriteBackend struct {
	filesystem.Backend
	failAt int
	writes int
}

func (b *failNthWriteBackend) Write(ctx context.Context, req *filesystem.WriteRequest) error {
	b.writes++
	if b.writes == b.failAt {
		return errors.New("injected write failure")
	}
	return b.Backend.Write(ctx, req)
}

func TestReductionClearFailureKeepsOriginalState(t *testing.T) {
	t.Run("message", func(t *testing.T) {
		runReductionClearFailureTest[*schema.Message](t)
	})
	t.Run("agentic message", func(t *testing.T) {
		runReductionClearFailureTest[*schema.AgenticMessage](t)
	})
}

func runReductionClearFailureTest[M adk.MessageType](t *testing.T) {
	ctx := context.Background()

	backend := filesystem.NewInMemoryBackend()
	failingBackend := &failNthWriteBackend{Backend: backend, failAt: 2}

	config := &TypedConfig[M]{
		Backend:                   failingBackend,
		SkipTruncation:            true,
		TokenCounter:              func(context.Context, []M, []*schema.ToolInfo) (int64, error) { return 100, nil },
		MaxTokensForClear:         10,
		ClearRetentionSuffixLimit: 0,
		GenClearOffloadFilePath: func(_ context.Context, detail *ToolDetail) (string, error) {
			return "/clear/" + detail.ToolContext.CallID, nil
		},
	}

	mw, err := NewTyped[M](ctx, config)
	require.NoError(t, err)

	msgs := []M{
		makeSystemMsgG[M]("you are a helpful assistant"),
		makeUserMsgG[M]("run both tools"),
		makeAssistantMsgWithToolCallsG[M]([]testToolCall{{ID: "first", Name: "get_weather", Arguments: `{"city":"A"}`}}),
		makeToolResultMsgG[M]("first original evidence", "first", "get_weather"),
		makeAssistantMsgWithToolCallsG[M]([]testToolCall{{ID: "second", Name: "get_weather", Arguments: `{"city":"B"}`}}),
		makeToolResultMsgG[M]("second original evidence", "second", "get_weather"),
		makeAssistantMsgWithToolCallsG[M]([]testToolCall{{ID: "retained", Name: "get_weather", Arguments: `{"city":"C"}`}}),
		makeToolResultMsgG[M]("retained evidence", "retained", "get_weather"),
	}
	state := &adk.TypedChatModelAgentState[M]{Messages: msgs}

	// The first offload write succeeds and the second fails.
	_, _, err = mw.BeforeModelRewriteState(ctx, state, nil)
	require.Error(t, err)

	// The original state must not be partially rewritten.
	assert.Equal(t, "first original evidence", getMsgContentG(state.Messages[3]))
	assert.Equal(t, "second original evidence", getMsgContentG(state.Messages[5]))
	assert.Equal(t, `{"city":"A"}`, getToolCallsGeneric(state.Messages[2])[0].Arguments)
	assert.Equal(t, `{"city":"B"}`, getToolCallsGeneric(state.Messages[4])[0].Arguments)

	// A retry must still offload the original evidence, not a placeholder.
	_, _, err = mw.BeforeModelRewriteState(ctx, state, nil)
	require.NoError(t, err)

	content, readErr := backend.Read(ctx, &filesystem.ReadRequest{FilePath: "/clear/first"})
	require.NoError(t, readErr)
	assert.Contains(t, content.Content, "first original evidence")
	assert.NotContains(t, content.Content, "<persisted-output>")
}