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
	"reflect"
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"

	"github.com/cloudwego/eino/internal/safe"
)

var ErrNoValue = errors.New("no value")
var ErrRecvAfterClosed = errors.New("recv after stream closed")

type SourceEOF struct {
	sourceName string
}

func (e *SourceEOF) Error() string {
	return fmt.Sprintf("EOF from source stream: %s", e.sourceName)
}

func GetSourceName(err error) (string, bool) {
	var sErr *SourceEOF
	if errors.As(err, &sErr) {
		return sErr.sourceName, true
	}
	return "", false
}

func Pipe[T any](cap int) (*StreamReader[T], *StreamWriter[T]) {
	stm := newStream[T](cap)
	return stm.asReader(), &StreamWriter[T]{stm: stm}
}

type StreamWriter[T any] struct {
	stm *stream[T]
}

func (sw *StreamWriter[T]) Send(chunk T, err error) (closed bool) {
	return sw.stm.send(chunk, err)
}

func (sw *StreamWriter[T]) Close() {
	sw.stm.closeSend()
}

type StreamReader[T any] struct {
	typ readerType
	st  *stream[T]
	ar  *arrayReader[T]
	msr *multiStreamReader[T]
	srw *streamReaderWithConvert[T]
	csr *childStreamReader[T]
}

func (sr *StreamReader[T]) Recv() (T, error) {
	switch sr.typ {
	case readerTypeStream:
		return sr.st.recv()
	case readerTypeArray:
		return sr.ar.recv()
	case readerTypeMultiStream:
		return sr.msr.recv()
	case readerTypeWithConvert:
		return sr.srw.recv()
	case readerTypeChild:
		return sr.csr.recv()
	default:
		panic("impossible")
	}
}

func (sr *StreamReader[T]) Close() {
	switch sr.typ {
	case readerTypeStream:
		sr.st.closeRecv()
	case readerTypeArray:
	case readerTypeMultiStream:
		sr.msr.close()
	case readerTypeWithConvert:
		sr.srw.close()
	case readerTypeChild:
		sr.csr.close()
	default:
		panic("impossible")
	}
}

func (sr *StreamReader[T]) Copy(n int) []*StreamReader[T] {
	if n < 2 {
		return []*StreamReader[T]{sr}
	}
	if sr.typ == readerTypeArray {
		ret := make([]*StreamReader[T], n)
		for i, ar := range sr.ar.copy(n) {
			ret[i] = &StreamReader[T]{typ: readerTypeArray, ar: ar}
		}
		return ret
	}
	return copyStreamReaders[T](sr, n)
}

func (sr *StreamReader[T]) SetAutomaticClose() {
	switch sr.typ {
	case readerTypeStream:
		if !sr.st.automaticClose {
			sr.st.automaticClose = true
			var flag uint32
			sr.st.closedFlag = &flag
			runtime.SetFinalizer(sr, func(s *StreamReader[T]) { s.Close() })
		}
	case readerTypeMultiStream:
		for _, s := range sr.msr.nonClosedStreams() {
			if !s.automaticClose {
				s.automaticClose = true
				var flag uint32
				s.closedFlag = &flag
				runtime.SetFinalizer(s, func(st *stream[T]) { st.closeRecv() })
			}
		}
	case readerTypeChild:
		sr.csr.parent.sr.SetAutomaticClose()
	case readerTypeWithConvert:
		sr.srw.sr.SetAutomaticClose()
	}
}

func (sr *StreamReader[T]) recvAny() (any, error) {
	return sr.Recv()
}

func (sr *StreamReader[T]) copyAny(n int) []iStreamReader {
	ret := make([]iStreamReader, n)
	srs := sr.Copy(n)
	for i := 0; i < n; i++ {
		ret[i] = srs[i]
	}
	return ret
}

func arrToStream[T any](arr []T) *stream[T] {
	s := newStream[T](len(arr))
	for i := range arr {
		s.send(arr[i], nil)
	}
	s.closeSend()
	return s
}

func (sr *StreamReader[T]) toStream() *stream[T] {
	switch sr.typ {
	case readerTypeStream:
		return sr.st
	case readerTypeArray:
		return sr.ar.toStream()
	case readerTypeMultiStream:
		return sr.msr.toStream()
	case readerTypeWithConvert:
		return sr.srw.toStream()
	case readerTypeChild:
		return sr.csr.toStream()
	default:
		panic("impossible")
	}
}

type readerType int

const (
	readerTypeStream readerType = iota
	readerTypeArray
	readerTypeMultiStream
	readerTypeWithConvert
	readerTypeChild
)

type iStreamReader interface {
	recvAny() (any, error)
	copyAny(int) []iStreamReader
	Close()
	SetAutomaticClose()
}

type stream[T any] struct {
	items          chan streamItem[T]
	closed         chan struct{}
	automaticClose bool
	closedFlag     *uint32
}

type streamItem[T any] struct {
	chunk T
	err   error
}

func newStream[T any](cap int) *stream[T] {
	return &stream[T]{
		items:  make(chan streamItem[T], cap),
		closed: make(chan struct{}),
	}
}

func (s *stream[T]) asReader() *StreamReader[T] {
	return &StreamReader[T]{typ: readerTypeStream, st: s}
}

func (s *stream[T]) recv() (chunk T, err error) {
	item, ok := <-s.items
	if !ok {
		item.err = io.EOF
	}
	return item.chunk, item.err
}

func (s *stream[T]) send(chunk T, err error) (closed bool) {
	select {
	case <-s.closed:
		return true
	default:
	}
	item := streamItem[T]{chunk, err}
	select {
	case <-s.closed:
		return true
	case s.items <- item:
		return false
	}
}

func (s *stream[T]) closeSend()    { close(s.items) }
func (s *stream[T]) closeRecv() {
	if s.automaticClose {
		if atomic.CompareAndSwapUint32(s.closedFlag, 0, 1) {
			close(s.closed)
		}
		return
	}
	close(s.closed)
}

func StreamReaderFromArray[T any](arr []T) *StreamReader[T] {
	return &StreamReader[T]{ar: &arrayReader[T]{arr: arr}, typ: readerTypeArray}
}

type arrayReader[T any] struct {
	arr   []T
	index int
}

func (ar *arrayReader[T]) recv() (T, error) {
	if ar.index < len(ar.arr) {
		ret := ar.arr[ar.index]
		ar.index++
		return ret, nil
	}
	var t T
	return t, io.EOF
}

func (ar *arrayReader[T]) copy(n int) []*arrayReader[T] {
	ret := make([]*arrayReader[T], n)
	for i := 0; i < n; i++ {
		ret[i] = &arrayReader[T]{arr: ar.arr, index: ar.index}
	}
	return ret
}

func (ar *arrayReader[T]) toStream() *stream[T] {
	return arrToStream(ar.arr[ar.index:])
}

type multiArrayReader[T any] struct {
	ars   []*arrayReader[T]
	index int
}

type multiStreamReader[T any] struct {
	sts               []*stream[T]
	itemsCases        []reflect.SelectCase
	nonClosed         []int
	sourceReaderNames []string
}

func newMultiStreamReader[T any](sts []*stream[T]) *multiStreamReader[T] {
	var itemsCases []reflect.SelectCase
	if len(sts) > maxSelectNum {
		itemsCases = make([]reflect.SelectCase, len(sts))
		for i, st := range sts {
			itemsCases[i] = reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(st.items)}
		}
	}
	nonClosed := make([]int, len(sts))
	for i := range sts {
		nonClosed[i] = i
	}
	return &multiStreamReader[T]{sts: sts, itemsCases: itemsCases, nonClosed: nonClosed}
}

func (msr *multiStreamReader[T]) recv() (T, error) {
	for len(msr.nonClosed) > 0 {
		var chosen int
		var ok bool
		if len(msr.nonClosed) > maxSelectNum {
			var recv reflect.Value
			chosen, recv, ok = reflect.Select(msr.itemsCases)
			if ok {
				item := recv.Interface().(streamItem[T])
				return item.chunk, item.err
			}
			msr.itemsCases[chosen].Chan = reflect.Value{}
		} else {
			var item *streamItem[T]
			chosen, item, ok = receiveN(msr.nonClosed, msr.sts)
			if ok {
				return item.chunk, item.err
			}
		}
		for i := range msr.nonClosed {
			if msr.nonClosed[i] == chosen {
				msr.nonClosed = append(msr.nonClosed[:i], msr.nonClosed[i+1:]...)
				break
			}
		}
		if len(msr.sourceReaderNames) > 0 {
			var t T
			return t, &SourceEOF{msr.sourceReaderNames[chosen]}
		}
	}
	var t T
	return t, io.EOF
}

func (msr *multiStreamReader[T]) nonClosedStreams() []*stream[T] {
	ret := make([]*stream[T], len(msr.nonClosed))
	for i, idx := range msr.nonClosed {
		ret[i] = msr.sts[idx]
	}
	return ret
}

func (msr *multiStreamReader[T]) close() {
	for _, s := range msr.sts {
		s.closeRecv()
	}
}

func (msr *multiStreamReader[T]) toStream() *stream[T] {
	return toStream[T, *multiStreamReader[T]](msr)
}

type streamReaderWithConvert[T any] struct {
	sr        iStreamReader
	convert   func(any) (T, error)
	errWrapper func(error) error
	onEOF     func() (T, error)
	eofDone   bool
}

func newStreamReaderWithConvert[T any](origin iStreamReader, convert func(any) (T, error), opts ...ConvertOption) *StreamReader[T] {
	opt := &convertOptions{}
	for _, o := range opts {
		o(opt)
	}
	srw := &streamReaderWithConvert[T]{sr: origin, convert: convert, errWrapper: opt.ErrWrapper}
	if opt.OnEOF != nil {
		typedOnEOF := opt.OnEOF
		srw.onEOF = func() (T, error) {
			v, err := typedOnEOF()
			if err != nil {
				var t T
				return t, err
			}
			if v == nil {
				var t T
				return t, nil
			}
			return v.(T), nil
		}
	}
	return &StreamReader[T]{typ: readerTypeWithConvert, srw: srw}
}

type convertOptions struct {
	ErrWrapper func(error) error
	OnEOF      func() (any, error)
}

type ConvertOption func(*convertOptions)

func WithErrWrapper(wrapper func(error) error) ConvertOption {
	return func(o *convertOptions) { o.ErrWrapper = wrapper }
}

func WithOnEOF(fn func() (any, error)) ConvertOption {
	return func(o *convertOptions) { o.OnEOF = fn }
}

func StreamReaderWithConvert[T, D any](sr *StreamReader[T], convert func(T) (D, error), opts ...ConvertOption) *StreamReader[D] {
	c := func(a any) (D, error) {
		if a == nil {
			var zero T
			return convert(zero)
		}
		return convert(a.(T))
	}
	return newStreamReaderWithConvert(sr, c, opts...)
}

func (srw *streamReaderWithConvert[T]) recv() (T, error) {
	for {
		out, err := srw.sr.recvAny()
		if err != nil {
			var t T
			if err == io.EOF {
				if srw.onEOF != nil && !srw.eofDone {
					srw.eofDone = true
					val, onEOFErr := srw.onEOF()
					if onEOFErr != io.EOF {
						return val, onEOFErr
					}
				}
				return t, io.EOF
			}
			if srw.errWrapper != nil {
				err = srw.errWrapper(err)
				if err != nil {
					return t, err
				}
				continue
			}
			return t, err
		}
		t, err := srw.convert(out)
		if err == nil {
			return t, nil
		}
		if !errors.Is(err, ErrNoValue) {
			return t, err
		}
	}
}

func (srw *streamReaderWithConvert[T]) close() { srw.sr.Close() }

type reader[T any] interface {
	recv() (T, error)
	close()
}

func toStream[T any, Reader reader[T]](r Reader) *stream[T] {
	ret := newStream[T](5)
	go func() {
		defer func() {
			panicErr := recover()
			if panicErr != nil {
				e := safe.NewPanicErr(panicErr, debug.Stack())
				var chunk T
				_ = ret.send(chunk, e)
			}
			ret.closeSend()
			r.close()
		}()
		for {
			out, err := r.recv()
			if err == io.EOF {
				break
			}
			closed := ret.send(out, err)
			if closed {
				break
			}
		}
	}()
	return ret
}

func (srw *streamReaderWithConvert[T]) toStream() *stream[T] {
	return toStream[T, *streamReaderWithConvert[T]](srw)
}

type cpStreamElement[T any] struct {
	once sync.Once
	next *cpStreamElement[T]
	item streamItem[T]
}

func copyStreamReaders[T any](sr *StreamReader[T], n int) []*StreamReader[T] {
	cpsr := &parentStreamReader[T]{sr: sr, subStreamList: make([]*cpStreamElement[T], n), closedNum: 0}
	elem := &cpStreamElement[T]{}
	for i := range cpsr.subStreamList {
		cpsr.subStreamList[i] = elem
	}
	ret := make([]*StreamReader[T], n)
	for i := range ret {
		ret[i] = &StreamReader[T]{csr: &childStreamReader[T]{parent: cpsr, index: i}, typ: readerTypeChild}
	}
	return ret
}

type parentStreamReader[T any] struct {
	sr            *StreamReader[T]
	subStreamList []*cpStreamElement[T]
	closedNum     uint32
}

func (p *parentStreamReader[T]) peek(idx int) (t T, err error) {
	elem := p.subStreamList[idx]
	if elem == nil {
		return t, ErrRecvAfterClosed
	}
	elem.once.Do(func() {
		t, err = p.sr.Recv()
		elem.item = streamItem[T]{chunk: t, err: err}
		if err != io.EOF {
			elem.next = &cpStreamElement[T]{}
			p.subStreamList[idx] = elem.next
		}
	})
	t = elem.item.chunk
	err = elem.item.err
	if err != io.EOF {
		p.subStreamList[idx] = elem.next
	}
	return t, err
}

func (p *parentStreamReader[T]) close(idx int) {
	if p.subStreamList[idx] == nil {
		return
	}
	p.subStreamList[idx] = nil
	curClosedNum := atomic.AddUint32(&p.closedNum, 1)
	allClosed := int(curClosedNum) == len(p.subStreamList)
	if allClosed {
		p.sr.Close()
	}
}

type childStreamReader[T any] struct {
	parent *parentStreamReader[T]
	index  int
}

func (csr *childStreamReader[T]) recv() (T, error)      { return csr.parent.peek(csr.index) }
func (csr *childStreamReader[T]) toStream() *stream[T] { return toStream[T, *childStreamReader[T]](csr) }
func (csr *childStreamReader[T]) close()               { csr.parent.close(csr.index) }

func MergeStreamReaders[T any](srs []*StreamReader[T]) *StreamReader[T] {
	if len(srs) < 1 {
		return nil
	}
	if len(srs) < 2 {
		return srs[0]
	}
	var arr []T
	var ss []*stream[T]
	for _, sr := range srs {
		switch sr.typ {
		case readerTypeStream:
			ss = append(ss, sr.st)
		case readerTypeArray:
			arr = append(arr, sr.ar.arr[sr.ar.index:]...)
		case readerTypeMultiStream:
			ss = append(ss, sr.msr.nonClosedStreams()...)
		case readerTypeWithConvert:
			ss = append(ss, sr.srw.toStream())
		case readerTypeChild:
			ss = append(ss, sr.csr.toStream())
		default:
			panic("impossible")
		}
	}
	if len(ss) == 0 {
		return &StreamReader[T]{typ: readerTypeArray, ar: &arrayReader[T]{arr: arr, index: 0}}
	}
	if len(arr) != 0 {
		s := arrToStream(arr)
		ss = append(ss, s)
	}
	return &StreamReader[T]{typ: readerTypeMultiStream, msr: newMultiStreamReader(ss)}
}

func MergeNamedStreamReaders[T any](srs map[string]*StreamReader[T]) *StreamReader[T] {
	if len(srs) < 1 {
		return nil
	}
	ss := make([]*StreamReader[T], len(srs))
	names := make([]string, len(srs))
	i := 0
	for name, sr := range srs {
		ss[i] = sr
		names[i] = name
		i++
	}
	return InternalMergeNamedStreamReaders(ss, names)
}

func InternalMergeNamedStreamReaders[T any](srs []*StreamReader[T], names []string) *StreamReader[T] {
	ss := make([]*stream[T], len(srs))
	for i, sr := range srs {
		ss[i] = sr.toStream()
	}
	msr := newMultiStreamReader(ss)
	msr.sourceReaderNames = names
	return &StreamReader[T]{typ: readerTypeMultiStream, msr: msr}
}