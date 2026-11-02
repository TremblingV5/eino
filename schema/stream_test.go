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
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
)

// StreamReaderWithConvert tests are already covered in existing test cases.
// This file contains additional tests for edge cases and new features.

// TestStreamReaderWithConvert_NilInterface verifies that StreamReaderWithConvert does not panic
// on nil interface input, which fixes issue #1420 where a direct type assertion would panic.
func TestStreamReaderWithConvert_NilInterface(t *testing.T) {
	t.Run("nil interface with StreamReader[any]", func(t *testing.T) {
		s := newStream[any](3)

		// Send nil interface values
		s.send(nil, nil)
		s.send("hello", nil)
		s.closeSend()

		// Use StreamReaderWithConvert to transform
		sr := StreamReaderWithConvert[any, string](s.asReader(), func(v any) (string, error) {
			if v == nil {
				return "nil_value", nil
			}
			return fmt.Sprintf("got:%v", v), nil
		})
		sr.SetAutomaticClose()

		// Receive values - should not panic
		v, err := sr.Recv()
		assert.NoError(t, err)
		assert.Equal(t, "nil_value", v)

		v, err = sr.Recv()
		assert.NoError(t, err)
		assert.Equal(t, "got:hello", v)

		_, err = sr.Recv()
		assert.ErrorIs(t, err, io.EOF)
	})

	t.Run("nil interface in copied stream", func(t *testing.T) {
		s := newStream[any](2)
		s.send(nil, nil)
		s.closeSend()

		sr := StreamReaderWithConvert[any, int](s.asReader(), func(v any) (int, error) {
			if v == nil {
				return -1, nil
			}
			return 0, nil
		})
		sr.SetAutomaticClose()

		// Test Copy scenario also works correctly
		copies := sr.Copy(2)
		for _, copy := range copies {
			v, err := copy.Recv()
			assert.NoError(t, err)
			assert.Equal(t, -1, v)
		}
	})

	t.Run("nil interface in pipe stream", func(t *testing.T) {
		// Test Pipe-created StreamReader[any] conversion of nil interfaces
		sr, sw := Pipe[any](3)
		sw.Send(nil, nil)
		sw.Send(nil, nil)
		sw.Close()

		conv := StreamReaderWithConvert[any, string](sr, func(v any) (string, error) {
			if v == nil {
				return "null", nil
			}
			return fmt.Sprintf("val:%v", v), nil
		})
		conv.SetAutomaticClose()

		results := make([]string, 0, 3)
		for {
			v, err := conv.Recv()
			if err == io.EOF {
				break
			}
			assert.NoError(t, err)
			results = append(results, v)
		}

		assert.Equal(t, []string{"null", "null"}, results)
	})
}