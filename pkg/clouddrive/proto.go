package clouddrive

import (
	"encoding/binary"
	"fmt"
)

type wireReader struct {
	b   []byte
	pos int
}

func (r *wireReader) tag() (int, int, bool, error) {
	v, ok, err := r.varint()
	if err != nil || !ok {
		return 0, 0, ok, err
	}
	return int(v >> 3), int(v & 7), true, nil
}

func (r *wireReader) varint() (uint64, bool, error) {
	if r.pos >= len(r.b) {
		return 0, false, nil
	}
	var v uint64
	for shift := uint(0); ; shift += 7 {
		if shift > 63 || r.pos >= len(r.b) {
			return 0, false, fmt.Errorf("invalid protobuf varint")
		}
		c := r.b[r.pos]
		r.pos++
		v |= uint64(c&0x7f) << shift
		if c&0x80 == 0 {
			return v, true, nil
		}
	}
}

func (r *wireReader) bytes() ([]byte, error) {
	n, ok, err := r.varint()
	maxInt := uint64(^uint(0) >> 1)
	if err != nil || !ok || n > maxInt || int(n) > len(r.b)-r.pos {
		return nil, fmt.Errorf("invalid protobuf length")
	}
	start := r.pos
	r.pos += int(n)
	return r.b[start:r.pos], nil
}

func (r *wireReader) skip(wire int) error {
	switch wire {
	case 0:
		_, _, err := r.varint()
		return err
	case 1:
		if len(r.b)-r.pos < 8 {
			return fmt.Errorf("truncated fixed64")
		}
		r.pos += 8
	case 2:
		_, err := r.bytes()
		return err
	case 5:
		if len(r.b)-r.pos < 4 {
			return fmt.Errorf("truncated fixed32")
		}
		r.pos += 4
	default:
		return fmt.Errorf("unsupported protobuf wire type %d", wire)
	}
	return nil
}

func putVarint(dst *[]byte, value uint64) {
	var buf [10]byte
	n := binary.PutUvarint(buf[:], value)
	*dst = append(*dst, buf[:n]...)
}

func putBytes(dst *[]byte, field int, value []byte) {
	if len(value) == 0 {
		return
	}
	putVarint(dst, uint64(field<<3|2))
	putVarint(dst, uint64(len(value)))
	*dst = append(*dst, value...)
}

func putString(dst *[]byte, field int, value string) { putBytes(dst, field, []byte(value)) }

func putBool(dst *[]byte, field int, value bool) {
	if !value {
		return
	}
	putVarint(dst, uint64(field<<3))
	putVarint(dst, 1)
}

func emptyRequest() []byte { return nil }

func listSubFilesRequest(path string, forceRefresh bool) []byte {
	var out []byte
	putString(&out, 1, path)
	putBool(&out, 2, forceRefresh)
	return out
}

type Mount struct {
	MountPath  string
	SourceDir  string
	LocalMount bool
	ReadOnly   bool
	IsMounted  bool
}

func parseMount(data []byte) (Mount, error) {
	var out Mount
	r := wireReader{b: data}
	for {
		field, wire, ok, err := r.tag()
		if err != nil || !ok {
			return out, err
		}
		switch field {
		case 1, 2:
			if wire != 2 {
				return out, fmt.Errorf("mount field %d has wire type %d", field, wire)
			}
			v, err := r.bytes()
			if err != nil {
				return out, err
			}
			if field == 1 {
				out.MountPath = string(v)
			} else {
				out.SourceDir = string(v)
			}
		case 3, 4, 9:
			if wire != 0 {
				return out, fmt.Errorf("mount field %d has wire type %d", field, wire)
			}
			v, _, err := r.varint()
			if err != nil {
				return out, err
			}
			switch field {
			case 3:
				out.LocalMount = v != 0
			case 4:
				out.ReadOnly = v != 0
			case 9:
				out.IsMounted = v != 0
			}
		default:
			if err := r.skip(wire); err != nil {
				return out, err
			}
		}
	}
}

func parseMounts(data []byte) ([]Mount, error) {
	var out []Mount
	r := wireReader{b: data}
	for {
		field, wire, ok, err := r.tag()
		if err != nil || !ok {
			return out, err
		}
		if field == 1 && wire == 2 {
			v, err := r.bytes()
			if err != nil {
				return out, err
			}
			mount, err := parseMount(v)
			if err != nil {
				return out, err
			}
			out = append(out, mount)
		} else if err := r.skip(wire); err != nil {
			return out, err
		}
	}
}

type Change struct {
	Type        int
	IsDirectory bool
	Path        string
	NewPath     string
}

func parseChange(data []byte) (Change, error) {
	var out Change
	r := wireReader{b: data}
	for {
		field, wire, ok, err := r.tag()
		if err != nil || !ok {
			return out, err
		}
		switch field {
		case 1, 2:
			if wire != 0 {
				return out, fmt.Errorf("change field %d has wire type %d", field, wire)
			}
			v, _, err := r.varint()
			if err != nil {
				return out, err
			}
			if field == 1 {
				out.Type = int(v)
			} else {
				out.IsDirectory = v != 0
			}
		case 3, 4:
			if wire != 2 {
				return out, fmt.Errorf("change field %d has wire type %d", field, wire)
			}
			v, err := r.bytes()
			if err != nil {
				return out, err
			}
			if field == 3 {
				out.Path = string(v)
			} else {
				out.NewPath = string(v)
			}
		default:
			if err := r.skip(wire); err != nil {
				return out, err
			}
		}
	}
}

func parsePush(data []byte) (Change, bool, error) {
	change, ok, _, err := parsePushMessage(data)
	return change, ok, err
}

// parsePushMessage decodes a CloudDrive2 push frame and also returns the
// message type so the long-lived stream can expose useful diagnostics.
func parsePushMessage(data []byte) (Change, bool, int, error) {
	var messageType int
	var nested []byte
	r := wireReader{b: data}
	for {
		field, wire, ok, err := r.tag()
		if err != nil {
			return Change{}, false, messageType, err
		}
		if !ok {
			break
		}
		switch field {
		case 1:
			if wire != 0 {
				return Change{}, false, messageType, fmt.Errorf("push type has wire type %d", wire)
			}
			v, _, err := r.varint()
			if err != nil {
				return Change{}, false, messageType, err
			}
			messageType = int(v)
		case 5:
			if wire != 2 {
				return Change{}, false, messageType, fmt.Errorf("push change has wire type %d", wire)
			}
			v, err := r.bytes()
			if err != nil {
				return Change{}, false, messageType, err
			}
			nested = v
		default:
			if err := r.skip(wire); err != nil {
				return Change{}, false, messageType, err
			}
		}
	}
	if messageType != 4 || len(nested) == 0 {
		return Change{}, false, messageType, nil
	}
	change, err := parseChange(nested)
	return change, err == nil, messageType, err
}
