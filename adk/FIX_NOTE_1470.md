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

// NOTE: Placeholder for fix documentation
// Fixed by: Track final message output separately from final control event
//
// Previously, InvokableRun used a single lastEvent variable for both:
// - The event carrying the agent's MessageOutput (used to extract return value)
// - The event carrying the final control Action (used for interrupt detection)
//
// AfterAgent middleware can emit custom events with only Actions (no Output).
// These custom events would overwrite lastEvent, causing:
// 1. AgentTool returns empty string (lastEvent has no MessageOutput)
// 2. The previous event's MessageStream gets closed prematurely
//
// The fix introduces finalMessageEvent alongside lastEvent:
// - lastEvent still tracks the final overall event for interrupt detection
// - finalMessageEvent only updates when an event has a MessageOutput
// - Stream close only happens when replacing one MessageOutput with another