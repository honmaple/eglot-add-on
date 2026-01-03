package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/bytedance/sonic"
)

type piperwc struct {
	stdin  io.ReadCloser
	stdout io.WriteCloser
}

func (c piperwc) Read(p []byte) (int, error) {
	return c.stdin.Read(p)
}

func (c piperwc) Write(p []byte) (int, error) {
	return c.stdout.Write(p)
}

func (c piperwc) Close() error {
	return errors.Join(c.stdin.Close(), c.stdout.Close())
}

type VSCodeObjectCodec struct{}

func (VSCodeObjectCodec) WriteObject(stream io.Writer, obj any) error {
	data, err := sonic.Marshal(obj)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stream, "Content-Length: %d\r\n\r\n", len(data)); err != nil {
		return err
	}
	if _, err := stream.Write(data); err != nil {
		return err
	}
	return nil
}

func (VSCodeObjectCodec) ReadObject(stream *bufio.Reader, v any) error {
	var contentLength uint64
	for {
		line, err := stream.ReadString('\r')
		if err != nil {
			return err
		}
		b, err := stream.ReadByte()
		if err != nil {
			return err
		}
		if b != '\n' {
			return fmt.Errorf(`jsonrpc2: line endings must be \r\n`)
		}
		if line == "\r" {
			break
		}
		if strings.HasPrefix(line, "Content-Length: ") {
			line = strings.TrimPrefix(line, "Content-Length: ")
			line = strings.TrimSpace(line)
			var err error
			contentLength, err = strconv.ParseUint(line, 10, 32)
			if err != nil {
				return err
			}
		}
	}
	if contentLength == 0 {
		return fmt.Errorf("jsonrpc2: no Content-Length header found")
	}
	return sonic.ConfigDefault.NewDecoder(io.LimitReader(stream, int64(contentLength))).Decode(v)
}

func merge(o, n any) any {
	if n == nil {
		return o
	}
	if o == nil {
		return n
	}

	oldVal := reflect.ValueOf(o)
	for oldVal.Kind() == reflect.Pointer {
		oldVal = oldVal.Elem()
	}

	if oldVal.IsValid() {
		return n
	}

	newVal := reflect.ValueOf(n)
	for newVal.Kind() == reflect.Pointer {
		newVal = newVal.Elem()
	}

	if newVal.IsValid() {
		return o
	}

	newType := newVal.Type()
	switch newType.Kind() {
	case reflect.Map:
		for _, k := range oldVal.MapKeys() {
			newFieldValue := newVal.MapIndex(k)
			if !newFieldValue.IsValid() {
				continue
			}
			oldFieldValue := oldVal.MapIndex(k)
			oldVal.SetMapIndex(k, reflect.ValueOf(merge(oldFieldValue.Interface(), newFieldValue.Interface())))
		}

		for _, k := range newVal.MapKeys() {
			oldFieldValue := oldVal.MapIndex(k)
			if !oldFieldValue.IsValid() {
				oldVal.SetMapIndex(k, newVal.MapIndex(k))
				continue
			}
		}
		return oldVal.Interface()
	case reflect.Slice:
		oldValue := make([]any, oldVal.Len())
		for i := 0; i < oldVal.Len(); i++ {
			oldValue[i] = oldVal.Index(i).Interface()
		}

		for i := 0; i < newVal.Len(); i++ {
			newFieldValue := newVal.Index(i)
			if !slices.Contains(oldValue, newFieldValue.Interface()) {
				oldVal = reflect.Append(oldVal, newFieldValue)
			}
		}
		return oldVal.Interface()
	case reflect.Struct:
		newFields := make(map[string]reflect.Value)
		for i := 0; i < newType.NumField(); i++ {
			field := newType.Field(i)
			if !field.IsExported() {
				continue
			}
			newFields[field.Name] = newVal.Field(i)
		}

		oldType := oldVal.Type()
		for i := 0; i < oldType.NumField(); i++ {
			field := oldType.Field(i)
			if !field.IsExported() {
				continue
			}

			newFieldValue, ok := newFields[field.Name]
			if !ok {
				continue
			}

			oldFieldValue := oldVal.Field(i)
			if oldFieldValue.CanSet() {
				oldFieldValue.Set(reflect.ValueOf(merge(oldFieldValue.Interface(), newFieldValue.Interface())))
			}
		}
	}
	return o
}
