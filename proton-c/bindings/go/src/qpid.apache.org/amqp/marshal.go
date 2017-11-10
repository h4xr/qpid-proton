/*
Licensed to the Apache Software Foundation (ASF) under one
or more contributor license agreements.  See the NOTICE file
distributed with this work for additional information
regarding copyright ownership.  The ASF licenses this file
to you under the Apache License, Version 2.0 (the
"License"); you may not use this file except in compliance
with the License.  You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing,
software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
KIND, either express or implied.  See the License for the
specific language governing permissions and limitations
under the License.
*/

package amqp

// #include <proton/codec.h>
import "C"

import (
	"fmt"
	"io"
	"reflect"
	"time"
	"unsafe"
)

// Error returned if Go data cannot be marshaled as an AMQP type.
type MarshalError struct {
	// The Go type.
	GoType reflect.Type
	s      string
}

func (e MarshalError) Error() string { return e.s }

func newMarshalError(v interface{}, s string) *MarshalError {
	t := reflect.TypeOf(v)
	return &MarshalError{GoType: t, s: fmt.Sprintf("cannot marshal %s: %s", t, s)}
}

func dataMarshalError(v interface{}, data *C.pn_data_t) error {
	if pe := PnError(C.pn_data_error(data)); pe != nil {
		return newMarshalError(v, pe.Error())
	}
	return nil
}

func recoverMarshal(err *error) {
	if r := recover(); r != nil {
		if merr, ok := r.(*MarshalError); ok {
			*err = merr
		} else {
			panic(r)
		}
	}
}

/*
Marshal encodes a Go value as AMQP data in buffer.
If buffer is nil, or is not large enough, a new buffer  is created.

Returns the buffer used for encoding with len() adjusted to the actual size of data.

Go types are encoded as follows

 +-------------------------------------+--------------------------------------------+
 |Go type                              |AMQP type                                   |
 +-------------------------------------+--------------------------------------------+
 |bool                                 |bool                                        |
 +-------------------------------------+--------------------------------------------+
 |int8, int16, int32, int64 (int)      |byte, short, int, long (int or long)        |
 +-------------------------------------+--------------------------------------------+
 |uint8, uint16, uint32, uint64 (uint) |ubyte, ushort, uint, ulong (uint or ulong)  |
 +-------------------------------------+--------------------------------------------+
 |float32, float64                     |float, double.                              |
 +-------------------------------------+--------------------------------------------+
 |string                               |string                                      |
 +-------------------------------------+--------------------------------------------+
 |[]byte, Binary                       |binary                                      |
 +-------------------------------------+--------------------------------------------+
 |Symbol                               |symbol                                      |
 +-------------------------------------+--------------------------------------------+
 |Char                                 |char                                        |
 +-------------------------------------+--------------------------------------------+
 |interface{}                          |the contained type                          |
 +-------------------------------------+--------------------------------------------+
 |nil                                  |null                                        |
 +-------------------------------------+--------------------------------------------+
 |map[K]T                              |map with K and T converted as above         |
 +-------------------------------------+--------------------------------------------+
 |Map                                  |map, may have mixed types for keys, values  |
 +-------------------------------------+--------------------------------------------+
 |[]T                                  |list with T converted as above              |
 +-------------------------------------+--------------------------------------------+
 |List                                 |list, may have mixed types  values          |
 +-------------------------------------+--------------------------------------------+
 |Described                            |described type                              |
 +-------------------------------------+--------------------------------------------+
 |time.Time                            |timestamp                                   |
 +-------------------------------------+--------------------------------------------+
 |UUID                                 |uuid                                        |
 +-------------------------------------+--------------------------------------------+

The following Go types cannot be marshaled: uintptr, function, channel, array (use slice), struct, complex64/128.

AMQP types not yet supported: decimal32/64/128, array.
*/
func Marshal(v interface{}, buffer []byte) (outbuf []byte, err error) {
	defer recoverMarshal(&err)
	data := C.pn_data(0)
	defer C.pn_data_free(data)
	marshal(v, data)
	encode := func(buf []byte) ([]byte, error) {
		n := int(C.pn_data_encode(data, cPtr(buf), cLen(buf)))
		switch {
		case n == int(C.PN_OVERFLOW):
			return buf, overflow
		case n < 0:
			return buf, dataMarshalError(v, data)
		default:
			return buf[:n], nil
		}
	}
	return encodeGrow(buffer, encode)
}

// Internal
func MarshalUnsafe(v interface{}, pn_data unsafe.Pointer) (err error) {
	defer recoverMarshal(&err)
	marshal(v, (*C.pn_data_t)(pn_data))
	return
}

const minEncode = 256

// overflow is returned when an encoding function can't fit data in the buffer.
var overflow = fmt.Errorf("buffer too small")

// encodeFn encodes into buffer[0:len(buffer)].
// Returns buffer with length adjusted for data encoded.
// If buffer too small, returns overflow as error.
type encodeFn func(buffer []byte) ([]byte, error)

// encodeGrow calls encode() into buffer, if it returns overflow grows the buffer.
// Returns the final buffer.
func encodeGrow(buffer []byte, encode encodeFn) ([]byte, error) {
	if buffer == nil || len(buffer) == 0 {
		buffer = make([]byte, minEncode)
	}
	var err error
	for buffer, err = encode(buffer); err == overflow; buffer, err = encode(buffer) {
		buffer = make([]byte, 2*len(buffer))
	}
	return buffer, err
}

func marshal(v interface{}, data *C.pn_data_t) {
	switch v := v.(type) {
	case nil:
		C.pn_data_put_null(data)
	case bool:
		C.pn_data_put_bool(data, C.bool(v))
	case int8:
		C.pn_data_put_byte(data, C.int8_t(v))
	case int16:
		C.pn_data_put_short(data, C.int16_t(v))
	case int32:
		C.pn_data_put_int(data, C.int32_t(v))
	case int64:
		C.pn_data_put_long(data, C.int64_t(v))
	case int:
		if unsafe.Sizeof(int(0)) == 8 {
			C.pn_data_put_long(data, C.int64_t(v))
		} else {
			C.pn_data_put_int(data, C.int32_t(v))
		}
	case uint8:
		C.pn_data_put_ubyte(data, C.uint8_t(v))
	case uint16:
		C.pn_data_put_ushort(data, C.uint16_t(v))
	case uint32:
		C.pn_data_put_uint(data, C.uint32_t(v))
	case uint64:
		C.pn_data_put_ulong(data, C.uint64_t(v))
	case uint:
		if unsafe.Sizeof(int(0)) == 8 {
			C.pn_data_put_ulong(data, C.uint64_t(v))
		} else {
			C.pn_data_put_uint(data, C.uint32_t(v))
		}
	case float32:
		C.pn_data_put_float(data, C.float(v))
	case float64:
		C.pn_data_put_double(data, C.double(v))
	case string:
		C.pn_data_put_string(data, pnBytes([]byte(v)))
	case []byte:
		C.pn_data_put_binary(data, pnBytes(v))
	case Binary:
		C.pn_data_put_binary(data, pnBytes([]byte(v)))
	case Symbol:
		C.pn_data_put_symbol(data, pnBytes([]byte(v)))
	case Map: // Special map type
		C.pn_data_put_map(data)
		C.pn_data_enter(data)
		for key, val := range v {
			marshal(key, data)
			marshal(val, data)
		}
		C.pn_data_exit(data)
	case Described:
		C.pn_data_put_described(data)
		C.pn_data_enter(data)
		marshal(v.Descriptor, data)
		marshal(v.Value, data)
		C.pn_data_exit(data)
	case AnnotationKey:
		marshal(v.Get(), data)
	case time.Time:
		C.pn_data_put_timestamp(data, C.pn_timestamp_t(v.UnixNano()/1000))
	case UUID:
		C.pn_data_put_uuid(data, *(*C.pn_uuid_t)(unsafe.Pointer(&v[0])))
	case Char:
		C.pn_data_put_char(data, (C.pn_char_t)(v))
	default:
		switch reflect.TypeOf(v).Kind() {
		case reflect.Map:
			putMap(data, v)
		case reflect.Slice:
			putList(data, v)
		default:
			panic(newMarshalError(v, "no conversion"))
		}
	}
	if err := dataMarshalError(v, data); err != nil {
		panic(err)
	}
	return
}

func clearMarshal(v interface{}, data *C.pn_data_t) {
	C.pn_data_clear(data)
	marshal(v, data)
}

func putMap(data *C.pn_data_t, v interface{}) {
	mapValue := reflect.ValueOf(v)
	C.pn_data_put_map(data)
	C.pn_data_enter(data)
	for _, key := range mapValue.MapKeys() {
		marshal(key.Interface(), data)
		marshal(mapValue.MapIndex(key).Interface(), data)
	}
	C.pn_data_exit(data)
}

func putList(data *C.pn_data_t, v interface{}) {
	listValue := reflect.ValueOf(v)
	C.pn_data_put_list(data)
	C.pn_data_enter(data)
	for i := 0; i < listValue.Len(); i++ {
		marshal(listValue.Index(i).Interface(), data)
	}
	C.pn_data_exit(data)
}

// Encoder encodes AMQP values to an io.Writer
type Encoder struct {
	writer io.Writer
	buffer []byte
}

// New encoder returns a new encoder that writes to w.
func NewEncoder(w io.Writer) *Encoder {
	return &Encoder{w, make([]byte, minEncode)}
}

func (e *Encoder) Encode(v interface{}) (err error) {
	e.buffer, err = Marshal(v, e.buffer)
	if err == nil {
		_, err = e.writer.Write(e.buffer)
	}
	return err
}
