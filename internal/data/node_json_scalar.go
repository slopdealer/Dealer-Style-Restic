package data

import (
	"encoding/hex"
	"slices"
	"strconv"
	"time"

	"github.com/restic/restic/internal/restic"
)

// scalarNodeJSON handles strings requiring neither Go name quoting nor JSON
// escaping. Other representations retain the general encoder and its errors.
// The returned storage belongs to the caller; no node storage is borrowed.
func scalarNodeJSON(node Node) ([]byte, bool) {
	return appendScalarNodeJSON(nil, node)
}

// appendScalarNodeJSON can use caller-owned spare capacity. Eligibility is
// checked before appending, so fallback does not modify the destination.
func appendScalarNodeJSON(b []byte, node Node) ([]byte, bool) {
	if node.LinkTargetRaw != nil || len(node.ExtendedAttributes) != 0 || len(node.GenericAttributes) != 0 || len(node.Content) > 4096 {
		return nil, false
	}
	strings := [...]string{node.Name, string(node.Type), node.User, node.Group, node.LinkTarget, node.Error}
	capacity := 384 + 67*len(node.Content)
	for _, s := range strings {
		if len(s) > 4096 {
			return nil, false
		}
		for i := range len(s) {
			c := s[i]
			if c < 0x20 || c >= 0x7f || c == '"' || c == '\\' || c == '<' || c == '>' || c == '&' {
				return nil, false
			}
		}
		capacity += len(s)
	}
	for _, tm := range [...]time.Time{node.ModTime, node.AccessTime, node.ChangeTime} {
		_, offset := tm.Zone()
		if tm.Year() < 0 || tm.Year() > 9999 || offset/60 <= -1440 || offset/60 >= 1440 {
			return nil, false
		}
	}
	b = slices.Grow(b, capacity)
	b = append(b, `{"name":"`...)
	b = append(b, node.Name...)
	b = append(b, `","type":"`...)
	b = append(b, string(node.Type)...)
	b = append(b, '"')
	if node.Mode != 0 {
		b = appendNodeUint(b, `,"mode":`, uint64(node.Mode))
	}
	b = appendNodeTime(b, `,"mtime":"`, node.ModTime)
	b = appendNodeTime(b, `,"atime":"`, node.AccessTime)
	b = appendNodeTime(b, `,"ctime":"`, node.ChangeTime)
	b = appendNodeUint(b, `,"uid":`, uint64(node.UID))
	b = appendNodeUint(b, `,"gid":`, uint64(node.GID))
	if node.User != "" {
		b = appendNodeString(b, `,"user":"`, node.User)
	}
	if node.Group != "" {
		b = appendNodeString(b, `,"group":"`, node.Group)
	}
	if node.Inode != 0 {
		b = appendNodeUint(b, `,"inode":`, node.Inode)
	}
	if node.DeviceID != 0 {
		b = appendNodeUint(b, `,"device_id":`, node.DeviceID)
	}
	if node.Size != 0 {
		b = appendNodeUint(b, `,"size":`, node.Size)
	}
	if node.Links != 0 {
		b = appendNodeUint(b, `,"links":`, node.Links)
	}
	if node.LinkTarget != "" {
		b = appendNodeString(b, `,"linktarget":"`, node.LinkTarget)
	}
	if node.Device != 0 {
		b = appendNodeUint(b, `,"device":`, node.Device)
	}
	b = append(b, `,"content":`...)
	if node.Content == nil {
		b = append(b, "null"...)
	} else {
		b = append(b, '[')
		for i, id := range node.Content {
			if i > 0 {
				b = append(b, ',')
			}
			b = appendNodeID(b, id)
		}
		b = append(b, ']')
	}
	if node.Subtree != nil {
		b = append(b, `,"subtree":`...)
		b = appendNodeID(b, *node.Subtree)
	}
	if node.Error != "" {
		b = appendNodeString(b, `,"error":"`, node.Error)
	}
	return append(b, '}'), true
}

func appendNodeUint(b []byte, field string, n uint64) []byte {
	return strconv.AppendUint(append(b, field...), n, 10)
}

func appendNodeString(b []byte, field, s string) []byte {
	b = append(b, field...)
	b = append(b, s...)
	return append(b, '"')
}

func appendNodeTime(b []byte, field string, tm time.Time) []byte {
	b = tm.AppendFormat(append(b, field...), time.RFC3339Nano)
	return append(b, '"')
}

func appendNodeID(b []byte, id restic.ID) []byte {
	b = append(b, '"')
	start := len(b)
	b = append(b, make([]byte, 64)...)
	hex.Encode(b[start:], id[:])
	return append(b, '"')
}
