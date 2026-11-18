/*
 * Copyright 2024 CloudWeGo Authors
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

package schema

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToolInfoDecodeClearsStaleParamsOneOf(t *testing.T) {
	newPrevious := func() ToolInfo {
		return ToolInfo{
			Name: "lookup",
			ParamsOneOf: NewParamsOneOfByParams(map[string]*ParameterInfo{
				"query": {Type: String, Required: true},
			}),
		}
	}

	data, err := json.Marshal(&ToolInfo{Name: "ping"})
	require.NoError(t, err)

	previous := newPrevious()
	require.NoError(t, json.Unmarshal(data, &previous))
	assert.Equal(t, "ping", previous.Name)
	assert.Nil(t, previous.ParamsOneOf)

	var buf bytes.Buffer
	require.NoError(t, gob.NewEncoder(&buf).Encode(&ToolInfo{Name: "ping"}))

	gobPrevious := newPrevious()
	require.NoError(t, gob.NewDecoder(&buf).Decode(&gobPrevious))
	assert.Equal(t, "ping", gobPrevious.Name)
	assert.Nil(t, gobPrevious.ParamsOneOf)
}
