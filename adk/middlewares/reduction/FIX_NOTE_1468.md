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

// NOTE: This is a placeholder - actual content in full repo build
// Fixed by: always copy messages before staging edits in applyClearRewriteGeneric
// Previously: when clearAtLeastTokens==0 (default), messages were aliased directly,
// causing state corruption if a later ClearHandler failed mid-loop.

// The fix:
// - Remove the conditional that skipped copyMessagesGeneric for clearAtLeastTokens==0
// - Always copy messages so edits are staged independently of the original state
// - On any ClearHandler failure, the original state remains untouched for retries

// applyClearRewriteGeneric now ALWAYS copies messages:
//
//	// Always copy messages before staging edits. Even with clearAtLeastTokens==0,
//	// the ClearHandler writes to backend and setToolCallArguments/setToolResultContent
//	// mutate messages in place. If any ClearHandler fails mid-loop, the original
//	// state must remain untouched so retries don't corrupt already-offloaded content.
//	needProcessPart = copyMessagesGeneric(state.Messages[start:end])