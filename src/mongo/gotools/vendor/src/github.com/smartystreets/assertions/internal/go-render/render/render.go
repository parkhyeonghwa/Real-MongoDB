// Copyright 2015 The Chromium Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package render

import (
	"bytes"
	"fmt"
	"reflect"
	"sort"
	"strconv"
)

var implicitTypeMap = map[reflect.Kind]string{
	reflect.Bool:       "bool",
	reflect.String:     "string",
	reflect.Int:        "int",
	reflect.Int8:       "int8",
	reflect.Int16:      "int16",
	reflect.Int32:      "int32",
	reflect.Int64:      "int64",
	reflect.Uint:       "uint",
	reflect.Uint8:      "uint8",
	reflect.Uint16:     "uint16",
	reflect.Uint32:     "uint32",
	reflect.Uint64:     "uint64",
	reflect.Float32:    "float32",
	reflect.Float64:    "float64",
	reflect.Complex64:  "complex64",
	reflect.Complex128: "complex128",
}

// Render converts a structure to a string representation. Unline the "%#v"
// format string, this resolves pointer types' contents in structs, maps, and
// slices/arrays and prints their field values.
func Render(v interface{}) string {
	buf := bytes.Buffer{}
	s := (*traverseState)(nil)
	s.render(&buf, 0, reflect.ValueOf(v))
	return buf.String()
}

// renderPointer is called to render a pointer value.
//
// This is overridable so that the test suite can have deterministic pointer
// values in its expectations.
var renderPointer = func(buf *bytes.Buffer, p uintptr) {
	fmt.Fprintf(buf, "0x%016x", p)
}

// traverseState is used to note and avoid recursion as struct members are being
// traversed.
//
// traverseState is allowed to be nil. Specifically, the root state is nil.
type traverseState struct {
	parent *traverseState
	ptr    uintptr
}

func (s *traverseState) forkFor(ptr uintptr) *traverseState {
	for cur := s; cur != nil; cur = cur.parent {
		if ptr == cur.ptr {
			return nil
		}
	}

	fs := &traverseState{
		parent: s,
		ptr:    ptr,
	}
	return fs
}

func (s *traverseState) render(buf *bytes.Buffer, ptrs int, v reflect.Value) {
	if v.Kind() == reflect.Invalid {
		buf.WriteString("nil")
		return
	}
	vt := v.Type()

	// If the type being rendered is a potentially recursive type (a type that
	// can contain itself as a member), we need to avoid recursion.
	//
	// If we've already seen this type before, mark that this is the case and
	// write a recursion placeholder instead of actually rendering it.
	//
	// If we haven't seen it before, fork our `seen` tracking so any higher-up
	// renderers will also render it at least once, then mark that we've seen it
	// to avoid recursing on lower layers.
	pe := uintptr(0)
	vk := vt.Kind()
	switch vk {
	case reflect.Ptr:
		// Since structs and arrays aren't pointers, they can't directly be
		// recursed, but they can contain pointers to themselves. Record their
		// pointer to avoid this.
		switch v.Elem().Kind() {
		case reflect.Struct, reflect.Array:
			pe = v.Pointer()
		}

	case reflect.Slice, reflect.Map:
		pe = v.Pointer()
	}
	if pe != 0 {
		s = s.forkFor(pe)
		if s == nil {
			buf.WriteString("<REC(")
			writeType(buf, ptrs, vt)
			buf.WriteString(")>")
			return
		}
	}

	switch vk {
	case reflect.Struct:
		writeType(buf, ptrs, vt)
		buf.WriteRune('{')
		for i := 0; i < vt.NumField(); i++ {
			if i > 0 {
				buf.WriteString(", ")
			}
			buf.WriteString(vt.Field(i).Name)
			buf.WriteRune(':')

			s.render(buf, 0, v.Field(i))
		}
		buf.WriteRune('}')

	case reflect.Slice:
		if v.IsNil() {
			writeType(buf, ptrs, vt)
			buf.WriteString("(nil)")
			return
		}
		fallthrough

	case reflect.Array:
		writeType(buf, ptrs, vt)
		buf.WriteString("{")
		for i := 0; i < v.Len(); i++ {
			if i > 0 {
				buf.WriteString(", ")
			}

			s.render(buf, 0, v.Index(i))
		}
		buf.WriteRune('}')

	case reflect.Map:
		writeType(buf, ptrs, vt)
		if v.IsNil() {
			buf.WriteString("(nil)")
		} else {
			buf.WriteString("{")

			mkeys := v.MapKeys()
			tryAndSortMapKeys(vt, mkeys)

			for i, mk := range mkeys {
				if i > 0 {
					buf.WriteString(", ")
				}

				s.render(buf, 0, mk)
				buf.WriteString(":")
				s.render(buf, 0, v.MapIndex(mk))
			}
			buf.WriteRune('}')
		}

	case reflect.Ptr:
		ptrs++
		fallthrough
	case reflect.Interface:
		if v.IsNil() {
			writeType(buf, ptrs, v.Type())
			buf.WriteRune('(')
			fmt.Fprint(buf, "nil")
			buf.WriteRune(')')
		} else {
			s.render(buf, ptrs, v.Elem())
		}

	case reflect.Chan, reflect.Func, reflect.UnsafePointer:
		writeType(buf, ptrs, vt)
		buf.WriteRune('(')
		renderPointer(buf, v.Pointer())
		buf.WriteRune(')')

	default:
		tstr := vt.String()
		implicit := ptrs == 0 && implicitTypeMap[vk] == tstr
		if !implicit {
			writeType(buf, ptrs, vt)
			buf.WriteRune('(')
		}

		switch vk {
		case reflect.String:
			fmt.Fprintf(buf, "%q", v.String())
		case reflect.Bool:
			fmt.Fprintf(buf, "%v", v.Bool())

		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			fmt.Fprintf(buf, "%d", v.Int())

		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			fmt.Fprintf(buf, "%d", v.Uint())

		case reflect.Float32, reflect.Float64:
			fmt.Fprintf(buf, "%g", v.Float())

		case reflect.Complex64, reflect.Complex128:
			fmt.Fprintf(buf, "%g", v.Complex())
		}

		if !implicit {
			buf.WriteRune(')')
		}
	}
}

func writeType(buf *bytes.Buffer, ptrs int, t reflect.Type) {
	parens := ptrs > 0
	switch t.Kind() {
	case reflect.Chan, reflect.Func, reflect.UnsafePointer:
		parens = true
	}

	if parens {
		buf.WriteRune('(')
		for i := 0; i < ptrs; i++ {
			buf.WriteRune('*')
		}
	}

	switch t.Kind() {
	case reflect.Ptr:
		if ptrs == 0 {
			// This pointer was referenced from within writeType (e.g., as part of
			// rendering a list), and so hasn't had its pointer asterisk accounted
			// for.
			buf.WriteRune('*')
		}
		writeType(buf, 0, t.Elem())

	case reflect.Interface:
		if n := t.Name(); n != "" {
			buf.WriteString(t.String())
		} else {
			buf.WriteString("interface{}")
		}

	case reflect.Array:
		buf.WriteRune('[')
		buf.WriteString(strconv.FormatInt(int64(t.Len()), 10))
		buf.WriteRune(']')
		writeType(buf, 0, t.Elem())

	case reflect.Slice:
		if t == reflect.SliceOf(t.Elem()) {
			buf.WriteString("[]")
			writeType(buf, 0, t.Elem())
		} else {
			// Custom slice type, use type name.
			buf.WriteString(t.String())
		}

	case reflect.Map:
		if t == reflect.MapOf(t.Key(), t.Elem()) {
			buf.WriteString("map[")
			writeType(buf, 0, t.Key())
			buf.WriteRune(']')
			writeType(buf, 0, t.Elem())
		} else {
			// Custom map type, use type name.
			buf.WriteString(t.String())
		}

	default:
		buf.WriteString(t.String())
	}

	if parens {
		buf.WriteRune(')')
	}
}

type sortableValueSlice struct {
	kind     reflect.Kind
	elements []reflect.Value
}

func (s *sortableValueSlice) Len() int {
	return len(s.elements)
}

func (s *sortableValueSlice) Less(i, j int) bool {
	switch s.kind {
	case reflect.String:
		return s.elements[i].String() < s.elements[j].String()

	case reflect.Int:
		return s.elements[i].Int() < s.elements[j].Int()

	default:
		panic(fmt.Errorf("unsupported sort kind: %s", s.kind))
	}
}

func (s *sortableValueSlice) Swap(i, j int) {
	s.elements[i], s.elements[j] = s.elements[j], s.elements[i]
}

func tryAndSortMapKeys(mt reflect.Type, k []reflect.Value) {
	// Try our stock sortable values.
	switch mt.Key().Kind() {
	case reflect.String, reflect.Int:
		vs := &sortableValueSlice{
			kind:     mt.Key().Kind(),
			elements: k,
		}
		sort.Sort(vs)
	}
}
