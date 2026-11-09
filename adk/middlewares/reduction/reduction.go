(some characters truncated)...
TypeFunctionToolResult || block.FunctionToolResult == nil {
				continue
			}
			if found != nil {
				return nil, false, fmt.Errorf("reduction: AgenticMessage contains multiple FunctionToolResult blocks; expected exactly one per message")
			}
			found = block.FunctionToolResult
		}
		if found == nil {
			return &schema.ToolResult{Parts: []schema.ToolOutputPart{{Type: schema.ToolPartTypeText, Text: ""}}}, true, nil
		}
		parts := toolResultToOutputParts(found)
		if len(parts) == 0 {
			return &schema.ToolResult{Parts: []schema.ToolOutputPart{{Type: schema.ToolPartTypeText, Text: ""}}}, true, nil
		}
		isSimple := len(parts) == 1 && parts[0].Type == schema.ToolPartTypeText
		return &schema.ToolResult{Parts: parts}, isSimple, nil
	}
	return nil, false, fmt.Errorf("unsupported message type")
}

// setToolResultContent updates the tool result content in a message.
// For *schema.Message: sets msg.Content or msg.UserInputMultiContent.
// For *schema.AgenticMessage: reconstructs FunctionToolResult.Content.
func setToolResultContent[M adk.MessageType](msg M, toolResult *schema.ToolResult, fromContent bool) {
	switch m := any(msg).(type) {
	case *schema.Message:
		if fromContent {
			if len(toolResult.Parts) > 0 {
				m.Content = toolResult.Parts[0].Text
			}
		} else {
			convResult, convErr := toolResult.ToMessageInputParts()
			if convErr == nil {
				m.UserInputMultiContent = convResult
			}
		}
	case *schema.AgenticMessage:
		for _, block := range m.ContentBlocks {
			if block == nil || block.Type != schema.ContentBlockTypeFunctionToolResult || block.FunctionToolResult == nil {
				continue
			}
			setToolResultFromOutputParts(block.FunctionToolResult, toolResult.Parts)
			return
		}
	}
}

// copyMessagesGeneric deep-copies a slice of messages.
func copyMessagesGeneric[M adk.MessageType](msgs []M) []M {
	var zero M
	switch any(zero).(type) {
	case *schema.Message:
		origMsgs := any(msgs).([]*schema.Message)
		copied := copyMessages(origMsgs)
		return any(copied).([]M)
	case *schema.AgenticMessage:
		origMsgs := any(msgs).([]*schema.AgenticMessage)
		copied := copyAgenticMessages(origMsgs)
		return any(copied).([]M)
	}
	panic("unreachable")
}

func copyAgenticMessages(msgs []*schema.AgenticMessage) []*schema.AgenticMessage {
	resp := make([]*schema.AgenticMessage, len(msgs))
	for i, msg := range msgs {
		if msg == nil {
			continue
		}
		copied := &schema.AgenticMessage{
			Role:         msg.Role,
			ResponseMeta: msg.ResponseMeta,
		}
		if msg.ContentBlocks != nil {
			copied.ContentBlocks = make([]*schema.ContentBlock, len(msg.ContentBlocks))
			for j, block := range msg.ContentBlocks {
				if block == nil {
					continue
				}
				cb := *block
				// Deep copy mutable sub-fields
				if block.FunctionToolCall != nil {
					ftc := *block.FunctionToolCall
					cb.FunctionToolCall = &ftc
				}
				if block.FunctionToolResult != nil {
					ftr := *block.FunctionToolResult
					if block.FunctionToolResult.Content != nil {
						ftr.Content = make([]*schema.FunctionToolResultContentBlock, len(block.FunctionToolResult.Content))
						for k, rb := range block.FunctionToolResult.Content {
							if rb != nil {
								rbCopy := *rb // shallow copy: Image/Audio/Video/File sub-fields are not deep-copied.
								// This is safe because the clear logic replaces entire blocks rather than
								// mutating media fields in-place. Custom ClearHandlers should follow the same pattern.
								if rb.Text != nil {
									t := *rb.Text
									rbCopy.Text = &t
								}
								ftr.Content[k] = &rbCopy
							}
						}
					}
					cb.FunctionToolResult = &ftr
				}
				if block.Extra != nil {
					cb.Extra = make(map[string]any, len(block.Extra))
					for k, v := range block.Extra {
						cb.Extra[k] = v
					}
				}
				copied.ContentBlocks[j] = &cb
			}
		}
		if msg.Extra != nil {
			copied.Extra = make(map[string]any, len(msg.Extra))
			for k, v := range msg.Extra {
				copied.Extra[k] = v
			}
		}
		resp[i] = copied
	}
	return resp
}

func copyMessages(msgs []*schema.Message) []*schema.Message {
	resp := make([]*schema.Message, len(msgs))
	for i, msg := range msgs {
		if msg == nil {
			continue
		}
		copied := &schema.Message{
			Role:                     msg.Role,
			Content:                  msg.Content,
			MultiContent:             msg.MultiContent,
			UserInputMultiContent:    msg.UserInputMultiContent,
			AssistantGenMultiContent: msg.AssistantGenMultiContent,
			Name:                     msg.Name,
			ToolCalls:                nil,
			ToolCallID:               msg.ToolCallID,
			ToolName:                 msg.ToolName,
			ResponseMeta:             msg.ResponseMeta,
			ReasoningContent:         msg.ReasoningContent,
			Extra:                    nil,
		}
		if msg.ToolCalls != nil {
			copied.ToolCalls = append(make([]schema.ToolCall, 0, len(msg.ToolCalls)), msg.ToolCalls...)
		}
		if msg.Extra != nil {
			copied.Extra = make(map[string]any, len(msg.Extra))
			for k, v := range msg.Extra {
				copied.Extra[k] = v
			}
		}
		resp[i] = copied
	}
	return resp
}

// defaultTokenCounter estimates tokens, which treats one token as ~4 characters of text for common English text.
// github.com/tiktoken-go/tokenizer is highly recommended to replace it.
func defaultTokenCounter(_ context.Context, msgs []*schema.Message, tools []*schema.ToolInfo) (int64, error) {
	var tokens int64
	for _, msg := range msgs {
		if msg == nil {
			continue
		}

		var sb strings.Builder
		sb.WriteString(string(msg.Role))
		sb.WriteString("\n")
		sb.WriteString(msg.ReasoningContent)
		sb.WriteString("\n")
		sb.WriteString(msg.Content)
		sb.WriteString("\n")
		if msg.Role == schema.Assistant && len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				sb.WriteString(tc.Function.Name)
				sb.WriteString("\n")
				sb.WriteString(tc.Function.Arguments)
			}
		}

		for _, mc := range msg.UserInputMultiContent {
			switch mc.Type {
			case schema.ChatMessagePartTypeText:
				sb.WriteString(mc.Text)
				sb.WriteString("\n")
			default:
				// do nothing for multi-modal content
			}
		}

		for _, mc := range msg.AssistantGenMultiContent {
			switch mc.Type {
			case schema.ChatMessagePartTypeText:
				sb.WriteString(mc.Text)
				sb.WriteString("\n")
			default:
				// do nothing for multi-modal content
			}
		}

		n := int64(len(sb.String()) / 4)
		tokens += n
	}

	for _, tl := range tools {
		tl_ := *tl
		tl_.Extra = nil
		text, err := sonic.MarshalString(tl_)
		if err != nil {
			return 0, fmt.Errorf("failed to marshal tool info: %w", err)
		}

		tokens += int64(len(text) / 4)
	}

	return tokens, nil
}

// defaultTruncHandler applies the shared buffered truncation strategy. The
// built-in default streaming wrappers bypass it so they can truncate
// incrementally with a streaming-specific notice.
//
// Processing steps:
//  1. Read and join tool output into a complete result:
//     - Non-streaming: use ToolResult directly.
//     - Streaming: consume the whole StreamToolResult, then concat all chunks.
//  2. If output is empty or total text length does not exceed truncMaxLength,
//     return NeedTrunc=false.
//  3. If exceeded, replace oversized text parts with truncation notices and
//     offload the full original content.
//
// Streaming-specific behavior:
//   - Truncation is not incremental. The handler waits until the entire stream is read
//     before deciding and producing output.
//   - If stream Recv() returns a non-EOF error, getJointToolResult treats it as
//     "skip processing" (needProcess=false, err=nil), so this handler returns
//     NeedTrunc=false and does not propagate that recv error.
//   - When truncation is applied to a streaming tool result, output is re-emitted as a
//     buffered single-result stream (not original chunk-by-chunk streaming semantics).
//
// Tool-specific configurations that install this handler for streaming outputs
// keep the buffered behavior below.
func defaultTruncHandler(
	genOffloadFilePathFn func(ctx context.Context, toolDetail *ToolDetail) (filePath string, err error),
	truncMaxLength int,
	readFileToolName string,
) func(ctx context.Context, detail *ToolDetail) (truncResult *TruncResult, err error) {

	return func(ctx context.Context, detail *ToolDetail) (offloadInfo *TruncResult, err error) {
		isStreamResult := detail.StreamToolResult != nil
		resultParts, needProcess, err := getJointToolResult(detail)
		if err != nil {
			return nil, err
		}
		if !needProcess {
			return &TruncResult{NeedTrunc: false}, nil
		}

		fullLength, textPartsCnt := 0, 0
		for _, part := range resultParts {
			if part.Type == schema.ToolPartTypeText {
				fullLength += len(part.Text)
				textPartsCnt++
			}
		}
		if textPartsCnt == 0 || fullLength <= truncMaxLength {
			return &TruncResult{NeedTrunc: false}, nil
		}

		var (
			offloadContent  = stringifyToolOutputParts(resultParts)
			truncPartLength = truncMaxLength / textPartsCnt
			previewSize     = truncPartLength / 2
		)

		filePath, err := genOffloadFilePathFn(ctx, detail)
		if err != nil {
			return nil, err
		}

		for i, part := range resultParts {
			text := part.Text
			if part.Type != schema.ToolPartTypeText ||
				len(text) < truncPartLength {
				continue
			}
			truncNotify, fmtErr := pyfmt.Fmt(getTruncFmt(), map[string]any{
				"original_size":  len(part.Text),
				"file_path":      filePath,
				"preview_size":   previewSize,
				"preview_first":  clampPrefixToUTF8Boundary(text, previewSize),
				"preview_last":   clampSuffixToUTF8Boundary(text, previewSize),
				"read_tool_name": readFileToolName,
			})
			if fmtErr != nil {
				return nil, fmtErr
			}
			resultParts[i].Text = truncNotify
		}

		tr := &TruncResult{
			NeedTrunc:       true,
			NeedOffload:     true,
			OffloadFilePath: filePath,
			OffloadContent:  offloadContent,
		}
		if !isStreamResult {
			tr.ToolResult = &schema.ToolResult{Parts: resultParts}
		} else {
			sr, sw := schema.Pipe[*schema.ToolResult](1)
			sw.Send(&schema.ToolResult{Parts: resultParts}, nil)
			sw.Close()
			tr.StreamToolResult = sr
		}
		return tr, nil
	}
}

func clampPrefixToUTF8Boundary(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if n >= len(s) {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func clampSuffixToUTF8Boundary(s string, n int) string {
	if n <= 0 {
		return ""
	}
	l := len(s)
	if n >= l {
		return s
	}
	start := l - n
	for start < l && !utf8.RuneStart(s[start]) {
		start++
	}
	return s[start:]
}

func defaultClearHandler(
	genOffloadFilePathFn func(ctx context.Context, toolDetail *ToolDetail) (filePath string, err error),
	needOffload bool,
	readFileToolName string,
) func(ctx context.Context, detail *ToolDetail) (*ClearResult, error) {

	return func(ctx context.Context, detail *ToolDetail) (clearResult *ClearResult, err error) {
		resultParts, needProcess, err := getJointToolResult(detail)
		if err != nil {
			return nil, err
		}
		if !needProcess {
			return &ClearResult{NeedClear: false}, nil
		}

		if needOffload {
			filePath, err := genOffloadFilePathFn(ctx, detail)
			if err != nil {
				return nil, err
			}
			textPlaceHolder, fmtErr := pyfmt.Fmt(getClearWithOffloadingFmt(), map[string]any{
				"file_path":      filePath,
				"read_tool_name": readFileToolName,
			})
			if fmtErr != nil {
				return nil, fmtErr
			}

			offloadContent := stringifyToolOutputParts(resultParts)
			for i, part := range resultParts {
				if part.Type != schema.ToolPartTypeText {
					continue
				}
				resultParts[i].Text = textPlaceHolder
			}
			clearResult = &ClearResult{
				NeedClear:       true,
				ToolArgument:    detail.ToolArgument,
				ToolResult:      &schema.ToolResult{Parts: resultParts},
				NeedOffload:     true,
				OffloadFilePath: filePath,
				OffloadContent:  offloadContent,
			}
		} else {
			textPlaceHolder := getClearWithoutOffloadingFmt()
			for i, part := range resultParts {
				if part.Type != schema.ToolPartTypeText {
					continue
				}
				resultParts[i].Text = textPlaceHolder
			}
			clearResult = &ClearResult{
				NeedClear:    true,
				ToolArgument: detail.ToolArgument,
				ToolResult:   &schema.ToolResult{Parts: resultParts},
				NeedOffload:  false,
			}
		}

		return clearResult, nil
	}
}

func getJointToolResult(toolDetail *ToolDetail) (toolOutputParts []schema.ToolOutputPart, needProcess bool, err error) {
	if toolDetail.ToolResult == nil && toolDetail.StreamToolResult == nil {
		return nil, false, fmt.Errorf("ToolResult and StreamToolResult are both nil")
	}

	if toolDetail.ToolResult != nil {
		toolOutputParts = toolDetail.ToolResult.Parts
	} else {
		var toolResultChunks []*schema.ToolResult
		for {
			toolResultChunk, recvErr := toolDetail.StreamToolResult.Recv()
			if recvErr != nil {
				if recvErr == io.EOF {
					break
				}
				// return original stream reader, not sending recvErr
				return nil, false, nil
			}
			toolResultChunks = append(toolResultChunks, toolResultChunk)
		}
		toolResult, concatErr := schema.ConcatToolResults(toolResultChunks)
		if concatErr != nil {
			return nil, false, concatErr
		}
		toolOutputParts = toolResult.Parts
	}

	if len(toolOutputParts) == 0 {
		return nil, false, nil
	}

	return toolOutputParts, true, nil
}

func stringifyToolOutputParts(toolOutputParts []schema.ToolOutputPart) string {
	if len(toolOutputParts) == 0 {
		return ""
	} else if len(toolOutputParts) == 1 && toolOutputParts[0].Type == schema.ToolPartTypeText {
		return toolOutputParts[0].Text
	} else {
		b, _ := json.MarshalIndent(toolOutputParts, "", "\t")
		return string(b)
	}
}

func getMsgClearedFlag(msg *schema.Message) (offloaded bool) {
	if msg.Extra == nil {
		return false
	}
	v, ok := msg.Extra[msgClearedFlag].(bool)
	if !ok {
		return false
	}
	return v
}

func setMsgClearedFlag(msg *schema.Message) {
	if msg.Extra == nil {
		msg.Extra = make(map[string]any)
	}
	msg.Extra[msgClearedFlag] = true
}

func toolResultFromMessage(msg *schema.Message) (result *schema.ToolResult, fromContent bool, err error) {
	if msg.Role != schema.Tool {
		return nil, false, fmt.Errorf("message role %s is not a tool", msg.Role)
	}
	if len(msg.UserInputMultiContent) > 0 {
		result = &schema.ToolResult{Parts: make([]schema.ToolOutputPart, 0, len(msg.UserInputMultiContent))}
		for _, part := range msg.UserInputMultiContent {
			top, convErr := convMessageInputPartToToolOutputPart(part)
			if convErr != nil {
				return nil, false, convErr
			}
			result.Parts = append(result.Parts, top)
		}
		return result, false, nil
	}
	return &schema.ToolResult{Parts: []schema.ToolOutputPart{{Type: schema.ToolPartTypeText, Text: msg.Content}}}, true, nil
}

func convMessageInputPartToToolOutputPart(msgPart schema.MessageInputPart) (schema.ToolOutputPart, error) {
	switch msgPart.Type {
	case schema.ChatMessagePartTypeText:
		return schema.ToolOutputPart{
			Type: schema.ToolPartTypeText,
			Text: msgPart.Text,
		}, nil
	case schema.ChatMessagePartTypeImageURL:
		return schema.ToolOutputPart{
			Type: schema.ToolPartTypeImage,
			Image: &schema.ToolOutputImage{
				MessagePartCommon: msgPart.Image.MessagePartCommon,
			},
		}, nil
	case schema.ChatMessagePartTypeAudioURL:
		return schema.ToolOutputPart{
			Type: schema.ToolPartTypeAudio,
			Audio: &schema.ToolOutputAudio{
				MessagePartCommon: msgPart.Audio.MessagePartCommon,
			},
		}, nil
	case schema.ChatMessagePartTypeVideoURL:
		return schema.ToolOutputPart{
			Type: schema.ToolPartTypeVideo,
			Video: &schema.ToolOutputVideo{
				MessagePartCommon: msgPart.Video.MessagePartCommon,
			},
		}, nil
	case schema.ChatMessagePartTypeFileURL:
		return schema.ToolOutputPart{
			Type: schema.ToolPartTypeFile,
			File: &schema.ToolOutputFile{
				MessagePartCommon: msgPart.File.MessagePartCommon,
			},
		}, nil
	default:
		return schema.ToolOutputPart{}, fmt.Errorf("unknown msg part type: %v", msgPart.Type)
	}
}

// toolResultToOutputParts converts a FunctionToolResult's Content blocks to ToolOutputPart slice.
func toolResultToOutputParts(f *schema.FunctionToolResult) []schema.ToolOutputPart {
	var parts []schema.ToolOutputPart
	for _, block := range f.Content {
		if block == nil {
			continue
		}
		if block.Text != nil {
			parts = append(parts, schema.ToolOutputPart{Type: schema.ToolPartTypeText, Text: block.Text.Text})
		} else if block.Image != nil {
			parts = append(parts, schema.ToolOutputPart{
				Type:  schema.ToolPartTypeImage,
				Image: &schema.ToolOutputImage{MessagePartCommon: schema.MessagePartCommon{URL: strPtr(block.Image.URL), Base64Data: strPtr(block.Image.Base64Data), FileID: strPtr(block.Image.FileID), MIMEType: block.Image.MIMEType}},
			})
		} else if block.Audio != nil {
			parts = append(parts, schema.ToolOutputPart{
				Type:  schema.ToolPartTypeAudio,
				Audio: &schema.ToolOutputAudio{MessagePartCommon: schema.MessagePartCommon{URL: strPtr(block.Audio.URL), Base64Data: strPtr(block.Audio.Base64Data), FileID: strPtr(block.Audio.FileID), MIMEType: block.Audio.MIMEType}},
			})
		} else if block.Video != nil {
			parts = append(parts, schema.ToolOutputPart{
				Type:  schema.ToolPartTypeVideo,
				Video: &schema.ToolOutputVideo{MessagePartCommon: schema.MessagePartCommon{URL: strPtr(block.Video.URL), Base64Data: strPtr(block.Video.Base64Data), FileID: strPtr(block.Video.FileID), MIMEType: block.Video.MIMEType}},
			})
		} else if block.File != nil {
			parts = append(parts, schema.ToolOutputPart{
				Type: schema.ToolPartTypeFile,
				File: &schema.ToolOutputFile{MessagePartCommon: schema.MessagePartCommon{URL: strPtr(block.File.URL), Base64Data: strPtr(block.File.Base64Data), FileID: strPtr(block.File.FileID), MIMEType: block.File.MIMEType}},
			})
		}
	}
	return parts
}

// setToolResultFromOutputParts converts ToolOutputPart slice back to FunctionToolResultContentBlock
// slice and sets f.Content.
func setToolResultFromOutputParts(f *schema.FunctionToolResult, parts []schema.ToolOutputPart) {
	var newBlocks []*schema.FunctionToolResultContentBlock
	for _, part := range parts {
		switch part.Type {
		case schema.ToolPartTypeText:
			newBlocks = append(newBlocks, &schema.FunctionToolResultContentBlock{
				Type: schema.FunctionToolResultContentBlockTypeText,
				Text: &schema.UserInputText{Text: part.Text},
			})
		case schema.ToolPartTypeImage:
			if part.Image != nil {
				newBlocks = append(newBlocks, &schema.FunctionToolResultContentBlock{
					Type:  schema.FunctionToolResultContentBlockTypeImage,
					Image: &schema.UserInputImage{URL: ptrStr(part.Image.URL), Base64Data: ptrStr(part.Image.Base64Data), FileID: ptrStr(part.Image.FileID), MIMEType: part.Image.MIMEType},
				})
			}
		case schema.ToolPartTypeAudio:
			if part.Audio != nil {
				newBlocks = append(newBlocks, &schema.FunctionToolResultContentBlock{
					Type:  schema.FunctionToolResultContentBlockTypeAudio,
					Audio: &schema.UserInputAudio{URL: ptrStr(part.Audio.URL), Base64Data: ptrStr(part.Audio.Base64Data), FileID: ptrStr(part.Audio.FileID), MIMEType: part.Audio.MIMEType},
				})
			}
		case schema.ToolPartTypeVideo:
			if part.Video != nil {
				newBlocks = append(newBlocks, &schema.FunctionToolResultContentBlock{
					Type:  schema.FunctionToolResultContentBlockTypeVideo,
					Video: &schema.UserInputVideo{URL: ptrStr(part.Video.URL), Base64Data: ptrStr(part.Video.Base64Data), FileID: ptrStr(part.Video.FileID), MIMEType: part.Video.MIMEType},
				})
			}
		case schema.ToolPartTypeFile:
			if part.File != nil {
				newBlocks = append(newBlocks, &schema.FunctionToolResultContentBlock{
					Type: schema.FunctionToolResultContentBlockTypeFile,
					File: &schema.UserInputFile{URL: ptrStr(part.File.URL), Base64Data: ptrStr(part.File.Base64Data), FileID: ptrStr(part.File.FileID), MIMEType: part.File.MIMEType},
				})
			}
		}
	}
	f.Content = newBlocks
}

// strPtr returns a pointer to s, or nil if s is empty.
func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// ptrStr safely dereferences a *string, returning "" if nil.
func ptrStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}