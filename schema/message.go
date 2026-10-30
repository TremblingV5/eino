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
	"context"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"sync"
	"text/template"

	"github.com/nikolalohinski/gonja"
	"github.com/nikolalohinski/gonja/config"
	"github.com/nikolalohinski/gonja/exec"
	"github.com/nikolalohinski/gonja/nodes"
	"github.com/nikolalohinski/gonja/parser"
	"github.com/slongfield/pyfmt"

	"github.com/cloudwego/eino/internal"
	"github.com/cloudwego/eino/internal/generic"
)

func init() {
	internal.RegisterStreamChunkConcatFunc(ConcatMessages)
	internal.RegisterStreamChunkConcatFunc(ConcatMessageArray)
	internal.RegisterStreamChunkConcatFunc(ConcatAgenticMessages)
	internal.RegisterStreamChunkConcatFunc(ConcatAgenticMessagesArray)
	internal.RegisterStreamChunkConcatFunc(ConcatToolResults)
}

func buildConcatGenericArray[T any](f func([]*T) (*T, error)) func([][]*T) ([]*T, error) {
	return func(mas [][]*T) ([]*T, error) {
		arrayLen := len(mas[0])
		ret := make([]*T, arrayLen)
		slicesToConcat := make([][]*T, arrayLen)
		for _, ma := range mas {
			if len(ma) != arrayLen {
				return nil, fmt.Errorf("unexpected array length. "+"Got %d, expected %d", len(ma), arrayLen)
			}
			for i := 0; i < arrayLen; i++ {
				m := ma[i]
				if m != nil {
					slicesToConcat[i] = append(slicesToConcat[i], m)
				}
			}
		}
		for i, slice := range slicesToConcat {
			if len(slice) == 0 {
				ret[i] = nil
			} else if len(slice) == 1 {
				ret[i] = slice[0]
			} else {
				cm, err := f(slice)
				if err != nil {
					return nil, err
				}
				ret[i] = cm
			}
		}
		return ret, nil
	}
}